// Package ingest coordinates idempotent document-version extraction and
// durable graph persistence without depending on provider or database types.
package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"stacks/internal/entity"
	"stacks/internal/extract"
	"stacks/internal/knowledge"
	"stacks/internal/observability"
	"stacks/internal/source"
)

const (
	ingestionSpanName     = "stacks.ingest.sync"
	ingestionDecisionName = "ingest_document"
	interactionPredicate  = "interaction_signal"
)

// Outcome is the bounded per-document result exposed by sync.
type Outcome string

const (
	OutcomeUnchanged  Outcome = "unchanged"
	OutcomeCompleted  Outcome = "completed"
	OutcomeIncomplete Outcome = "incomplete"
	OutcomeFailed     Outcome = "failed"
)

// VersionStatus is the durable processing state for one immutable version.
type VersionStatus string

const (
	VersionStatusPending    VersionStatus = "pending"
	VersionStatusComplete   VersionStatus = "complete"
	VersionStatusIncomplete VersionStatus = "incomplete"
	VersionStatusFailed     VersionStatus = "failed"
)

// FailureCode is deliberately finite so persistence and telemetry never
// contain private provider, model, or document text.
type FailureCode string

const (
	FailureSource        FailureCode = "source_error"
	FailureInvalidSource FailureCode = "invalid_source"
	FailureModel         FailureCode = "model_error"
	FailureInvalidOutput FailureCode = "invalid_output"
	FailureStorage       FailureCode = "storage_error"
)

// VersionState identifies the durable processing state returned before model
// work begins. RetryCount counts attempts after the initial attempt.
type VersionState struct {
	ID          string
	Status      VersionStatus
	RetryCount  int
	FailureCode FailureCode
}

// EvidenceRecord maps a model-local citation key to validated exact evidence.
type EvidenceRecord struct {
	Key  string
	Span knowledge.EvidenceSpan
}

// MentionRecord is one source-grounded person mention and its deterministic
// identity-resolution result.
type MentionRecord struct {
	Key         string
	EvidenceKey string
	Surface     string
	Role        string
	Resolution  entity.Resolution
}

// ObservationRecord is one inferred interaction observation. Empty entity IDs
// preserve unresolved identity rather than promoting a guess to graph truth.
type ObservationRecord struct {
	ID              string
	SubjectEntityID string
	ObjectEntityID  string
	Predicate       string
	ValidStart      *time.Time
	EvidenceKeys    []string
	Confidence      *float64
}

// SignalEvidenceRecord associates one citation with its bounded signal role.
type SignalEvidenceRecord struct {
	EvidenceKey string
	Role        string
}

// SignalRecord contains the validated, model-derived interaction signal.
type SignalRecord struct {
	ID                string
	ObservationID     string
	Category          string
	Direction         string
	ExtractionModelID string
	PromptVersion     string
	Rationale         string
	Confidence        float64
	Evidence          []SignalEvidenceRecord
}

// Completion is the complete atomic write-set for one document version.
type Completion struct {
	VersionID    string
	Evidence     []EvidenceRecord
	Mentions     []MentionRecord
	Observations []ObservationRecord
	Signals      []SignalRecord
}

// Repository owns durable processing state and the atomic completion boundary.
type Repository interface {
	PrepareVersion(context.Context, knowledge.DocumentVersion) (VersionState, error)
	CompleteVersion(context.Context, Completion) error
	RecordFailure(context.Context, string, VersionStatus, FailureCode) error
	EntitySnapshots(context.Context) ([]entity.EntitySnapshot, error)
}

// Resolver admits only accepted identities and otherwise returns reviewable
// candidates.
type Resolver interface {
	Resolve(entity.Mention, []entity.EntitySnapshot) entity.Resolution
}

// DecisionRecorder records one bounded ingestion decision on the owning span.
type DecisionRecorder interface {
	Record(context.Context, observability.DecisionObservation) error
}

// Service coordinates one folder sync. Dependencies remain lazy at the
// command boundary, so unrelated commands do not initialize them.
type Service struct {
	Source        source.Source
	Model         extract.Model
	Resolver      Resolver
	Repository    Repository
	CollectionID  string
	PromptVersion string
	Tracer        trace.Tracer
	Decisions     DecisionRecorder
	Now           func() time.Time
}

// Result is one privacy-safe per-document outcome.
type Result struct {
	DocumentID  string
	VersionID   string
	Outcome     Outcome
	RetryCount  int
	FailureCode FailureCode
}

// Summary reports bounded totals and ordered per-document results.
type Summary struct {
	Results    []Result
	Unchanged  int
	Completed  int
	Incomplete int
	Failed     int
}

// Sync lists direct documents and processes each as an independent failure
// boundary. Only cancellation or failure to list the collection aborts the run.
func (service *Service) Sync(ctx context.Context) (summary Summary, resultErr error) {
	if err := service.validate(); err != nil {
		return Summary{}, err
	}
	tracer := service.Tracer
	if tracer == nil {
		tracer = noop.NewTracerProvider().Tracer("stacks")
	}
	ctx, span := tracer.Start(ctx, ingestionSpanName)
	defer func() {
		span.SetAttributes(
			attribute.Int("stacks.ingest.document_count", len(summary.Results)),
			attribute.Int("stacks.ingest.unchanged", summary.Unchanged),
			attribute.Int("stacks.ingest.completed", summary.Completed),
			attribute.Int("stacks.ingest.incomplete", summary.Incomplete),
			attribute.Int("stacks.ingest.failed", summary.Failed),
		)
		observability.FinishSpan(span, resultErr)
	}()

	documents, err := service.Source.List(ctx, service.CollectionID)
	if err != nil {
		return Summary{}, fmt.Errorf("list source documents: %w", err)
	}
	summary.Results = make([]Result, 0, len(documents))
	for _, listed := range documents {
		if err := ctx.Err(); err != nil {
			return summary, err
		}
		started := service.now()
		result := service.processDocument(ctx, listed)
		summary.add(result)
		service.recordDecision(ctx, result.Outcome, service.now().Sub(started))
	}
	return summary, nil
}

func (service *Service) processDocument(ctx context.Context, listed source.Document) Result {
	documentID := strings.TrimSpace(listed.ID)
	document, err := service.Source.Get(ctx, documentID)
	if err != nil {
		return Result{DocumentID: documentID, Outcome: OutcomeFailed, FailureCode: FailureSource}
	}
	version, err := knowledge.NewDocumentVersion(knowledge.DocumentVersionInput{
		Provider: document.Provider, ProviderDocumentID: document.ID,
		RecordedAt: service.now(), Tabs: document.Tabs,
	})
	if err != nil {
		return Result{DocumentID: documentID, Outcome: OutcomeFailed, FailureCode: FailureInvalidSource}
	}
	state, err := service.Repository.PrepareVersion(ctx, version)
	if err != nil {
		return Result{DocumentID: documentID, Outcome: OutcomeIncomplete, FailureCode: FailureStorage}
	}
	result := Result{DocumentID: documentID, VersionID: state.ID, RetryCount: state.RetryCount}
	if state.Status == VersionStatusComplete {
		result.Outcome = OutcomeUnchanged
		return result
	}

	submitted, request, err := extractionRequest(document.Tabs, service.PromptVersion)
	if err != nil {
		return service.fail(ctx, result, VersionStatusFailed, FailureInvalidSource)
	}
	response, err := service.Model.Generate(ctx, request)
	if err != nil {
		return service.fail(ctx, result, VersionStatusIncomplete, FailureModel)
	}
	if response.ModelID == "" || response.PromptVersion != service.PromptVersion {
		return service.fail(ctx, result, VersionStatusFailed, FailureInvalidOutput)
	}
	output, err := extract.DecodeAndValidateExtraction(submitted, response.Output)
	if err != nil {
		return service.fail(ctx, result, VersionStatusFailed, FailureInvalidOutput)
	}
	snapshots, err := service.Repository.EntitySnapshots(ctx)
	if err != nil {
		return service.fail(ctx, result, VersionStatusIncomplete, FailureStorage)
	}
	completion, err := service.completion(version, state.ID, response, output, snapshots)
	if err != nil {
		return service.fail(ctx, result, VersionStatusFailed, FailureInvalidOutput)
	}
	if err := service.Repository.CompleteVersion(ctx, completion); err != nil {
		return service.fail(ctx, result, VersionStatusIncomplete, FailureStorage)
	}
	result.Outcome = OutcomeCompleted
	return result
}

func (service *Service) completion(
	version knowledge.DocumentVersion,
	versionID string,
	response extract.Response,
	output extract.ExtractionOutput,
	snapshots []entity.EntitySnapshot,
) (Completion, error) {
	completion := Completion{VersionID: versionID}
	for _, citation := range output.Citations {
		span, err := knowledge.NewEvidenceSpan(knowledge.EvidenceSpanInput{
			Document: version, TabID: citation.TabID, StartOffset: citation.StartOffset,
			EndOffset: citation.EndOffset, Quote: citation.Quote,
		})
		if err != nil {
			return Completion{}, err
		}
		completion.Evidence = append(completion.Evidence, EvidenceRecord{Key: citation.ID, Span: span})
	}

	resolutions := make(map[string]entity.Resolution, len(output.People))
	for _, person := range output.People {
		resolution := service.Resolver.Resolve(entity.Mention{Surface: person.Email}, snapshots)
		if person.Email == "" || (!resolution.AutoResolved && len(resolution.Candidates) == 0) {
			resolution = service.Resolver.Resolve(entity.Mention{Surface: person.Surface}, snapshots)
		}
		resolutions[person.ID] = resolution
		completion.Mentions = append(completion.Mentions, MentionRecord{
			Key: person.ID, EvidenceKey: person.CitationIDs[0], Surface: person.Surface,
			Role: person.Role, Resolution: resolution,
		})
	}

	validStart, err := parseOptionalDate(output.MeetingDate)
	if err != nil {
		return Completion{}, err
	}
	statements := make(map[string]extract.AttributedStatement, len(output.Statements))
	for _, statement := range output.Statements {
		statements[statement.ID] = statement
	}
	for _, signal := range output.Signals {
		observationID := stableID(version, "observation", signal.ID)
		subject := resolvedEntityID(resolutions[signal.SubjectMentionID])
		object := resolvedEntityID(resolutions[signal.ObjectMentionID])
		evidenceKeys := signalEvidenceKeys(signal, statements)
		confidence := signal.Confidence
		completion.Observations = append(completion.Observations, ObservationRecord{
			ID: observationID, SubjectEntityID: subject, ObjectEntityID: object,
			Predicate: interactionPredicate, ValidStart: validStart,
			EvidenceKeys: evidenceKeys, Confidence: &confidence,
		})
		signalEvidence := make([]SignalEvidenceRecord, 0, len(signal.SupportingCitationIDs)+len(signal.ContradictingCitationIDs))
		for _, key := range signal.SupportingCitationIDs {
			signalEvidence = append(signalEvidence, SignalEvidenceRecord{EvidenceKey: key, Role: "supporting"})
		}
		for _, key := range signal.ContradictingCitationIDs {
			signalEvidence = append(signalEvidence, SignalEvidenceRecord{EvidenceKey: key, Role: "contradicting"})
		}
		completion.Signals = append(completion.Signals, SignalRecord{
			ID: stableID(version, "signal", signal.ID), ObservationID: observationID,
			Category: signal.Category, Direction: signal.Direction,
			ExtractionModelID: response.ModelID, PromptVersion: response.PromptVersion,
			Rationale: signal.Rationale, Confidence: signal.Confidence, Evidence: signalEvidence,
		})
	}
	return completion, nil
}

func extractionRequest(tabs []source.Tab, promptVersion string) (extract.SubmittedText, extract.Request, error) {
	contract, err := extract.PromptContract(promptVersion)
	if err != nil {
		return extract.SubmittedText{}, extract.Request{}, err
	}
	submitted := extract.SubmittedText{}
	transcriptCount := 0
	for _, tab := range tabs {
		var role extract.TabRole
		switch tab.Role {
		case source.TabRoleTranscript:
			role = extract.TabRoleTranscript
			transcriptCount++
		case source.TabRoleGeminiNotes:
			role = extract.TabRoleNotes
		default:
			continue
		}
		submitted.Tabs = append(submitted.Tabs, extract.SubmittedTab{ID: tab.ID, Role: role, Text: tab.Text})
	}
	if transcriptCount != 1 {
		return extract.SubmittedText{}, extract.Request{}, fmt.Errorf("document transcript classification is invalid")
	}
	input, err := json.Marshal(struct {
		Tabs []struct {
			ID   string          `json:"id"`
			Role extract.TabRole `json:"role"`
			Text string          `json:"text"`
		} `json:"tabs"`
	}{Tabs: modelTabs(submitted.Tabs)})
	if err != nil {
		return extract.SubmittedText{}, extract.Request{}, fmt.Errorf("encode extraction input")
	}
	return submitted, extract.Request{
		PromptVersion: contract.Version, SystemPrompt: contract.SystemPrompt,
		Input: string(input), SchemaName: contract.SchemaName, JSONSchema: contract.JSONSchema,
	}, nil
}

func modelTabs(tabs []extract.SubmittedTab) []struct {
	ID   string          `json:"id"`
	Role extract.TabRole `json:"role"`
	Text string          `json:"text"`
} {
	encoded := make([]struct {
		ID   string          `json:"id"`
		Role extract.TabRole `json:"role"`
		Text string          `json:"text"`
	}, len(tabs))
	for index, tab := range tabs {
		encoded[index].ID = tab.ID
		encoded[index].Role = tab.Role
		encoded[index].Text = tab.Text
	}
	return encoded
}

func signalEvidenceKeys(signal extract.InteractionSignal, statements map[string]extract.AttributedStatement) []string {
	seen := make(map[string]struct{})
	keys := make([]string, 0)
	appendKey := func(key string) {
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	for _, statementID := range signal.StatementIDs {
		for _, key := range statements[statementID].CitationIDs {
			appendKey(key)
		}
	}
	for _, key := range signal.SupportingCitationIDs {
		appendKey(key)
	}
	for _, key := range signal.ContradictingCitationIDs {
		appendKey(key)
	}
	return keys
}

func resolvedEntityID(resolution entity.Resolution) string {
	if resolution.AutoResolved {
		return resolution.EntityID
	}
	return ""
}

func parseOptionalDate(value string) (*time.Time, error) {
	if value == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.DateOnly, value)
	if err != nil {
		return nil, err
	}
	parsed = parsed.UTC()
	return &parsed, nil
}

func stableID(version knowledge.DocumentVersion, kind, sourceID string) string {
	seed := strings.Join([]string{version.Provider(), version.ProviderDocumentID(), version.Digest().String(), kind, sourceID}, "\x00")
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(seed)).String()
}

func (service *Service) fail(ctx context.Context, result Result, status VersionStatus, code FailureCode) Result {
	if result.VersionID != "" {
		_ = service.Repository.RecordFailure(ctx, result.VersionID, status, code)
	}
	result.FailureCode = code
	if status == VersionStatusFailed {
		result.Outcome = OutcomeFailed
	} else {
		result.Outcome = OutcomeIncomplete
	}
	return result
}

func (service *Service) validate() error {
	if service == nil || service.Source == nil || service.Model == nil || service.Resolver == nil || service.Repository == nil || service.Now == nil {
		return fmt.Errorf("sync service dependencies are required")
	}
	if strings.TrimSpace(service.CollectionID) == "" || strings.TrimSpace(service.PromptVersion) == "" {
		return fmt.Errorf("sync collection and prompt version are required")
	}
	return nil
}

func (service *Service) now() time.Time {
	return service.Now().UTC()
}

func (service *Service) recordDecision(ctx context.Context, outcome Outcome, duration time.Duration) {
	if service.Decisions == nil {
		return
	}
	_ = service.Decisions.Record(ctx, observability.DecisionObservation{
		Name: ingestionDecisionName, Outcome: string(outcome), Duration: duration,
		InputSize: 1, OutputSize: 1,
	})
}

func (summary *Summary) add(result Result) {
	summary.Results = append(summary.Results, result)
	switch result.Outcome {
	case OutcomeUnchanged:
		summary.Unchanged++
	case OutcomeCompleted:
		summary.Completed++
	case OutcomeIncomplete:
		summary.Incomplete++
	case OutcomeFailed:
		summary.Failed++
	}
}
