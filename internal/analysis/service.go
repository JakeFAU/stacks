package analysis

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"stacks/internal/extract"
	"stacks/internal/observability"
)

const (
	// AnalysisPolicyVersion changes whenever deterministic admission semantics
	// change, ensuring old completed runs remain distinguishable.
	AnalysisPolicyVersion = "manager-confidence-policy-v3"
	analysisSpanName      = "stacks.analysis.pair"
	analysisDecisionName  = "pair_analysis"
	temporalDigestScope   = "stacks.temporal-pair-analysis.v1"
)

var (
	// ErrPairNotAccepted prevents pending identity guesses from becoming facts.
	ErrPairNotAccepted = errors.New("configured pair identities are not accepted")
	// ErrInvalidModelOutput is deliberately bounded because raw model output can
	// contain private source-derived material.
	ErrInvalidModelOutput = errors.New("analysis model output is invalid")
	// ErrStaleAnalysisInput indicates that an accepted identity decision changed
	// after analysis inputs were loaded. Callers may safely retry from a fresh
	// pair snapshot.
	ErrStaleAnalysisInput = errors.New("analysis inputs are stale; retry with a fresh snapshot")
)

// InputKind is a durable provenance class included in completed-run identity.
type InputKind string

const (
	InputDocumentVersion    InputKind = "document_version"
	InputSourceDocument     InputKind = "source_document"
	InputDocumentTab        InputKind = "document_tab"
	InputObservation        InputKind = "observation"
	InputSignal             InputKind = "signal"
	InputResolutionDecision InputKind = "resolution_decision"
)

// InputReference identifies one immutable analysis input and its exact digest.
type InputReference struct {
	Kind   InputKind
	ID     string
	Digest [sha256.Size]byte
}

// PairSnapshot is the repository projection for one configured pair.
type PairSnapshot struct {
	Accepted bool
	Inputs   []InputReference
	Signals  []Signal
}

// AnalysisIdentity is the stable identity of one completed analysis.
type AnalysisIdentity struct {
	EmployeeEntityID string
	ManagerEntityID  string
	PromptVersion    string
	PolicyVersion    string
	Inputs           []InputReference
	InputDigest      [sha256.Size]byte
}

// InputDigestString returns the compact stable completed-run identity.
func (identity AnalysisIdentity) InputDigestString() string {
	return hex.EncodeToString(identity.InputDigest[:])
}

// Report is a cited, bounded synthesis of observable interaction signals.
type Report struct {
	ID              string
	InputDigest     [sha256.Size]byte
	Status          ReportStatus
	Rationale       string
	Limitations     []string
	Chronology      []Signal
	UnknownTime     []Signal
	Counterevidence []Signal
	Gaps            []string
	RecordedAt      time.Time
	ModelID         string
	Region          string
	MaxTokens       int
	PromptVersion   string
	PolicyVersion   string
}

// Completion is the complete durable analysis payload. Raw prompts and model
// output are intentionally absent.
type Completion struct {
	Identity AnalysisIdentity
	Report   Report
}

// Repository retrieves eligible pair inputs, validates cache identities, and
// atomically deduplicates completed runs by InputDigest.
type Repository interface {
	LoadPairInputs(context.Context, string, string) (PairSnapshot, error)
	FindCompleted(context.Context, AnalysisIdentity) (Report, bool, error)
	CompleteAnalysis(context.Context, Completion) (Report, error)
}

// DecisionRecorder records one bounded decision on the owning analysis span.
type DecisionRecorder interface {
	Record(context.Context, observability.DecisionObservation) error
}

// Service performs deterministic eligibility/admission around model synthesis.
type Service struct {
	Repository    Repository
	Model         extract.Model
	PromptVersion string
	Region        string
	ModelID       string
	MaxTokens     int
	Tracer        trace.Tracer
	Decisions     DecisionRecorder
	Now           func() time.Time
}

// Analyze returns a cached report for identical inputs or completes one new
// analysis while retaining exact immutable input provenance.
func (service *Service) Analyze(ctx context.Context, employeeID, managerID string) (report Report, resultErr error) {
	if err := service.validate(employeeID, managerID); err != nil {
		return Report{}, err
	}
	tracer := service.Tracer
	if tracer == nil {
		tracer = noop.NewTracerProvider().Tracer("stacks")
	}
	ctx, span := tracer.Start(ctx, analysisSpanName)
	started := service.now()
	defer func() {
		span.SetAttributes(
			attribute.String("stacks.analysis.outcome", string(report.Status)),
			attribute.Int("stacks.analysis.dated_signal_count", len(report.Chronology)),
			attribute.Int("stacks.analysis.unknown_time_count", len(report.UnknownTime)),
		)
		observability.FinishSpan(span, resultErr)
	}()

	snapshot, err := service.Repository.LoadPairInputs(ctx, employeeID, managerID)
	if err != nil {
		return Report{}, fmt.Errorf("load pair analysis inputs: %w", err)
	}
	if !snapshot.Accepted {
		return Report{}, ErrPairNotAccepted
	}
	eligible := eligibleSignals(snapshot.Signals)
	chronology := OrderSignals(eligible)
	identity := AnalysisIdentity{
		EmployeeEntityID: strings.TrimSpace(employeeID),
		ManagerEntityID:  strings.TrimSpace(managerID),
		PromptVersion:    service.PromptVersion,
		PolicyVersion:    AnalysisPolicyVersion,
		Inputs:           append([]InputReference(nil), snapshot.Inputs...),
	}
	identity.InputDigest, err = ComputeInputDigest(identity)
	if err != nil {
		return Report{}, err
	}
	if cached, found, err := service.Repository.FindCompleted(ctx, identity); err != nil {
		return Report{}, fmt.Errorf("load completed analysis: %w", err)
	} else if found {
		service.recordDecision(ctx, cached.Status, len(cached.Chronology), len(cached.Counterevidence), service.now().Sub(started))
		return cached, nil
	}

	report = service.baseReport(chronology)
	if distinctMeetingCount(chronology.Dated) < 2 {
		report.Status = StatusInsufficientEvidence
		report.Rationale = "At least two distinct dated meetings are required before a directional comparison can be synthesized."
		report.Gaps = append(report.Gaps, "Fewer than two distinct meetings have known source-valid dates.")
		service.recordDecision(ctx, report.Status, len(chronology.Dated), 0, service.now().Sub(started))
		return service.complete(ctx, identity, report, service.now())
	}

	proposal, response, err := service.synthesize(ctx, chronology.Dated)
	if err != nil {
		return Report{}, err
	}
	report.Status = AdmitConclusion(AdmissionInput{
		PairAccepted: true, Proposed: proposal.Conclusion, Signals: chronology.Dated,
		SupportingSignalIDs: proposal.SupportingSignalIDs,
	})
	applyAdmittedProposal(&report, proposal)
	if report.Status == proposal.Conclusion {
		report.Counterevidence = collectAdmittedCounterevidence(eligible, proposal.ContradictingSignalIDs)
	} else {
		report.Counterevidence = collectSourceCounterevidence(eligible)
	}
	report.ModelID = response.ModelID
	report.RecordedAt = service.now()
	service.recordDecision(ctx, report.Status, len(chronology.Dated), len(proposal.SupportingSignalIDs), service.now().Sub(started))
	return service.complete(ctx, identity, report, report.RecordedAt)
}

func (service *Service) baseReport(chronology Chronology) Report {
	return Report{
		Chronology:      append([]Signal(nil), chronology.Dated...),
		UnknownTime:     append([]Signal(nil), chronology.UnknownTime...),
		Counterevidence: collectSourceCounterevidence(append(append([]Signal(nil), chronology.Dated...), chronology.UnknownTime...)),
		Limitations: []string{
			"This report describes observable interaction patterns and does not claim access to private mental state.",
			"Extraction confidence does not establish truth or resolve conflicting evidence.",
		},
		RecordedAt: service.now(), ModelID: service.ModelID, Region: service.Region,
		MaxTokens: service.MaxTokens, PromptVersion: service.PromptVersion,
		PolicyVersion: AnalysisPolicyVersion,
	}
}

func applyAdmittedProposal(report *Report, proposal analysisProposal) {
	if report.Status == proposal.Conclusion {
		report.Rationale = proposal.Rationale
		report.Gaps = append(report.Gaps, proposal.Gaps...)
		return
	}
	report.Limitations = append(report.Limitations,
		"The model-proposed status and explanatory prose were replaced because the cited evidence did not satisfy deterministic admission policy.")
	switch report.Status {
	case StatusMixedOrConflicting:
		report.Rationale = "Deterministic policy found conflicting dated observations and did not admit the model-proposed directional conclusion."
	case StatusInsufficientEvidence:
		report.Rationale = "Deterministic policy did not find the cited earlier/later meeting comparison required to admit the model-proposed conclusion."
		report.Gaps = append(report.Gaps, "The cited signals do not establish an earlier/later weakening comparison across distinct meetings.")
	default:
		report.Rationale = "Deterministic policy replaced a model-proposed conclusion that was not supported by the admitted evidence."
	}
}

func (service *Service) synthesize(ctx context.Context, dated []Signal) (analysisProposal, extract.Response, error) {
	contract, err := extract.PromptContract(service.PromptVersion)
	if err != nil {
		return analysisProposal{}, extract.Response{}, err
	}
	input := struct {
		Signals []modelSignal `json:"signals"`
	}{Signals: make([]modelSignal, len(dated))}
	for index, signal := range dated {
		input.Signals[index] = modelSignal{
			ID: signal.ID, MeetingDate: signal.ValidTime.UTC().Format(time.DateOnly),
			Category: signal.Category, Direction: signal.Direction,
			Rationale: signal.Rationale, HasCounterevidence: hasContradictingCitation(signal),
		}
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return analysisProposal{}, extract.Response{}, fmt.Errorf("encode bounded analysis input")
	}
	response, err := service.Model.Generate(ctx, extract.Request{
		PromptVersion: contract.Version, SystemPrompt: contract.SystemPrompt,
		Input: string(encoded), SchemaName: contract.SchemaName, JSONSchema: contract.JSONSchema,
	})
	if err != nil {
		return analysisProposal{}, extract.Response{}, fmt.Errorf("generate pair analysis: %w", err)
	}
	if response.ModelID != service.ModelID || response.PromptVersion != service.PromptVersion {
		return analysisProposal{}, extract.Response{}, ErrInvalidModelOutput
	}
	proposal, err := decodeAnalysisProposal(response.Output, dated)
	if err != nil {
		return analysisProposal{}, extract.Response{}, ErrInvalidModelOutput
	}
	return proposal, response, nil
}

type modelSignal struct {
	ID                 string    `json:"id"`
	MeetingDate        string    `json:"meeting_date"`
	Category           Category  `json:"category"`
	Direction          Direction `json:"direction"`
	Rationale          string    `json:"rationale"`
	HasCounterevidence bool      `json:"has_counterevidence"`
}

type analysisProposal struct {
	Conclusion             ReportStatus `json:"conclusion"`
	Rationale              string       `json:"rationale"`
	SupportingSignalIDs    []string     `json:"supporting_signal_ids"`
	ContradictingSignalIDs []string     `json:"contradicting_signal_ids"`
	Gaps                   []string     `json:"gaps"`
}

func decodeAnalysisProposal(raw []byte, signals []Signal) (analysisProposal, error) {
	var required map[string]json.RawMessage
	if err := json.Unmarshal(raw, &required); err != nil {
		return analysisProposal{}, err
	}
	for _, field := range []string{"conclusion", "rationale", "supporting_signal_ids", "contradicting_signal_ids", "gaps"} {
		value, exists := required[field]
		if !exists || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return analysisProposal{}, fmt.Errorf("required analysis field is missing")
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var proposal analysisProposal
	if err := decoder.Decode(&proposal); err != nil {
		return analysisProposal{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return analysisProposal{}, fmt.Errorf("trailing model output")
	}
	if !validReportStatus(proposal.Conclusion) || strings.TrimSpace(proposal.Rationale) == "" {
		return analysisProposal{}, fmt.Errorf("required analysis fields are invalid")
	}
	known := make(map[string]struct{}, len(signals))
	for _, signal := range signals {
		known[signal.ID] = struct{}{}
	}
	seen := make(map[string]struct{}, len(proposal.SupportingSignalIDs)+len(proposal.ContradictingSignalIDs))
	for _, ids := range [][]string{proposal.SupportingSignalIDs, proposal.ContradictingSignalIDs} {
		for _, id := range ids {
			if _, ok := known[id]; !ok || strings.TrimSpace(id) != id {
				return analysisProposal{}, fmt.Errorf("analysis references unknown signal")
			}
			if _, duplicate := seen[id]; duplicate {
				return analysisProposal{}, fmt.Errorf("analysis repeats signal reference")
			}
			seen[id] = struct{}{}
		}
	}
	return proposal, nil
}

func eligibleSignals(signals []Signal) []Signal {
	eligible := make([]Signal, 0, len(signals))
	for _, signal := range signals {
		if signal.Validated && signal.TranscriptBacked && strings.TrimSpace(signal.MeetingID) != "" &&
			validCategory(signal.Category) && validDirection(signal.Direction) {
			eligible = append(eligible, signal)
		}
	}
	return eligible
}

func collectSourceCounterevidence(signals []Signal) []Signal {
	return collectCounterevidence(signals, nil)
}

func collectAdmittedCounterevidence(signals []Signal, modelIDs []string) []Signal {
	selectedByModel := make(map[string]struct{}, len(modelIDs))
	for _, id := range modelIDs {
		selectedByModel[id] = struct{}{}
	}
	return collectCounterevidence(signals, selectedByModel)
}

func collectCounterevidence(signals []Signal, selectedByModel map[string]struct{}) []Signal {
	chronology := OrderSignals(signals)
	ordered := append(append([]Signal(nil), chronology.Dated...), chronology.UnknownTime...)
	result := make([]Signal, 0, len(ordered))
	for _, signal := range ordered {
		_, modelSelected := selectedByModel[signal.ID]
		modelSelected = modelSelected && hasSupportingCitation(signal)
		hasExplicit := hasContradictingCitation(signal)
		if !modelSelected && !hasExplicit {
			continue
		}
		filtered := signal
		filtered.Citations = relevantCounterevidenceCitations(signal.Citations, modelSelected)
		if len(filtered.Citations) != 0 {
			result = append(result, filtered)
		}
	}
	return result
}

func relevantCounterevidenceCitations(citations []Citation, includeSupporting bool) []Citation {
	result := make([]Citation, 0, len(citations))
	seen := make(map[string]struct{}, len(citations))
	for _, citation := range citations {
		if citation.Role != CitationContradicting && (!includeSupporting || citation.Role != CitationSupporting || !citation.Transcript) {
			continue
		}
		key := citation.ID + "\x00" + string(citation.Role)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, citation)
	}
	return result
}

func hasSupportingCitation(signal Signal) bool {
	for _, citation := range signal.Citations {
		if citation.Role == CitationSupporting && citation.Transcript {
			return true
		}
	}
	return false
}

func hasContradictingCitation(signal Signal) bool {
	for _, citation := range signal.Citations {
		if citation.Role == CitationContradicting {
			return true
		}
	}
	return false
}

func validCategory(category Category) bool {
	switch category {
	case CategoryDelegationAutonomy, CategoryScrutinyCorrection, CategoryEndorsementTrust, CategorySupportAdvocacy, CategoryFutureResponsibility:
		return true
	default:
		return false
	}
}

func validDirection(direction Direction) bool {
	switch direction {
	case DirectionStrengthening, DirectionWeakening, DirectionMixed, DirectionUnclear:
		return true
	default:
		return false
	}
}

// ComputeInputDigest derives a stable identity from the canonical pair,
// ordered immutable inputs, and active prompt/policy versions.
func ComputeInputDigest(identity AnalysisIdentity) ([sha256.Size]byte, error) {
	if strings.TrimSpace(identity.EmployeeEntityID) == "" || strings.TrimSpace(identity.ManagerEntityID) == "" ||
		strings.TrimSpace(identity.PromptVersion) == "" || strings.TrimSpace(identity.PolicyVersion) == "" {
		return [sha256.Size]byte{}, fmt.Errorf("analysis identity fields are required")
	}
	employeeID, err := uuid.Parse(strings.TrimSpace(identity.EmployeeEntityID))
	if err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("analysis employee entity ID is invalid")
	}
	managerID, err := uuid.Parse(strings.TrimSpace(identity.ManagerEntityID))
	if err != nil || employeeID == managerID {
		return [sha256.Size]byte{}, fmt.Errorf("analysis manager entity ID is invalid")
	}
	hasher := sha256.New()
	writeDigestString(hasher, temporalDigestScope)
	writeDigestString(hasher, employeeID.String())
	writeDigestString(hasher, managerID.String())
	writeDigestString(hasher, strings.TrimSpace(identity.PromptVersion))
	writeDigestString(hasher, strings.TrimSpace(identity.PolicyVersion))
	writeDigestLength(hasher, uint64(len(identity.Inputs)))
	seenInputs := make(map[string]struct{}, len(identity.Inputs))
	for index, input := range identity.Inputs {
		if !validInputKind(input.Kind) || strings.TrimSpace(input.ID) == "" || input.Digest == ([sha256.Size]byte{}) {
			return [sha256.Size]byte{}, fmt.Errorf("analysis identity input %d is invalid", index)
		}
		writeDigestString(hasher, string(input.Kind))
		inputID := strings.TrimSpace(input.ID)
		if parsed, err := uuid.Parse(inputID); err == nil {
			inputID = parsed.String()
		}
		inputKey := string(input.Kind) + "\x00" + inputID
		if _, exists := seenInputs[inputKey]; exists {
			return [sha256.Size]byte{}, fmt.Errorf("analysis identity input %d is repeated", index)
		}
		seenInputs[inputKey] = struct{}{}
		writeDigestString(hasher, inputID)
		writeDigestBytes(hasher, input.Digest[:])
	}
	var digest [sha256.Size]byte
	copy(digest[:], hasher.Sum(nil))
	return digest, nil
}

func validInputKind(kind InputKind) bool {
	switch kind {
	case InputDocumentVersion, InputSourceDocument, InputDocumentTab, InputObservation, InputSignal, InputResolutionDecision:
		return true
	default:
		return false
	}
}

func writeDigestString(hasher hash.Hash, value string) { writeDigestBytes(hasher, []byte(value)) }

func writeDigestBytes(hasher hash.Hash, value []byte) {
	writeDigestLength(hasher, uint64(len(value)))
	_, _ = hasher.Write(value)
}

func writeDigestLength(hasher hash.Hash, length uint64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], length)
	_, _ = hasher.Write(encoded[:])
}

func (service *Service) complete(ctx context.Context, identity AnalysisIdentity, report Report, recordedAt time.Time) (Report, error) {
	report.InputDigest = identity.InputDigest
	report.RecordedAt = recordedAt.UTC()
	completed, err := service.Repository.CompleteAnalysis(ctx, Completion{Identity: identity, Report: report})
	if err != nil {
		return Report{}, fmt.Errorf("complete pair analysis: %w", err)
	}
	return completed, nil
}

func (service *Service) validate(employeeID, managerID string) error {
	if service == nil || service.Repository == nil || service.Model == nil || service.Now == nil {
		return fmt.Errorf("analysis service dependencies are required")
	}
	if strings.TrimSpace(employeeID) == "" || strings.TrimSpace(managerID) == "" || employeeID == managerID {
		return fmt.Errorf("analysis employee and manager IDs must be distinct")
	}
	if strings.TrimSpace(service.PromptVersion) == "" || strings.TrimSpace(service.Region) == "" || strings.TrimSpace(service.ModelID) == "" || service.MaxTokens <= 0 {
		return fmt.Errorf("analysis model configuration is required")
	}
	return nil
}

func (service *Service) now() time.Time { return service.Now().UTC() }

func (service *Service) recordDecision(ctx context.Context, status ReportStatus, inputSize, outputSize int, duration time.Duration) {
	if service.Decisions == nil {
		return
	}
	_ = service.Decisions.Record(ctx, observability.DecisionObservation{
		Name: analysisDecisionName, Outcome: string(status), Duration: duration,
		InputSize: int64(inputSize), OutputSize: int64(outputSize),
	})
}
