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

// Enrich attempts bounded identity enrichment without making directory
// availability a document-processing requirement.
func (service *Service) Enrich(ctx context.Context, derivationID string) (summary Summary, resultErr error) {
	if service == nil || !service.Enabled {
		return summary, nil
	}
	if err := ctx.Err(); err != nil {
		return summary, err
	}
	if service.Repository == nil || service.Now == nil {
		summary.Unavailable++
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

	now := service.Now().UTC()
	work, err := service.Repository.LoadWork(ctx, derivationID, now, service.Freshness, service.RetryAfter)
	if err != nil {
		if cancellationErr := directoryCancellation(ctx, err); cancellationErr != nil {
			return Summary{}, cancellationErr
		}
		summary.Unavailable++
		return summary, nil
	}
	summary.Reused = work.Reused

	identityState, err := service.Repository.LoadIdentityState(ctx)
	if err != nil {
		if cancellationErr := directoryCancellation(ctx, err); cancellationErr != nil {
			return Summary{}, cancellationErr
		}
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
		if service.Lookup == nil {
			summary.Unavailable++
			service.recordDecision(ctx, entity.DirectoryUnavailable, 0, 0, started)
			continue
		}

		lookup, attemptCount, lookupErr := service.search(ctx, query)
		if cancellationErr := directoryCancellation(ctx, lookupErr); cancellationErr != nil {
			outcome := lookup.Outcome
			if outcome == "" {
				outcome = entity.DirectoryUnavailable
			}
			service.recordDecision(ctx, outcome, attemptCount, len(lookup.Profiles), started)
			return summary, cancellationErr
		}
		recordedAt := service.Now().UTC()
		evaluation := entity.DirectoryEvaluation{}
		if lookup.Outcome == "" {
			evaluation = service.Policy.Evaluate(query, lookup.Profiles, identityState.Snapshots, identityState.Links)
			lookup.Outcome = evaluation.Outcome
		}
		var retryAfter *time.Time
		if retryableDirectoryOutcome(lookup.Outcome) {
			value := recordedAt.Add(service.retryDelay(lookup))
			retryAfter = &value
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
			service.recordDecision(
				ctx,
				entity.DirectoryUnavailable,
				attemptCount,
				len(lookup.Profiles),
				started,
			)
			return summary, cancellationErr
		}
		if persistErr != nil {
			summary.Unavailable++
			service.recordDecision(ctx, entity.DirectoryUnavailable, attemptCount, len(lookup.Profiles), started)
			continue
		}
		summary.add(lookup.Outcome, persisted)
		service.recordDecision(
			ctx,
			effectiveDirectoryOutcome(lookup.Outcome, persisted),
			attemptCount,
			len(lookup.Profiles),
			started,
		)
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
		var err error
		result, err = service.Lookup.Search(ctx, query)
		if cancellationErr := directoryCancellation(ctx, err); cancellationErr != nil {
			return result, attempt, cancellationErr
		}
		if err != nil {
			result = LookupResult{Outcome: entity.DirectoryUnavailable}
		}
		if !retryableDirectoryOutcome(result.Outcome) || attempt == maxAttempts {
			return result, attempt, nil
		}
		if service.Wait == nil {
			return result, attempt, nil
		}
		if err := service.Wait(ctx, service.retryDelay(result)); err != nil {
			if cancellationErr := directoryCancellation(ctx, err); cancellationErr != nil {
				return result, attempt, cancellationErr
			}
			return LookupResult{Outcome: entity.DirectoryUnavailable}, attempt, nil
		}
	}
	return result, maxAttempts, nil
}

func retryableDirectoryOutcome(outcome entity.DirectoryOutcome) bool {
	return outcome == entity.DirectoryRateLimited || outcome == entity.DirectoryUnavailable
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
) {
	if service.Decisions == nil {
		return
	}
	duration := service.Now().UTC().Sub(started)
	if duration < 0 {
		duration = 0
	}
	_ = service.Decisions.Record(ctx, observability.DecisionObservation{
		Name:       directoryLookupDecisionName,
		Outcome:    string(outcome),
		Duration:   duration,
		InputSize:  int64(attemptCount),
		OutputSize: int64(profileCount),
	})
}
