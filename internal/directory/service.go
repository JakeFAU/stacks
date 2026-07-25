package directory

import (
	"context"
	"errors"
	"time"

	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"stacks/internal/entity"
	"stacks/internal/observability"
)

const (
	directoryEnrichmentSpanName = "stacks.directory.enrich"
	directoryLookupDecisionName = "directory_lookup"
	directoryInstrumentation    = "stacks"
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
	verification := ReviewerVerification{Query: query}
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
	verification.RecordedAt = service.Now().UTC()
	if !entity.ValidEmail(query.Email) {
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
		verification.Lookup = LookupResult{Outcome: entity.DirectoryUnavailable}
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

	now := service.Now().UTC()
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
		query, eligible := directoryQuery(mention)
		if !eligible {
			continue
		}
		started := service.Now().UTC()
		summary.Attempted++

		lookup := LookupResult{Outcome: entity.DirectoryNotConfigured}
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
		recordedAt := service.Now().UTC()
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
			return LookupResult{Outcome: entity.DirectoryUnavailable}, attempt - 1, err
		}
		untrusted, err := service.Lookup.Search(ctx, query)
		if cancellationErr := directoryCancellation(ctx, err); cancellationErr != nil {
			return LookupResult{Outcome: entity.DirectoryUnavailable}, attempt, cancellationErr
		}
		if err != nil {
			result = LookupResult{Outcome: entity.DirectoryUnavailable}
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
			return LookupResult{Outcome: entity.DirectoryUnavailable}, attempt, err
		}
		waitErr := service.Wait(ctx, service.retryDelay(result))
		if cancellationErr := directoryCancellation(ctx, waitErr); cancellationErr != nil {
			return LookupResult{Outcome: entity.DirectoryUnavailable}, attempt, cancellationErr
		}
		if waitErr != nil {
			return LookupResult{Outcome: entity.DirectoryUnavailable}, attempt, nil
		}
	}
	return result, maxAttempts, nil
}

func retryableDirectoryOutcome(outcome entity.DirectoryOutcome) bool {
	return outcome == entity.DirectoryRateLimited || outcome == entity.DirectoryUnavailable
}

func normalizeLookupResult(result LookupResult) LookupResult {
	if result.Outcome == "" {
		result.RetryAfter = 0
		return result
	}
	if !boundedDirectoryOutcome(result.Outcome) {
		return LookupResult{Outcome: entity.DirectoryInvalidResponse}
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

func directoryQuery(mention PendingMention) (entity.DirectoryQuery, bool) {
	email := entity.NormalizeEmail(mention.ProposedEmail)
	if entity.ValidEmail(email) {
		evidence := entity.EmailEvidenceCitationVerified
		if entity.SourceBoundMailbox(mention.Surface, email, mention.EmailQuote) {
			evidence = entity.EmailEvidenceSourceBound
		}
		return entity.DirectoryQuery{
			Kind:          entity.DirectoryQueryEmail,
			Email:         email,
			EmailEvidence: evidence,
		}, true
	}
	name := entity.NormalizeName(mention.NormalizedName)
	if name == "" {
		return entity.DirectoryQuery{}, false
	}
	return entity.DirectoryQuery{
		Kind:          entity.DirectoryQueryName,
		Name:          name,
		EmailEvidence: entity.EmailEvidenceNone,
	}, true
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
	duration := service.Now().UTC().Sub(started)
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
