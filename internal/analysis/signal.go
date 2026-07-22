package analysis

import (
	"sort"
	"time"
)

// Category is the finite, versioned vocabulary of observable interactions.
type Category string

const (
	CategoryDelegationAutonomy   Category = "delegation_autonomy"
	CategoryScrutinyCorrection   Category = "scrutiny_correction"
	CategoryEndorsementTrust     Category = "endorsement_trust"
	CategorySupportAdvocacy      Category = "support_advocacy"
	CategoryFutureResponsibility Category = "future_responsibility"
)

// Direction describes an observable signal without claiming private state.
type Direction string

const (
	DirectionStrengthening Direction = "strengthening"
	DirectionWeakening     Direction = "weakening"
	DirectionMixed         Direction = "mixed"
	DirectionUnclear       Direction = "unclear"
)

// ReportStatus is the bounded conclusion vocabulary exposed by analysis.
type ReportStatus string

const (
	StatusInsufficientEvidence ReportStatus = "insufficient evidence"
	StatusNoMaterialChange     ReportStatus = "no material directional change detected"
	StatusMixedOrConflicting   ReportStatus = "mixed or conflicting signals"
	StatusPossibleDecline      ReportStatus = "possible declining-confidence signal"
)

// Signal is one validated, transcript-backed interaction observation.
// ValidTime is source time; RecordedAt is when Stacks learned the signal.
type Signal struct {
	ID               string
	ObservationID    string
	Category         Category
	Direction        Direction
	ValidTime        *time.Time
	RecordedAt       time.Time
	Rationale        string
	Confidence       float64
	Validated        bool
	TranscriptBacked bool
	Inputs           []InputReference
	Citations        []Citation
}

// CitationRole identifies whether exact transcript evidence supports or
// contradicts the observable signal.
type CitationRole string

const (
	CitationSupporting    CitationRole = "supporting"
	CitationContradicting CitationRole = "contradicting"
)

// Citation is an exact private source passage. Quote is permitted at the
// explicit local CLI boundary and must never be placed in telemetry or logs.
type Citation struct {
	ID                 string
	ProviderDocumentID string
	ProviderTabID      string
	StartOffset        int
	EndOffset          int
	Quote              string
	Locator            string
	Role               CitationRole
}

// Chronology keeps genuinely unknown source time out of the dated sequence.
type Chronology struct {
	Dated       []Signal
	UnknownTime []Signal
}

// AdmissionInput contains the structural evidence used to constrain a model
// proposal. Confidence is deliberately absent from the admission policy.
type AdmissionInput struct {
	PairAccepted        bool
	Proposed            ReportStatus
	Signals             []Signal
	SupportingSignalIDs []string
}

// OrderSignals returns stable valid-time ordering and a separate unknown-time
// section. Recorded time never substitutes for source-valid time.
func OrderSignals(signals []Signal) Chronology {
	chronology := Chronology{}
	for _, signal := range signals {
		if signal.ValidTime == nil || signal.ValidTime.IsZero() {
			chronology.UnknownTime = append(chronology.UnknownTime, signal)
			continue
		}
		chronology.Dated = append(chronology.Dated, signal)
	}
	sort.SliceStable(chronology.Dated, func(left, right int) bool {
		leftTime := chronology.Dated[left].ValidTime.UTC()
		rightTime := chronology.Dated[right].ValidTime.UTC()
		if leftTime.Equal(rightTime) {
			return chronology.Dated[left].ID < chronology.Dated[right].ID
		}
		return leftTime.Before(rightTime)
	})
	sort.SliceStable(chronology.UnknownTime, func(left, right int) bool {
		return chronology.UnknownTime[left].ID < chronology.UnknownTime[right].ID
	})
	return chronology
}

// AdmitConclusion enforces deterministic evidence sufficiency after model
// synthesis. It cannot establish truth; it only admits a bounded report state.
func AdmitConclusion(input AdmissionInput) ReportStatus {
	if !input.PairAccepted {
		return StatusInsufficientEvidence
	}
	chronology := OrderSignals(input.Signals)
	if distinctMeetingCount(chronology.Dated) < 2 {
		return StatusInsufficientEvidence
	}
	if !validReportStatus(input.Proposed) {
		return StatusInsufficientEvidence
	}
	if input.Proposed != StatusPossibleDecline {
		if input.Proposed == StatusNoMaterialChange && hasDirectionalConflict(chronology.Dated) {
			return StatusMixedOrConflicting
		}
		return input.Proposed
	}
	if supportsLaterWeakening(chronology.Dated, input.SupportingSignalIDs) {
		return StatusPossibleDecline
	}
	if hasDirectionalConflict(chronology.Dated) {
		return StatusMixedOrConflicting
	}
	return StatusInsufficientEvidence
}

func distinctMeetingCount(signals []Signal) int {
	dates := make(map[string]struct{}, len(signals))
	for _, signal := range signals {
		dates[signal.ValidTime.UTC().Format(time.RFC3339Nano)] = struct{}{}
	}
	return len(dates)
}

func supportsLaterWeakening(signals []Signal, supportingIDs []string) bool {
	supported := make(map[string]struct{}, len(supportingIDs))
	for _, id := range supportingIDs {
		supported[id] = struct{}{}
	}
	for laterIndex, later := range signals {
		if later.Direction != DirectionWeakening {
			continue
		}
		if _, ok := supported[later.ID]; !ok {
			continue
		}
		for _, earlier := range signals[:laterIndex] {
			if earlier.ValidTime.Equal(*later.ValidTime) {
				continue
			}
			if _, ok := supported[earlier.ID]; ok {
				return true
			}
		}
	}
	return false
}

func hasDirectionalConflict(signals []Signal) bool {
	var strengthening, weakening bool
	for _, signal := range signals {
		if signal.Direction == DirectionMixed {
			return true
		}
		strengthening = strengthening || signal.Direction == DirectionStrengthening
		weakening = weakening || signal.Direction == DirectionWeakening
	}
	return strengthening && weakening
}

func validReportStatus(status ReportStatus) bool {
	switch status {
	case StatusInsufficientEvidence, StatusNoMaterialChange, StatusMixedOrConflicting, StatusPossibleDecline:
		return true
	default:
		return false
	}
}
