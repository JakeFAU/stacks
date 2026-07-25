// Package ingest coordinates idempotent document-version extraction and
// durable graph persistence without depending on provider or database types.
package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"strings"
	"time"

	"github.com/JakeFAU/stacks/core/evidence"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"stacks/internal/analysis"
	"stacks/internal/directory"
	"stacks/internal/entity"
	"stacks/internal/extract"
	"stacks/internal/modelpolicy"
	"stacks/internal/observability"
	"stacks/internal/source"
)

// ErrSourceList is returned without wrapping provider errors so private or
// unbounded source text cannot reach command logging or span error events.
var ErrSourceList = errors.New("sync source listing failed")

// ErrFailurePersistence reports that a classified per-document state could
// not be made durable. It never wraps the repository error.
var ErrFailurePersistence = errors.New("sync failure state persistence failed")

const (
	ingestionSpanName                 = "stacks.ingest.sync"
	ingestionDecisionName             = "ingest_document"
	interactionPredicate              = "interaction_signal"
	extractionDerivationDigestVersion = "stacks.extraction-derivation.v5"
	providerDerivationDigestVersion   = "stacks.extraction-derivation.v6.provider"
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
	// VersionStatusBusy is returned but never persisted when another live claim owns a derivation.
	VersionStatusBusy VersionStatus = "busy"
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
	FailureBusy          FailureCode = "busy"
)

// VersionState identifies the durable processing state returned before model
// work begins. RetryCount counts attempts after the initial attempt.
type VersionState struct {
	ID               string
	DerivationID     string
	DerivationDigest [sha256.Size]byte
	RecordedAt       time.Time
	LeaseOwner       string
	LeaseExpiresAt   time.Time
	Status           VersionStatus
	RetryCount       int
	FailureCode      FailureCode
}

// DerivationIdentity names one extraction attempt configuration independently
// of the immutable source version. Digest is computed from both boundaries.
type DerivationIdentity struct {
	Provider      modelpolicy.Provider
	Region        string
	ModelID       string
	MaxTokens     int
	PromptVersion string
	SchemaDigest  [sha256.Size]byte
	Digest        [sha256.Size]byte
}

// EvidenceRecord maps a model-local citation key to validated exact evidence.
type EvidenceRecord struct {
	Key  string
	Span evidence.EvidenceSpan
}

// MentionRecord is one source-grounded person mention and its deterministic
// identity-resolution result.
type MentionRecord struct {
	Key                      string
	EvidenceKey              string
	ProposedEmailEvidenceKey string
	Surface                  string
	// NormalizedName is the independently grounded name alias input. A model
	// email remains separate audit-only proposal provenance and is never passed
	// to the resolver or taught as an alias by ingestion or proposal acceptance.
	NormalizedName string
	ProposedEmail  string
	Role           string
	Resolution     entity.Resolution
}

// ObservationRecord is one inferred interaction observation. Empty entity IDs
// preserve unresolved identity rather than promoting a guess to graph truth.
type ObservationRecord struct {
	ID                string
	SubjectEntityID   string
	ObjectEntityID    string
	SubjectMentionKey string
	ObjectMentionKey  string
	Predicate         string
	ValidStart        *time.Time
	EvidenceKeys      []string
	Confidence        *float64
	RecordedAt        time.Time
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
	DerivationID string
	LeaseOwner   string
	DataMode     modelpolicy.DataMode
	Evidence     []EvidenceRecord
	Mentions     []MentionRecord
	Observations []ObservationRecord
	Signals      []SignalRecord
}

// Repository owns durable processing state and the atomic completion boundary.
type Repository interface {
	PrepareVersion(context.Context, evidence.DocumentVersion, DerivationIdentity, modelpolicy.DataMode, time.Duration) (VersionState, error)
	CompleteVersion(context.Context, Completion) error
	RecordFailure(context.Context, string, string, VersionStatus, FailureCode) error
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

// IdentityEnricher adds optional directory-backed identity evidence only after
// extraction completion is durable.
type IdentityEnricher interface {
	Enrich(context.Context, string) (directory.Summary, error)
}

// Service coordinates one folder sync. Dependencies remain lazy at the
// command boundary, so unrelated commands do not initialize them.
type Service struct {
	Source           source.Source
	Model            extract.Model
	Resolver         Resolver
	Repository       Repository
	CollectionID     string
	PromptVersion    string
	Provider         modelpolicy.Provider
	DataMode         modelpolicy.DataMode
	Region           string
	ModelID          string
	MaxTokens        int
	LeaseDuration    time.Duration
	AttemptTimeout   time.Duration
	Tracer           trace.Tracer
	Decisions        DecisionRecorder
	IdentityEnricher IdentityEnricher
	Now              func() time.Time
}

// Result is one privacy-safe per-document outcome.
type Result struct {
	DocumentID   string
	VersionID    string
	DerivationID string
	Outcome      Outcome
	RetryCount   int
	FailureCode  FailureCode
	Directory    directory.Summary
	leaseOwner   string
}

// Summary reports bounded totals and ordered per-document results.
type Summary struct {
	Results    []Result
	Unchanged  int
	Completed  int
	Incomplete int
	Failed     int
	Directory  directory.Summary
}

// Sync lists direct documents and processes each as an independent failure
// boundary. Cancellation and bounded global authentication or authorization
// failures abort the run immediately.
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
	if cancellationErr := boundedCancellation(ctx, err); cancellationErr != nil {
		return Summary{}, cancellationErr
	}
	if authErr := boundedGlobalAuthentication(err); authErr != nil {
		return Summary{}, authErr
	}
	if err != nil {
		return Summary{}, ErrSourceList
	}
	summary.Results = make([]Result, 0, len(documents))
	var aggregateErr error
	for _, listed := range documents {
		if err := ctx.Err(); err != nil {
			return summary, errors.Join(aggregateErr, err)
		}
		started := service.now()
		result, documentErr := service.processDocument(ctx, listed)
		if cancellationErr := boundedCancellation(ctx, documentErr); cancellationErr != nil {
			if result.Outcome == OutcomeCompleted || result.Outcome == OutcomeUnchanged {
				summary.add(result)
				service.recordDecision(ctx, result.Outcome, service.now().Sub(started))
			}
			return summary, cancellationErr
		}
		if authErr := boundedGlobalAuthentication(documentErr); authErr != nil {
			return summary, errors.Join(aggregateErr, authErr)
		}
		summary.add(result)
		service.recordDecision(ctx, result.Outcome, service.now().Sub(started))
		if documentErr != nil {
			aggregateErr = ErrFailurePersistence
		}
	}
	return summary, aggregateErr
}

func (service *Service) processDocument(ctx context.Context, listed source.Document) (Result, error) {
	documentID := strings.TrimSpace(listed.ID)
	document, err := service.Source.Get(ctx, documentID)
	if cancellationErr := boundedCancellation(ctx, err); cancellationErr != nil {
		return Result{}, cancellationErr
	}
	if authErr := boundedGlobalAuthentication(err); authErr != nil {
		return Result{}, authErr
	}
	if err != nil {
		return Result{DocumentID: documentID, Outcome: OutcomeFailed, FailureCode: FailureSource}, nil
	}
	document, err = mergeSourceDocument(listed, document)
	if err != nil {
		return Result{DocumentID: documentID, Outcome: OutcomeFailed, FailureCode: FailureInvalidSource}, nil
	}
	sections, err := evidenceSections(document.Tabs)
	if err != nil {
		return Result{DocumentID: documentID, Outcome: OutcomeFailed, FailureCode: FailureInvalidSource}, nil
	}
	version, err := evidence.NewDocumentVersion(evidence.DocumentVersionInput{
		Provider: document.Provider, ProviderDocumentID: document.ID, Title: document.Title,
		Locator: document.Locator, ProviderVersion: document.Version, ProviderRevision: document.Revision,
		ModifiedAt: document.ModifiedAt, SourceTime: document.MeetingTime,
		RecordedAt: service.now(), Sections: sections,
	})
	if err != nil {
		return Result{DocumentID: documentID, Outcome: OutcomeFailed, FailureCode: FailureInvalidSource}, nil
	}
	contract, err := extract.PromptContract(service.PromptVersion)
	if err != nil {
		return Result{DocumentID: documentID, Outcome: OutcomeFailed, FailureCode: FailureInvalidSource}, nil
	}
	derivation := DerivationIdentity{
		Provider: service.Provider,
		Region:   service.Region, ModelID: strings.TrimSpace(service.ModelID), MaxTokens: service.MaxTokens,
		PromptVersion: contract.Version, SchemaDigest: sha256.Sum256(contract.JSONSchema),
	}
	derivation.Digest, err = ComputeDerivationDigest(version, derivation)
	if err != nil {
		return Result{DocumentID: documentID, Outcome: OutcomeFailed, FailureCode: FailureInvalidSource}, nil
	}
	state, err := service.Repository.PrepareVersion(ctx, version, derivation, service.DataMode, service.LeaseDuration)
	if cancellationErr := boundedCancellation(ctx, err); cancellationErr != nil {
		return Result{}, cancellationErr
	}
	if authErr := boundedGlobalAuthentication(err); authErr != nil {
		return Result{}, authErr
	}
	if err != nil {
		return Result{DocumentID: documentID, Outcome: OutcomeIncomplete, FailureCode: FailureStorage}, nil
	}
	result := Result{
		DocumentID: documentID, VersionID: state.ID, DerivationID: state.DerivationID,
		RetryCount: state.RetryCount, leaseOwner: state.LeaseOwner,
	}
	if state.Status == VersionStatusComplete {
		result.Outcome = OutcomeUnchanged
		return service.enrich(ctx, result)
	}
	if state.Status == VersionStatusBusy {
		result.Outcome = OutcomeIncomplete
		result.FailureCode = FailureBusy
		return result, nil
	}
	attemptCtx, cancelAttempt, err := service.attemptContext(ctx, state)
	if err != nil {
		return service.fail(ctx, result, VersionStatusIncomplete, FailureModel)
	}
	defer cancelAttempt()

	submitted, request, err := extractionRequest(document.Tabs, document.MeetingTime, service.PromptVersion)
	if err != nil {
		return service.fail(ctx, result, VersionStatusFailed, FailureInvalidSource)
	}
	response, err := service.Model.Generate(attemptCtx, request)
	if cancellationErr := boundedAttemptCancellation(ctx, attemptCtx, err); cancellationErr != nil {
		return Result{}, cancellationErr
	}
	if errors.Is(attemptCtx.Err(), context.DeadlineExceeded) {
		return service.fail(ctx, result, VersionStatusIncomplete, FailureModel)
	}
	if authErr := boundedGlobalAuthentication(err); authErr != nil {
		if _, failureErr := service.fail(ctx, result, VersionStatusIncomplete, FailureModel); failureErr != nil {
			return Result{}, errors.Join(authErr, failureErr)
		}
		return Result{}, authErr
	}
	if err != nil {
		return service.fail(ctx, result, VersionStatusIncomplete, FailureModel)
	}
	if response.ModelID != derivation.ModelID || response.PromptVersion != derivation.PromptVersion {
		return service.fail(ctx, result, VersionStatusFailed, FailureInvalidOutput)
	}
	output, err := extract.DecodeAndValidateExtraction(submitted, response.Output)
	if err != nil {
		return service.fail(ctx, result, VersionStatusFailed, FailureInvalidOutput)
	}
	snapshots, err := service.Repository.EntitySnapshots(attemptCtx)
	if cancellationErr := boundedAttemptCancellation(ctx, attemptCtx, err); cancellationErr != nil {
		return Result{}, cancellationErr
	}
	if errors.Is(attemptCtx.Err(), context.DeadlineExceeded) {
		return service.fail(ctx, result, VersionStatusIncomplete, FailureStorage)
	}
	if err != nil {
		return service.fail(ctx, result, VersionStatusIncomplete, FailureStorage)
	}
	completion, err := service.completion(version, state, response, output, snapshots)
	if err != nil {
		return service.fail(ctx, result, VersionStatusFailed, FailureInvalidOutput)
	}
	if err := ValidateForPersistence(completion); err != nil {
		return service.fail(ctx, result, VersionStatusFailed, FailureInvalidOutput)
	}
	if err := service.Repository.CompleteVersion(attemptCtx, completion); err != nil {
		if cancellationErr := boundedAttemptCancellation(ctx, attemptCtx, err); cancellationErr != nil {
			return Result{}, cancellationErr
		}
		if errors.Is(attemptCtx.Err(), context.DeadlineExceeded) {
			return service.fail(ctx, result, VersionStatusIncomplete, FailureStorage)
		}
		return service.fail(ctx, result, VersionStatusIncomplete, FailureStorage)
	}
	cancelAttempt()
	result.Outcome = OutcomeCompleted
	return service.enrich(ctx, result)
}

func (service *Service) enrich(ctx context.Context, result Result) (Result, error) {
	if service.IdentityEnricher == nil {
		return result, nil
	}
	summary, err := service.IdentityEnricher.Enrich(ctx, result.DerivationID)
	result.Directory = summary
	if cancellationErr := boundedCancellation(ctx, err); cancellationErr != nil {
		return result, cancellationErr
	}
	if err != nil {
		result.Directory.Unavailable++
	}
	return result, nil
}

func (service *Service) completion(
	version evidence.DocumentVersion,
	state VersionState,
	response extract.Response,
	output extract.ExtractionOutput,
	snapshots []entity.EntitySnapshot,
) (Completion, error) {
	completion := Completion{
		VersionID: state.ID, DerivationID: state.DerivationID, LeaseOwner: state.LeaseOwner,
		DataMode: service.DataMode,
	}
	for _, citation := range output.Citations {
		span, err := evidence.NewEvidenceSpan(evidence.EvidenceSpanInput{
			Document: version, SectionID: citation.TabID, StartOffset: citation.StartOffset,
			EndOffset: citation.EndOffset, Quote: citation.Quote,
		})
		if err != nil {
			return Completion{}, err
		}
		completion.Evidence = append(completion.Evidence, EvidenceRecord{Key: citation.ID, Span: span})
	}

	resolutions := make(map[string]entity.Resolution, len(output.People))
	for _, person := range output.People {
		identity, err := extract.GroundPersonIdentity(person, output.Citations)
		if err != nil {
			return Completion{}, err
		}
		resolution := service.Resolver.Resolve(entity.Mention{Name: person.Surface}, snapshots)
		resolutions[person.ID] = resolution
		completion.Mentions = append(completion.Mentions, MentionRecord{
			Key: person.ID, EvidenceKey: identity.NameEvidenceCitationID,
			ProposedEmailEvidenceKey: identity.EmailEvidenceCitationID,
			Surface:                  person.Surface, NormalizedName: identity.NormalizedName,
			ProposedEmail: identity.ProposedEmail, Role: person.Role, Resolution: resolution,
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
		observationID := stableDerivationID(state.DerivationDigest, "observation", signal.ID)
		subject := resolvedEntityID(resolutions[signal.SubjectMentionID])
		object := resolvedEntityID(resolutions[signal.ObjectMentionID])
		evidenceKeys := signalEvidenceKeys(signal, statements)
		confidence := signal.Confidence
		completion.Observations = append(completion.Observations, ObservationRecord{
			ID: observationID, SubjectEntityID: subject, ObjectEntityID: object,
			SubjectMentionKey: signal.SubjectMentionID, ObjectMentionKey: signal.ObjectMentionID,
			Predicate: interactionPredicate, ValidStart: validStart,
			EvidenceKeys: evidenceKeys, Confidence: &confidence, RecordedAt: state.RecordedAt,
		})
		signalEvidence := make([]SignalEvidenceRecord, 0, len(signal.SupportingCitationIDs)+len(signal.ContradictingCitationIDs))
		for _, key := range signal.SupportingCitationIDs {
			signalEvidence = append(signalEvidence, SignalEvidenceRecord{EvidenceKey: key, Role: "supporting"})
		}
		for _, key := range signal.ContradictingCitationIDs {
			signalEvidence = append(signalEvidence, SignalEvidenceRecord{EvidenceKey: key, Role: "contradicting"})
		}
		completion.Signals = append(completion.Signals, SignalRecord{
			ID: stableDerivationID(state.DerivationDigest, "signal", signal.ID), ObservationID: observationID,
			Category: signal.Category, Direction: signal.Direction,
			ExtractionModelID: response.ModelID, PromptVersion: response.PromptVersion,
			Rationale:  analysis.ExplainSignal(analysis.Category(signal.Category), analysis.Direction(signal.Direction)),
			Confidence: signal.Confidence, Evidence: signalEvidence,
		})
	}
	return completion, nil
}

func extractionRequest(tabs []source.Tab, sourceMeetingTime *time.Time, promptVersion string) (extract.SubmittedText, extract.Request, error) {
	contract, err := extract.PromptContract(promptVersion)
	if err != nil {
		return extract.SubmittedText{}, extract.Request{}, err
	}
	submitted := extract.SubmittedText{SourceMeetingTime: sourceMeetingTime}
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

func evidenceSections(tabs []source.Tab) ([]evidence.Section, error) {
	sections := make([]evidence.Section, len(tabs))
	for index, tab := range tabs {
		switch tab.Role {
		case source.TabRoleOther, source.TabRoleTranscript, source.TabRoleGeminiNotes:
		default:
			return nil, fmt.Errorf("document tab %d: role is invalid", index)
		}
		section, err := evidence.NewSection(evidence.SectionInput{
			ID: tab.ID, Title: tab.Title, ParentID: tab.ParentID, Path: tab.Path,
			Order: tab.Order, Role: string(tab.Role), Text: tab.Text,
		})
		if err != nil {
			return nil, fmt.Errorf("document tab %d: %w", index, err)
		}
		sections[index] = section
	}
	return sections, nil
}

func mergeSourceDocument(listed, fetched source.Document) (source.Document, error) {
	if strings.TrimSpace(listed.ID) == "" || strings.TrimSpace(fetched.ID) == "" || listed.ID != fetched.ID {
		return source.Document{}, fmt.Errorf("source document identity is inconsistent")
	}
	if fetched.Provider == "" {
		fetched.Provider = listed.Provider
	}
	if listed.Provider != "" && fetched.Provider != listed.Provider {
		return source.Document{}, fmt.Errorf("source document provider is inconsistent")
	}
	if fetched.Title == "" {
		// MeetingTime is derived from the title at the source boundary, so both
		// values must come from the same provider snapshot.
		fetched.Title = listed.Title
		fetched.MeetingTime = listed.MeetingTime
	}
	if fetched.Locator == "" {
		fetched.Locator = listed.Locator
	}
	if listed.Version != "" {
		fetched.Version = listed.Version
	}
	if !listed.ModifiedAt.IsZero() {
		fetched.ModifiedAt = listed.ModifiedAt
	}
	return fetched, nil
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

func stableDerivationID(derivationDigest [sha256.Size]byte, kind, sourceID string) string {
	seed := strings.Join([]string{fmt.Sprintf("%x", derivationDigest), kind, sourceID}, "\x00")
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(seed)).String()
}

// ComputeDerivationDigest identifies one immutable source version processed by
// one exact extraction configuration. It excludes attempt/lease state.
func ComputeDerivationDigest(version evidence.DocumentVersion, identity DerivationIdentity) ([sha256.Size]byte, error) {
	if err := (modelpolicy.Invocation{
		Provider: identity.Provider,
		DataMode: modelpolicy.DataModePersonal,
		Region:   identity.Region,
	}).Validate(); err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("extraction derivation identity is invalid")
	}
	if strings.TrimSpace(identity.ModelID) == "" ||
		strings.TrimSpace(identity.PromptVersion) == "" || identity.MaxTokens <= 0 ||
		identity.SchemaDigest == ([sha256.Size]byte{}) {
		return [sha256.Size]byte{}, fmt.Errorf("extraction derivation identity is invalid")
	}
	hasher := sha256.New()
	digestVersion := providerDerivationDigestVersion
	if identity.Provider == modelpolicy.ProviderBedrock {
		digestVersion = extractionDerivationDigestVersion
	}
	writeDerivationString(hasher, digestVersion)
	writeDerivationString(hasher, version.Provider())
	writeDerivationString(hasher, version.ProviderDocumentID())
	documentDigest := version.Digest()
	writeDerivationBytes(hasher, documentDigest[:])
	writeDerivationString(hasher, strings.TrimSpace(identity.Region))
	if identity.Provider != modelpolicy.ProviderBedrock {
		writeDerivationString(hasher, string(identity.Provider))
	}
	writeDerivationString(hasher, strings.TrimSpace(identity.ModelID))
	writeDerivationLength(hasher, uint64(identity.MaxTokens))
	writeDerivationString(hasher, strings.TrimSpace(identity.PromptVersion))
	writeDerivationBytes(hasher, identity.SchemaDigest[:])
	var digest [sha256.Size]byte
	copy(digest[:], hasher.Sum(nil))
	return digest, nil
}

func writeDerivationString(hasher hash.Hash, value string) {
	writeDerivationBytes(hasher, []byte(value))
}

func writeDerivationBytes(hasher hash.Hash, value []byte) {
	writeDerivationLength(hasher, uint64(len(value)))
	_, _ = hasher.Write(value)
}

func writeDerivationLength(hasher hash.Hash, value uint64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	_, _ = hasher.Write(encoded[:])
}

func (service *Service) fail(ctx context.Context, result Result, status VersionStatus, code FailureCode) (Result, error) {
	if cancellationErr := boundedCancellation(ctx, nil); cancellationErr != nil {
		return Result{}, cancellationErr
	}
	if result.DerivationID != "" {
		if err := service.Repository.RecordFailure(ctx, result.DerivationID, result.leaseOwner, status, code); err != nil {
			if cancellationErr := boundedCancellation(ctx, err); cancellationErr != nil {
				return Result{}, cancellationErr
			}
			result.FailureCode = FailureStorage
			result.Outcome = OutcomeIncomplete
			return result, ErrFailurePersistence
		}
	}
	result.FailureCode = code
	if status == VersionStatusFailed {
		result.Outcome = OutcomeFailed
	} else {
		result.Outcome = OutcomeIncomplete
	}
	return result, nil
}

func boundedCancellation(ctx context.Context, err error) error {
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

func boundedAttemptCancellation(parent, attempt context.Context, err error) error {
	if parent.Err() == nil && attempt.Err() != nil {
		return nil
	}
	return boundedCancellation(parent, err)
}

func boundedGlobalAuthentication(err error) error {
	switch {
	case errors.Is(err, source.ErrAuthentication):
		return source.ErrAuthentication
	case errors.Is(err, source.ErrAuthorization):
		return source.ErrAuthorization
	case errors.Is(err, extract.ErrAuthentication):
		return extract.ErrAuthentication
	case errors.Is(err, extract.ErrAuthorization):
		return extract.ErrAuthorization
	default:
		return nil
	}
}

func (service *Service) validate() error {
	if service == nil || service.Source == nil || service.Model == nil || service.Resolver == nil || service.Repository == nil || service.Now == nil {
		return fmt.Errorf("sync service dependencies are required")
	}
	if strings.TrimSpace(service.CollectionID) == "" || strings.TrimSpace(service.PromptVersion) == "" ||
		strings.TrimSpace(service.ModelID) == "" || service.MaxTokens <= 0 ||
		service.LeaseDuration <= 0 || service.AttemptTimeout <= 0 || service.AttemptTimeout >= service.LeaseDuration {
		return fmt.Errorf("sync collection and model configuration are required")
	}
	if err := (modelpolicy.Invocation{
		Provider: service.Provider,
		DataMode: service.DataMode,
		Region:   service.Region,
	}).Validate(); err != nil {
		return fmt.Errorf("sync model policy: %w", err)
	}
	return nil
}

func (service *Service) attemptContext(ctx context.Context, state VersionState) (context.Context, context.CancelFunc, error) {
	if state.LeaseExpiresAt.IsZero() {
		return nil, nil, fmt.Errorf("sync extraction lease expiry is required")
	}
	now := time.Now()
	deadline := now.Add(service.AttemptTimeout)
	cleanupMargin := service.LeaseDuration - service.AttemptTimeout
	latestDeadline := state.LeaseExpiresAt.Add(-cleanupMargin)
	if latestDeadline.Before(deadline) {
		deadline = latestDeadline
	}
	if !deadline.After(now) {
		return nil, nil, fmt.Errorf("sync extraction lease has no attempt window")
	}
	attemptCtx, cancel := context.WithDeadline(ctx, deadline)
	return attemptCtx, cancel, nil
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
	summary.Directory.Attempted += result.Directory.Attempted
	summary.Directory.Reused += result.Directory.Reused
	summary.Directory.Matched += result.Directory.Matched
	summary.Directory.Review += result.Directory.Review
	summary.Directory.NoMatch += result.Directory.NoMatch
	summary.Directory.Ambiguous += result.Directory.Ambiguous
	summary.Directory.Unavailable += result.Directory.Unavailable
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
