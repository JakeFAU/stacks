package directory

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"stacks/internal/entity"
	"stacks/internal/observability"
)

var errPrivateDirectoryDetail = errors.New("synthetic private directory detail")

func TestServiceDisabledPerformsNoRepositoryOrLookupCalls(t *testing.T) {
	repository := &fakeDirectoryRepository{}
	lookup := &fakeDirectoryLookup{}
	service := Service{
		Enabled:    false,
		Repository: repository,
		Lookup:     lookup,
	}

	summary, err := service.Enrich(context.Background(), "derivation-1")

	if err != nil {
		t.Fatalf("Enrich() error = %v", err)
	}
	if summary != (Summary{}) {
		t.Fatalf("Enrich() summary = %#v, want zero summary", summary)
	}
	if repository.calls != (repositoryCallCounts{}) {
		t.Fatalf("repository calls = %#v, want none", repository.calls)
	}
	if lookup.calls != 0 {
		t.Fatalf("lookup calls = %d, want 0", lookup.calls)
	}
}

func TestServiceMissingOptionalLookupIsUnavailable(t *testing.T) {
	repository := &fakeDirectoryRepository{
		work: Workset{Mentions: []PendingMention{pendingEmailMention()}},
	}
	service := newTestDirectoryService(repository, nil)
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	service.Tracer = provider.Tracer("directory-service-test")

	summary, err := service.Enrich(context.Background(), "derivation-1")

	if err != nil {
		t.Fatalf("Enrich() error = %v", err)
	}
	requireSummary(t, summary, Summary{Attempted: 1, Unavailable: 1})
	if repository.calls.persist != 1 || len(repository.persisted) != 1 {
		t.Fatalf("Persist() calls = %d, want 1", repository.calls.persist)
	}
	input := repository.persisted[0]
	if input.Lookup.Outcome != entity.DirectoryNotConfigured ||
		input.AttemptCount != 0 ||
		!reflect.DeepEqual(input.Evaluation, entity.DirectoryEvaluation{}) {
		t.Fatalf("Persist() input is not one bounded not-configured terminal result")
	}
	requireSingleSpanStatus(t, exporter, codes.Ok)
}

func TestServiceVerifyReviewerEmailUsesReviewerEvidenceAndCurrentIdentityPolicy(t *testing.T) {
	repository := &fakeDirectoryRepository{}
	profile := matchedDirectoryProfile()
	lookup := &fakeDirectoryLookup{results: []LookupResult{{
		Profiles: []entity.DirectoryProfile{profile},
	}}}
	service := newTestDirectoryService(repository, lookup)

	verification, err := service.VerifyReviewerEmail(
		context.Background(),
		"  RIYA.CHEN@CORP.EXAMPLE ",
	)
	if err != nil {
		t.Fatalf("VerifyReviewerEmail() error = %v", err)
	}
	wantQuery := entity.DirectoryQuery{
		Kind:          entity.DirectoryQueryEmail,
		Email:         "riya.chen@corp.example",
		EmailEvidence: entity.EmailEvidenceReviewerSupplied,
	}
	if verification.Query != wantQuery {
		t.Fatalf("verification query = %#v, want %#v", verification.Query, wantQuery)
	}
	if !reflect.DeepEqual(lookup.queries, []entity.DirectoryQuery{wantQuery}) {
		t.Fatalf("lookup queries = %#v, want reviewer-supplied query", lookup.queries)
	}
	if verification.Lookup.Outcome != entity.DirectoryMatched ||
		verification.Evaluation.Outcome != entity.DirectoryMatched ||
		!verification.Evaluation.CreatePerson ||
		verification.Evaluation.Profile == nil ||
		verification.AttemptCount != 1 ||
		verification.RecordedAt.IsZero() {
		t.Fatalf("reviewer verification = %#v, want one unique automatic profile evaluation", verification)
	}
	if repository.calls.loadIdentity != 1 {
		t.Fatalf("LoadIdentityState() calls = %d, want 1", repository.calls.loadIdentity)
	}
	if !verification.ValidForEmail("riya.chen@corp.example") {
		t.Fatalf("reviewer verification = %#v, want complete durable boundary metadata", verification)
	}
}

func TestServiceVerifyReviewerEmailReturnsBoundedUnavailableWithoutProviderError(t *testing.T) {
	repository := &fakeDirectoryRepository{}
	lookup := &fakeDirectoryLookup{
		errors: []error{errPrivateDirectoryDetail, errPrivateDirectoryDetail, errPrivateDirectoryDetail},
	}
	service := newTestDirectoryService(repository, lookup)

	verification, err := service.VerifyReviewerEmail(
		context.Background(),
		"reviewer@corp.example",
	)
	if err != nil {
		t.Fatalf("VerifyReviewerEmail() error = %v, want fail-soft metadata", err)
	}
	if verification.Lookup.Outcome != entity.DirectoryUnavailable ||
		len(verification.Lookup.Profiles) != 0 ||
		verification.AttemptCount != service.MaxAttempts ||
		verification.RetryAfter == nil {
		t.Fatalf("unavailable reviewer verification = %#v, want bounded retry metadata", verification)
	}
	if strings.Contains(fmt.Sprint(verification), errPrivateDirectoryDetail.Error()) {
		t.Fatalf("reviewer verification leaked provider error: %#v", verification)
	}
	if !verification.ValidForEmail("reviewer@corp.example") {
		t.Fatalf("unavailable reviewer verification = %#v, want complete bounded metadata", verification)
	}
}

func TestServiceVerifyReviewerEmailSkipsUnapprovedDomain(t *testing.T) {
	const reviewerEmail = "reviewer@personal.example"
	repository := &fakeDirectoryRepository{}
	lookup := &fakeDirectoryLookup{}
	service := newTestDirectoryService(repository, lookup)

	verification, err := service.VerifyReviewerEmail(
		context.Background(),
		reviewerEmail,
	)

	if err != nil {
		t.Fatalf("VerifyReviewerEmail() error = %v", err)
	}
	if lookup.calls != 0 {
		t.Fatalf("Search() calls = %d, want 0", lookup.calls)
	}
	if repository.calls.loadIdentity != 0 {
		t.Fatalf("LoadIdentityState() calls = %d, want 0", repository.calls.loadIdentity)
	}
	if verification.Lookup.Outcome != entity.DirectoryNoMatch ||
		verification.Evaluation.Outcome != entity.DirectoryNoMatch ||
		verification.AttemptCount != 0 ||
		len(verification.Lookup.Profiles) != 0 ||
		verification.RecordedAt.IsZero() ||
		verification.RetryAfter != nil {
		t.Fatalf("unapproved reviewer verification is not a complete non-authoritative result")
	}
	if !verification.ValidForEmail(reviewerEmail) {
		t.Fatalf("unapproved reviewer verification must remain additive to explicit review")
	}
}

func TestServiceRepositoryLoadFailureIsUnavailableWithoutPrivateError(t *testing.T) {
	repository := &fakeDirectoryRepository{loadWorkErr: errPrivateDirectoryDetail}
	service := newTestDirectoryService(repository, &fakeDirectoryLookup{})

	summary, err := service.Enrich(context.Background(), "derivation-1")

	if err != nil {
		t.Fatalf("Enrich() error = %v, want fail-soft result", err)
	}
	if summary.Unavailable != 1 {
		t.Fatalf("Enrich() Unavailable = %d, want 1", summary.Unavailable)
	}
	if repository.calls.loadIdentity != 0 || repository.calls.persist != 0 {
		t.Fatalf("repository calls after LoadWork failure = %#v", repository.calls)
	}
}

func TestServicePersistenceFailureIsUnavailableWithoutPrivateError(t *testing.T) {
	repository := &fakeDirectoryRepository{
		work:       Workset{Mentions: []PendingMention{pendingEmailMention()}},
		persistErr: errPrivateDirectoryDetail,
	}
	lookup := &fakeDirectoryLookup{
		results: []LookupResult{{Outcome: entity.DirectoryNoMatch}},
	}
	service := newTestDirectoryService(repository, lookup)

	summary, err := service.Enrich(context.Background(), "derivation-1")

	if err != nil {
		t.Fatalf("Enrich() error = %v, want fail-soft result", err)
	}
	if summary.Unavailable != 1 {
		t.Fatalf("Enrich() Unavailable = %d, want 1", summary.Unavailable)
	}
	if summary.NoMatch != 0 {
		t.Fatalf("Enrich() NoMatch = %d, want 0 after persistence failure", summary.NoMatch)
	}
}

func TestServiceReturnsCanonicalContextError(t *testing.T) {
	repository := &fakeDirectoryRepository{
		loadWorkErr: errors.Join(errPrivateDirectoryDetail, context.Canceled),
	}
	service := newTestDirectoryService(repository, &fakeDirectoryLookup{})

	summary, err := service.Enrich(context.Background(), "derivation-1")

	if err != context.Canceled {
		t.Fatalf("Enrich() error = %v, want canonical context.Canceled", err)
	}
	if summary != (Summary{}) {
		t.Fatalf("Enrich() summary = %#v, want zero summary", summary)
	}
}

func TestServiceBuildsEligibleQueriesWithoutCombiningSeparateEvidence(t *testing.T) {
	sourceBound := pendingEmailMention()
	sourceBound.MentionID = "mention-source-bound"
	sourceBound.ProposalID = "proposal-source-bound"
	separatelyCited := pendingEmailMention()
	separatelyCited.MentionID = "mention-separately-cited"
	separatelyCited.ProposalID = "proposal-separately-cited"
	separatelyCited.EmailQuote = "riya.chen@corp.example"
	nameFallback := PendingMention{
		MentionID:      "mention-name",
		ProposalID:     "proposal-name",
		Surface:        "Riya Chen",
		NormalizedName: "  RIYA   CHEN ",
		ProposedEmail:  "not-an-email",
		NameQuote:      "Riya Chen",
	}
	ineligible := PendingMention{
		MentionID:  "mention-ineligible",
		ProposalID: "proposal-ineligible",
	}
	profile := matchedDirectoryProfile()
	repository := &fakeDirectoryRepository{
		work: Workset{Mentions: []PendingMention{
			sourceBound,
			separatelyCited,
			nameFallback,
			ineligible,
		}},
		identity: IdentityState{
			Snapshots: []entity.EntitySnapshot{{
				ID:   "person-1",
				Kind: entity.KindPerson,
				Aliases: []entity.Alias{{
					Type:  entity.AliasTypeEmail,
					Value: "riya.chen@corp.example",
				}},
			}},
		},
		persistResults: []PersistResult{{AutoResolved: true}},
	}
	lookup := &fakeDirectoryLookup{
		results: []LookupResult{
			{Profiles: []entity.DirectoryProfile{profile}},
			{Profiles: []entity.DirectoryProfile{profile}},
			{Profiles: []entity.DirectoryProfile{profile}},
		},
	}
	service := newTestDirectoryService(repository, lookup)

	summary, err := service.Enrich(context.Background(), "derivation-1")

	if err != nil {
		t.Fatalf("Enrich() error = %v", err)
	}
	requireSummary(t, summary, Summary{Attempted: 3, Matched: 1, Review: 2})
	wantQueries := []entity.DirectoryQuery{
		{
			Kind:          entity.DirectoryQueryEmail,
			Email:         "riya.chen@corp.example",
			EmailEvidence: entity.EmailEvidenceSourceBound,
		},
		{
			Kind:          entity.DirectoryQueryEmail,
			Email:         "riya.chen@corp.example",
			EmailEvidence: entity.EmailEvidenceCitationVerified,
		},
		{
			Kind:          entity.DirectoryQueryName,
			Name:          "riya chen",
			EmailEvidence: entity.EmailEvidenceNone,
		},
	}
	if !reflect.DeepEqual(lookup.queries, wantQueries) {
		t.Fatalf("Search() queries = %#v, want %#v", lookup.queries, wantQueries)
	}
	if repository.calls.loadIdentity != 1 {
		t.Fatalf("LoadIdentityState() calls = %d, want 1", repository.calls.loadIdentity)
	}
	if len(repository.persisted) != 3 {
		t.Fatalf("Persist() inputs = %d, want 3", len(repository.persisted))
	}
	if repository.persisted[0].Evaluation.EntityID != "person-1" {
		t.Fatalf("source-bound evaluation EntityID = %q, want existing identity state owner", repository.persisted[0].Evaluation.EntityID)
	}
}

func TestServiceFallsBackToNameForUnapprovedProposedEmail(t *testing.T) {
	mention := PendingMention{
		MentionID:      "mention-unapproved-email",
		ProposalID:     "proposal-unapproved-email",
		Surface:        "Synthetic Person",
		NormalizedName: "synthetic person",
		ProposedEmail:  "synthetic.person@personal.example",
		NameQuote:      "Synthetic Person",
		EmailQuote:     "Synthetic Person <synthetic.person@personal.example>",
	}
	repository := &fakeDirectoryRepository{
		work: Workset{Mentions: []PendingMention{mention}},
	}
	lookup := &fakeDirectoryLookup{
		results: []LookupResult{{Outcome: entity.DirectoryNoMatch}},
	}
	service := newTestDirectoryService(repository, lookup)

	summary, err := service.Enrich(context.Background(), "derivation-1")

	if err != nil {
		t.Fatalf("Enrich() error = %v", err)
	}
	requireSummary(t, summary, Summary{Attempted: 1, NoMatch: 1})
	wantQuery := entity.DirectoryQuery{
		Kind:          entity.DirectoryQueryName,
		Name:          "synthetic person",
		EmailEvidence: entity.EmailEvidenceNone,
	}
	if !reflect.DeepEqual(lookup.queries, []entity.DirectoryQuery{wantQuery}) {
		t.Fatalf("Search() did not receive only the source-grounded name query")
	}
	if len(repository.persisted) != 1 ||
		repository.persisted[0].Query != wantQuery {
		t.Fatalf("Persist() did not receive only the name-query result")
	}
}

func TestServiceRetriesRateLimitThenPersistsMatch(t *testing.T) {
	providerDelay := 2 * time.Minute
	repository := &fakeDirectoryRepository{
		work: Workset{Mentions: []PendingMention{pendingEmailMention()}},
		identity: IdentityState{
			Snapshots: []entity.EntitySnapshot{{
				ID:   "person-1",
				Kind: entity.KindPerson,
				Aliases: []entity.Alias{{
					Type:  entity.AliasTypeEmail,
					Value: "riya.chen@corp.example",
				}},
			}},
		},
		persistResults: []PersistResult{{AutoResolved: true}},
	}
	lookup := &fakeDirectoryLookup{
		results: []LookupResult{
			{Outcome: entity.DirectoryRateLimited, RetryAfter: providerDelay},
			{Profiles: []entity.DirectoryProfile{matchedDirectoryProfile()}},
		},
	}
	var waits []time.Duration
	service := newTestDirectoryService(repository, lookup)
	service.Wait = func(_ context.Context, delay time.Duration) error {
		waits = append(waits, delay)
		return nil
	}

	summary, err := service.Enrich(context.Background(), "derivation-1")

	if err != nil {
		t.Fatalf("Enrich() error = %v", err)
	}
	requireSummary(t, summary, Summary{Attempted: 1, Matched: 1})
	if !reflect.DeepEqual(waits, []time.Duration{providerDelay}) {
		t.Fatalf("Wait() delays = %v, want [%v]", waits, providerDelay)
	}
	if lookup.calls != 2 {
		t.Fatalf("Search() calls = %d, want 2", lookup.calls)
	}
	if len(repository.persisted) != 1 || repository.persisted[0].AttemptCount != 2 {
		t.Fatalf("Persist() attempts = %#v, want one terminal attempt with count 2", repository.persisted)
	}
}

func TestServiceDoesNotRetryUnauthorizedOrInvalidResponse(t *testing.T) {
	for _, outcome := range []entity.DirectoryOutcome{
		entity.DirectoryUnauthorized,
		entity.DirectoryInvalidResponse,
	} {
		t.Run(string(outcome), func(t *testing.T) {
			repository := &fakeDirectoryRepository{
				work: Workset{Mentions: []PendingMention{pendingEmailMention()}},
			}
			lookup := &fakeDirectoryLookup{
				results: []LookupResult{{Outcome: outcome}},
			}
			waitCalls := 0
			service := newTestDirectoryService(repository, lookup)
			service.Wait = func(context.Context, time.Duration) error {
				waitCalls++
				return nil
			}

			summary, err := service.Enrich(context.Background(), "derivation-1")

			if err != nil {
				t.Fatalf("Enrich() error = %v", err)
			}
			requireSummary(t, summary, Summary{Attempted: 1, Unavailable: 1})
			if lookup.calls != 1 || waitCalls != 0 {
				t.Fatalf("retry calls = search:%d wait:%d, want search:1 wait:0", lookup.calls, waitCalls)
			}
			if len(repository.persisted) != 1 || repository.persisted[0].AttemptCount != 1 {
				t.Fatalf("Persist() inputs = %#v, want one attempt", repository.persisted)
			}
		})
	}
}

func TestServiceBoundsTransientRetriesAndUsesConfiguredFallbackDelay(t *testing.T) {
	repository := &fakeDirectoryRepository{
		work: Workset{Mentions: []PendingMention{pendingEmailMention()}},
	}
	lookup := &fakeDirectoryLookup{
		results: []LookupResult{
			{Outcome: entity.DirectoryUnavailable},
			{Outcome: entity.DirectoryRateLimited},
			{Outcome: entity.DirectoryUnavailable},
			{Outcome: entity.DirectoryNoMatch},
		},
	}
	var waits []time.Duration
	service := newTestDirectoryService(repository, lookup)
	service.MaxAttempts = 3
	service.RetryAfter = 45 * time.Second
	service.Wait = func(_ context.Context, delay time.Duration) error {
		waits = append(waits, delay)
		return nil
	}

	summary, err := service.Enrich(context.Background(), "derivation-1")

	if err != nil {
		t.Fatalf("Enrich() error = %v", err)
	}
	requireSummary(t, summary, Summary{Attempted: 1, Unavailable: 1})
	if lookup.calls != 3 {
		t.Fatalf("Search() calls = %d, want 3", lookup.calls)
	}
	if !reflect.DeepEqual(waits, []time.Duration{45 * time.Second, 45 * time.Second}) {
		t.Fatalf("Wait() delays = %v, want configured fallback delays", waits)
	}
	if len(repository.persisted) != 1 || repository.persisted[0].AttemptCount != 3 {
		t.Fatalf("Persist() inputs = %#v, want one terminal attempt with count 3", repository.persisted)
	}
	wantRetryAfter := service.Now().UTC().Add(service.RetryAfter)
	if repository.persisted[0].RetryAfter == nil || !repository.persisted[0].RetryAfter.Equal(wantRetryAfter) {
		t.Fatalf("Persist() RetryAfter = %v, want %v", repository.persisted[0].RetryAfter, wantRetryAfter)
	}
}

func TestServicePersistsTerminalRetryScheduleFromCompletionTime(t *testing.T) {
	started := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	completed := started.Add(5 * time.Minute)
	repository := &fakeDirectoryRepository{
		work: Workset{Mentions: []PendingMention{pendingEmailMention()}},
	}
	lookup := &fakeDirectoryLookup{
		results: []LookupResult{{Outcome: entity.DirectoryUnavailable}},
	}
	service := newTestDirectoryService(repository, lookup)
	service.MaxAttempts = 1
	service.RetryAfter = time.Minute
	times := []time.Time{started, started, completed}
	service.Now = func() time.Time {
		value := times[0]
		times = times[1:]
		return value
	}

	if _, err := service.Enrich(context.Background(), "derivation-1"); err != nil {
		t.Fatalf("Enrich() error = %v", err)
	}

	if len(repository.persisted) != 1 {
		t.Fatalf("Persist() calls = %d, want 1", len(repository.persisted))
	}
	input := repository.persisted[0]
	if !input.RecordedAt.Equal(completed) {
		t.Fatalf("Persist() RecordedAt = %v, want completion time %v", input.RecordedAt, completed)
	}
	wantRetryAfter := completed.Add(service.RetryAfter)
	if input.RetryAfter == nil || !input.RetryAfter.Equal(wantRetryAfter) {
		t.Fatalf("Persist() RetryAfter = %v, want %v", input.RetryAfter, wantRetryAfter)
	}
}

func TestServiceReusesFreshDurableAttemptsWithoutNetwork(t *testing.T) {
	repository := &fakeDirectoryRepository{
		work: Workset{Reused: 3},
	}
	lookup := &fakeDirectoryLookup{}
	service := newTestDirectoryService(repository, lookup)

	summary, err := service.Enrich(context.Background(), "derivation-1")

	if err != nil {
		t.Fatalf("Enrich() error = %v", err)
	}
	requireSummary(t, summary, Summary{Reused: 3})
	if lookup.calls != 0 || repository.calls.persist != 0 {
		t.Fatalf("network/persistence calls = search:%d persist:%d, want none", lookup.calls, repository.calls.persist)
	}
	if repository.calls.loadIdentity != 1 {
		t.Fatalf("LoadIdentityState() calls = %d, want 1", repository.calls.loadIdentity)
	}
}

func TestServicePersistsOneTerminalBoundedOutcome(t *testing.T) {
	for _, outcome := range []entity.DirectoryOutcome{
		entity.DirectoryNoMatch,
		entity.DirectoryUnauthorized,
		entity.DirectoryForbidden,
		entity.DirectoryInvalidResponse,
		entity.DirectoryResultLimitExceeded,
	} {
		t.Run(string(outcome), func(t *testing.T) {
			repository := &fakeDirectoryRepository{
				work: Workset{Mentions: []PendingMention{pendingEmailMention()}},
			}
			lookup := &fakeDirectoryLookup{
				results: []LookupResult{{Outcome: outcome}},
			}
			service := newTestDirectoryService(repository, lookup)

			if _, err := service.Enrich(context.Background(), "derivation-1"); err != nil {
				t.Fatalf("Enrich() error = %v", err)
			}
			if len(repository.persisted) != 1 {
				t.Fatalf("Persist() calls = %d, want 1", len(repository.persisted))
			}
			input := repository.persisted[0]
			if input.Lookup.Outcome != outcome || input.AttemptCount != 1 || input.RecordedAt.IsZero() {
				t.Fatalf("Persist() terminal input = %#v", input)
			}
			wantEvaluation := entity.DirectoryEvaluation{}
			if outcome == entity.DirectoryNoMatch {
				wantEvaluation.Outcome = entity.DirectoryNoMatch
			}
			if !reflect.DeepEqual(input.Evaluation, wantEvaluation) {
				t.Fatalf("Persist() evaluation = %#v, want %#v", input.Evaluation, wantEvaluation)
			}
		})
	}
}

func TestServiceReportsStorageDowngradeAsReview(t *testing.T) {
	repository := &fakeDirectoryRepository{
		work: Workset{Mentions: []PendingMention{pendingEmailMention()}},
		persistResults: []PersistResult{{
			AutoResolved: false,
		}},
	}
	lookup := &fakeDirectoryLookup{
		results: []LookupResult{{Profiles: []entity.DirectoryProfile{matchedDirectoryProfile()}}},
	}
	service := newTestDirectoryService(repository, lookup)

	summary, err := service.Enrich(context.Background(), "derivation-1")

	if err != nil {
		t.Fatalf("Enrich() error = %v", err)
	}
	requireSummary(t, summary, Summary{Attempted: 1, Review: 1})
}

func TestServiceContinuesAfterOneMentionFails(t *testing.T) {
	first := pendingEmailMention()
	second := pendingEmailMention()
	second.MentionID = "mention-2"
	second.ProposalID = "proposal-2"
	repository := &fakeDirectoryRepository{
		work:        Workset{Mentions: []PendingMention{first, second}},
		persistErrs: []error{errPrivateDirectoryDetail, nil},
	}
	lookup := &fakeDirectoryLookup{
		results: []LookupResult{
			{Outcome: entity.DirectoryNoMatch},
			{Outcome: entity.DirectoryNoMatch},
		},
	}
	service := newTestDirectoryService(repository, lookup)

	summary, err := service.Enrich(context.Background(), "derivation-1")

	if err != nil {
		t.Fatalf("Enrich() error = %v, want fail-soft continuation", err)
	}
	requireSummary(t, summary, Summary{Attempted: 2, NoMatch: 1, Unavailable: 1})
	if lookup.calls != 2 || repository.calls.persist != 2 {
		t.Fatalf("calls = search:%d persist:%d, want 2 each", lookup.calls, repository.calls.persist)
	}
}

func TestServiceCancellationDuringBackoffStopsImmediately(t *testing.T) {
	repository := &fakeDirectoryRepository{
		work: Workset{Mentions: []PendingMention{pendingEmailMention()}},
	}
	lookup := &fakeDirectoryLookup{
		results: []LookupResult{
			{Outcome: entity.DirectoryRateLimited},
			{Outcome: entity.DirectoryNoMatch},
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	waitCalls := 0
	service := newTestDirectoryService(repository, lookup)
	service.Wait = func(waitCtx context.Context, _ time.Duration) error {
		waitCalls++
		cancel()
		return waitCtx.Err()
	}

	summary, err := service.Enrich(ctx, "derivation-1")

	if err != context.Canceled {
		t.Fatalf("Enrich() error = %v, want canonical context.Canceled", err)
	}
	requireSummary(t, summary, Summary{Attempted: 1})
	if waitCalls != 1 || lookup.calls != 1 || repository.calls.persist != 0 {
		t.Fatalf(
			"calls after cancellation = wait:%d search:%d persist:%d, want 1, 1, 0",
			waitCalls,
			lookup.calls,
			repository.calls.persist,
		)
	}
}

func TestServiceCancellationDuringPersistenceReturnsCanonicalError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	repository := &fakeDirectoryRepository{
		work:          Workset{Mentions: []PendingMention{pendingEmailMention()}},
		beforePersist: cancel,
	}
	lookup := &fakeDirectoryLookup{
		results: []LookupResult{{Outcome: entity.DirectoryNoMatch}},
	}
	service := newTestDirectoryService(repository, lookup)

	summary, err := service.Enrich(ctx, "derivation-1")

	if err != context.Canceled {
		t.Fatalf("Enrich() error = %v, want canonical context.Canceled", err)
	}
	requireSummary(t, summary, Summary{Attempted: 1})
}

func TestServiceStopsBeforeLoadIdentityWhenWorkLoadCancels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	repository := &fakeDirectoryRepository{afterLoadWork: cancel}
	service := newTestDirectoryService(repository, &fakeDirectoryLookup{})

	_, err := service.Enrich(ctx, "derivation-1")

	if err != context.Canceled {
		t.Fatalf("Enrich() error = %v, want canonical context.Canceled", err)
	}
	if repository.calls.loadIdentity != 0 {
		t.Fatalf("LoadIdentityState() calls = %d, want 0", repository.calls.loadIdentity)
	}
}

func TestServiceStopsBeforeLookupWhenIdentityLoadCancels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	repository := &fakeDirectoryRepository{
		work:              Workset{Mentions: []PendingMention{pendingEmailMention()}},
		afterLoadIdentity: cancel,
	}
	lookup := &fakeDirectoryLookup{}
	service := newTestDirectoryService(repository, lookup)

	_, err := service.Enrich(ctx, "derivation-1")

	if err != context.Canceled {
		t.Fatalf("Enrich() error = %v, want canonical context.Canceled", err)
	}
	if lookup.calls != 0 {
		t.Fatalf("Search() calls = %d, want 0", lookup.calls)
	}
}

func TestServiceStopsBeforePersistWhenContextCancelsAfterLookup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	repository := &fakeDirectoryRepository{
		work: Workset{Mentions: []PendingMention{pendingEmailMention()}},
	}
	lookup := &fakeDirectoryLookup{
		results: []LookupResult{{Outcome: entity.DirectoryNoMatch}},
	}
	service := newTestDirectoryService(repository, lookup)
	nowCalls := 0
	service.Now = func() time.Time {
		nowCalls++
		if nowCalls == 3 {
			cancel()
		}
		return time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	}

	_, err := service.Enrich(ctx, "derivation-1")

	if err != context.Canceled {
		t.Fatalf("Enrich() error = %v, want canonical context.Canceled", err)
	}
	if repository.calls.persist != 0 {
		t.Fatalf("Persist() calls = %d, want 0", repository.calls.persist)
	}
}

func TestServiceCancellationAfterSuccessfulWaitStopsBeforeRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	repository := &fakeDirectoryRepository{
		work: Workset{Mentions: []PendingMention{pendingEmailMention()}},
	}
	lookup := &fakeDirectoryLookup{
		results: []LookupResult{
			{Outcome: entity.DirectoryRateLimited},
			{Outcome: entity.DirectoryNoMatch},
		},
	}
	service := newTestDirectoryService(repository, lookup)
	service.Wait = func(context.Context, time.Duration) error {
		cancel()
		return nil
	}

	_, err := service.Enrich(ctx, "derivation-1")

	if err != context.Canceled {
		t.Fatalf("Enrich() error = %v, want canonical context.Canceled", err)
	}
	if lookup.calls != 1 {
		t.Fatalf("Search() calls = %d, want 1", lookup.calls)
	}
	if repository.calls.persist != 0 {
		t.Fatalf("Persist() calls = %d, want 0", repository.calls.persist)
	}
}

func TestServicePreCanceledContextStopsBeforeRepositoryCalls(t *testing.T) {
	repository := &fakeDirectoryRepository{
		work: Workset{Mentions: []PendingMention{pendingEmailMention()}},
	}
	lookup := &fakeDirectoryLookup{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	service := newTestDirectoryService(repository, lookup)
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	service.Tracer = provider.Tracer("directory-service-test")

	summary, err := service.Enrich(ctx, "derivation-1")

	if err != context.Canceled {
		t.Fatalf("Enrich() error = %v, want canonical context.Canceled", err)
	}
	if summary != (Summary{}) {
		t.Fatalf("Enrich() summary = %#v, want zero", summary)
	}
	if repository.calls != (repositoryCallCounts{}) || lookup.calls != 0 {
		t.Fatalf("calls after pre-cancellation = repository:%#v search:%d", repository.calls, lookup.calls)
	}
	requireSingleSpanStatus(t, exporter, codes.Error)
}

func TestServiceMissingDependenciesEndsOKSpan(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	service := Service{
		Enabled: true,
		Tracer:  provider.Tracer("directory-service-test"),
	}

	summary, err := service.Enrich(context.Background(), "derivation-1")

	if err != nil {
		t.Fatalf("Enrich() error = %v", err)
	}
	requireSummary(t, summary, Summary{Unavailable: 1})
	requireSingleSpanStatus(t, exporter, codes.Ok)
}

func TestServiceDecisionRecorderCancellationIsCanonical(t *testing.T) {
	tests := []struct {
		name      string
		recorder  *recordingDirectoryDecisions
		ctx       context.Context
		wantError error
	}{
		{
			name: "recorder error",
			recorder: &recordingDirectoryDecisions{
				err: errors.Join(errPrivateDirectoryDetail, context.DeadlineExceeded),
			},
			ctx:       context.Background(),
			wantError: context.DeadlineExceeded,
		},
		{
			name:      "context canceled after record",
			recorder:  &recordingDirectoryDecisions{},
			wantError: context.Canceled,
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := testCase.ctx
			if ctx == nil {
				cancelCtx, cancel := context.WithCancel(context.Background())
				ctx = cancelCtx
				testCase.recorder.afterRecord = cancel
			}
			repository := &fakeDirectoryRepository{
				work: Workset{Mentions: []PendingMention{pendingEmailMention()}},
			}
			lookup := &fakeDirectoryLookup{
				results: []LookupResult{{Outcome: entity.DirectoryNoMatch}},
			}
			exporter := tracetest.NewInMemoryExporter()
			provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
			service := newTestDirectoryService(repository, lookup)
			service.Tracer = provider.Tracer("directory-service-test")
			service.Decisions = testCase.recorder

			_, err := service.Enrich(ctx, "derivation-1")

			if err != testCase.wantError {
				t.Fatalf("Enrich() error = %v, want canonical %v", err, testCase.wantError)
			}
			requireSingleSpanStatus(t, exporter, codes.Error)
		})
	}
}

func TestServiceDecisionRecorderNonContextFailureIsFailSoft(t *testing.T) {
	repository := &fakeDirectoryRepository{
		work: Workset{Mentions: []PendingMention{pendingEmailMention()}},
	}
	lookup := &fakeDirectoryLookup{
		results: []LookupResult{{Outcome: entity.DirectoryNoMatch}},
	}
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	service := newTestDirectoryService(repository, lookup)
	service.Tracer = provider.Tracer("directory-service-test")
	service.Decisions = &recordingDirectoryDecisions{err: errPrivateDirectoryDetail}

	summary, err := service.Enrich(context.Background(), "derivation-1")

	if err != nil {
		t.Fatalf("Enrich() error = %v, want fail-soft telemetry error", err)
	}
	requireSummary(t, summary, Summary{Attempted: 1, NoMatch: 1})
	requireSingleSpanStatus(t, exporter, codes.Ok)
}

func TestServiceDiscardsErroredLookupResultFromTelemetryAndPersistence(t *testing.T) {
	const privateMarker = "private-provider-marker-42f1"
	for _, testCase := range []struct {
		name        string
		lookupError error
		wantError   error
		wantPersist int
		wantStatus  codes.Code
	}{
		{
			name:        "provider failure",
			lookupError: errors.New(privateMarker),
			wantPersist: 1,
			wantStatus:  codes.Ok,
		},
		{
			name:        "provider cancellation",
			lookupError: errors.Join(errors.New(privateMarker), context.Canceled),
			wantError:   context.Canceled,
			wantStatus:  codes.Error,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			repository := &fakeDirectoryRepository{
				work: Workset{Mentions: []PendingMention{pendingEmailMention()}},
			}
			lookup := &fakeDirectoryLookup{
				results: []LookupResult{{
					Outcome: entity.DirectoryOutcome(privateMarker),
					Profiles: []entity.DirectoryProfile{{
						Provider:    privateMarker,
						SubjectID:   privateMarker,
						DisplayName: privateMarker,
					}},
				}},
				errors: []error{testCase.lookupError},
			}
			exporter := tracetest.NewInMemoryExporter()
			provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
			decisions := &recordingDirectoryDecisions{}
			service := newTestDirectoryService(repository, lookup)
			service.MaxAttempts = 1
			service.Tracer = provider.Tracer("directory-service-test")
			service.Decisions = decisions

			_, err := service.Enrich(context.Background(), "derivation-1")

			if err != testCase.wantError {
				t.Fatalf("Enrich() error = %v, want %v", err, testCase.wantError)
			}
			if repository.calls.persist != testCase.wantPersist {
				t.Fatalf("Persist() calls = %d, want %d", repository.calls.persist, testCase.wantPersist)
			}
			if testCase.wantPersist == 1 {
				input := repository.persisted[0]
				if input.Lookup.Outcome != entity.DirectoryUnavailable ||
					len(input.Lookup.Profiles) != 0 {
					t.Fatalf("Persist() did not receive a normalized unavailable outcome")
				}
			}
			if len(decisions.observations) != 1 ||
				decisions.observations[0].Outcome != string(entity.DirectoryUnavailable) {
				t.Fatalf("decision did not receive a normalized unavailable outcome")
			}
			requireSingleSpanStatus(t, exporter, testCase.wantStatus)
			telemetry := fmt.Sprintf("%#v %#v", decisions.observations, exporter.GetSpans())
			if strings.Contains(telemetry, privateMarker) {
				t.Fatalf("telemetry contains a provider-controlled private marker")
			}
		})
	}
}

func TestServiceRecordsPrivacySafeDecisionAndExplicitOKSpan(t *testing.T) {
	const (
		privateDerivation = "private-derivation-9d83"
		privateEmail      = "private.person@corp.example"
		privateMentionID  = "private-mention-7a62"
	)
	mention := pendingEmailMention()
	mention.MentionID = privateMentionID
	mention.ProposedEmail = privateEmail
	mention.EmailQuote = privateEmail
	repository := &fakeDirectoryRepository{
		work: Workset{Mentions: []PendingMention{mention}},
	}
	lookup := &fakeDirectoryLookup{
		results: []LookupResult{{Outcome: entity.DirectoryNoMatch}},
	}
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	decisions := &recordingDirectoryDecisions{}
	service := newTestDirectoryService(repository, lookup)
	service.Tracer = provider.Tracer("directory-service-test")
	service.Decisions = decisions

	summary, err := service.Enrich(context.Background(), privateDerivation)

	if err != nil {
		t.Fatalf("Enrich() error = %v", err)
	}
	requireSummary(t, summary, Summary{Attempted: 1, NoMatch: 1})
	if len(decisions.observations) != 1 {
		t.Fatalf("decision count = %d, want 1", len(decisions.observations))
	}
	observation := decisions.observations[0]
	if observation.Name != "directory_lookup" ||
		observation.Outcome != string(entity.DirectoryNoMatch) ||
		observation.InputSize != 1 ||
		observation.OutputSize != 0 {
		t.Fatalf("decision observation = %#v, want bounded lookup telemetry", observation)
	}

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("span count = %d, want 1", len(spans))
	}
	if spans[0].Name != "stacks.directory.enrich" {
		t.Fatalf("span name = %q, want stacks.directory.enrich", spans[0].Name)
	}
	if spans[0].Status.Code != codes.Ok {
		t.Fatalf("span status = %v, want OK", spans[0].Status.Code)
	}
	telemetry := fmt.Sprintf("%#v %#v", observation, spans[0])
	for _, privateValue := range []string{
		privateDerivation,
		privateEmail,
		privateMentionID,
		mention.ProposalID,
	} {
		if strings.Contains(telemetry, privateValue) {
			t.Fatalf("telemetry contains a private query value or ID")
		}
	}
}

type recordingDirectoryDecisions struct {
	observations []observability.DecisionObservation
	err          error
	afterRecord  func()
}

func (recorder *recordingDirectoryDecisions) Record(
	_ context.Context,
	observation observability.DecisionObservation,
) error {
	recorder.observations = append(recorder.observations, observation)
	if recorder.afterRecord != nil {
		recorder.afterRecord()
	}
	return recorder.err
}

type repositoryCallCounts struct {
	loadWork     int
	loadIdentity int
	persist      int
}

type fakeDirectoryRepository struct {
	work              Workset
	identity          IdentityState
	persisted         []PersistInput
	loadWorkErr       error
	identityErr       error
	persistErr        error
	persistErrs       []error
	persistResults    []PersistResult
	afterLoadWork     func()
	afterLoadIdentity func()
	beforePersist     func()
	calls             repositoryCallCounts
}

func (repository *fakeDirectoryRepository) LoadWork(
	context.Context,
	string,
	time.Time,
	time.Duration,
	time.Duration,
) (Workset, error) {
	repository.calls.loadWork++
	if repository.afterLoadWork != nil {
		repository.afterLoadWork()
	}
	return repository.work, repository.loadWorkErr
}

func (repository *fakeDirectoryRepository) LoadIdentityState(context.Context) (IdentityState, error) {
	repository.calls.loadIdentity++
	if repository.afterLoadIdentity != nil {
		repository.afterLoadIdentity()
	}
	return repository.identity, repository.identityErr
}

func (repository *fakeDirectoryRepository) Persist(_ context.Context, input PersistInput) (PersistResult, error) {
	index := repository.calls.persist
	repository.calls.persist++
	repository.persisted = append(repository.persisted, input)
	if repository.beforePersist != nil {
		repository.beforePersist()
	}
	if index < len(repository.persistErrs) {
		err := repository.persistErrs[index]
		if err != nil {
			return PersistResult{}, err
		}
	}
	if index < len(repository.persistResults) {
		return repository.persistResults[index], nil
	}
	if repository.persistErr != nil {
		err := repository.persistErr
		return PersistResult{}, err
	}
	return PersistResult{}, nil
}

type fakeDirectoryLookup struct {
	results []LookupResult
	errors  []error
	queries []entity.DirectoryQuery
	calls   int
}

func (lookup *fakeDirectoryLookup) Search(_ context.Context, query entity.DirectoryQuery) (LookupResult, error) {
	index := lookup.calls
	lookup.calls++
	lookup.queries = append(lookup.queries, query)
	var result LookupResult
	if index < len(lookup.results) {
		result = lookup.results[index]
	}
	var err error
	if index < len(lookup.errors) {
		err = lookup.errors[index]
	}
	return result, err
}

func newTestDirectoryService(repository Repository, lookup Lookup) Service {
	policy, err := entity.NewDirectoryPolicy([]string{"corp.example"})
	if err != nil {
		panic(err)
	}
	return Service{
		Lookup:      lookup,
		Repository:  repository,
		Policy:      policy,
		Enabled:     true,
		Freshness:   24 * time.Hour,
		RetryAfter:  time.Minute,
		MaxAttempts: 3,
		Now:         func() time.Time { return time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC) },
		Wait:        func(context.Context, time.Duration) error { return nil },
	}
}

func pendingEmailMention() PendingMention {
	return PendingMention{
		MentionID:      "mention-1",
		ProposalID:     "proposal-1",
		Surface:        "Riya Chen",
		NormalizedName: "riya chen",
		ProposedEmail:  "riya.chen@corp.example",
		NameQuote:      "Riya Chen",
		EmailQuote:     "Riya Chen <riya.chen@corp.example>",
	}
}

func matchedDirectoryProfile() entity.DirectoryProfile {
	return entity.DirectoryProfile{
		Provider:    "google_people",
		SubjectID:   "people/riya",
		Source:      entity.DirectorySourceDomainProfile,
		DisplayName: "Riya Chen",
		Emails: []entity.DirectoryEmail{{
			Value:   "riya.chen@corp.example",
			Primary: true,
		}},
	}
}

func requireSummary(t *testing.T, got, want Summary) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Enrich() summary = %#v, want %#v", got, want)
	}
}

func requireSingleSpanStatus(t *testing.T, exporter *tracetest.InMemoryExporter, want codes.Code) {
	t.Helper()
	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("span count = %d, want 1", len(spans))
	}
	if spans[0].Name != "stacks.directory.enrich" || spans[0].Status.Code != want {
		t.Fatalf("span = %q status %v, want stacks.directory.enrich status %v", spans[0].Name, spans[0].Status.Code, want)
	}
}
