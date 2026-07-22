package analysis

import (
	"slices"
	"testing"
	"time"
)

func TestAdmitConclusionRequiresTwoDatedMeetings(t *testing.T) {
	meeting := testMeetingDate(2026, time.July, 1)
	status := AdmitConclusion(AdmissionInput{
		PairAccepted: true,
		Proposed:     StatusNoMaterialChange,
		Signals: []Signal{{
			ID: "signal-1", Category: CategoryDelegationAutonomy,
			Direction: DirectionStrengthening, ValidTime: &meeting,
		}},
	})

	if status != StatusInsufficientEvidence {
		t.Fatalf("AdmitConclusion() = %q, want %q", status, StatusInsufficientEvidence)
	}
}

func TestAdmitConclusionAcceptsCitedLaterWeakening(t *testing.T) {
	earlier := testMeetingDate(2026, time.June, 3)
	later := testMeetingDate(2026, time.July, 8)
	status := AdmitConclusion(AdmissionInput{
		PairAccepted: true,
		Proposed:     StatusPossibleDecline,
		Signals: []Signal{
			{ID: "earlier", Category: CategoryDelegationAutonomy, Direction: DirectionStrengthening, ValidTime: &earlier},
			{ID: "later", Category: CategoryScrutinyCorrection, Direction: DirectionWeakening, ValidTime: &later},
		},
		SupportingSignalIDs: []string{"later", "earlier"},
	})

	if status != StatusPossibleDecline {
		t.Fatalf("AdmitConclusion() = %q, want %q", status, StatusPossibleDecline)
	}
}

func TestAdmitConclusionRejectsPendingPairIdentity(t *testing.T) {
	earlier := testMeetingDate(2026, time.June, 3)
	later := testMeetingDate(2026, time.July, 8)
	status := AdmitConclusion(AdmissionInput{
		PairAccepted: false,
		Proposed:     StatusPossibleDecline,
		Signals: []Signal{
			{ID: "earlier", Direction: DirectionStrengthening, ValidTime: &earlier},
			{ID: "later", Direction: DirectionWeakening, ValidTime: &later},
		},
		SupportingSignalIDs: []string{"earlier", "later"},
	})

	if status != StatusInsufficientEvidence {
		t.Fatalf("AdmitConclusion() = %q, want %q", status, StatusInsufficientEvidence)
	}
}

func TestAdmitConclusionDoesNotUseUnknownTimeAsDatedMeeting(t *testing.T) {
	dated := testMeetingDate(2026, time.July, 8)
	status := AdmitConclusion(AdmissionInput{
		PairAccepted: true,
		Proposed:     StatusPossibleDecline,
		Signals: []Signal{
			{ID: "unknown", Direction: DirectionStrengthening},
			{ID: "dated", Direction: DirectionWeakening, ValidTime: &dated},
		},
		SupportingSignalIDs: []string{"unknown", "dated"},
	})

	if status != StatusInsufficientEvidence {
		t.Fatalf("AdmitConclusion() = %q, want %q", status, StatusInsufficientEvidence)
	}
}

func TestAdmitConclusionDowngradesUnsupportedDeclineToMixedWhenConflictExists(t *testing.T) {
	earlier := testMeetingDate(2026, time.June, 3)
	later := testMeetingDate(2026, time.July, 8)
	status := AdmitConclusion(AdmissionInput{
		PairAccepted: true,
		Proposed:     StatusPossibleDecline,
		Signals: []Signal{
			{ID: "earlier", Direction: DirectionStrengthening, ValidTime: &earlier, Confidence: 0.1},
			{ID: "later", Direction: DirectionWeakening, ValidTime: &later, Confidence: 0.99},
		},
		SupportingSignalIDs: []string{"later"},
	})

	if status != StatusMixedOrConflicting {
		t.Fatalf("AdmitConclusion() = %q, want %q", status, StatusMixedOrConflicting)
	}
}

func TestAdmitConclusionPreservesConflictInsteadOfAcceptingNoChange(t *testing.T) {
	earlier := testMeetingDate(2026, time.June, 3)
	later := testMeetingDate(2026, time.July, 8)
	status := AdmitConclusion(AdmissionInput{
		PairAccepted: true,
		Proposed:     StatusNoMaterialChange,
		Signals: []Signal{
			{ID: "earlier", Direction: DirectionStrengthening, ValidTime: &earlier, Confidence: 0.99},
			{ID: "later", Direction: DirectionWeakening, ValidTime: &later, Confidence: 0.01},
		},
	})

	if status != StatusMixedOrConflicting {
		t.Fatalf("AdmitConclusion() = %q, want conflict preserved independently of confidence", status)
	}
}

func TestOrderSignalsUsesValidTimeAndSeparatesUnknownTime(t *testing.T) {
	earlier := testMeetingDate(2026, time.June, 3)
	later := testMeetingDate(2026, time.July, 8)
	recordedFirst := testMeetingDate(2026, time.July, 10)
	recordedLast := testMeetingDate(2026, time.July, 20)
	chronology := OrderSignals([]Signal{
		{ID: "later", ValidTime: &later, RecordedAt: recordedFirst, Confidence: 0.99},
		{ID: "unknown", RecordedAt: recordedFirst},
		{ID: "earlier", ValidTime: &earlier, RecordedAt: recordedLast, Confidence: 0.01},
	})

	if got := signalIDs(chronology.Dated); !slices.Equal(got, []string{"earlier", "later"}) {
		t.Fatalf("dated signal IDs = %#v, want stable valid-time order", got)
	}
	if got := signalIDs(chronology.UnknownTime); !slices.Equal(got, []string{"unknown"}) {
		t.Fatalf("unknown-time signal IDs = %#v, want separate section", got)
	}
}

func testMeetingDate(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func signalIDs(signals []Signal) []string {
	ids := make([]string, len(signals))
	for index, signal := range signals {
		ids[index] = signal.ID
	}
	return ids
}
