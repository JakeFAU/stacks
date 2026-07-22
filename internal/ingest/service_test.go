package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"stacks/internal/entity"
	"stacks/internal/extract"
	"stacks/internal/knowledge"
	"stacks/internal/observability"
	"stacks/internal/source"
)

var recordedAt = time.Date(2026, 7, 21, 14, 0, 0, 0, time.UTC)

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

type memorySource struct {
	listed  []source.Document
	fetched map[string]source.Document
}

func (sourceBoundary *memorySource) List(context.Context, string) ([]source.Document, error) {
	return append([]source.Document(nil), sourceBoundary.listed...), nil
}

func (sourceBoundary *memorySource) Get(_ context.Context, documentID string) (source.Document, error) {
	document, ok := sourceBoundary.fetched[documentID]
	if !ok {
		return source.Document{}, errors.New("synthetic source failure")
	}
	return document, nil
}

type recordingModel struct {
	responses []extract.Response
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
	persistedEvidence     int
	persistedMentions     int
	persistedObservations int
	persistedSignals      int
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{versions: make(map[string]VersionState), completions: make(map[string]Completion)}
}

func (repository *memoryRepository) PrepareVersion(_ context.Context, version knowledge.DocumentVersion) (VersionState, error) {
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
	return nil, nil
}

func versionKey(version knowledge.DocumentVersion) string {
	return version.Provider() + "/" + version.ProviderDocumentID() + "/" + version.Digest().String()
}
