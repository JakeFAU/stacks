package googledirectory

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
	"google.golang.org/api/people/v1"

	"stacks/internal/directory"
	"stacks/internal/entity"
)

const (
	defaultPageSize        int64 = 25
	googlePeopleProvider         = "google_people"
	directoryReadMask            = "metadata,names,emailAddresses"
	directoryProfileSource       = "DIRECTORY_SOURCE_TYPE_DOMAIN_PROFILE"
	domainProfileSource          = "DOMAIN_PROFILE"
	retryAfterHeader             = "Retry-After"
)

// Client is a Google People directory lookup adapter.
type Client struct {
	people         *people.Service
	maximumResults int
}

var _ directory.Lookup = (*Client)(nil)

// NewClient constructs a Google People adapter using the separately authorized
// directory-only HTTP client.
func NewClient(ctx context.Context, httpClient *http.Client, maximumResults int) (*Client, error) {
	if maximumResults < 1 {
		return nil, errors.New("maximum directory results must be positive")
	}
	if httpClient == nil {
		return nil, errors.New("construct Google People client: HTTP client is required")
	}
	service, err := people.NewService(ctx, option.WithHTTPClient(httpClient))
	if err != nil {
		return nil, errors.New("construct Google People client")
	}
	return &Client{people: service, maximumResults: maximumResults}, nil
}

// Search returns complete domain-profile records from bounded requests.
func (client *Client) Search(ctx context.Context, query entity.DirectoryQuery) (directory.LookupResult, error) {
	if err := ctx.Err(); err != nil {
		return directory.LookupResult{}, err
	}
	prefix, exactEmail, ok := directoryPrefix(query)
	if !ok {
		return directory.LookupResult{Outcome: entity.DirectoryNoMatch}, nil
	}

	profiles := make([]entity.DirectoryProfile, 0)
	resultCount := 0
	pageRequests := 0
	pageToken := ""
	seenPageTokens := make(map[string]struct{})
	for {
		if pageRequests >= client.maximumResults {
			return directory.LookupResult{Outcome: entity.DirectoryResultLimitExceeded}, nil
		}
		pageRequests++
		call := client.people.People.SearchDirectoryPeople().
			ReadMask(directoryReadMask).
			Sources(directoryProfileSource).
			PageSize(defaultPageSize).
			Query(prefix).
			Context(ctx)
		if pageToken != "" {
			call.PageToken(pageToken)
		}
		response, err := call.Do()
		if err != nil {
			return classifySearchError(ctx, err)
		}
		if response == nil {
			return directory.LookupResult{Outcome: entity.DirectoryInvalidResponse}, nil
		}
		resultCount += len(response.People)
		if resultCount > client.maximumResults {
			return directory.LookupResult{Outcome: entity.DirectoryResultLimitExceeded}, nil
		}
		for _, person := range response.People {
			profile, valid := directoryProfile(person)
			if !valid {
				return directory.LookupResult{Outcome: entity.DirectoryInvalidResponse}, nil
			}
			profiles = append(profiles, profile)
		}
		if response.NextPageToken == "" {
			break
		}
		if resultCount >= client.maximumResults {
			return directory.LookupResult{Outcome: entity.DirectoryResultLimitExceeded}, nil
		}
		if _, repeated := seenPageTokens[response.NextPageToken]; repeated {
			return directory.LookupResult{Outcome: entity.DirectoryInvalidResponse}, nil
		}
		seenPageTokens[response.NextPageToken] = struct{}{}
		pageToken = response.NextPageToken
	}
	profiles = deduplicateProfiles(profiles)
	if exactEmail != "" {
		profiles = profilesWithEmail(profiles, exactEmail)
	}
	if len(profiles) == 0 {
		return directory.LookupResult{Outcome: entity.DirectoryNoMatch}, nil
	}
	return directory.LookupResult{Profiles: profiles}, nil
}

func classifySearchError(ctx context.Context, err error) (directory.LookupResult, error) {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return directory.LookupResult{}, ctxErr
	}
	var apiError *googleapi.Error
	if !errors.As(err, &apiError) {
		return directory.LookupResult{Outcome: entity.DirectoryUnavailable}, nil
	}
	result := directory.LookupResult{}
	switch apiError.Code {
	case http.StatusUnauthorized:
		result.Outcome = entity.DirectoryUnauthorized
	case http.StatusForbidden:
		result.Outcome = entity.DirectoryForbidden
	case http.StatusTooManyRequests:
		result.Outcome = entity.DirectoryRateLimited
		result.RetryAfter = retryAfter(apiError.Header)
	case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		result.Outcome = entity.DirectoryUnavailable
	default:
		if apiError.Code >= http.StatusBadRequest && apiError.Code < http.StatusInternalServerError {
			result.Outcome = entity.DirectoryInvalidResponse
		} else {
			result.Outcome = entity.DirectoryUnavailable
		}
	}
	return result, nil
}

func retryAfter(header http.Header) time.Duration {
	if header == nil {
		return 0
	}
	seconds, err := strconv.ParseInt(strings.TrimSpace(header.Get(retryAfterHeader)), 10, 64)
	if err != nil || seconds < 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

func deduplicateProfiles(profiles []entity.DirectoryProfile) []entity.DirectoryProfile {
	sort.Slice(profiles, func(left, right int) bool {
		return profileSortKey(profiles[left]) < profileSortKey(profiles[right])
	})
	bySubject := make(map[string]entity.DirectoryProfile, len(profiles))
	for _, profile := range profiles {
		existing, found := bySubject[profile.SubjectID]
		if !found {
			bySubject[profile.SubjectID] = profile
			continue
		}
		bySubject[profile.SubjectID] = mergeProfiles(existing, profile)
	}
	deduplicated := make([]entity.DirectoryProfile, 0, len(bySubject))
	for _, profile := range bySubject {
		deduplicated = append(deduplicated, profile)
	}
	sort.Slice(deduplicated, func(left, right int) bool {
		return profileSortKey(deduplicated[left]) < profileSortKey(deduplicated[right])
	})
	return deduplicated
}

func mergeProfiles(first, second entity.DirectoryProfile) entity.DirectoryProfile {
	first.Emails = mergeEmails(first.Emails, second.Emails)
	if second.ObservedAt.After(first.ObservedAt) {
		first.ObservedAt = second.ObservedAt
	}
	return first
}

func mergeEmails(first, second []entity.DirectoryEmail) []entity.DirectoryEmail {
	emailsByValue := make(map[string]entity.DirectoryEmail, len(first)+len(second))
	for _, email := range first {
		emailsByValue[email.Value] = email
	}
	for _, email := range second {
		existing := emailsByValue[email.Value]
		existing.Value = email.Value
		existing.Primary = existing.Primary || email.Primary
		emailsByValue[email.Value] = existing
	}
	emails := make([]entity.DirectoryEmail, 0, len(emailsByValue))
	for _, email := range emailsByValue {
		emails = append(emails, email)
	}
	sort.Slice(emails, func(left, right int) bool {
		return emails[left].Value < emails[right].Value
	})
	return emails
}

func profileSortKey(profile entity.DirectoryProfile) string {
	emails := make([]string, 0, len(profile.Emails))
	for _, email := range profile.Emails {
		primary := "0"
		if email.Primary {
			primary = "1"
		}
		emails = append(emails, email.Value+"\x00"+primary)
	}
	sort.Strings(emails)
	return strings.Join([]string{
		profile.SubjectID,
		profile.DisplayName,
		strings.Join(emails, "\x01"),
		profile.ObservedAt.UTC().Format(time.RFC3339Nano),
	}, "\x00")
}

func directoryPrefix(query entity.DirectoryQuery) (prefix, exactEmail string, ok bool) {
	switch query.Kind {
	case entity.DirectoryQueryEmail:
		exactEmail = entity.NormalizeEmail(query.Email)
		if !entity.ValidEmail(exactEmail) {
			return "", "", false
		}
		return query.Email, exactEmail, true
	case entity.DirectoryQueryName:
		if entity.NormalizeName(query.Name) == "" {
			return "", "", false
		}
		return query.Name, "", true
	default:
		return "", "", false
	}
}

func profilesWithEmail(profiles []entity.DirectoryProfile, email string) []entity.DirectoryProfile {
	matches := make([]entity.DirectoryProfile, 0, len(profiles))
	for _, profile := range profiles {
		for _, profileEmail := range profile.Emails {
			if profileEmail.Value == email {
				matches = append(matches, profile)
				break
			}
		}
	}
	return matches
}

func directoryProfile(person *people.Person) (entity.DirectoryProfile, bool) {
	if person == nil || strings.TrimSpace(person.ResourceName) == "" {
		return entity.DirectoryProfile{}, false
	}
	displayName, nameOK := primaryName(person.Names)
	emails, emailOK := normalizedEmails(person.EmailAddresses)
	observedAt, sourceOK := observedSourceTime(person.Metadata)
	if !nameOK || !emailOK || !sourceOK {
		return entity.DirectoryProfile{}, false
	}
	return entity.DirectoryProfile{
		Provider:    googlePeopleProvider,
		SubjectID:   person.ResourceName,
		Source:      entity.DirectorySourceDomainProfile,
		DisplayName: displayName,
		Emails:      emails,
		ObservedAt:  observedAt,
	}, true
}

func primaryName(names []*people.Name) (string, bool) {
	var displayName string
	for _, name := range names {
		if name == nil || name.Metadata == nil || !name.Metadata.Primary {
			continue
		}
		value := strings.TrimSpace(name.DisplayName)
		if value == "" || displayName != "" {
			return "", false
		}
		displayName = value
	}
	return displayName, displayName != ""
}

func normalizedEmails(addresses []*people.EmailAddress) ([]entity.DirectoryEmail, bool) {
	emailsByValue := make(map[string]entity.DirectoryEmail, len(addresses))
	for _, address := range addresses {
		if address == nil {
			return nil, false
		}
		value := entity.NormalizeEmail(address.Value)
		if !entity.ValidEmail(value) {
			return nil, false
		}
		email := emailsByValue[value]
		email.Value = value
		email.Primary = email.Primary || (address.Metadata != nil && address.Metadata.Primary)
		emailsByValue[value] = email
	}
	if len(emailsByValue) == 0 {
		return nil, false
	}
	emails := make([]entity.DirectoryEmail, 0, len(emailsByValue))
	for _, email := range emailsByValue {
		emails = append(emails, email)
	}
	sort.Slice(emails, func(left, right int) bool {
		return emails[left].Value < emails[right].Value
	})
	return emails, true
}

func observedSourceTime(metadata *people.PersonMetadata) (time.Time, bool) {
	if metadata == nil {
		return time.Time{}, false
	}
	var observedAt time.Time
	foundSource := false
	for _, source := range metadata.Sources {
		if source == nil {
			return time.Time{}, false
		}
		if source.Type != domainProfileSource {
			continue
		}
		foundSource = true
		if source.UpdateTime == "" {
			continue
		}
		updatedAt, err := time.Parse(time.RFC3339, source.UpdateTime)
		if err != nil {
			return time.Time{}, false
		}
		if updatedAt.After(observedAt) {
			observedAt = updatedAt.UTC()
		}
	}
	return observedAt, foundSource
}
