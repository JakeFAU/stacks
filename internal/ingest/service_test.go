package ingest

import (
	"context"
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
	repository.versions[versionKey(version)] = VersionState{ID: "version-unchanged", Status: VersionStatusComplete}
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
			{ID: "mention-1", Surface: "Synthetic Leader", Role: extract.MentionRoleSpeaker, CitationIDs: []string{"citation-1"}},
			{ID: "mention-2", Surface: "Synthetic Report", Role: extract.MentionRoleReference, CitationIDs: []string{"citation-1"}},
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

func testService(document source.Document, repository *memoryRepository, model *recordingModel) *Service {
	return testServiceWithSource(&memorySource{
		listed:  []source.Document{{Provider: document.Provider, ID: document.ID}},
		fetched: map[string]source.Document{document.ID: document},
	}, repository, model)
}

func testServiceWithSource(sourceBoundary source.Source, repository *memoryRepository, model extract.Model) *Service {
	return &Service{
		Source:        sourceBoundary,
		Model:         model,
		Resolver:      entity.Resolver{},
		Repository:    repository,
		CollectionID:  "synthetic-folder",
		PromptVersion: extract.ExtractionPromptVersion,
		Now:           func() time.Time { return recordedAt },
	}
}

func syntheticDocument(id, transcript string) source.Document {
	return source.Document{
		Provider: "drive", ID: id, Title: "Synthetic Meeting", Locator: "https://example.invalid/document", Version: "revision-1",
		Tabs: []source.Tab{{ID: "transcript-tab", Title: "Transcript", Path: []string{"Transcript"}, Order: 0, Role: source.TabRoleTranscript, Text: transcript}},
	}
}

func documentVersion(t *testing.T, document source.Document) knowledge.DocumentVersion {
	t.Helper()
	version, err := knowledge.NewDocumentVersion(knowledge.DocumentVersionInput{
		Provider: document.Provider, ProviderDocumentID: document.ID, RecordedAt: recordedAt, Tabs: document.Tabs,
	})
	if err != nil {
		t.Fatalf("NewDocumentVersion() error = %v", err)
	}
	return version
}

func validEmptyResponse(t *testing.T) extract.Response {
	t.Helper()
	output, err := json.Marshal(extract.ExtractionOutput{
		Citations:  []extract.Citation{},
		People:     []extract.PersonMention{},
		Statements: []extract.AttributedStatement{},
		Signals:    []extract.InteractionSignal{},
	})
	if err != nil {
		t.Fatalf("marshal empty extraction: %v", err)
	}
	return extract.Response{Output: output, ModelID: "synthetic-model", PromptVersion: extract.ExtractionPromptVersion, Outcome: "success"}
}

func validSignalResponse(t *testing.T, meetingDate string) extract.Response {
	t.Helper()
	const transcript = "Leader assigns follow-up."
	output, err := json.Marshal(extract.ExtractionOutput{
		MeetingDate: meetingDate,
		Citations:   []extract.Citation{{ID: "citation-1", TabID: "transcript-tab", StartOffset: 0, EndOffset: len(transcript), Quote: transcript}},
		People: []extract.PersonMention{
			{ID: "mention-leader", Surface: "Synthetic Leader", Role: extract.MentionRoleSpeaker, CitationIDs: []string{"citation-1"}},
			{ID: "mention-report", Surface: "Synthetic Report", Role: extract.MentionRoleReference, CitationIDs: []string{"citation-1"}},
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
			{ID: "mention-1", Surface: "Synthetic Person", Role: extract.MentionRoleSpeaker, CitationIDs: []string{"citation-1"}},
			{ID: "mention-2", Surface: "Synthetic Person", Role: extract.MentionRoleSpeaker, CitationIDs: []string{"citation-1"}},
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
	listed  []source.Document
	fetched map[string]source.Document
	listErr error
	getErrs map[string]error
}

func (sourceBoundary *memorySource) List(context.Context, string) ([]source.Document, error) {
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
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{versions: make(map[string]VersionState), completions: make(map[string]Completion)}
}

func (repository *memoryRepository) PrepareVersion(_ context.Context, version knowledge.DocumentVersion) (VersionState, error) {
	if repository.prepareErr != nil {
		return VersionState{}, repository.prepareErr
	}
	key := versionKey(version)
	state, exists := repository.versions[key]
	if !exists {
		state = VersionState{ID: "version-" + version.Digest().String()[:12], Status: VersionStatusPending}
	} else if state.Status != VersionStatusComplete {
		state.Status = VersionStatusPending
		state.RetryCount++
		state.FailureCode = ""
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
		if state.ID == completion.VersionID {
			state.Status = VersionStatusComplete
			state.FailureCode = ""
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

func (repository *memoryRepository) RecordFailure(_ context.Context, versionID string, status VersionStatus, code FailureCode) error {
	if repository.failureRecordErr != nil {
		return repository.failureRecordErr
	}
	for key, state := range repository.versions {
		if state.ID == versionID {
			state.Status = status
			state.FailureCode = code
			repository.versions[key] = state
			return nil
		}
	}
	return errors.New("synthetic version not found")
}

func (repository *memoryRepository) EntitySnapshots(context.Context) ([]entity.EntitySnapshot, error) {
	return nil, repository.snapshotErr
}

func versionKey(version knowledge.DocumentVersion) string {
	return version.Provider() + "/" + version.ProviderDocumentID() + "/" + version.Digest().String()
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
