package ingest

import (
	"errors"
	"testing"
	"time"

	"github.com/JakeFAU/stacks/core/evidence"
	"github.com/JakeFAU/stacks/core/observation"
)

var validationRecordedAt = time.Date(2026, time.July, 25, 12, 0, 0, 123456000, time.UTC)

func TestValidateForPersistenceRejectsUnknownCanonicalReferences(t *testing.T) {
	completion := validationCompletion(t)
	completion.Observations[0].Subject.MentionKey = "missing-mention"

	if err := ValidateForPersistence(completion); !errors.Is(err, ErrPersistenceReference) {
		t.Fatalf("ValidateForPersistence() error = %v, want reference rejection", err)
	}
}

func TestValidateForPersistenceRejectsDuplicateEvidenceRolePair(t *testing.T) {
	completion := validationCompletion(t)
	completion.Observations[0].Evidence = append(
		completion.Observations[0].Evidence,
		completion.Observations[0].Evidence[0],
	)

	if err := ValidateForPersistence(completion); !errors.Is(err, ErrPersistenceCollision) {
		t.Fatalf("ValidateForPersistence() error = %v, want collision", err)
	}
}

func TestValidateForPersistenceAcceptsCanonicalDraft(t *testing.T) {
	if err := ValidateForPersistence(validationCompletion(t)); err != nil {
		t.Fatalf("ValidateForPersistence() error = %v", err)
	}
}

func validationCompletion(t *testing.T) Completion {
	t.Helper()
	evidence := persistenceEvidence(t)
	confidence, err := observation.NewUnitIntervalConfidence(0.8)
	if err != nil {
		t.Fatal(err)
	}
	return Completion{
		VersionID: "version-1", RunID: "run-1", AttemptID: "attempt-1",
		LeaseOwner: "owner-1", CompletedAt: validationRecordedAt,
		Evidence: []EvidenceRecord{{Key: "citation-1", Span: evidence}},
		Mentions: []MentionRecord{{
			Key: "mention-1", EvidenceKey: "citation-1", Surface: "Synthetic",
			NormalizedName: "synthetic", Role: "speaker",
		}},
		Observations: []CanonicalObservationDraft{{
			ID:         "observation-1",
			Subject:    DraftTerm{Kind: observation.TermMention, MentionKey: "mention-1"},
			Predicate:  "stacks.interaction.v1/future_responsibility/strengthening",
			Object:     DraftTerm{Kind: observation.TermMention, MentionKey: "mention-1"},
			ValidTime:  observation.UnknownTime(),
			RecordedAt: validationRecordedAt,
			Evidence: []DraftEvidenceLink{{
				EvidenceKey: "citation-1", Role: observation.EvidenceSupporting,
			}},
			Derivation: observation.Derivation{
				Method: "model_extraction", Version: "extract-v2", RunID: "run-1",
				Model: "synthetic-model", PromptVersion: "extract-v2",
			},
			Status: observation.StatusInferred, Confidence: &confidence,
		}},
	}
}

func persistenceEvidence(t *testing.T) evidence.EvidenceSpan {
	t.Helper()
	version := documentVersion(t, syntheticDocument("document-persistence-validation", "Synthetic evidence."))
	span, err := evidence.NewEvidenceSpan(evidence.EvidenceSpanInput{
		Document: version, SectionID: "transcript-tab", StartOffset: 0,
		EndOffset: len("Synthetic evidence."), Quote: "Synthetic evidence.", RecordedAt: version.RecordedAt(),
	})
	if err != nil {
		t.Fatalf("NewEvidenceSpan() error = %v", err)
	}
	return span
}
