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
			ID: "signal-1", MeetingID: "meeting-1", Category: CategoryDelegationAutonomy,
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
			{ID: "earlier", MeetingID: "meeting-earlier", Category: CategoryDelegationAutonomy, Direction: DirectionStrengthening, ValidTime: &earlier},
			{ID: "later", MeetingID: "meeting-later", Category: CategoryScrutinyCorrection, Direction: DirectionWeakening, ValidTime: &later},
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
			{ID: "earlier", MeetingID: "meeting-earlier", Direction: DirectionStrengthening, ValidTime: &earlier},
			{ID: "later", MeetingID: "meeting-later", Direction: DirectionWeakening, ValidTime: &later},
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
			{ID: "unknown", MeetingID: "meeting-unknown", Direction: DirectionStrengthening},
			{ID: "dated", MeetingID: "meeting-dated", Direction: DirectionWeakening, ValidTime: &dated},
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
			{ID: "earlier", MeetingID: "meeting-earlier", Direction: DirectionStrengthening, ValidTime: &earlier, Confidence: 0.1},
			{ID: "later", MeetingID: "meeting-later", Direction: DirectionWeakening, ValidTime: &later, Confidence: 0.99},
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
			{ID: "earlier", MeetingID: "meeting-earlier", Direction: DirectionStrengthening, ValidTime: &earlier, Confidence: 0.99},
			{ID: "later", MeetingID: "meeting-later", Direction: DirectionWeakening, ValidTime: &later, Confidence: 0.01},
		},
	})

	if status != StatusMixedOrConflicting {
		t.Fatalf("AdmitConclusion() = %q, want conflict preserved independently of confidence", status)
	}
}

func TestAdmitConclusionCountsDocumentRevisionsAsOneMeeting(t *testing.T) {
	earlier := testMeetingDate(2026, time.June, 3)
	laterRevision := testMeetingDate(2026, time.July, 8)
	status := AdmitConclusion(AdmissionInput{
		PairAccepted: true,
		Proposed:     StatusPossibleDecline,
		Signals: []Signal{
			{ID: "revision-1", MeetingID: "meeting-stable", Direction: DirectionStrengthening, ValidTime: &earlier},
			{ID: "revision-2", MeetingID: "meeting-stable", Direction: DirectionWeakening, ValidTime: &laterRevision},
		},
		SupportingSignalIDs: []string{"revision-1", "revision-2"},
	})

	if status != StatusInsufficientEvidence {
		t.Fatalf("AdmitConclusion() = %q, want one source document counted as one meeting", status)
	}
}

func TestAdmitConclusionCountsSameDateDocumentsButRequiresTimeOrderForDecline(t *testing.T) {
	meetingDate := testMeetingDate(2026, time.July, 8)
	signals := []Signal{
		{ID: "meeting-a-signal", MeetingID: "meeting-a", Direction: DirectionStrengthening, ValidTime: &meetingDate},
		{ID: "meeting-b-signal", MeetingID: "meeting-b", Direction: DirectionWeakening, ValidTime: &meetingDate},
	}

	if got := distinctMeetingCount(signals); got != 2 {
		t.Fatalf("distinctMeetingCount() = %d, want 2 source documents", got)
	}
	status := AdmitConclusion(AdmissionInput{
		PairAccepted: true,
		Proposed:     StatusPossibleDecline,
		Signals:      signals,
		SupportingSignalIDs: []string{
			"meeting-a-signal", "meeting-b-signal",
		},
	})
	if status != StatusMixedOrConflicting {
		t.Fatalf("AdmitConclusion() = %q, want no decline without earlier/later ordering", status)
	}
}

func TestAdmitConclusionRequiresDifferentMeetingIdentitiesForDecline(t *testing.T) {
	earlier := testMeetingDate(2026, time.June, 3)
	later := testMeetingDate(2026, time.July, 8)
	signals := []Signal{
		{ID: "same-meeting-earlier", MeetingID: "meeting-stable", Direction: DirectionStrengthening, ValidTime: &earlier},
		{ID: "same-meeting-later", MeetingID: "meeting-stable", Direction: DirectionWeakening, ValidTime: &later},
		{ID: "other-meeting", MeetingID: "meeting-other", Direction: DirectionUnclear, ValidTime: &later},
	}
	status := AdmitConclusion(AdmissionInput{
		PairAccepted:        true,
		Proposed:            StatusPossibleDecline,
		Signals:             signals,
		SupportingSignalIDs: []string{"same-meeting-earlier", "same-meeting-later"},
	})

	if status != StatusMixedOrConflicting {
		t.Fatalf("AdmitConclusion() = %q, want no decline from revisions of one meeting", status)
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
