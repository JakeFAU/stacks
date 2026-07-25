package ingest

import (
	"errors"
	"strings"
	"testing"

	"github.com/JakeFAU/stacks/core/evidence"
	"stacks/internal/entity"
	"stacks/internal/modelpolicy"
)

func TestValidateForPersistenceRequiresNewRunDataMode(t *testing.T) {
	completion := Completion{VersionID: "version-1"}
	if err := ValidateForPersistence(completion); err == nil {
		t.Fatal("ValidateForPersistence() error = nil, want missing data mode rejection")
	}
	completion.DataMode = modelpolicy.DataModeLegacy
	if err := ValidateForPersistence(completion); err == nil {
		t.Fatal("ValidateForPersistence() error = nil, want legacy data mode rejection")
	}
	completion.DataMode = modelpolicy.DataModePersonal
	if err := ValidateForPersistence(completion); err != nil {
		t.Fatalf("ValidateForPersistence() error = %v, want valid new-run data mode", err)
	}
}

func TestValidateForPersistenceRejectsDistinctModelRecordsWithSameDurableIdentity(t *testing.T) {
	evidence := persistenceEvidence(t)
	baseObservation := ObservationRecord{
		ID: "11111111-1111-1111-1111-111111111111", Predicate: interactionPredicate,
		EvidenceKeys: []string{"citation-1"},
	}
	baseSignal := SignalRecord{
		ID: "22222222-2222-2222-2222-222222222222", ObservationID: baseObservation.ID,
		Category: "future_responsibility", Direction: "strengthening",
		ExtractionModelID: "synthetic-model", PromptVersion: "extract-v1",
		Rationale: "Synthetic rationale", Confidence: 0.8,
		Evidence: []SignalEvidenceRecord{{EvidenceKey: "citation-1", Role: "supporting"}},
	}

	cases := []struct {
		name       string
		completion Completion
	}{
		{
			name: "evidence spans",
			completion: Completion{VersionID: "version-1", DataMode: modelpolicy.DataModePersonal, Evidence: []EvidenceRecord{
				{Key: "citation-1", Span: evidence},
				{Key: "citation-2", Span: evidence},
			}},
		},
		{
			name: "mention and proposal identities",
			completion: Completion{
				VersionID: "version-1", DataMode: modelpolicy.DataModePersonal, Evidence: []EvidenceRecord{{Key: "citation-1", Span: evidence}},
				Mentions: []MentionRecord{
					{Key: "mention-1", EvidenceKey: "citation-1", Surface: "Synthetic", NormalizedName: "synthetic", Role: "speaker", Resolution: entity.Resolution{AutoResolved: true, EntityID: "entity-1"}},
					{Key: "mention-2", EvidenceKey: "citation-1", Surface: "Synthetic", NormalizedName: "synthetic", Role: "speaker", Resolution: entity.Resolution{AutoResolved: true, EntityID: "entity-1"}},
				},
			},
		},
		{
			name: "observation semantic identities",
			completion: Completion{
				VersionID: "version-1", DataMode: modelpolicy.DataModePersonal, Evidence: []EvidenceRecord{{Key: "citation-1", Span: evidence}},
				Observations: []ObservationRecord{
					baseObservation,
					{ID: "33333333-3333-3333-3333-333333333333", Predicate: interactionPredicate, EvidenceKeys: []string{"citation-1"}},
				},
			},
		},
		{
			name: "signal semantic identities and evidence sets",
			completion: Completion{
				VersionID: "version-1", DataMode: modelpolicy.DataModePersonal, Evidence: []EvidenceRecord{{Key: "citation-1", Span: evidence}},
				Observations: []ObservationRecord{baseObservation},
				Signals: []SignalRecord{
					baseSignal,
					{ID: "44444444-4444-4444-4444-444444444444", ObservationID: baseObservation.ID, Category: baseSignal.Category, Direction: baseSignal.Direction, ExtractionModelID: baseSignal.ExtractionModelID, PromptVersion: baseSignal.PromptVersion, Rationale: baseSignal.Rationale, Confidence: baseSignal.Confidence, Evidence: baseSignal.Evidence},
				},
			},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			err := ValidateForPersistence(testCase.completion)
			if !errors.Is(err, ErrPersistenceCollision) {
				t.Fatalf("ValidateForPersistence() error = %v, want collision", err)
			}
			if strings.Contains(err.Error(), "Private Synthetic Person") || strings.Contains(err.Error(), evidence.Text()) {
				t.Fatalf("validation error disclosed private content: %v", err)
			}
		})
	}
}

func TestValidateForPersistenceAcceptsDistinctDurableIdentities(t *testing.T) {
	evidence := persistenceEvidence(t)
	completion := Completion{
		VersionID: "version-1", DataMode: modelpolicy.DataModePersonal,
		Evidence: []EvidenceRecord{{Key: "citation-1", Span: evidence}},
		Mentions: []MentionRecord{
			{Key: "mention-1", EvidenceKey: "citation-1", Surface: "Synthetic", Role: "speaker"},
			{Key: "mention-2", EvidenceKey: "citation-1", Surface: "evidence", Role: "reference"},
		},
	}
	if err := ValidateForPersistence(completion); err != nil {
		t.Fatalf("ValidateForPersistence() error = %v", err)
	}
}

func TestValidateForPersistenceRejectsIdentityNotGroundedInSelectedEvidence(t *testing.T) {
	evidence := persistenceEvidence(t)
	tests := []MentionRecord{
		{
			Key: "mention-1", EvidenceKey: "citation-1", Surface: "Invented Person",
			Role: "speaker",
		},
		{
			Key: "mention-1", EvidenceKey: "citation-1", Surface: "Synthetic",
			NormalizedName: "synthetic", ProposedEmail: "bob.builder@synthetic.example",
			ProposedEmailEvidenceKey: "citation-1",
			Role:                     "speaker",
		},
	}
	for _, mention := range tests {
		completion := Completion{
			VersionID: "version-1", DataMode: modelpolicy.DataModePersonal,
			Evidence: []EvidenceRecord{{Key: "citation-1", Span: evidence}},
			Mentions: []MentionRecord{mention},
		}
		if err := ValidateForPersistence(completion); !errors.Is(err, ErrPersistenceReference) {
			t.Fatalf("ValidateForPersistence() error = %v, want selected identity-evidence rejection", err)
		}
	}
}

func TestValidateForPersistenceRejectsUnknownObservationMentionReferences(t *testing.T) {
	evidence := persistenceEvidence(t)
	completion := Completion{
		VersionID: "version-1", DataMode: modelpolicy.DataModePersonal,
		Evidence: []EvidenceRecord{{Key: "citation-1", Span: evidence}},
		Mentions: []MentionRecord{
			{Key: "mention-manager", EvidenceKey: "citation-1", Surface: "Synthetic Manager", Role: "speaker"},
			{Key: "mention-employee", EvidenceKey: "citation-1", Surface: "Synthetic Employee", Role: "reference"},
		},
		Observations: []ObservationRecord{{
			ID: "11111111-1111-1111-1111-111111111111", Predicate: interactionPredicate,
			SubjectMentionKey: "mention-unknown", ObjectMentionKey: "mention-employee",
			EvidenceKeys: []string{"citation-1"},
		}},
	}

	if err := ValidateForPersistence(completion); !errors.Is(err, ErrPersistenceReference) {
		t.Fatalf("ValidateForPersistence() error = %v, want unknown mention reference rejection", err)
	}
}

func TestValidateForPersistenceRejectsWhitespacePaddedLocalIdentifiersAndReferences(t *testing.T) {
	newCompletion := func() Completion {
		evidence := persistenceEvidence(t)
		observation := ObservationRecord{
			ID: "11111111-1111-1111-1111-111111111111", Predicate: interactionPredicate,
			EvidenceKeys: []string{"citation-1"},
		}
		return Completion{
			VersionID:    "version-1",
			DataMode:     modelpolicy.DataModePersonal,
			Evidence:     []EvidenceRecord{{Key: "citation-1", Span: evidence}},
			Mentions:     []MentionRecord{{Key: "mention-1", EvidenceKey: "citation-1", Surface: "Synthetic Person", Role: "speaker"}},
			Observations: []ObservationRecord{observation},
			Signals: []SignalRecord{{
				ID: "22222222-2222-2222-2222-222222222222", ObservationID: observation.ID,
				Category: "future_responsibility", Direction: "strengthening",
				ExtractionModelID: "synthetic-model", PromptVersion: "extract-v1",
				Rationale: "Synthetic rationale", Confidence: 0.8,
				Evidence: []SignalEvidenceRecord{{EvidenceKey: "citation-1", Role: "supporting"}},
			}},
		}
	}
	tests := map[string]func(*Completion){
		"evidence key and references": func(completion *Completion) {
			completion.Evidence[0].Key = " citation-1"
			completion.Mentions[0].EvidenceKey = " citation-1"
			completion.Observations[0].EvidenceKeys[0] = " citation-1"
			completion.Signals[0].Evidence[0].EvidenceKey = " citation-1"
		},
		"mention key":                func(completion *Completion) { completion.Mentions[0].Key = "mention-1 " },
		"mention evidence reference": func(completion *Completion) { completion.Mentions[0].EvidenceKey = "citation-1 " },
		"observation ID and reference": func(completion *Completion) {
			completion.Observations[0].ID = " 11111111-1111-1111-1111-111111111111"
			completion.Signals[0].ObservationID = " 11111111-1111-1111-1111-111111111111"
		},
		"observation evidence reference": func(completion *Completion) { completion.Observations[0].EvidenceKeys[0] = "citation-1 " },
		"signal ID":                      func(completion *Completion) { completion.Signals[0].ID = "22222222-2222-2222-2222-222222222222 " },
		"signal observation reference": func(completion *Completion) {
			completion.Signals[0].ObservationID = "11111111-1111-1111-1111-111111111111 "
		},
		"signal evidence reference": func(completion *Completion) { completion.Signals[0].Evidence[0].EvidenceKey = " citation-1" },
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			completion := newCompletion()
			mutate(&completion)
			if err := ValidateForPersistence(completion); !errors.Is(err, ErrPersistenceReference) {
				t.Fatalf("ValidateForPersistence() error = %v, want padded reference rejection", err)
			}
		})
	}
}

func persistenceEvidence(t *testing.T) evidence.EvidenceSpan {
	t.Helper()
	version := documentVersion(t, syntheticDocument("document-persistence-validation", "Synthetic evidence."))
	span, err := evidence.NewEvidenceSpan(evidence.EvidenceSpanInput{
		Document: version, SectionID: "transcript-tab", StartOffset: 0,
		EndOffset: len("Synthetic evidence."), Quote: "Synthetic evidence.",
	})
	if err != nil {
		t.Fatalf("NewEvidenceSpan() error = %v", err)
	}
	return span
}
