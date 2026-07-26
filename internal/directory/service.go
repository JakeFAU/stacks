package directory

import (
	"context"
	"errors"
	"strings"
	"time"

	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"github.com/JakeFAU/stacks/core/timepoint"
	"stacks/internal/entity"
	"stacks/internal/observability"
)

const (
	directoryEnrichmentSpanName = "stacks.directory.enrich"
	directoryLookupDecisionName = "directory_lookup"
	directoryInstrumentation    = "stacks"
	reviewerDirectoryProvider   = "google_people"
)

// Summary contains only bounded directory enrichment counts suitable for
// ordinary operational output.
type Summary struct {
	Attempted   int
	Reused      int
	Matched     int
	Review      int
	NoMatch     int
	Ambiguous   int
	Unavailable int
}

// ReviewerVerification contains the bounded lookup and deterministic policy
// result for one email explicitly supplied by a local reviewer.
type ReviewerVerification struct {
	Query        entity.DirectoryQuery
	Lookup       LookupResult
	Evaluation   entity.DirectoryEvaluation
	AttemptCount int
	RecordedAt   time.Time
	RetryAfter   *time.Time
}

// ValidForEmail reports whether a nil-error verifier result is complete enough
// to cross the durable reviewer boundary. Invalid metadata is intentionally
// fail-soft: the explicit reviewer decision remains authoritative without
// directory evidence.
func (verification ReviewerVerification) ValidForEmail(email string) bool {
	normalizedEmail := entity.NormalizeEmail(email)
	if !entity.ValidEmail(normalizedEmail) ||
		verification.Query.Kind != entity.DirectoryQueryEmail ||
		entity.NormalizeEmail(verification.Query.Email) != normalizedEmail ||
		entity.NormalizeName(verification.Query.Name) != "" ||
		verification.Query.EmailEvidence != entity.EmailEvidenceReviewerSupplied ||
		strings.TrimSpace(verification.Lookup.Provider) != reviewerDirectoryProvider ||
		verification.RecordedAt.IsZero() ||
		!timepoint.IsCanonical(verification.RecordedAt) ||
		verification.AttemptCount < 0 ||
		!boundedDirectoryOutcome(verification.Lookup.Outcome) ||
		verification.Evaluation.Outcome != verification.Lookup.Outcome ||
		verification.Lookup.RetryAfter < 0 ||
		(verification.RetryAfter != nil &&
			(verification.RetryAfter.IsZero() ||
				!timepoint.IsCanonical(*verification.RetryAfter) ||
				verification.RetryAfter.Before(verification.RecordedAt))) {
		return false
	}
	if retryableDirectoryOutcome(verification.Lookup.Outcome) {
		if verification.RetryAfter == nil {
			return false
		}
	} else if verification.RetryAfter != nil {
		return false
	}
	for _, profile := range verification.Lookup.Profiles {
		if !validReviewerDirectoryProfile(profile) {
			return false
		}
	}
	if verification.Evaluation.Profile != nil {
		if !validReviewerDirectoryProfile(*verification.Evaluation.Profile) ||
			!reviewerDirectoryProfileInSet(
				*verification.Evaluation.Profile,
				verification.Lookup.Profiles,
			) {
			return false
		}
	}
	for _, candidate := range verification.Evaluation.Candidates {
		if !validReviewerDirectoryProfile(candidate) ||
			!reviewerDirectoryProfileInSet(candidate, verification.Lookup.Profiles) ||
			!reviewerDirectoryProfileHasEmail(candidate, normalizedEmail) {
			return false
		}
	}

	switch verification.Lookup.Outcome {
	case entity.DirectoryMatched:
		return validMatchedReviewerVerification(normalizedEmail, verification)
	case entity.DirectoryAmbiguous, entity.DirectoryReview:
		return len(verification.Lookup.Profiles) > 0 &&
			len(verification.Evaluation.Candidates) > 0 &&
			verification.Evaluation.EntityID == "" &&
			!verification.Evaluation.CreatePerson &&
			verification.Evaluation.AcceptedEmail == "" &&
			verification.Evaluation.Profile == nil
	default:
		return len(verification.Lookup.Profiles) == 0 &&
			verification.Evaluation.EntityID == "" &&
			!verification.Evaluation.CreatePerson &&
			verification.Evaluation.AcceptedEmail == "" &&
			verification.Evaluation.Profile == nil &&
			len(verification.Evaluation.Candidates) == 0
	}
}

func validMatchedReviewerVerification(
	email string,
	verification ReviewerVerification,
) bool {
	if len(verification.Lookup.Profiles) == 0 ||
		verification.Evaluation.Profile == nil ||
		entity.NormalizeEmail(verification.Evaluation.AcceptedEmail) != email ||
		(verification.Evaluation.CreatePerson &&
			verification.Evaluation.EntityID != "") ||
		(!verification.Evaluation.CreatePerson &&
			strings.TrimSpace(verification.Evaluation.EntityID) == "") ||
		len(verification.Evaluation.Candidates) != 0 {
		return false
	}
	evaluated := *verification.Evaluation.Profile
	if !validReviewerDirectoryProfile(evaluated) ||
		evaluated.Source != entity.DirectorySourceDomainProfile ||
		!reviewerDirectoryProfileHasEmail(evaluated, email) {
		return false
	}
	for _, profile := range verification.Lookup.Profiles {
		if sameReviewerDirectoryProfile(profile, evaluated) {
			return true
		}
	}
	return false
}

func validReviewerDirectoryProfile(profile entity.DirectoryProfile) bool {
	if strings.TrimSpace(profile.Provider) != reviewerDirectoryProvider ||
		strings.TrimSpace(profile.SubjectID) == "" ||
		strings.TrimSpace(profile.DisplayName) == "" ||
		len(profile.Emails) == 0 ||
		profile.ObservedAt.IsZero() ||
		!timepoint.IsCanonical(profile.ObservedAt) {
		return false
	}
	switch profile.Source {
	case entity.DirectorySourceDomainProfile, entity.DirectorySourceDomainContact:
	default:
		return false
	}
	for _, email := range profile.Emails {
		if !entity.ValidEmail(entity.NormalizeEmail(email.Value)) {
			return false
		}
	}
	return true
}

func reviewerDirectoryProfileHasEmail(
	profile entity.DirectoryProfile,
	email string,
) bool {
	for _, candidate := range profile.Emails {
		if entity.NormalizeEmail(candidate.Value) == email {
			return true
		}
	}
	return false
}

func reviewerDirectoryProfileInSet(
	profile entity.DirectoryProfile,
	values []entity.DirectoryProfile,
) bool {
	for _, value := range values {
		if sameReviewerDirectoryProfile(profile, value) {
			return true
		}
	}
	return false
}

func sameReviewerDirectoryProfile(
	left entity.DirectoryProfile,
	right entity.DirectoryProfile,
) bool {
	if strings.TrimSpace(left.Provider) != strings.TrimSpace(right.Provider) ||
		strings.TrimSpace(left.SubjectID) != strings.TrimSpace(right.SubjectID) ||
		left.Source != right.Source ||
		strings.TrimSpace(left.DisplayName) != strings.TrimSpace(right.DisplayName) ||
		!left.ObservedAt.UTC().Truncate(time.Microsecond).Equal(
			right.ObservedAt.UTC().Truncate(time.Microsecond),
		) {
		return false
	}
	leftEmails := reviewerDirectoryEmails(left.Emails)
	rightEmails := reviewerDirectoryEmails(right.Emails)
	if len(leftEmails) != len(rightEmails) {
		return false
	}
	for email, leftPrimary := range leftEmails {
		if rightPrimary, found := rightEmails[email]; !found ||
			rightPrimary != leftPrimary {
			return false
		}
	}
	return true
}

func reviewerDirectoryEmails(
	emails []entity.DirectoryEmail,
) map[string]bool {
	result := make(map[string]bool, len(emails))
	for _, email := range emails {
		value := entity.NormalizeEmail(email.Value)
		result[value] = result[value] || email.Primary
	}
	return result
}

// DecisionRecorder records one bounded operational decision without receiving
// private query values or provider payloads.
type DecisionRecorder interface {
	Record(context.Context, observability.DecisionObservation) error
}

// Service orchestrates optional directory enrichment around pure identity
// policy and durable repository boundaries.
type Service struct {
	Lookup      Lookup
	Repository  Repository
	Policy      entity.DirectoryPolicy
	Enabled     bool
	Freshness   time.Duration
	RetryAfter  time.Duration
	MaxAttempts int
	Tracer      trace.Tracer
	Decisions   DecisionRecorder
	Now         func() time.Time
	Wait        func(context.Context, time.Duration) error
}

// VerifyReviewerEmail evaluates one reviewer-supplied email through the same
// retry and identity policy boundary as enrichment. Provider failures are
// returned only as bounded lookup outcomes.
func (service *Service) VerifyReviewerEmail(ctx context.Context, email string) (ReviewerVerification, error) {
	query := entity.DirectoryQuery{
		Kind:          entity.DirectoryQueryEmail,
		Email:         entity.NormalizeEmail(email),
		EmailEvidence: entity.EmailEvidenceReviewerSupplied,
	}
	verification := ReviewerVerification{
		Query:  query,
		Lookup: LookupResult{Provider: reviewerDirectoryProvider},
	}
	if err := ctx.Err(); err != nil {
		return ReviewerVerification{}, err
	}
	if service == nil || !service.Enabled {
		verification.Lookup.Outcome = entity.DirectoryDisabled
		verification.Evaluation.Outcome = entity.DirectoryDisabled
		return verification, nil
	}
	if service.Now == nil {
		verification.Lookup.Outcome = entity.DirectoryUnavailable
		verification.Evaluation.Outcome = entity.DirectoryUnavailable
		return verification, nil
	}
	verification.RecordedAt = timepoint.Normalize(service.Now())
	if !service.Policy.LookupEligible(query) {
		verification.Lookup.Outcome = entity.DirectoryNoMatch
		verification.Evaluation.Outcome = entity.DirectoryNoMatch
		return verification, nil
	}
	if service.Repository == nil {
		verification.Lookup.Outcome = entity.DirectoryUnavailable
		verification.Evaluation.Outcome = entity.DirectoryUnavailable
		verification.RetryAfter = service.reviewerRetryAfter(verification.RecordedAt, verification.Lookup)
		return verification, nil
	}
	identityState, err := service.Repository.LoadIdentityState(ctx)
	if cancellationErr := directoryCancellation(ctx, err); cancellationErr != nil {
		return ReviewerVerification{}, cancellationErr
	}
	if err != nil {
		verification.Lookup.Outcome = entity.DirectoryUnavailable
		verification.Evaluation.Outcome = entity.DirectoryUnavailable
		verification.RetryAfter = service.reviewerRetryAfter(verification.RecordedAt, verification.Lookup)
		return verification, nil
	}
	if service.Lookup == nil {
		verification.Lookup.Outcome = entity.DirectoryNotConfigured
		verification.Evaluation.Outcome = entity.DirectoryNotConfigured
		return verification, nil
	}

	verification.Lookup, verification.AttemptCount, err = service.search(ctx, query)
	if cancellationErr := directoryCancellation(ctx, err); cancellationErr != nil {
		return ReviewerVerification{}, cancellationErr
	}
	if err != nil {
		verification.Lookup = LookupResult{
			Provider: reviewerDirectoryProvider,
			Outcome:  entity.DirectoryUnavailable,
		}
	}
	if verification.Lookup.Outcome == "" {
		verification.Evaluation = service.Policy.Evaluate(
			query,
			verification.Lookup.Profiles,
			identityState.Snapshots,
			identityState.Links,
		)
		verification.Lookup.Outcome = verification.Evaluation.Outcome
	} else {
		verification.Evaluation.Outcome = verification.Lookup.Outcome
	}
	verification.RetryAfter = service.reviewerRetryAfter(
		verification.RecordedAt,
		verification.Lookup,
	)
	return verification, nil
}

func (service *Service) reviewerRetryAfter(recordedAt time.Time, lookup LookupResult) *time.Time {
	if !retryableDirectoryOutcome(lookup.Outcome) {
		return nil
	}
	value := recordedAt.Add(service.retryDelay(lookup))
	return &value
}

// Enrich attempts bounded identity enrichment without making directory
// availability a document-processing requirement.
func (service *Service) Enrich(ctx context.Context, derivationID string) (summary Summary, resultErr error) {
	if service == nil || !service.Enabled {
		return summary, nil
	}

	tracer := service.Tracer
	if tracer == nil {
		tracer = tracenoop.NewTracerProvider().Tracer(directoryInstrumentation)
	}
	ctx, span := tracer.Start(ctx, directoryEnrichmentSpanName)
	defer func() {
		observability.FinishSpan(span, resultErr)
	}()

	if err := ctx.Err(); err != nil {
		return summary, err
	}
	if service.Repository == nil || service.Now == nil {
		summary.Unavailable++
		return summary, nil
	}

	now := timepoint.Normalize(service.Now())
	if err := ctx.Err(); err != nil {
		return summary, err
	}
	work, err := service.Repository.LoadWork(ctx, derivationID, now, service.Freshness, service.RetryAfter)
	if cancellationErr := directoryCancellation(ctx, err); cancellationErr != nil {
		return Summary{}, cancellationErr
	}
	if err != nil {
		summary.Unavailable++
		return summary, nil
	}
	summary.Reused = work.Reused

	if err := ctx.Err(); err != nil {
		return summary, err
	}
	identityState, err := service.Repository.LoadIdentityState(ctx)
	if cancellationErr := directoryCancellation(ctx, err); cancellationErr != nil {
		return Summary{}, cancellationErr
	}
	if err != nil {
		summary.Unavailable++
		return summary, nil
	}

	for _, mention := range work.Mentions {
		query, eligible := directoryQuery(service.Policy, mention)
		if !eligible {
			continue
		}
		started := timepoint.Normalize(service.Now())
		summary.Attempted++

		lookup := LookupResult{
			Provider: reviewerDirectoryProvider,
			Outcome:  entity.DirectoryNotConfigured,
		}
		attemptCount := 0
		var lookupErr error
		if service.Lookup != nil {
			lookup, attemptCount, lookupErr = service.search(ctx, query)
		}
		if cancellationErr := directoryCancellation(ctx, lookupErr); cancellationErr != nil {
			if decisionErr := service.recordDecision(
				ctx,
				entity.DirectoryUnavailable,
				attemptCount,
				0,
				started,
			); decisionErr != nil {
				return summary, decisionErr
			}
			return summary, cancellationErr
		}
		recordedAt := timepoint.Normalize(service.Now())
		evaluation := entity.DirectoryEvaluation{}
		if lookup.Outcome == "" {
			evaluation = service.Policy.Evaluate(query, lookup.Profiles, identityState.Snapshots, identityState.Links)
			lookup.Outcome = evaluation.Outcome
		} else if lookup.Outcome == entity.DirectoryNoMatch {
			evaluation.Outcome = entity.DirectoryNoMatch
		}
		var retryAfter *time.Time
		if retryableDirectoryOutcome(lookup.Outcome) {
			value := recordedAt.Add(service.retryDelay(lookup))
			retryAfter = &value
		}

		if err := ctx.Err(); err != nil {
			return summary, err
		}
		persisted, persistErr := service.Repository.Persist(ctx, PersistInput{
			Mention:      mention,
			Query:        query,
			Lookup:       lookup,
			Evaluation:   evaluation,
			AttemptCount: attemptCount,
			RecordedAt:   recordedAt,
			RetryAfter:   retryAfter,
		})
		if cancellationErr := directoryCancellation(ctx, persistErr); cancellationErr != nil {
			if decisionErr := service.recordDecision(
				ctx,
				entity.DirectoryUnavailable,
				attemptCount,
				0,
				started,
			); decisionErr != nil {
				return summary, decisionErr
			}
			return summary, cancellationErr
		}
		if persistErr != nil {
			summary.Unavailable++
			if decisionErr := service.recordDecision(
				ctx,
				entity.DirectoryUnavailable,
				attemptCount,
				0,
				started,
			); decisionErr != nil {
				return summary, decisionErr
			}
			continue
		}
		summary.add(lookup.Outcome, persisted)
		if decisionErr := service.recordDecision(
			ctx,
			effectiveDirectoryOutcome(lookup.Outcome, persisted),
			attemptCount,
			len(lookup.Profiles),
			started,
		); decisionErr != nil {
			return summary, decisionErr
		}
	}
	return summary, nil
}

func (service *Service) search(ctx context.Context, query entity.DirectoryQuery) (LookupResult, int, error) {
	maxAttempts := service.MaxAttempts
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	var result LookupResult
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return LookupResult{
				Provider: reviewerDirectoryProvider,
				Outcome:  entity.DirectoryUnavailable,
			}, attempt - 1, err
		}
		untrusted, err := service.Lookup.Search(ctx, query)
		if cancellationErr := directoryCancellation(ctx, err); cancellationErr != nil {
			return LookupResult{
				Provider: reviewerDirectoryProvider,
				Outcome:  entity.DirectoryUnavailable,
			}, attempt, cancellationErr
		}
		if err != nil {
			result = LookupResult{
				Provider: reviewerDirectoryProvider,
				Outcome:  entity.DirectoryUnavailable,
			}
		} else {
			result = normalizeLookupResult(untrusted)
		}
		if !retryableDirectoryOutcome(result.Outcome) || attempt == maxAttempts {
			return result, attempt, nil
		}
		if service.Wait == nil {
			return result, attempt, nil
		}
		if err := ctx.Err(); err != nil {
			return LookupResult{
				Provider: reviewerDirectoryProvider,
				Outcome:  entity.DirectoryUnavailable,
			}, attempt, err
		}
		waitErr := service.Wait(ctx, service.retryDelay(result))
		if cancellationErr := directoryCancellation(ctx, waitErr); cancellationErr != nil {
			return LookupResult{
				Provider: reviewerDirectoryProvider,
				Outcome:  entity.DirectoryUnavailable,
			}, attempt, cancellationErr
		}
		if waitErr != nil {
			return LookupResult{
				Provider: reviewerDirectoryProvider,
				Outcome:  entity.DirectoryUnavailable,
			}, attempt, nil
		}
	}
	return result, maxAttempts, nil
}

func retryableDirectoryOutcome(outcome entity.DirectoryOutcome) bool {
	return outcome == entity.DirectoryRateLimited || outcome == entity.DirectoryUnavailable
}

func normalizeLookupResult(result LookupResult) LookupResult {
	result.Provider = strings.TrimSpace(result.Provider)
	if result.Provider == "" {
		result.Provider = reviewerDirectoryProvider
	}
	if result.Outcome == "" {
		result.RetryAfter = 0
		return result
	}
	if !boundedDirectoryOutcome(result.Outcome) {
		return LookupResult{
			Provider: result.Provider,
			Outcome:  entity.DirectoryInvalidResponse,
		}
	}
	result.Profiles = nil
	if !retryableDirectoryOutcome(result.Outcome) {
		result.RetryAfter = 0
	}
	return result
}

func boundedDirectoryOutcome(outcome entity.DirectoryOutcome) bool {
	switch outcome {
	case entity.DirectoryMatched,
		entity.DirectoryNoMatch,
		entity.DirectoryAmbiguous,
		entity.DirectoryReview,
		entity.DirectoryDisabled,
		entity.DirectoryNotConfigured,
		entity.DirectoryUnauthorized,
		entity.DirectoryForbidden,
		entity.DirectoryRateLimited,
		entity.DirectoryUnavailable,
		entity.DirectoryInvalidResponse,
		entity.DirectoryResultLimitExceeded:
		return true
	default:
		return false
	}
}

func (service *Service) retryDelay(result LookupResult) time.Duration {
	if result.RetryAfter > 0 {
		return result.RetryAfter
	}
	return service.RetryAfter
}

func directoryQuery(
	policy entity.DirectoryPolicy,
	mention PendingMention,
) (entity.DirectoryQuery, bool) {
	email := entity.NormalizeEmail(mention.ProposedEmail)
	if entity.ValidEmail(email) {
		evidence := entity.EmailEvidenceCitationVerified
		if entity.SourceBoundMailbox(mention.Surface, email, mention.EmailQuote) {
			evidence = entity.EmailEvidenceSourceBound
		}
		query := entity.DirectoryQuery{
			Kind:          entity.DirectoryQueryEmail,
			Email:         email,
			EmailEvidence: evidence,
		}
		if policy.LookupEligible(query) {
			return query, true
		}
	}
	name := entity.NormalizeName(mention.NormalizedName)
	query := entity.DirectoryQuery{
		Kind:          entity.DirectoryQueryName,
		Name:          name,
		EmailEvidence: entity.EmailEvidenceNone,
	}
	return query, policy.LookupEligible(query)
}

func directoryCancellation(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return nil
}

func (summary *Summary) add(outcome entity.DirectoryOutcome, persisted PersistResult) {
	switch effectiveDirectoryOutcome(outcome, persisted) {
	case entity.DirectoryMatched:
		summary.Matched++
	case entity.DirectoryReview:
		summary.Review++
	case entity.DirectoryNoMatch:
		summary.NoMatch++
	case entity.DirectoryAmbiguous:
		summary.Ambiguous++
	default:
		summary.Unavailable++
	}
}

func effectiveDirectoryOutcome(
	outcome entity.DirectoryOutcome,
	persisted PersistResult,
) entity.DirectoryOutcome {
	if outcome == entity.DirectoryMatched && !persisted.AutoResolved {
		return entity.DirectoryReview
	}
	return outcome
}

func (service *Service) recordDecision(
	ctx context.Context,
	outcome entity.DirectoryOutcome,
	attemptCount int,
	profileCount int,
	started time.Time,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if service.Decisions == nil {
		return nil
	}
	duration := timepoint.Normalize(service.Now()).Sub(started)
	if duration < 0 {
		duration = 0
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	err := service.Decisions.Record(ctx, observability.DecisionObservation{
		Name:       directoryLookupDecisionName,
		Outcome:    string(normalizeDirectoryOutcome(outcome)),
		Duration:   duration,
		InputSize:  int64(attemptCount),
		OutputSize: int64(profileCount),
	})
	return directoryCancellation(ctx, err)
}

func normalizeDirectoryOutcome(outcome entity.DirectoryOutcome) entity.DirectoryOutcome {
	if !boundedDirectoryOutcome(outcome) {
		return entity.DirectoryUnavailable
	}
	return outcome
}
