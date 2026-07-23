package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"stacks/internal/entity"
	"stacks/internal/extract"
	"stacks/internal/knowledge"
	"stacks/internal/modelpolicy"
	"stacks/internal/observability"
	"stacks/internal/source"
)

var recordedAt = time.Date(2026, 7, 21, 14, 0, 0, 0, time.UTC)

func TestSyncBoundsSourceListFailureBeforeErrorTelemetryAndLogging(t *testing.T) {
	const privateValue = "SECRET meeting title https://private.invalid/transcript"
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	service := testServiceWithSource(
		&memorySource{listErr: errors.New(privateValue)},
		newMemoryRepository(),
		&recordingModel{},
	)
	service.Tracer = provider.Tracer("synthetic")

	_, err := service.Sync(context.Background())
	if !errors.Is(err, ErrSourceList) {
		t.Fatalf("Sync() error = %v, want bounded source-list error", err)
	}
	if strings.Contains(err.Error(), privateValue) {
		t.Fatalf("Sync() error disclosed private source text: %v", err)
	}
	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("span count = %d, want 1", len(spans))
	}
	if strings.Contains(renderRecordedSpan(spans[0]), privateValue) {
		t.Fatal("recorded span disclosed private source text")
	}
	core, observed := observer.New(zap.ErrorLevel)
	zap.New(core).Error("run stacks", zap.Error(err))
	if strings.Contains(observed.All()[0].ContextMap()["error"].(string), privateValue) {
		t.Fatal("logger path disclosed private source text")
	}
}

func TestSyncPreservesBoundedCancellationFromSourceList(t *testing.T) {
	const privateValue = "private source cancellation detail"
	service := testServiceWithSource(
		&memorySource{listErr: fmt.Errorf("%s: %w", privateValue, context.Canceled)},
		newMemoryRepository(),
		&recordingModel{},
	)

	summary, err := service.Sync(context.Background())
	if !errors.Is(err, context.Canceled) || errors.Is(err, ErrSourceList) {
		t.Fatalf("Sync() error = %v, want context cancellation", err)
	}
	if err.Error() != context.Canceled.Error() || strings.Contains(err.Error(), privateValue) {
		t.Fatalf("Sync() error = %q, want bounded cancellation sentinel", err.Error())
	}
	if len(summary.Results) != 0 {
		t.Fatalf("summary results = %#v, want none", summary.Results)
	}
}

func TestSyncPreservesCancellationDuringDocumentProcessing(t *testing.T) {
	const privateValue = "private dependency cancellation detail"
	wrappedCancellation := fmt.Errorf("%s: %w", privateValue, context.DeadlineExceeded)
	tests := []struct {
		name  string
		setup func(*memorySource, *memoryRepository, *recordingModel)
	}{
		{name: "source get", setup: func(sourceBoundary *memorySource, _ *memoryRepository, _ *recordingModel) {
			sourceBoundary.getErrs = map[string]error{"document-canceled": wrappedCancellation}
		}},
		{name: "prepare version", setup: func(_ *memorySource, repository *memoryRepository, _ *recordingModel) {
			repository.prepareErr = wrappedCancellation
		}},
		{name: "model", setup: func(_ *memorySource, _ *memoryRepository, model *recordingModel) {
			model.errs = []error{wrappedCancellation}
		}},
		{name: "entity snapshots", setup: func(_ *memorySource, repository *memoryRepository, _ *recordingModel) {
			repository.snapshotErr = wrappedCancellation
		}},
		{name: "completion", setup: func(_ *memorySource, repository *memoryRepository, _ *recordingModel) {
			repository.completionErr = wrappedCancellation
		}},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			sourceBoundary := &memorySource{
				listed:  []source.Document{{Provider: "drive", ID: "document-canceled"}},
				fetched: map[string]source.Document{"document-canceled": syntheticDocument("document-canceled", "Synthetic source.")},
			}
			repository := newMemoryRepository()
			model := &recordingModel{responses: []extract.Response{validEmptyResponse(t)}}
			testCase.setup(sourceBoundary, repository, model)
			service := testServiceWithSource(sourceBoundary, repository, model)

			summary, err := service.Sync(context.Background())
			if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrFailurePersistence) {
				t.Fatalf("Sync() error = %v, want deadline exceeded", err)
			}
			if err.Error() != context.DeadlineExceeded.Error() || strings.Contains(err.Error(), privateValue) {
				t.Fatalf("Sync() error = %q, want bounded deadline sentinel", err.Error())
			}
			if len(summary.Results) != 0 || summary.Completed != 0 || summary.Incomplete != 0 || summary.Failed != 0 {
				t.Fatalf("summary = %#v, want no classified outcome for canceled work", summary)
			}
			for _, state := range repository.versions {
				if state.Status != VersionStatusPending || state.FailureCode != "" {
					t.Fatalf("durable state = %#v, want resumable pending state", state)
				}
			}
		})
	}
}

func TestSyncPreservesEarlierSuccessWhenLaterDocumentIsCanceled(t *testing.T) {
	model := &recordingModel{
		responses: []extract.Response{validEmptyResponse(t)},
		errs:      []error{nil, context.Canceled},
	}
	repository := newMemoryRepository()
	service := testServiceWithSource(&memorySource{
		listed: []source.Document{{Provider: "drive", ID: "document-complete"}, {Provider: "drive", ID: "document-canceled"}},
		fetched: map[string]source.Document{
			"document-complete": syntheticDocument("document-complete", "Completed synthetic source."),
			"document-canceled": syntheticDocument("document-canceled", "Canceled synthetic source."),
		},
	}, repository, model)

	summary, err := service.Sync(context.Background())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Sync() error = %v, want context cancellation", err)
	}
	if len(summary.Results) != 1 || summary.Completed != 1 || summary.Results[0].DocumentID != "document-complete" {
		t.Fatalf("summary = %#v, want only the earlier completed outcome", summary)
	}
}

func TestSyncPreservesCancellationWhenFailureStateCannotBeRecorded(t *testing.T) {
	malformed := extract.Response{Output: json.RawMessage(`{"unexpected":true}`), ModelID: "synthetic-model", PromptVersion: extract.ExtractionPromptVersion}
	repository := newMemoryRepository()
	repository.failureRecordErr = fmt.Errorf("private persistence detail: %w", context.Canceled)
	service := testService(syntheticDocument("document-canceled-failure", "Synthetic source."), repository, &recordingModel{responses: []extract.Response{malformed}})

	summary, err := service.Sync(context.Background())
	if !errors.Is(err, context.Canceled) || errors.Is(err, ErrFailurePersistence) {
		t.Fatalf("Sync() error = %v, want context cancellation", err)
	}
	if err.Error() != context.Canceled.Error() {
		t.Fatalf("Sync() error = %q, want bounded cancellation sentinel", err.Error())
	}
	if len(summary.Results) != 0 {
		t.Fatalf("summary results = %#v, want no misleading failure outcome", summary.Results)
	}
	for _, state := range repository.versions {
		if state.Status != VersionStatusPending || state.FailureCode != "" {
			t.Fatalf("durable state = %#v, want resumable pending state", state)
		}
	}
}

func TestSyncSkipsExtractionForUnchangedVersion(t *testing.T) {
	document := syntheticDocument("document-unchanged", "Leader assigns follow-up.")
	repository := newMemoryRepository()
	version := documentVersion(t, document)
	derivation := testDerivationIdentity(t, version)
	repository.versions[derivationKey(version, derivation)] = VersionState{
		ID: "version-unchanged", DerivationID: "derivation-unchanged",
		DerivationDigest: derivation.Digest, Status: VersionStatusComplete,
	}
	model := &recordingModel{responses: []extract.Response{validEmptyResponse(t)}}
	service := testService(document, repository, model)

	summary, err := service.Sync(context.Background())
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if model.calls != 0 {
		t.Fatalf("model calls = %d, want 0", model.calls)
	}
	if len(summary.Results) != 1 || summary.Results[0].Outcome != OutcomeUnchanged {
		t.Fatalf("summary results = %#v, want one unchanged result", summary.Results)
	}
}

func TestSyncReusesBedrockDerivationAcrossDataModesWithoutNewInvocation(t *testing.T) {
	document := syntheticDocument("document-data-mode-cache", "Leader assigns follow-up.")
	repository := newMemoryRepository()
	model := &recordingModel{responses: []extract.Response{validEmptyResponse(t)}}
	service := testService(document, repository, model)

	first, err := service.Sync(context.Background())
	if err != nil || first.Completed != 1 {
		t.Fatalf("first Sync() = (%#v, %v), want one completed derivation", first, err)
	}
	if repository.lastCompletion.DataMode != modelpolicy.DataModePersonal {
		t.Fatalf("completion data mode = %q, want %q", repository.lastCompletion.DataMode, modelpolicy.DataModePersonal)
	}
	firstDerivationID := first.Results[0].DerivationID
	service.DataMode = modelpolicy.DataModeRestricted

	second, err := service.Sync(context.Background())
	if err != nil || second.Unchanged != 1 {
		t.Fatalf("second Sync() = (%#v, %v), want cached unchanged derivation", second, err)
	}
	if second.Results[0].DerivationID != firstDerivationID || model.calls != 1 || repository.completionCalls != 1 {
		t.Fatalf("derivation/model/completion = %q/%d/%d, want same derivation and no new disclosure", second.Results[0].DerivationID, model.calls, repository.completionCalls)
	}
}

func TestSyncRejectsProviderRegionPolicyBeforeSourceAccess(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Service)
	}{
		{"missing provider", func(service *Service) { service.Provider = "" }},
		{"bedrock missing region", func(service *Service) { service.Region = "" }},
		{"bedrock padded region", func(service *Service) { service.Region = " us-east-1 " }},
		{"openai region", func(service *Service) {
			service.Provider = modelpolicy.ProviderOpenAI
			service.Region = "us-east-1"
		}},
		{"restricted direct provider", func(service *Service) {
			service.Provider = modelpolicy.ProviderAnthropic
			service.DataMode = modelpolicy.DataModeRestricted
			service.Region = ""
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			model := &recordingModel{}
			service := testService(syntheticDocument("document-invalid-provider-policy", "Synthetic source."), newMemoryRepository(), model)
			sourceBoundary := service.Source.(*memorySource)
			test.mutate(service)

			if _, err := service.Sync(context.Background()); err == nil {
				t.Fatal("Sync() error = nil, want provider policy rejection")
			}
			if sourceBoundary.listCalls != 0 || model.calls != 0 {
				t.Fatalf("source/model calls = %d/%d, want validation before boundaries", sourceBoundary.listCalls, model.calls)
			}
		})
	}
}

func TestSyncReturnsBusyWithoutInvokingModelForActiveDerivationLease(t *testing.T) {
	document := syntheticDocument("document-busy", "Leader assigns follow-up.")
	repository := newMemoryRepository()
	version := documentVersion(t, document)
	derivation := testDerivationIdentity(t, version)
	repository.versions[derivationKey(version, derivation)] = VersionState{
		ID: "version-busy", DerivationID: "derivation-busy", DerivationDigest: derivation.Digest,
		Status: VersionStatusPending, LeaseOwner: "other-worker",
	}
	model := &recordingModel{responses: []extract.Response{validEmptyResponse(t)}}
	service := testService(document, repository, model)

	summary, err := service.Sync(context.Background())
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if model.calls != 0 || len(summary.Results) != 1 || summary.Results[0].Outcome != OutcomeIncomplete || summary.Results[0].FailureCode != FailureBusy {
		t.Fatalf("model/results = %d/%#v, want bounded busy without model invocation", model.calls, summary.Results)
	}
}

func TestSyncCancelsAttemptBeforeLeaseExpiryAndReleasesClaim(t *testing.T) {
	document := syntheticDocument("document-attempt-deadline", "Leader assigns follow-up.")
	repository := newMemoryRepository()
	model := &deadlineBlockingModel{}
	service := testServiceWithSource(&memorySource{
		listed:  []source.Document{{Provider: "drive", ID: document.ID}},
		fetched: map[string]source.Document{document.ID: document},
	}, repository, model)
	service.LeaseDuration = 6 * time.Second
	service.AttemptTimeout = 50 * time.Millisecond

	parent, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	summary, err := service.Sync(parent)
	if err != nil {
		t.Fatalf("Sync() error = %v, want bounded per-document timeout", err)
	}
	if model.calls != 1 || model.deadline.IsZero() {
		t.Fatalf("model calls/deadline = %d/%v, want one deadline-bounded call", model.calls, model.deadline)
	}
	if len(summary.Results) != 1 || summary.Results[0].Outcome != OutcomeIncomplete || summary.Results[0].FailureCode != FailureModel {
		t.Fatalf("summary = %#v, want released incomplete model failure", summary)
	}
	for _, state := range repository.versions {
		if state.Status != VersionStatusIncomplete || state.LeaseOwner != "" || !model.deadline.Before(repository.lastLeaseExpiresAt) {
			t.Fatalf("state/deadline/lease = %#v/%v/%v, want released owner and deadline before lease", state, model.deadline, repository.lastLeaseExpiresAt)
		}
	}

	service.Model = &recordingModel{responses: []extract.Response{validEmptyResponse(t)}}
	retry, err := service.Sync(context.Background())
	if err != nil || retry.Completed != 1 {
		t.Fatalf("retry Sync() = (%#v, %v), want immediate successful retry", retry, err)
	}
}

func TestAttemptContextRejectsLeaseWithoutConfiguredCleanupWindow(t *testing.T) {
	service := &Service{
		LeaseDuration:  time.Minute,
		AttemptTimeout: 20 * time.Second,
	}

	attemptCtx, cancel, err := service.attemptContext(context.Background(), VersionState{
		LeaseExpiresAt: time.Now().Add(30 * time.Second),
	})
	if cancel != nil {
		cancel()
	}
	if attemptCtx != nil || err == nil {
		t.Fatalf("attemptContext() = (%v, %v), want no attempt after the configured cleanup window", attemptCtx, err)
	}
}

func TestSyncCreatesNewVersionWhenOneTabChanges(t *testing.T) {
	repository := newMemoryRepository()
	model := &recordingModel{responses: []extract.Response{validEmptyResponse(t), validEmptyResponse(t)}}
	sourceBoundary := &memorySource{listed: []source.Document{{Provider: "drive", ID: "document-changed"}}}
	service := testServiceWithSource(sourceBoundary, repository, model)

	sourceBoundary.fetched = map[string]source.Document{
		"document-changed": syntheticDocument("document-changed", "First synthetic transcript."),
	}
	first, err := service.Sync(context.Background())
	if err != nil || first.Completed != 1 {
		t.Fatalf("first Sync() = (%#v, %v), want one completed", first, err)
	}
	sourceBoundary.fetched["document-changed"] = syntheticDocument("document-changed", "Changed synthetic transcript.")
	second, err := service.Sync(context.Background())
	if err != nil || second.Completed != 1 {
		t.Fatalf("second Sync() = (%#v, %v), want one completed", second, err)
	}
	if len(repository.versions) != 2 {
		t.Fatalf("durable version count = %d, want 2", len(repository.versions))
	}
	if model.calls != 2 {
		t.Fatalf("model calls = %d, want 2", model.calls)
	}
}

func TestSyncTreatsProviderRevisionChurnAsUnchangedContent(t *testing.T) {
	document := syntheticDocument("document-revision-churn", "Leader assigns follow-up.")
	listed := document
	listed.Tabs = nil
	listed.Revision = ""
	sourceBoundary := &memorySource{
		listed:  []source.Document{listed},
		fetched: map[string]source.Document{document.ID: document},
	}
	repository := newMemoryRepository()
	model := &recordingModel{responses: []extract.Response{validEmptyResponse(t)}}
	service := testServiceWithSource(sourceBoundary, repository, model)

	first, err := service.Sync(context.Background())
	if err != nil || first.Completed != 1 {
		t.Fatalf("first Sync() = (%#v, %v), want one completed version", first, err)
	}
	changedRevision := document
	changedRevision.Revision = "revision-2"
	sourceBoundary.fetched[document.ID] = changedRevision

	second, err := service.Sync(context.Background())
	if err != nil || second.Unchanged != 1 {
		t.Fatalf("second Sync() = (%#v, %v), want unchanged content despite revision churn", second, err)
	}
	if model.calls != 1 || len(repository.versions) != 1 {
		t.Fatalf("model calls/derivations = %d/%d, want 1/1", model.calls, len(repository.versions))
	}
}

func TestSyncAcceptsViewOnlyDocumentWithoutProviderRevision(t *testing.T) {
	document := syntheticDocument("document-view-only", "Leader assigns follow-up.")
	document.Revision = ""
	repository := newMemoryRepository()
	model := &recordingModel{responses: []extract.Response{validEmptyResponse(t)}}
	service := testService(document, repository, model)

	summary, err := service.Sync(context.Background())
	if err != nil || summary.Completed != 1 {
		t.Fatalf("Sync() = (%#v, %v), want view-only document completed without revision metadata", summary, err)
	}
	if model.calls != 1 || len(repository.versions) != 1 {
		t.Fatalf("model calls/derivations = %d/%d, want 1/1", model.calls, len(repository.versions))
	}
}

func TestSyncCreatesNewDerivationWithoutDuplicatingSourceVersionWhenConfigurationChanges(t *testing.T) {
	document := syntheticDocument("document-config-change", "Leader assigns follow-up.")
	repository := newMemoryRepository()
	model := &recordingModel{responses: []extract.Response{
		validEmptyResponse(t),
		{Output: validEmptyResponse(t).Output, ModelID: "synthetic-model-v2", PromptVersion: extract.ExtractionPromptVersion},
	}}
	service := testService(document, repository, model)
	first, err := service.Sync(context.Background())
	if err != nil {
		t.Fatalf("first Sync() error = %v", err)
	}
	service.ModelID = "synthetic-model-v2"
	second, err := service.Sync(context.Background())
	if err != nil {
		t.Fatalf("configured Sync() error = %v", err)
	}
	if model.calls != 2 || len(repository.versions) != 2 {
		t.Fatalf("model calls/derivations = %d/%d, want 2/2", model.calls, len(repository.versions))
	}
	if first.Results[0].VersionID != second.Results[0].VersionID ||
		first.Results[0].DerivationID == second.Results[0].DerivationID {
		t.Fatalf("source/derivation identities = %#v / %#v, want shared source and distinct derivations", first.Results[0], second.Results[0])
	}
}

func TestSyncContinuesAfterOneDocumentFails(t *testing.T) {
	malformed := extract.Response{Output: json.RawMessage(`{"unexpected":true}`), ModelID: "synthetic-model", PromptVersion: extract.ExtractionPromptVersion}
	model := &recordingModel{responses: []extract.Response{malformed, validEmptyResponse(t)}}
	repository := newMemoryRepository()
	service := testServiceWithSource(&memorySource{
		listed: []source.Document{{Provider: "drive", ID: "document-malformed"}, {Provider: "drive", ID: "document-valid"}},
		fetched: map[string]source.Document{
			"document-malformed": syntheticDocument("document-malformed", "Malformed model output source."),
			"document-valid":     syntheticDocument("document-valid", "Valid model output source."),
		},
	}, repository, model)

	summary, err := service.Sync(context.Background())
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if summary.Completed != 1 || summary.Failed != 1 || len(summary.Results) != 2 {
		t.Fatalf("summary = %#v, want completed=1 failed=1", summary)
	}
	if summary.Results[0].FailureCode != FailureInvalidOutput {
		t.Fatalf("failure code = %q, want %q", summary.Results[0].FailureCode, FailureInvalidOutput)
	}
	if repository.completionCalls != 1 {
		t.Fatalf("completion transactions = %d, want 1", repository.completionCalls)
	}
}

func TestSyncAbortsImmediatelyOnGlobalModelAuthenticationFailure(t *testing.T) {
	first := syntheticDocument("document-auth-first", "Leader assigns follow-up.")
	second := syntheticDocument("document-auth-second", "Leader assigns another follow-up.")
	listedFirst, listedSecond := first, second
	listedFirst.Tabs, listedSecond.Tabs = nil, nil
	listedFirst.Revision, listedSecond.Revision = "", ""
	model := &recordingModel{errs: []error{fmt.Errorf("private provider detail: %w", extract.ErrAuthentication)}}
	repository := newMemoryRepository()
	service := testServiceWithSource(&memorySource{
		listed:  []source.Document{listedFirst, listedSecond},
		fetched: map[string]source.Document{first.ID: first, second.ID: second},
	}, repository, model)

	summary, err := service.Sync(context.Background())
	if !errors.Is(err, extract.ErrAuthentication) || err.Error() != extract.ErrAuthentication.Error() {
		t.Fatalf("Sync() error = %v, want bounded global authentication sentinel", err)
	}
	if model.calls != 1 || len(summary.Results) != 0 {
		t.Fatalf("model calls/results = %d/%d, want immediate abort before per-document result", model.calls, len(summary.Results))
	}
	for _, state := range repository.versions {
		if state.Status != VersionStatusIncomplete || state.FailureCode != FailureModel || state.LeaseOwner != "" {
			t.Fatalf("authentication-failed derivation state = %#v, want released incomplete model failure", state)
		}
	}
}

func TestSyncRejectsResponseFromDifferentModelWithoutCompletingConfiguredDerivation(t *testing.T) {
	document := syntheticDocument("document-model-mismatch", "Leader assigns follow-up.")
	response := validEmptyResponse(t)
	response.ModelID = "different-model"
	repository := newMemoryRepository()
	model := &recordingModel{responses: []extract.Response{response}}
	service := testService(document, repository, model)

	summary, err := service.Sync(context.Background())
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if repository.completionCalls != 0 || len(summary.Results) != 1 ||
		summary.Results[0].Outcome != OutcomeFailed || summary.Results[0].FailureCode != FailureInvalidOutput {
		t.Fatalf("completion/results = %d/%#v, want mismatched model rejected before persistence", repository.completionCalls, summary.Results)
	}
}

func TestSyncContinuesAndReturnsBoundedErrorWhenFailureStateCannotPersist(t *testing.T) {
	const privateStorageError = "database rejected private transcript row"
	malformed := extract.Response{Output: json.RawMessage(`{"unexpected":true}`), ModelID: "synthetic-model", PromptVersion: extract.ExtractionPromptVersion}
	model := &recordingModel{responses: []extract.Response{malformed, malformed, validEmptyResponse(t)}}
	repository := newMemoryRepository()
	repository.failureRecordErr = errors.New(privateStorageError)
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	service := testServiceWithSource(&memorySource{
		listed: []source.Document{{Provider: "drive", ID: "document-unpersisted-1"}, {Provider: "drive", ID: "document-unpersisted-2"}, {Provider: "drive", ID: "document-later"}},
		fetched: map[string]source.Document{
			"document-unpersisted-1": syntheticDocument("document-unpersisted-1", "First synthetic source."),
			"document-unpersisted-2": syntheticDocument("document-unpersisted-2", "Second synthetic source."),
			"document-later":         syntheticDocument("document-later", "Later synthetic source."),
		},
	}, repository, model)
	service.Tracer = provider.Tracer("synthetic")

	summary, err := service.Sync(context.Background())
	if !errors.Is(err, ErrFailurePersistence) {
		t.Fatalf("Sync() error = %v, want bounded failure-persistence error", err)
	}
	if strings.Contains(err.Error(), privateStorageError) {
		t.Fatalf("Sync() error disclosed storage text: %v", err)
	}
	if err.Error() != ErrFailurePersistence.Error() {
		t.Fatalf("Sync() error = %q, want one bounded aggregate error", err.Error())
	}
	if len(summary.Results) != 3 || summary.Results[0].Outcome != OutcomeIncomplete || summary.Results[0].FailureCode != FailureStorage || summary.Results[1].Outcome != OutcomeIncomplete || summary.Results[2].Outcome != OutcomeCompleted {
		t.Fatalf("summary results = %#v, want two bounded incomplete outcomes then completed", summary.Results)
	}
	if summary.Incomplete != 2 || summary.Completed != 1 || model.calls != 3 || repository.completionCalls != 1 {
		t.Fatalf("summary/model/completion = %#v/%d/%d, want isolated later completion", summary, model.calls, repository.completionCalls)
	}
	for _, state := range repository.versions {
		if state.ID == summary.Results[0].VersionID && state.Status != VersionStatusPending {
			t.Fatalf("unpersisted failure state = %q, want prior pending state", state.Status)
		}
	}
	spans := exporter.GetSpans()
	if len(spans) != 1 || strings.Contains(renderRecordedSpan(spans[0]), privateStorageError) {
		t.Fatalf("recorded spans leaked private persistence error: %#v", spans)
	}
}

func TestSyncResumesPendingVersionWithoutDuplicates(t *testing.T) {
	repository := newMemoryRepository()
	repository.failCompletionOnce = true
	model := &recordingModel{responses: []extract.Response{validSignalResponse(t, "2026-07-20"), validSignalResponse(t, "2026-07-20")}}
	service := testService(syntheticDocument("document-retry", "Leader assigns follow-up."), repository, model)

	first, err := service.Sync(context.Background())
	if err != nil || first.Incomplete != 1 {
		t.Fatalf("first Sync() = (%#v, %v), want one incomplete result", first, err)
	}
	second, err := service.Sync(context.Background())
	if err != nil || second.Completed != 1 || second.Results[0].RetryCount != 1 {
		t.Fatalf("second Sync() = (%#v, %v), want completed retry_count=1", second, err)
	}
	third, err := service.Sync(context.Background())
	if err != nil || third.Unchanged != 1 {
		t.Fatalf("third Sync() = (%#v, %v), want unchanged", third, err)
	}
	if len(repository.versions) != 1 || repository.persistedEvidence != 1 || repository.persistedMentions != 2 || repository.persistedObservations != 1 || repository.persistedSignals != 1 {
		t.Fatalf("durable counts = versions:%d evidence:%d mentions:%d observations:%d signals:%d, want 1,1,2,1,1",
			len(repository.versions), repository.persistedEvidence, repository.persistedMentions, repository.persistedObservations, repository.persistedSignals)
	}
	if model.calls != 2 {
		t.Fatalf("model calls = %d, want 2", model.calls)
	}
}

func TestSyncPreservesUnknownMeetingTime(t *testing.T) {
	repository := newMemoryRepository()
	model := &recordingModel{responses: []extract.Response{validSignalResponse(t, "")}}
	service := testService(syntheticDocument("document-unknown-time", "Leader assigns follow-up."), repository, model)

	summary, err := service.Sync(context.Background())
	if err != nil || summary.Completed != 1 {
		t.Fatalf("Sync() = (%#v, %v), want one completed", summary, err)
	}
	if len(repository.lastCompletion.Observations) != 1 || repository.lastCompletion.Observations[0].ValidStart != nil {
		t.Fatalf("observation valid time = %#v, want unknown", repository.lastCompletion.Observations)
	}
}

func TestSyncDoesNotCombineFetchedTitleWithListedTitleMeetingTime(t *testing.T) {
	listedMeetingTime := time.Date(2026, time.July, 20, 0, 0, 0, 0, time.UTC)
	listed := syntheticDocument("document-renamed", "Leader assigns follow-up.")
	listed.Title = "[2026-07-20] Weekly"
	listed.MeetingTime = &listedMeetingTime
	listed.Tabs = nil
	listed.Revision = ""
	fetched := syntheticDocument(listed.ID, "Leader assigns follow-up.")
	fetched.Title = "Weekly"
	fetched.MeetingTime = nil

	repository := newMemoryRepository()
	service := testServiceWithSource(&memorySource{
		listed:  []source.Document{listed},
		fetched: map[string]source.Document{listed.ID: fetched},
	}, repository, &recordingModel{responses: []extract.Response{validSignalResponse(t, "")}})

	summary, err := service.Sync(context.Background())
	if err != nil || summary.Completed != 1 {
		t.Fatalf("Sync() = (%#v, %v), want one completed renamed document", summary, err)
	}
	if got := repository.lastPreparedVersion.Title(); got != "Weekly" {
		t.Fatalf("persisted title = %q, want fetched title", got)
	}
	if got := repository.lastPreparedVersion.SourceMeetingTime(); got != nil {
		t.Fatalf("persisted meeting time = %v, want unknown for fetched undated title", got)
	}
	if len(repository.lastCompletion.Observations) != 1 || repository.lastCompletion.Observations[0].ValidStart != nil {
		t.Fatalf("observation valid time = %#v, want stale listed date excluded from chronology", repository.lastCompletion.Observations)
	}
}

func TestSyncInheritsListedTitleAndMeetingTimeWhenFetchedTitleIsAbsent(t *testing.T) {
	listedMeetingTime := time.Date(2026, time.July, 20, 0, 0, 0, 0, time.UTC)
	listed := syntheticDocument("document-title-fallback", "Leader assigns follow-up.")
	listed.Title = "[2026-07-20] Weekly"
	listed.MeetingTime = &listedMeetingTime
	listed.Tabs = nil
	listed.Revision = ""
	fetched := syntheticDocument(listed.ID, "Leader assigns follow-up.")
	fetched.Title = ""
	fetched.MeetingTime = nil

	repository := newMemoryRepository()
	service := testServiceWithSource(&memorySource{
		listed:  []source.Document{listed},
		fetched: map[string]source.Document{listed.ID: fetched},
	}, repository, &recordingModel{responses: []extract.Response{validSignalResponse(t, "")}})

	summary, err := service.Sync(context.Background())
	if err != nil || summary.Completed != 1 {
		t.Fatalf("Sync() = (%#v, %v), want one completed fallback document", summary, err)
	}
	if got := repository.lastPreparedVersion.Title(); got != listed.Title {
		t.Fatalf("persisted title = %q, want listed fallback %q", got, listed.Title)
	}
	if got := repository.lastPreparedVersion.SourceMeetingTime(); got == nil || !got.Equal(listedMeetingTime) {
		t.Fatalf("persisted meeting time = %v, want listed fallback %v", got, listedMeetingTime)
	}
	if len(repository.lastCompletion.Observations) != 1 || repository.lastCompletion.Observations[0].ValidStart == nil ||
		!repository.lastCompletion.Observations[0].ValidStart.Equal(listedMeetingTime) {
		t.Fatalf("observation valid time = %#v, want coherent listed title time", repository.lastCompletion.Observations)
	}
}

func TestSyncCannotManufactureChronologyFromDeadlineCitation(t *testing.T) {
	const transcript = "Leader assigns follow-up by 2026-07-20."
	response := signalResponse(t, transcript, "2026-07-20")
	repository := newMemoryRepository()
	service := testService(
		syntheticDocument("document-deadline-not-meeting-time", transcript),
		repository,
		&recordingModel{responses: []extract.Response{response}},
	)

	summary, err := service.Sync(context.Background())
	if err != nil || summary.Completed != 1 {
		t.Fatalf("Sync() = (%#v, %v), want one completed extraction", summary, err)
	}
	if len(repository.lastCompletion.Observations) != 1 || repository.lastCompletion.Observations[0].ValidStart != nil {
		t.Fatalf("observation valid time = %#v, want deadline citation unable to manufacture chronology", repository.lastCompletion.Observations)
	}
}

func TestSyncKeepsSeparatelyCitedAlexNameAndBobEmailPendingWithoutTeachingAliases(t *testing.T) {
	const (
		alexEvidence = "Alex Reviewer led the review."
		bobEvidence  = "Bob Builder uses bob.builder@synthetic.example."
		transcript   = alexEvidence + " " + bobEvidence
	)
	output := extract.ExtractionOutput{
		Citations: []extract.Citation{
			{ID: "citation-alex", TabID: "transcript-tab", StartOffset: 0, EndOffset: len(alexEvidence), Quote: alexEvidence},
			{ID: "citation-bob", TabID: "transcript-tab", StartOffset: len(alexEvidence) + 1, EndOffset: len(transcript), Quote: bobEvidence},
		},
		People: []extract.PersonMention{{
			ID: "mention-alex", Surface: "Alex Reviewer", Email: "bob.builder@synthetic.example",
			Role: extract.MentionRoleSpeaker, CitationIDs: []string{"citation-alex", "citation-bob"},
		}},
		Statements: []extract.AttributedStatement{},
		Signals:    []extract.InteractionSignal{},
	}
	repository := newMemoryRepository()
	repository.snapshots = []entity.EntitySnapshot{{
		ID: "entity-bob", Kind: entity.KindPerson,
		Aliases: []entity.Alias{{Type: entity.AliasTypeEmail, Value: "bob.builder@synthetic.example"}},
	}}
	service := testService(
		syntheticDocument("document-separated-identity", transcript),
		repository,
		&recordingModel{responses: []extract.Response{extractionResponse(t, output)}},
	)

	summary, err := service.Sync(context.Background())
	if err != nil || summary.Completed != 1 {
		t.Fatalf("Sync() = (%#v, %v), want reviewable pending mention", summary, err)
	}
	if len(repository.lastCompletion.Mentions) != 1 {
		t.Fatalf("mentions = %#v, want one durable mention", repository.lastCompletion.Mentions)
	}
	mention := repository.lastCompletion.Mentions[0]
	if mention.Resolution.AutoResolved || mention.Resolution.EntityID != "" {
		t.Fatalf("resolution = %#v, want Alex/Bob association pending", mention.Resolution)
	}
	if mention.EvidenceKey != "citation-alex" {
		t.Fatalf("EvidenceKey = %q, want exact Alex identity evidence", mention.EvidenceKey)
	}
	if mention.NormalizedName != "alex reviewer" || mention.ProposedEmail != "bob.builder@synthetic.example" || mention.ProposedEmailEvidenceKey != "citation-bob" {
		t.Fatalf("durable identity proposal = %#v, want independently grounded name and non-authoritative email evidence", mention)
	}
}

func TestSyncNeverUsesCooccurringModelEmailForAutomaticResolution(t *testing.T) {
	const transcript = "Alex Reviewer asked Bob Builder (bob.builder@synthetic.example) to follow up."
	output := extract.ExtractionOutput{
		Citations: []extract.Citation{{ID: "citation-shared", TabID: "transcript-tab", StartOffset: 0, EndOffset: len(transcript), Quote: transcript}},
		People: []extract.PersonMention{{
			ID: "mention-alex", Surface: "Alex Reviewer", Email: "bob.builder@synthetic.example",
			Role: extract.MentionRoleSpeaker, CitationIDs: []string{"citation-shared"},
		}},
		Statements: []extract.AttributedStatement{},
		Signals:    []extract.InteractionSignal{},
	}
	repository := newMemoryRepository()
	repository.snapshots = []entity.EntitySnapshot{{
		ID: "entity-bob", Kind: entity.KindPerson,
		Aliases: []entity.Alias{{Type: entity.AliasTypeEmail, Value: "bob.builder@synthetic.example"}},
	}}
	service := testService(
		syntheticDocument("document-cooccurring-email", transcript),
		repository,
		&recordingModel{responses: []extract.Response{extractionResponse(t, output)}},
	)

	summary, err := service.Sync(context.Background())
	if err != nil || summary.Completed != 1 {
		t.Fatalf("Sync() = (%#v, %v), want reviewable identity completed", summary, err)
	}
	mention := repository.lastCompletion.Mentions[0]
	if mention.Resolution.AutoResolved || mention.Resolution.EntityID != "" {
		t.Fatalf("resolution = %#v, want cooccurring model email unable to resolve Alex to Bob", mention.Resolution)
	}
	if mention.EvidenceKey != "citation-shared" {
		t.Fatalf("EvidenceKey = %q, want exact Alex name evidence", mention.EvidenceKey)
	}
	if mention.NormalizedName != "alex reviewer" || mention.ProposedEmail != "bob.builder@synthetic.example" || mention.ProposedEmailEvidenceKey != "citation-shared" {
		t.Fatalf("durable identity proposal = %#v, want grounded name plus non-authoritative email provenance", mention)
	}
}

func TestSyncResolvesGroundedNameIndependentlyOfModelEmail(t *testing.T) {
	const transcript = "Alex Reviewer asked Bob Builder (bob.builder@synthetic.example) to follow up."
	output := extract.ExtractionOutput{
		Citations: []extract.Citation{{ID: "citation-shared", TabID: "transcript-tab", StartOffset: 0, EndOffset: len(transcript), Quote: transcript}},
		People: []extract.PersonMention{{
			ID: "mention-alex", Surface: "Alex Reviewer", Email: "bob.builder@synthetic.example",
			Role: extract.MentionRoleSpeaker, CitationIDs: []string{"citation-shared"},
		}},
		Statements: []extract.AttributedStatement{},
		Signals:    []extract.InteractionSignal{},
	}
	repository := newMemoryRepository()
	repository.snapshots = []entity.EntitySnapshot{
		{ID: "entity-alex", Kind: entity.KindPerson, Aliases: []entity.Alias{{Type: entity.AliasTypeName, Value: "alex reviewer"}}},
		{ID: "entity-bob", Kind: entity.KindPerson, Aliases: []entity.Alias{{Type: entity.AliasTypeEmail, Value: "bob.builder@synthetic.example"}}},
	}
	service := testService(
		syntheticDocument("document-name-only-resolution", transcript),
		repository,
		&recordingModel{responses: []extract.Response{extractionResponse(t, output)}},
	)

	summary, err := service.Sync(context.Background())
	if err != nil || summary.Completed != 1 {
		t.Fatalf("Sync() = (%#v, %v), want completed name-only resolution", summary, err)
	}
	mention := repository.lastCompletion.Mentions[0]
	if !mention.Resolution.AutoResolved || mention.Resolution.EntityID != "entity-alex" {
		t.Fatalf("resolution = %#v, want grounded name independently resolved to Alex", mention.Resolution)
	}
}

func TestSyncPreservesSignalSubjectAndObjectMentionKeys(t *testing.T) {
	repository := newMemoryRepository()
	service := testService(
		syntheticDocument("document-mention-links", "Leader assigns follow-up."),
		repository,
		&recordingModel{responses: []extract.Response{validSignalResponse(t, "2026-07-20")}},
	)

	if _, err := service.Sync(context.Background()); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if len(repository.lastCompletion.Observations) != 1 {
		t.Fatalf("observations = %d, want 1", len(repository.lastCompletion.Observations))
	}
	observation := repository.lastCompletion.Observations[0]
	if observation.SubjectMentionKey != "mention-leader" || observation.ObjectMentionKey != "mention-report" {
		t.Fatalf("observation mention keys = %q/%q, want mention-leader/mention-report", observation.SubjectMentionKey, observation.ObjectMentionKey)
	}
}

func TestSyncClassifiesPersistenceIdentityCollisionAsInvalidOutput(t *testing.T) {
	cases := []struct {
		name       string
		transcript string
		response   extract.Response
	}{
		{name: "mention proposal", transcript: "Synthetic duplicate mention.", response: duplicateMentionResponse(t)},
		{name: "observation and signal", transcript: "Synthetic duplicate signal.", response: duplicateSignalResponse(t)},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			repository := newMemoryRepository()
			model := &recordingModel{responses: []extract.Response{testCase.response}}
			service := testService(syntheticDocument("document-duplicate-output", testCase.transcript), repository, model)

			summary, err := service.Sync(context.Background())
			if err != nil {
				t.Fatalf("Sync() error = %v", err)
			}
			if len(summary.Results) != 1 || summary.Results[0].Outcome != OutcomeFailed || summary.Results[0].FailureCode != FailureInvalidOutput {
				t.Fatalf("summary results = %#v, want failed invalid_output", summary.Results)
			}
			if repository.completionCalls != 0 {
				t.Fatalf("completion calls = %d, want 0 for rejected output", repository.completionCalls)
			}
			for _, state := range repository.versions {
				if state.Status != VersionStatusFailed || state.FailureCode != FailureInvalidOutput {
					t.Fatalf("durable state = %#v, want bounded invalid-output failure", state)
				}
			}
		})
	}
}

func TestSyncClassifiesPaddedModelLocalIDAsInvalidOutputBeforeStorage(t *testing.T) {
	const transcript = "Synthetic padded identifier."
	output := extract.ExtractionOutput{
		Citations: []extract.Citation{{ID: " citation-1", TabID: "transcript-tab", StartOffset: 0, EndOffset: len(transcript), Quote: transcript}},
		People: []extract.PersonMention{{
			ID: " mention-1", Surface: "Synthetic Person", Role: extract.MentionRoleSpeaker,
			CitationIDs: []string{" citation-1"},
		}},
		Statements: []extract.AttributedStatement{},
		Signals:    []extract.InteractionSignal{},
	}
	raw, err := json.Marshal(output)
	if err != nil {
		t.Fatalf("marshal padded extraction: %v", err)
	}
	repository := newMemoryRepository()
	service := testService(
		syntheticDocument("document-padded-output", transcript),
		repository,
		&recordingModel{responses: []extract.Response{{
			Output: raw, ModelID: "synthetic-model", PromptVersion: extract.ExtractionPromptVersion, Outcome: "success",
		}}},
	)

	summary, err := service.Sync(context.Background())
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if len(summary.Results) != 1 || summary.Results[0].Outcome != OutcomeFailed || summary.Results[0].FailureCode != FailureInvalidOutput {
		t.Fatalf("summary = %#v, want invalid-output failure", summary)
	}
	if repository.completionCalls != 0 {
		t.Fatalf("completion calls = %d, want padded IDs rejected before storage", repository.completionCalls)
	}
}

func duplicateSignalResponse(t *testing.T) extract.Response {
	t.Helper()
	const transcript = "Synthetic duplicate signal."
	output, err := json.Marshal(extract.ExtractionOutput{
		Citations: []extract.Citation{{ID: "citation-1", TabID: "transcript-tab", StartOffset: 0, EndOffset: len(transcript), Quote: transcript}},
		People: []extract.PersonMention{
			{ID: "mention-1", Surface: "Synthetic", Role: extract.MentionRoleSpeaker, CitationIDs: []string{"citation-1"}},
			{ID: "mention-2", Surface: "signal", Role: extract.MentionRoleReference, CitationIDs: []string{"citation-1"}},
		},
		Statements: []extract.AttributedStatement{{
			ID: "statement-1", SpeakerMentionID: "mention-1", SubjectMentionID: "mention-2",
			Predicate: "assigned", ObjectText: "follow-up", CitationIDs: []string{"citation-1"},
		}},
		Signals: []extract.InteractionSignal{
			{
				ID: "signal-1", SubjectMentionID: "mention-1", ObjectMentionID: "mention-2", StatementIDs: []string{"statement-1"},
				Category: extract.SignalCategoryFutureResponsibility, Direction: extract.SignalDirectionStrengthening,
				Rationale: "Synthetic rationale one", Confidence: 0.8, SupportingCitationIDs: []string{"citation-1"}, ContradictingCitationIDs: []string{},
			},
			{
				ID: "signal-2", SubjectMentionID: "mention-1", ObjectMentionID: "mention-2", StatementIDs: []string{"statement-1"},
				Category: extract.SignalCategorySupportAdvocacy, Direction: extract.SignalDirectionStrengthening,
				Rationale: "Synthetic rationale two", Confidence: 0.8, SupportingCitationIDs: []string{"citation-1"}, ContradictingCitationIDs: []string{},
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal duplicate signal extraction: %v", err)
	}
	if _, err := extract.DecodeAndValidateExtraction(extract.SubmittedText{Tabs: []extract.SubmittedTab{{
		ID: "transcript-tab", Role: extract.TabRoleTranscript, Text: transcript,
	}}}, output); err != nil {
		t.Fatalf("duplicate signal fixture must remain schema-valid: %v", err)
	}
	return extract.Response{Output: output, ModelID: "synthetic-model", PromptVersion: extract.ExtractionPromptVersion, Outcome: "success"}
}

func TestSyncSubmitsTranscriptAndNotesAsSeparatelyLabeledTabs(t *testing.T) {
	document := syntheticDocument("document-labeled-tabs", "Leader assigns follow-up.")
	document.Tabs = append(document.Tabs,
		source.Tab{ID: "notes-tab", Title: "Notes", Path: []string{"Notes"}, Order: 1, Role: source.TabRoleGeminiNotes, Text: "Synthetic secondary summary."},
		source.Tab{ID: "other-tab", Title: "Agenda", Path: []string{"Agenda"}, Order: 2, Role: source.TabRoleOther, Text: "Synthetic private agenda."},
	)
	model := &recordingModel{responses: []extract.Response{validEmptyResponse(t)}}
	service := testService(document, newMemoryRepository(), model)

	if _, err := service.Sync(context.Background()); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if len(model.requests) != 1 {
		t.Fatalf("model request count = %d, want 1", len(model.requests))
	}
	input := model.requests[0].Input
	if !strings.Contains(input, `"role":"transcript"`) || !strings.Contains(input, `"role":"gemini-notes"`) {
		t.Fatalf("model input does not label transcript and notes separately: %s", input)
	}
	if strings.Contains(input, "Synthetic private agenda") {
		t.Fatal("model input includes an unrelated tab")
	}
}

func TestSyncRecordsOneSuccessfulBoundedIngestionSpanAndDecision(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	decisions := &recordingDecisionRecorder{}
	service := testService(syntheticDocument("document-telemetry", "Leader assigns follow-up."), newMemoryRepository(), &recordingModel{responses: []extract.Response{validEmptyResponse(t)}})
	service.Tracer = provider.Tracer("synthetic")
	service.Decisions = decisions

	if _, err := service.Sync(context.Background()); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	spans := exporter.GetSpans()
	if len(spans) != 1 || spans[0].Name != ingestionSpanName || spans[0].Status.Code != codes.Ok {
		t.Fatalf("spans = %#v, want one explicitly OK ingestion span", spans)
	}
	attributes := make(map[string]string)
	for _, value := range spans[0].Attributes {
		attributes[string(value.Key)] = value.Value.String()
	}
	if attributes["stacks.ingest.document_count"] != "1" || attributes["stacks.ingest.completed"] != "1" {
		t.Fatalf("span attributes = %#v, want bounded document/completion counts", attributes)
	}
	for _, privateValue := range []string{"document-telemetry", "Synthetic Meeting", "Leader assigns follow-up."} {
		for key, value := range attributes {
			if strings.Contains(key, privateValue) || strings.Contains(value, privateValue) {
				t.Fatalf("span attribute %q=%q contains private value", key, value)
			}
		}
	}
	if len(decisions.observations) != 1 || decisions.observations[0].Name != ingestionDecisionName || decisions.observations[0].Outcome != string(OutcomeCompleted) {
		t.Fatalf("decision observations = %#v, want one bounded completed decision", decisions.observations)
	}
}

func TestCompletionReplacesRawSignalRationaleWithDeterministicExplanation(t *testing.T) {
	const unsafeRationale = "The manager secretly distrusts the employee."
	const transcript = "Leader assigns follow-up."
	version := documentVersion(t, syntheticDocument("document-safe-explanation", transcript))
	output := extract.ExtractionOutput{
		Citations: []extract.Citation{{
			ID: "citation-1", TabID: "transcript-tab", StartOffset: 0,
			EndOffset: len(transcript), Quote: transcript,
		}},
		People: []extract.PersonMention{{
			ID: "mention-leader", Surface: "Leader", Role: extract.MentionRoleSpeaker,
			CitationIDs: []string{"citation-1"},
		}},
		Statements: []extract.AttributedStatement{{
			ID: "statement-1", SpeakerMentionID: "mention-leader", SubjectMentionID: "mention-leader",
			Predicate: "assigned", ObjectText: "follow-up", CitationIDs: []string{"citation-1"},
		}},
		Signals: []extract.InteractionSignal{{
			ID: "signal-1", SubjectMentionID: "mention-leader", ObjectMentionID: "mention-leader",
			StatementIDs: []string{"statement-1"}, Category: extract.SignalCategoryFutureResponsibility,
			Direction: extract.SignalDirectionStrengthening, Rationale: unsafeRationale,
			Confidence: 0.8, SupportingCitationIDs: []string{"citation-1"},
		}},
	}
	service := &Service{Resolver: entity.Resolver{}}

	completion, err := service.completion(version, VersionState{
		ID: "version-id", DerivationID: "derivation-id",
		DerivationDigest: sha256.Sum256([]byte("synthetic derivation")),
	}, extract.Response{
		ModelID: "synthetic-model", PromptVersion: extract.ExtractionPromptVersion,
	}, output, nil)
	if err != nil {
		t.Fatalf("completion() error = %v", err)
	}
	if len(completion.Signals) != 1 || completion.Signals[0].Rationale == unsafeRationale ||
		!strings.Contains(completion.Signals[0].Rationale, "future responsibility") {
		t.Fatalf("stored signal rationale = %#v, want deterministic category/direction explanation", completion.Signals)
	}
}

func testService(document source.Document, repository *memoryRepository, model *recordingModel) *Service {
	listed := document
	listed.Tabs = nil
	listed.Revision = ""
	return testServiceWithSource(&memorySource{
		listed:  []source.Document{listed},
		fetched: map[string]source.Document{document.ID: document},
	}, repository, model)
}

func testServiceWithSource(sourceBoundary source.Source, repository *memoryRepository, model extract.Model) *Service {
	return &Service{
		Source:         sourceBoundary,
		Model:          model,
		Resolver:       entity.Resolver{},
		Repository:     repository,
		CollectionID:   "synthetic-folder",
		PromptVersion:  extract.ExtractionPromptVersion,
		Provider:       modelpolicy.ProviderBedrock,
		DataMode:       modelpolicy.DataModePersonal,
		Region:         "us-east-1",
		ModelID:        "synthetic-model",
		MaxTokens:      256,
		LeaseDuration:  5 * time.Minute,
		AttemptTimeout: 4 * time.Minute,
		Now:            func() time.Time { return recordedAt },
	}
}

func syntheticDocument(id, transcript string) source.Document {
	return source.Document{
		Provider: "drive", ID: id, Title: "Synthetic Meeting", Locator: "https://example.invalid/document",
		Version: "version-1", Revision: "revision-1", ModifiedAt: time.Date(2026, time.July, 21, 11, 0, 0, 0, time.UTC),
		Tabs: []source.Tab{{ID: "transcript-tab", Title: "Transcript", Path: []string{"Transcript"}, Order: 0, Role: source.TabRoleTranscript, Text: transcript}},
	}
}

func documentVersion(t *testing.T, document source.Document) knowledge.DocumentVersion {
	t.Helper()
	version, err := knowledge.NewDocumentVersion(knowledge.DocumentVersionInput{
		Provider: document.Provider, ProviderDocumentID: document.ID, Title: document.Title,
		Locator: document.Locator, ProviderVersion: document.Version, ProviderRevision: document.Revision,
		ModifiedAt: document.ModifiedAt, SourceMeetingTime: document.MeetingTime,
		RecordedAt: recordedAt, Tabs: document.Tabs,
	})
	if err != nil {
		t.Fatalf("NewDocumentVersion() error = %v", err)
	}
	return version
}

func validEmptyResponse(t *testing.T) extract.Response {
	t.Helper()
	return extractionResponse(t, extract.ExtractionOutput{
		Citations:  []extract.Citation{},
		People:     []extract.PersonMention{},
		Statements: []extract.AttributedStatement{},
		Signals:    []extract.InteractionSignal{},
	})
}

func extractionResponse(t *testing.T, extraction extract.ExtractionOutput) extract.Response {
	t.Helper()
	output, err := json.Marshal(extraction)
	if err != nil {
		t.Fatalf("marshal extraction: %v", err)
	}
	return extract.Response{Output: output, ModelID: "synthetic-model", PromptVersion: extract.ExtractionPromptVersion, Outcome: "success"}
}

func signalResponse(t *testing.T, transcript, meetingDate string) extract.Response {
	t.Helper()
	return extractionResponse(t, extract.ExtractionOutput{
		MeetingDate: meetingDate,
		Citations: []extract.Citation{{
			ID: "citation-1", TabID: "transcript-tab", StartOffset: 0, EndOffset: len(transcript), Quote: transcript,
		}},
		People: []extract.PersonMention{
			{ID: "mention-leader", Surface: "Leader", Role: extract.MentionRoleSpeaker, CitationIDs: []string{"citation-1"}},
			{ID: "mention-report", Surface: "follow-up", Role: extract.MentionRoleReference, CitationIDs: []string{"citation-1"}},
		},
		Statements: []extract.AttributedStatement{{
			ID: "statement-1", SpeakerMentionID: "mention-leader", SubjectMentionID: "mention-report",
			Predicate: "assigned", ObjectText: "follow-up", ValidDate: meetingDate, CitationIDs: []string{"citation-1"},
		}},
		Signals: []extract.InteractionSignal{{
			ID: "signal-1", SubjectMentionID: "mention-leader", ObjectMentionID: "mention-report", StatementIDs: []string{"statement-1"},
			Category: extract.SignalCategoryFutureResponsibility, Direction: extract.SignalDirectionStrengthening,
			Rationale: "Synthetic rationale", Confidence: 0.8, SupportingCitationIDs: []string{"citation-1"}, ContradictingCitationIDs: []string{},
		}},
	})
}

func validSignalResponse(t *testing.T, meetingDate string) extract.Response {
	t.Helper()
	const transcript = "Leader assigns follow-up."
	output, err := json.Marshal(extract.ExtractionOutput{
		MeetingDate: meetingDate,
		Citations:   []extract.Citation{{ID: "citation-1", TabID: "transcript-tab", StartOffset: 0, EndOffset: len(transcript), Quote: transcript}},
		People: []extract.PersonMention{
			{ID: "mention-leader", Surface: "Leader", Role: extract.MentionRoleSpeaker, CitationIDs: []string{"citation-1"}},
			{ID: "mention-report", Surface: "follow-up", Role: extract.MentionRoleReference, CitationIDs: []string{"citation-1"}},
		},
		Statements: []extract.AttributedStatement{{
			ID: "statement-1", SpeakerMentionID: "mention-leader", SubjectMentionID: "mention-report",
			Predicate: "assigned", ObjectText: "follow-up", ValidDate: meetingDate, CitationIDs: []string{"citation-1"},
		}},
		Signals: []extract.InteractionSignal{{
			ID: "signal-1", SubjectMentionID: "mention-leader", ObjectMentionID: "mention-report", StatementIDs: []string{"statement-1"},
			Category: extract.SignalCategoryFutureResponsibility, Direction: extract.SignalDirectionStrengthening,
			Rationale: "Synthetic rationale", Confidence: 0.8, SupportingCitationIDs: []string{"citation-1"}, ContradictingCitationIDs: []string{},
		}},
	})
	if err != nil {
		t.Fatalf("marshal signal extraction: %v", err)
	}
	if _, err := extract.DecodeAndValidateExtraction(extract.SubmittedText{Tabs: []extract.SubmittedTab{{
		ID: "transcript-tab", Role: extract.TabRoleTranscript, Text: transcript,
	}}}, output); err != nil {
		t.Fatalf("synthetic signal extraction is invalid: %v", err)
	}
	return extract.Response{Output: output, ModelID: "synthetic-model", PromptVersion: extract.ExtractionPromptVersion, Outcome: "success"}
}

func duplicateMentionResponse(t *testing.T) extract.Response {
	t.Helper()
	const transcript = "Synthetic duplicate mention."
	output, err := json.Marshal(extract.ExtractionOutput{
		Citations: []extract.Citation{{ID: "citation-1", TabID: "transcript-tab", StartOffset: 0, EndOffset: len(transcript), Quote: transcript}},
		People: []extract.PersonMention{
			{ID: "mention-1", Surface: "Synthetic", Role: extract.MentionRoleSpeaker, CitationIDs: []string{"citation-1"}},
			{ID: "mention-2", Surface: "Synthetic", Role: extract.MentionRoleSpeaker, CitationIDs: []string{"citation-1"}},
		},
		Statements: []extract.AttributedStatement{},
		Signals:    []extract.InteractionSignal{},
	})
	if err != nil {
		t.Fatalf("marshal duplicate mention extraction: %v", err)
	}
	if _, err := extract.DecodeAndValidateExtraction(extract.SubmittedText{Tabs: []extract.SubmittedTab{{
		ID: "transcript-tab", Role: extract.TabRoleTranscript, Text: transcript,
	}}}, output); err != nil {
		t.Fatalf("duplicate mention fixture must remain schema-valid: %v", err)
	}
	return extract.Response{Output: output, ModelID: "synthetic-model", PromptVersion: extract.ExtractionPromptVersion, Outcome: "success"}
}

type memorySource struct {
	listed    []source.Document
	fetched   map[string]source.Document
	listErr   error
	getErrs   map[string]error
	listCalls int
}

func (sourceBoundary *memorySource) List(context.Context, string) ([]source.Document, error) {
	sourceBoundary.listCalls++
	if sourceBoundary.listErr != nil {
		return nil, sourceBoundary.listErr
	}
	return append([]source.Document(nil), sourceBoundary.listed...), nil
}

func (sourceBoundary *memorySource) Get(_ context.Context, documentID string) (source.Document, error) {
	if err := sourceBoundary.getErrs[documentID]; err != nil {
		return source.Document{}, err
	}
	document, ok := sourceBoundary.fetched[documentID]
	if !ok {
		return source.Document{}, errors.New("synthetic source failure")
	}
	return document, nil
}

type recordingModel struct {
	responses []extract.Response
	errs      []error
	requests  []extract.Request
	calls     int
}

type deadlineBlockingModel struct {
	calls    int
	deadline time.Time
}

func (model *deadlineBlockingModel) Generate(ctx context.Context, _ extract.Request) (extract.Response, error) {
	model.calls++
	model.deadline, _ = ctx.Deadline()
	<-ctx.Done()
	return extract.Response{}, ctx.Err()
}

type recordingDecisionRecorder struct {
	observations []observability.DecisionObservation
}

func (recorder *recordingDecisionRecorder) Record(_ context.Context, observation observability.DecisionObservation) error {
	recorder.observations = append(recorder.observations, observation)
	return nil
}

func (model *recordingModel) Generate(_ context.Context, request extract.Request) (extract.Response, error) {
	model.requests = append(model.requests, request)
	index := model.calls
	model.calls++
	if index < len(model.errs) && model.errs[index] != nil {
		return extract.Response{}, model.errs[index]
	}
	if index >= len(model.responses) {
		return extract.Response{}, errors.New("synthetic model response missing")
	}
	return model.responses[index], nil
}

type memoryRepository struct {
	versions              map[string]VersionState
	completions           map[string]Completion
	snapshots             []entity.EntitySnapshot
	lastPreparedVersion   knowledge.DocumentVersion
	lastCompletion        Completion
	completionCalls       int
	failCompletionOnce    bool
	prepareErr            error
	completionErr         error
	snapshotErr           error
	failureRecordErr      error
	persistedEvidence     int
	persistedMentions     int
	persistedObservations int
	persistedSignals      int
	prepareClaims         int
	lastLeaseExpiresAt    time.Time
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{versions: make(map[string]VersionState), completions: make(map[string]Completion)}
}

func (repository *memoryRepository) PrepareVersion(_ context.Context, version knowledge.DocumentVersion, derivation DerivationIdentity, _ modelpolicy.DataMode, leaseDuration time.Duration) (VersionState, error) {
	if repository.prepareErr != nil {
		return VersionState{}, repository.prepareErr
	}
	repository.lastPreparedVersion = version
	if leaseDuration <= 0 {
		return VersionState{}, errors.New("synthetic invalid lease duration")
	}
	key := derivationKey(version, derivation)
	state, exists := repository.versions[key]
	if !exists {
		repository.prepareClaims++
		leaseExpiresAt := time.Now().Add(leaseDuration)
		state = VersionState{
			ID:               "version-" + version.Digest().String()[:12],
			DerivationID:     "derivation-" + fmt.Sprintf("%x", derivation.Digest[:6]),
			DerivationDigest: derivation.Digest, Status: VersionStatusPending,
			LeaseOwner: fmt.Sprintf("owner-%d", repository.prepareClaims), LeaseExpiresAt: leaseExpiresAt,
		}
		repository.lastLeaseExpiresAt = leaseExpiresAt
	} else if state.Status == VersionStatusPending && state.LeaseOwner != "" {
		busy := state
		busy.Status = VersionStatusBusy
		busy.FailureCode = FailureBusy
		busy.LeaseOwner = ""
		return busy, nil
	} else if state.Status != VersionStatusComplete {
		repository.prepareClaims++
		leaseExpiresAt := time.Now().Add(leaseDuration)
		state.Status = VersionStatusPending
		state.RetryCount++
		state.FailureCode = ""
		state.LeaseOwner = fmt.Sprintf("owner-%d", repository.prepareClaims)
		state.LeaseExpiresAt = leaseExpiresAt
		repository.lastLeaseExpiresAt = leaseExpiresAt
	}
	repository.versions[key] = state
	return state, nil
}

func (repository *memoryRepository) CompleteVersion(_ context.Context, completion Completion) error {
	repository.completionCalls++
	if repository.completionErr != nil {
		return repository.completionErr
	}
	if repository.failCompletionOnce {
		repository.failCompletionOnce = false
		return errors.New("synthetic completion interruption")
	}
	for key, state := range repository.versions {
		if state.DerivationID == completion.DerivationID {
			if state.LeaseOwner != completion.LeaseOwner {
				return errors.New("synthetic completion lease is not owned")
			}
			state.Status = VersionStatusComplete
			state.FailureCode = ""
			state.LeaseOwner = ""
			state.LeaseExpiresAt = time.Time{}
			repository.versions[key] = state
			break
		}
	}
	repository.completions[completion.VersionID] = completion
	repository.lastCompletion = completion
	repository.persistedEvidence = len(completion.Evidence)
	repository.persistedMentions = len(completion.Mentions)
	repository.persistedObservations = len(completion.Observations)
	repository.persistedSignals = len(completion.Signals)
	return nil
}

func (repository *memoryRepository) RecordFailure(_ context.Context, derivationID, leaseOwner string, status VersionStatus, code FailureCode) error {
	if repository.failureRecordErr != nil {
		return repository.failureRecordErr
	}
	for key, state := range repository.versions {
		if state.DerivationID == derivationID {
			if state.LeaseOwner != leaseOwner {
				return errors.New("synthetic failure lease is not owned")
			}
			state.Status = status
			state.FailureCode = code
			state.LeaseOwner = ""
			state.LeaseExpiresAt = time.Time{}
			repository.versions[key] = state
			return nil
		}
	}
	return errors.New("synthetic version not found")
}

func (repository *memoryRepository) EntitySnapshots(context.Context) ([]entity.EntitySnapshot, error) {
	return append([]entity.EntitySnapshot(nil), repository.snapshots...), repository.snapshotErr
}

func versionKey(version knowledge.DocumentVersion) string {
	return version.Provider() + "/" + version.ProviderDocumentID() + "/" + version.Digest().String()
}

func derivationKey(version knowledge.DocumentVersion, identity DerivationIdentity) string {
	return versionKey(version) + "/" + fmt.Sprintf("%x", identity.Digest)
}

func testDerivationIdentity(t *testing.T, version knowledge.DocumentVersion) DerivationIdentity {
	t.Helper()
	identity := DerivationIdentity{
		Provider: modelpolicy.ProviderBedrock,
		Region:   "us-east-1", ModelID: "synthetic-model", MaxTokens: 256,
		PromptVersion: extract.ExtractionPromptVersion,
		SchemaDigest:  sha256.Sum256(extract.ExtractionJSONSchema()),
	}
	var err error
	identity.Digest, err = ComputeDerivationDigest(version, identity)
	if err != nil {
		t.Fatalf("ComputeDerivationDigest() error = %v", err)
	}
	return identity
}

func TestComputeDerivationDigestChangesWithMaterialExtractionConfiguration(t *testing.T) {
	version := documentVersion(t, syntheticDocument("document-derivation-identity", "Synthetic Person delegated a task."))
	base := DerivationIdentity{
		Provider: modelpolicy.ProviderBedrock,
		Region:   "us-east-1", ModelID: "synthetic-model-v1", MaxTokens: 256,
		PromptVersion: extract.ExtractionPromptVersion,
		SchemaDigest:  sha256.Sum256(extract.ExtractionJSONSchema()),
	}
	first, err := ComputeDerivationDigest(version, base)
	if err != nil {
		t.Fatalf("ComputeDerivationDigest() error = %v", err)
	}
	tests := map[string]func(*DerivationIdentity){
		"region":         func(identity *DerivationIdentity) { identity.Region = "us-west-2" },
		"model ID":       func(identity *DerivationIdentity) { identity.ModelID = "synthetic-model-v2" },
		"max tokens":     func(identity *DerivationIdentity) { identity.MaxTokens++ },
		"prompt version": func(identity *DerivationIdentity) { identity.PromptVersion = "extract-v3" },
		"schema":         func(identity *DerivationIdentity) { identity.SchemaDigest = sha256.Sum256([]byte("changed schema")) },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			changed := base
			mutate(&changed)
			digest, err := ComputeDerivationDigest(version, changed)
			if err != nil {
				t.Fatalf("ComputeDerivationDigest() error = %v", err)
			}
			if digest == first {
				t.Fatal("material extraction configuration reused derivation identity")
			}
		})
	}
}

func TestComputeDerivationDigestPreservesBedrockV5Bytes(t *testing.T) {
	version, err := knowledge.NewDocumentVersion(knowledge.DocumentVersionInput{
		Provider: "drive", ProviderDocumentID: "document-compat", Title: "Synthetic Meeting",
		Locator: "https://example.invalid/document", ProviderVersion: "version-1", ProviderRevision: "revision-1",
		ModifiedAt: time.Date(2026, time.July, 21, 11, 0, 0, 0, time.UTC),
		RecordedAt: time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC),
		Tabs:       []source.Tab{{ID: "transcript-tab", Title: "Transcript", Path: []string{"Transcript"}, Role: source.TabRoleTranscript, Text: "Synthetic transcript."}},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := ComputeDerivationDigest(version, DerivationIdentity{
		Provider: modelpolicy.ProviderBedrock,
		Region:   "us-east-1", ModelID: "synthetic-model", MaxTokens: 256,
		PromptVersion: extract.ExtractionPromptVersion,
		SchemaDigest:  sha256.Sum256(extract.ExtractionJSONSchema()),
	})
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(got[:]) != "b000ccd59675147eebe18c0698207b23ab154160413ec40800c09dbb32e266e1" {
		t.Fatalf("digest = %x", got)
	}
}

func TestComputeDerivationDigestSeparatesProviders(t *testing.T) {
	version := documentVersion(t, syntheticDocument("document-provider-identity", "Synthetic Person delegated a task."))
	base := DerivationIdentity{
		ModelID: "synthetic-model", MaxTokens: 256,
		PromptVersion: extract.ExtractionPromptVersion,
		SchemaDigest:  sha256.Sum256(extract.ExtractionJSONSchema()),
	}
	digests := make(map[modelpolicy.Provider][sha256.Size]byte)
	for _, provider := range []modelpolicy.Provider{modelpolicy.ProviderBedrock, modelpolicy.ProviderOpenAI, modelpolicy.ProviderAnthropic} {
		identity := base
		identity.Provider = provider
		if provider == modelpolicy.ProviderBedrock {
			identity.Region = "us-east-1"
		}
		digest, err := ComputeDerivationDigest(version, identity)
		if err != nil {
			t.Fatalf("ComputeDerivationDigest(%q) error = %v", provider, err)
		}
		digests[provider] = digest
	}
	if digests[modelpolicy.ProviderBedrock] == digests[modelpolicy.ProviderOpenAI] ||
		digests[modelpolicy.ProviderBedrock] == digests[modelpolicy.ProviderAnthropic] ||
		digests[modelpolicy.ProviderOpenAI] == digests[modelpolicy.ProviderAnthropic] {
		t.Fatalf("provider digests collided: %#v", digests)
	}
}

func TestExtractionDerivationNamespaceAdvancesPastSupersededIdentitySemantics(t *testing.T) {
	if extractionDerivationDigestVersion != "stacks.extraction-derivation.v5" {
		t.Fatalf("extractionDerivationDigestVersion = %q, want snapshot-coherent v5", extractionDerivationDigestVersion)
	}
}

func TestComputeDerivationDigestIncludesLogicalSourceIdentity(t *testing.T) {
	firstVersion := documentVersion(t, syntheticDocument("source-document-a", "Synthetic Person delegated a task."))
	secondVersion := documentVersion(t, syntheticDocument("source-document-b", "Synthetic Person delegated a task."))
	if firstVersion.Digest() != secondVersion.Digest() {
		t.Fatal("fixture document content digests differ; source identity regression is not isolated")
	}
	identity := DerivationIdentity{
		Provider: modelpolicy.ProviderBedrock,
		Region:   "us-east-1", ModelID: "synthetic-model-v1", MaxTokens: 256,
		PromptVersion: extract.ExtractionPromptVersion,
		SchemaDigest:  sha256.Sum256(extract.ExtractionJSONSchema()),
	}
	first, err := ComputeDerivationDigest(firstVersion, identity)
	if err != nil {
		t.Fatalf("ComputeDerivationDigest(first) error = %v", err)
	}
	second, err := ComputeDerivationDigest(secondVersion, identity)
	if err != nil {
		t.Fatalf("ComputeDerivationDigest(second) error = %v", err)
	}
	if first == second {
		t.Fatal("distinct logical source documents reused one extraction derivation identity")
	}
}

func renderRecordedSpan(span tracetest.SpanStub) string {
	var rendered strings.Builder
	rendered.WriteString(span.Name)
	rendered.WriteString(span.Status.Description)
	for _, value := range span.Attributes {
		rendered.WriteString(string(value.Key))
		rendered.WriteString(value.Value.String())
	}
	for _, event := range span.Events {
		rendered.WriteString(event.Name)
		for _, value := range event.Attributes {
			rendered.WriteString(string(value.Key))
			rendered.WriteString(value.Value.String())
		}
	}
	return rendered.String()
}
