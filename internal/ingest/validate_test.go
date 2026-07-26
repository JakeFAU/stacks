package ingest

import (
	"errors"
	"testing"
	"time"

	"github.com/JakeFAU/stacks/core/evidence"
	"github.com/JakeFAU/stacks/core/identity"
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

func TestValidateForPersistenceRejectsDuplicateCanonicalGraphIDs(t *testing.T) {
	for _, testCase := range canonicalDuplicateGraphCases() {
		t.Run(testCase.name, func(t *testing.T) {
			completion := validationFullCompletion(t)
			testCase.mutate(&completion)

			if err := ValidateForPersistence(completion); !errors.Is(err, ErrPersistenceCollision) {
				t.Fatalf("ValidateForPersistence() error = %v, want collision", err)
			}
		})
	}
}

func TestValidateForPersistenceRejectsAliasForDecisionOutsideWriteSet(t *testing.T) {
	completion := validationFullCompletion(t)
	appendOrphanAlias(t, &completion)

	if err := ValidateForPersistence(completion); !errors.Is(err, ErrPersistenceReference) {
		t.Fatalf("ValidateForPersistence() error = %v, want reference rejection", err)
	}
}

type canonicalGraphMutation struct {
	name   string
	mutate func(*Completion)
}

func canonicalDuplicateGraphCases() []canonicalGraphMutation {
	return []canonicalGraphMutation{
		{
			name: "evidence key",
			mutate: func(completion *Completion) {
				completion.Evidence = append(completion.Evidence, completion.Evidence[0])
			},
		},
		{
			name: "evidence durable ID",
			mutate: func(completion *Completion) {
				duplicate := completion.Evidence[0]
				duplicate.Key = "different-evidence-key"
				completion.Evidence = append(completion.Evidence, duplicate)
			},
		},
		{
			name: "mention",
			mutate: func(completion *Completion) {
				completion.Mentions = append(completion.Mentions, completion.Mentions[0])
			},
		},
		{
			name: "proposal",
			mutate: func(completion *Completion) {
				completion.Proposals = append(completion.Proposals, completion.Proposals[0])
			},
		},
		{
			name: "candidate",
			mutate: func(completion *Completion) {
				completion.Candidates = append(completion.Candidates, completion.Candidates[0])
			},
		},
		{
			name: "decision",
			mutate: func(completion *Completion) {
				completion.Decisions = append(completion.Decisions, completion.Decisions[0])
			},
		},
		{
			name: "alias",
			mutate: func(completion *Completion) {
				completion.AliasAssertions = append(
					completion.AliasAssertions,
					completion.AliasAssertions[0],
				)
			},
		},
		{
			name: "observation",
			mutate: func(completion *Completion) {
				completion.Observations = append(
					completion.Observations,
					completion.Observations[0],
				)
			},
		},
		{
			name: "admission decision",
			mutate: func(completion *Completion) {
				completion.AdmissionDecisions = append(
					completion.AdmissionDecisions,
					completion.AdmissionDecisions[0],
				)
			},
		},
	}
}

func appendOrphanAlias(t *testing.T, completion *Completion) {
	t.Helper()
	orphan, err := identity.NewAliasAssertion(identity.AliasAssertionInput{
		ID: "alias-orphan", DecisionID: "decision-outside-write-set",
		EntityID: completion.Decisions[0].EntityID(),
		Alias: identity.Alias{
			Type: identity.AliasTypeName, Value: "Synthetic Orphan",
		},
		RecordedAt: completion.CompletedAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	completion.AliasAssertions = append(completion.AliasAssertions, orphan)
}

func validationFullCompletion(t *testing.T) Completion {
	t.Helper()
	version, _, derivation := canonicalPreparationInput(t, validationRecordedAt)
	return canonicalLiveCompletion(t, version, VersionState{
		VersionID: "version-1", RunID: "run-1", AttemptID: "attempt-1",
		LeaseOwner: "owner-1", DocumentRecordedAt: version.RecordedAt(),
	}, derivation, validationRecordedAt)
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
