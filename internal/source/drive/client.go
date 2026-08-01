package drive

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"google.golang.org/api/docs/v1"
	googledrive "google.golang.org/api/drive/v3"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"

	"stacks/internal/source"
)

const (
	driveProvider        = "drive"
	googleDocumentMIME   = "application/vnd.google-apps.document"
	googleFolderMIME     = "application/vnd.google-apps.folder"
	driveDocumentFields  = "nextPageToken,files(id,name,modifiedTime,version,webViewLink)"
	collectionFields     = "id,mimeType"
	representativeFields = "files(id)"
	representativeLimit  = 1
	docsLocatorFormat    = "https://docs.google.com/document/d/%s/edit"
	meetingDateTokenSize = len("[2006-01-02]")
)

// Client reads direct Google Doc children from Drive and retrieves complete
// tab trees from Docs. Google SDK values do not cross this boundary.
type Client struct {
	drive      *googledrive.Service
	docs       *docs.Service
	classifier TabClassifier
}

var _ source.Source = (*Client)(nil)
var _ source.RepresentativeSource = (*Client)(nil)

// NewClient constructs a Google Drive and Docs source without exposing
// provider SDK types at the package boundary.
func NewClient(ctx context.Context, httpClient *http.Client, classifier TabClassifier) (*Client, error) {
	driveService, err := googledrive.NewService(ctx, option.WithHTTPClient(httpClient))
	if err != nil {
		return nil, fmt.Errorf("construct Google Drive client: %w", err)
	}
	docsService, err := docs.NewService(ctx, option.WithHTTPClient(httpClient))
	if err != nil {
		return nil, fmt.Errorf("construct Google Docs client: %w", err)
	}
	return newClient(driveService, docsService, classifier), nil
}

func newClient(driveService *googledrive.Service, docsService *docs.Service, classifier TabClassifier) *Client {
	return &Client{drive: driveService, docs: docsService, classifier: classifier}
}

// List returns supported direct children of folderID without following links
// or recursing into child folders.
func (client *Client) List(ctx context.Context, folderID string) ([]source.Document, error) {
	query := fmt.Sprintf("'%s' in parents and trashed = false and mimeType = '%s'", escapeDriveQueryValue(folderID), googleDocumentMIME)
	var documents []source.Document
	pageToken := ""
	seenPageTokens := make(map[string]struct{})
	for {
		call := client.drive.Files.List().
			Q(query).
			Fields(googleapi.Field(driveDocumentFields)).
			Context(ctx)
		if pageToken != "" {
			call.PageToken(pageToken)
		}
		files, err := call.Do()
		if err != nil {
			return nil, sanitizedGoogleError(ctx, "list direct Google Docs", err)
		}
		for _, file := range files.Files {
			if file == nil {
				continue
			}
			modifiedAt, err := parseModifiedTime(file.ModifiedTime)
			if err != nil {
				return nil, err
			}
			documents = append(documents, source.Document{
				Provider:    driveProvider,
				ID:          file.Id,
				Title:       file.Name,
				Locator:     file.WebViewLink,
				Version:     strconv.FormatInt(file.Version, 10),
				ModifiedAt:  modifiedAt,
				MeetingTime: meetingTimeFromTitle(file.Name),
			})
		}
		if files.NextPageToken == "" {
			break
		}
		if _, repeated := seenPageTokens[files.NextPageToken]; repeated {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, fmt.Errorf("list direct Google Docs: %w", ctxErr)
			}
			return nil, errors.New("list direct Google Docs: invalid pagination response")
		}
		seenPageTokens[files.NextPageToken] = struct{}{}
		pageToken = files.NextPageToken
	}
	return documents, nil
}

// CheckCollection verifies that collectionID is an accessible Google Drive
// folder without retrieving provider-controlled names or other metadata.
func (client *Client) CheckCollection(ctx context.Context, collectionID string) error {
	file, err := client.drive.Files.Get(collectionID).
		Fields(googleapi.Field(collectionFields)).
		Context(ctx).
		Do()
	if err != nil {
		return sanitizedGoogleError(ctx, "inspect Google Drive folder", err)
	}
	if file == nil || file.MimeType != googleFolderMIME {
		return errors.New("configured Google Drive collection is not a folder")
	}
	return nil
}

// GetRepresentative returns at most one supported direct child without
// following Drive pagination or retrieving provider-controlled metadata that
// doctor does not need.
func (client *Client) GetRepresentative(ctx context.Context, folderID string) (source.Document, bool, error) {
	query := fmt.Sprintf("'%s' in parents and trashed = false and mimeType = '%s'", escapeDriveQueryValue(folderID), googleDocumentMIME)
	files, err := client.drive.Files.List().
		Q(query).
		PageSize(representativeLimit).
		Fields(googleapi.Field(representativeFields)).
		Context(ctx).
		Do()
	if err != nil {
		return source.Document{}, false, sanitizedGoogleError(ctx, "find representative Google Doc", err)
	}
	for _, file := range files.Files {
		if file != nil && strings.TrimSpace(file.Id) != "" {
			return source.Document{Provider: driveProvider, ID: file.Id}, true, nil
		}
	}
	return source.Document{}, false, nil
}

// Get retrieves all document tabs and immediately converts them into source
// contracts.
func (client *Client) Get(ctx context.Context, documentID string) (source.Document, error) {
	document, err := client.docs.Documents.Get(documentID).
		IncludeTabsContent(true).
		Context(ctx).
		Do()
	if err != nil {
		return source.Document{}, sanitizedGoogleError(ctx, "get Google Doc", err)
	}

	tabs, err := FlattenTabs(document.Tabs, client.classifier)
	if err != nil {
		return source.Document{}, fmt.Errorf("convert Google Doc tabs: %w", err)
	}
	return source.Document{
		Provider:    driveProvider,
		ID:          document.DocumentId,
		Title:       document.Title,
		Locator:     fmt.Sprintf(docsLocatorFormat, url.PathEscape(document.DocumentId)),
		Revision:    document.RevisionId,
		MeetingTime: meetingTimeFromTitle(document.Title),
		Tabs:        tabs,
	}, nil
}

// meetingTimeFromTitle implements the Drive source-time contract. A meeting
// title is dated only when it starts with exactly one valid [YYYY-MM-DD] token,
// followed by one space and a non-empty description. Any other or ambiguous
// title leaves source-valid time unknown.
func meetingTimeFromTitle(title string) *time.Time {
	if len(title) <= meetingDateTokenSize+1 || title[0] != '[' || title[meetingDateTokenSize-1] != ']' ||
		title[meetingDateTokenSize] != ' ' || strings.TrimSpace(title[meetingDateTokenSize+1:]) == "" {
		return nil
	}

	meetingDate, ok := parseMeetingDateToken(title[:meetingDateTokenSize])
	if !ok {
		return nil
	}
	validDateTokens := 0
	for offset := 0; offset+meetingDateTokenSize <= len(title); offset++ {
		if _, ok := parseMeetingDateToken(title[offset : offset+meetingDateTokenSize]); ok {
			validDateTokens++
		}
	}
	if validDateTokens != 1 {
		return nil
	}
	return &meetingDate
}

func parseMeetingDateToken(token string) (time.Time, bool) {
	if len(token) != meetingDateTokenSize || token[0] != '[' || token[len(token)-1] != ']' {
		return time.Time{}, false
	}
	value := token[1 : len(token)-1]
	parsed, err := time.Parse(time.DateOnly, value)
	if err != nil || parsed.Year() < 1 || parsed.Format(time.DateOnly) != value {
		return time.Time{}, false
	}
	return parsed.UTC(), true
}

func sanitizedGoogleError(ctx context.Context, operation string, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("%s: %w", operation, ctxErr)
	}
	var apiError *googleapi.Error
	if errors.As(err, &apiError) {
		switch apiError.Code {
		case http.StatusUnauthorized:
			return fmt.Errorf("%s: %w", operation, source.ErrAuthentication)
		case http.StatusForbidden:
			return fmt.Errorf("%s: %w", operation, source.ErrAuthorization)
		}
		return fmt.Errorf("%s: Google API returned HTTP %d", operation, apiError.Code)
	}
	return fmt.Errorf("%s: request failed", operation)
}

func escapeDriveQueryValue(value string) string {
	return strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(value)
}

func parseModifiedTime(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	modifiedAt, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, errors.New("parse Google Doc modified time: invalid timestamp")
	}
	return modifiedAt, nil
}
