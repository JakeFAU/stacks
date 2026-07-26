package analysis

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"stacks/internal/extract"
	"stacks/internal/modelpolicy"
	"stacks/internal/observability"
)

const (
	// AnalysisPolicyVersion changes whenever deterministic admission semantics
	// change, keeping generated report policy explicit.
	AnalysisPolicyVersion = "manager-confidence-policy-v6"
	analysisSpanName      = "stacks.analysis.pair"
	analysisDecisionName  = "pair_analysis"
)

var (
	// ErrPairNotAccepted prevents pending identity guesses from becoming facts.
	ErrPairNotAccepted = errors.New("configured pair identities are not accepted")
	// ErrInvalidModelOutput is deliberately bounded because raw model output can
	// contain private source-derived material.
	ErrInvalidModelOutput = errors.New("analysis model output is invalid")
)

// PairSnapshot is the repository projection for one configured pair.
type PairSnapshot struct {
	Accepted bool
	Signals  []Signal
}

// Report is a cited, bounded synthesis of observable interaction signals.
type Report struct {
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

// Repository loads one current canonical snapshot on demand.
type Repository interface {
	LoadPairInputs(context.Context, string, string) (PairSnapshot, error)
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
	Provider      modelpolicy.Provider
	DataMode      modelpolicy.DataMode
	Region        string
	ModelID       string
	MaxTokens     int
	Tracer        trace.Tracer
	Decisions     DecisionRecorder
	Now           func() time.Time
}

// Analyze loads current canonical authority and evidence once, then returns one
// bounded on-demand report without persisting a manager-specific cache.
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

	report = service.baseReport(chronology)
	if distinctMeetingCount(chronology.Dated) < 2 {
		report.Status = StatusInsufficientEvidence
		report.Rationale = "At least two distinct dated meetings are required before a directional comparison can be synthesized."
		report.Gaps = append(report.Gaps, "Fewer than two distinct meetings have known source-valid dates.")
		service.recordDecision(ctx, report.Status, len(chronology.Dated), 0, service.now().Sub(started))
		return report, nil
	}

	proposal, response, err := service.synthesize(ctx, chronology.Dated)
	if err != nil {
		return Report{}, err
	}
	report.Status = AdmitConclusion(AdmissionInput{
		PairAccepted: true, Proposed: proposal.Conclusion, Signals: chronology.Dated,
		SupportingSignalIDs: proposal.SupportingSignalIDs,
	})
	if report.Status == proposal.Conclusion {
		report.Counterevidence = collectAdmittedCounterevidence(eligible, proposal.ContradictingSignalIDs)
	} else {
		report.Counterevidence = collectSourceCounterevidence(eligible)
	}
	applyAdmittedProposal(&report, proposal)
	report.ModelID = response.ModelID
	report.RecordedAt = service.now()
	service.recordDecision(ctx, report.Status, len(chronology.Dated), len(proposal.SupportingSignalIDs), service.now().Sub(started))
	return report, nil
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
	if report.Status != proposal.Conclusion {
		report.Limitations = append(report.Limitations,
			"The model-proposed status was replaced because its cited signal references did not satisfy deterministic admission policy.")
	}
	switch report.Status {
	case StatusMixedOrConflicting:
		report.Rationale = "Deterministic admission policy found mixed or opposing observable directions across the dated, transcript-backed signals."
	case StatusInsufficientEvidence:
		report.Rationale = "Deterministic admission policy did not find the required dated, transcript-backed comparison for a stronger conclusion."
		report.Gaps = append(report.Gaps, "The admitted signal references do not establish the required earlier/later comparison across distinct meetings.")
	case StatusNoMaterialChange:
		report.Rationale = "Deterministic admission policy found no material opposing directional pattern in the dated, transcript-backed signals."
	case StatusPossibleDecline:
		report.Rationale = "Deterministic admission policy found a cited earlier comparison and a later weakening observable signal in different dated meetings."
	}
	if len(report.UnknownTime) > 0 {
		report.Gaps = append(report.Gaps, "One or more eligible signals lack source-valid time and are excluded from dated comparison.")
	}
	if len(report.Counterevidence) == 0 {
		report.Gaps = append(report.Gaps, "No explicit contradicting transcript citation was identified in the admitted inputs.")
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

func (service *Service) validate(employeeID, managerID string) error {
	if service == nil || service.Repository == nil || service.Model == nil || service.Now == nil {
		return fmt.Errorf("analysis service dependencies are required")
	}
	if strings.TrimSpace(employeeID) == "" || strings.TrimSpace(managerID) == "" || employeeID == managerID {
		return fmt.Errorf("analysis employee and manager IDs must be distinct")
	}
	if strings.TrimSpace(service.PromptVersion) == "" || strings.TrimSpace(service.ModelID) == "" || service.MaxTokens <= 0 {
		return fmt.Errorf("analysis model configuration is required")
	}
	if err := (modelpolicy.Invocation{
		Provider: service.Provider,
		DataMode: service.DataMode,
		Region:   service.Region,
	}).Validate(); err != nil {
		return fmt.Errorf("analysis model policy: %w", err)
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
