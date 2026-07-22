package ingest

import (
	"errors"
	"strings"
	"testing"

	"stacks/internal/entity"
	"stacks/internal/knowledge"
)

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
			completion: Completion{VersionID: "version-1", Evidence: []EvidenceRecord{
				{Key: "citation-1", Span: evidence},
				{Key: "citation-2", Span: evidence},
			}},
		},
		{
			name: "mention and proposal identities",
			completion: Completion{
				VersionID: "version-1", Evidence: []EvidenceRecord{{Key: "citation-1", Span: evidence}},
				Mentions: []MentionRecord{
					{Key: "mention-1", EvidenceKey: "citation-1", Surface: "Private Synthetic Person", Role: "speaker", Resolution: entity.Resolution{AutoResolved: true, EntityID: "entity-1"}},
					{Key: "mention-2", EvidenceKey: "citation-1", Surface: "Private Synthetic Person", Role: "speaker", Resolution: entity.Resolution{AutoResolved: true, EntityID: "entity-1"}},
				},
			},
		},
		{
			name: "observation semantic identities",
			completion: Completion{
				VersionID: "version-1", Evidence: []EvidenceRecord{{Key: "citation-1", Span: evidence}},
				Observations: []ObservationRecord{
					baseObservation,
					{ID: "33333333-3333-3333-3333-333333333333", Predicate: interactionPredicate, EvidenceKeys: []string{"citation-1"}},
				},
			},
		},
		{
			name: "signal semantic identities and evidence sets",
			completion: Completion{
				VersionID: "version-1", Evidence: []EvidenceRecord{{Key: "citation-1", Span: evidence}},
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
		VersionID: "version-1",
		Evidence:  []EvidenceRecord{{Key: "citation-1", Span: evidence}},
		Mentions: []MentionRecord{
			{Key: "mention-1", EvidenceKey: "citation-1", Surface: "Synthetic Person One", Role: "speaker"},
			{Key: "mention-2", EvidenceKey: "citation-1", Surface: "Synthetic Person Two", Role: "reference"},
		},
	}
	if err := ValidateForPersistence(completion); err != nil {
		t.Fatalf("ValidateForPersistence() error = %v", err)
	}
}

func persistenceEvidence(t *testing.T) knowledge.EvidenceSpan {
	t.Helper()
	version := documentVersion(t, syntheticDocument("document-persistence-validation", "Synthetic evidence."))
	span, err := knowledge.NewEvidenceSpan(knowledge.EvidenceSpanInput{
		Document: version, TabID: "transcript-tab", StartOffset: 0,
		EndOffset: len("Synthetic"), Quote: "Synthetic",
	})
	if err != nil {
		t.Fatalf("NewEvidenceSpan() error = %v", err)
	}
	return span
}
