package ingest

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/JakeFAU/stacks/core/evidence"
	"github.com/JakeFAU/stacks/core/observation"
	"stacks/internal/entity"
	"stacks/internal/modelpolicy"
)

var validationRecordedAt = time.Date(2026, time.July, 25, 12, 0, 0, 123456000, time.UTC)
var validationSourceConfidence = func() observation.Confidence {
	value, err := observation.NewUnitIntervalConfidence(0.8)
	if err != nil {
		panic(err)
	}
	return value
}()

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
		Observations: []ObservationDraft{{
			ID: "11111111-1111-1111-1111-111111111111", Predicate: observation.Predicate(interactionPredicate),
			ValidTime: observation.UnknownTime(), SourceConfidence: validationSourceConfidence,
			SubjectMentionKey: "mention-unknown", ObjectMentionKey: "mention-employee",
			EvidenceKeys: []string{"citation-1"}, RecordedAt: validationRecordedAt,
		}},
	}

	if err := ValidateForPersistence(completion); !errors.Is(err, ErrPersistenceReference) {
		t.Fatalf("ValidateForPersistence() error = %v, want unknown mention reference rejection", err)
	}
}

func TestValidateForPersistenceRejectsZeroObservationRecordedAt(t *testing.T) {
	evidence := persistenceEvidence(t)
	completion := Completion{
		VersionID: "version-1", DataMode: modelpolicy.DataModePersonal,
		Evidence: []EvidenceRecord{{Key: "citation-1", Span: evidence}},
		Observations: []ObservationDraft{{
			ID: "11111111-1111-1111-1111-111111111111", Predicate: observation.Predicate(interactionPredicate),
			ValidTime: observation.UnknownTime(), SourceConfidence: validationSourceConfidence,
			EvidenceKeys: []string{"citation-1"},
		}},
	}

	if err := ValidateForPersistence(completion); !errors.Is(err, ErrPersistenceReference) {
		t.Fatalf("ValidateForPersistence() error = %v, want zero recorded time rejection", err)
	}
}

func TestValidateForPersistenceRejectsInvalidSourceConfidence(t *testing.T) {
	evidence := persistenceEvidence(t)
	legacyConfidence, err := observation.NewLegacyConfidence(0.5)
	if err != nil {
		t.Fatalf("NewLegacyConfidence() error = %v", err)
	}
	for _, testCase := range []struct {
		name       string
		confidence observation.Confidence
	}{
		{name: "zero_value", confidence: observation.Confidence{}},
		{name: "legacy_scale", confidence: legacyConfidence},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			completion := Completion{
				VersionID: "version-1", DataMode: modelpolicy.DataModePersonal,
				Evidence: []EvidenceRecord{{Key: "citation-1", Span: evidence}},
				Observations: []ObservationDraft{{
					ID:        "11111111-1111-1111-1111-111111111111",
					Predicate: observation.Predicate(interactionPredicate),
					ValidTime: observation.UnknownTime(), EvidenceKeys: []string{"citation-1"},
					SourceConfidence: testCase.confidence, RecordedAt: validationRecordedAt,
				}},
			}
			if err := ValidateForPersistence(completion); !errors.Is(err, ErrPersistenceReference) {
				t.Fatalf("ValidateForPersistence() error = %v, want invalid source confidence rejection", err)
			}
		})
	}
}

func TestValidateForPersistenceUsesReferencesNotObservationSemanticHash(t *testing.T) {
	evidence := persistenceEvidence(t)
	sourceConfidence, err := observation.NewUnitIntervalConfidence(0.5)
	if err != nil {
		t.Fatalf("NewUnitIntervalConfidence() error = %v", err)
	}
	newDraft := func(id observation.ObservationID) ObservationDraft {
		return ObservationDraft{
			ID: id, Predicate: observation.Predicate(interactionPredicate),
			ValidTime: observation.UnknownTime(), EvidenceKeys: []string{"citation-1"},
			SourceConfidence: sourceConfidence, RecordedAt: validationRecordedAt,
		}
	}
	base := Completion{
		VersionID: "version-1", DataMode: modelpolicy.DataModePersonal,
		Evidence: []EvidenceRecord{{Key: "citation-1", Span: evidence}},
	}

	t.Run("duplicate stable ID", func(t *testing.T) {
		completion := base
		completion.Observations = []ObservationDraft{
			newDraft("11111111-1111-1111-1111-111111111111"),
			newDraft("11111111-1111-1111-1111-111111111111"),
		}
		if err := ValidateForPersistence(completion); !errors.Is(err, ErrPersistenceCollision) {
			t.Fatalf("ValidateForPersistence() error = %v, want duplicate stable ID rejection", err)
		}
	})

	t.Run("unknown evidence", func(t *testing.T) {
		completion := base
		draft := newDraft("11111111-1111-1111-1111-111111111111")
		draft.EvidenceKeys = []string{"citation-unknown"}
		completion.Observations = []ObservationDraft{draft}
		if err := ValidateForPersistence(completion); !errors.Is(err, ErrPersistenceReference) {
			t.Fatalf("ValidateForPersistence() error = %v, want unknown evidence rejection", err)
		}
	})

	t.Run("unknown mention", func(t *testing.T) {
		completion := base
		completion.Mentions = []MentionRecord{
			{Key: "mention-subject", EvidenceKey: "citation-1", Surface: "Synthetic", Role: "speaker"},
			{Key: "mention-object", EvidenceKey: "citation-1", Surface: "evidence", Role: "reference"},
		}
		draft := newDraft("11111111-1111-1111-1111-111111111111")
		draft.SubjectMentionKey = "mention-unknown"
		draft.ObjectMentionKey = "mention-object"
		completion.Observations = []ObservationDraft{draft}
		if err := ValidateForPersistence(completion); !errors.Is(err, ErrPersistenceReference) {
			t.Fatalf("ValidateForPersistence() error = %v, want unknown mention rejection", err)
		}
	})

	t.Run("equal unresolved semantics with different stable IDs", func(t *testing.T) {
		completion := base
		completion.Observations = []ObservationDraft{
			newDraft("11111111-1111-1111-1111-111111111111"),
			newDraft("22222222-2222-2222-2222-222222222222"),
		}
		if err := ValidateForPersistence(completion); err != nil {
			t.Fatalf("ValidateForPersistence() error = %v, want reference-valid drafts", err)
		}
	})
}

func TestValidateForPersistenceRejectsWhitespacePaddedLocalIdentifiersAndReferences(t *testing.T) {
	newCompletion := func() Completion {
		evidence := persistenceEvidence(t)
		observationDraft := ObservationDraft{
			ID: "11111111-1111-1111-1111-111111111111", Predicate: observation.Predicate(interactionPredicate),
			ValidTime: observation.UnknownTime(), EvidenceKeys: []string{"citation-1"},
			SourceConfidence: validationSourceConfidence, RecordedAt: validationRecordedAt,
		}
		return Completion{
			VersionID:    "version-1",
			DataMode:     modelpolicy.DataModePersonal,
			Evidence:     []EvidenceRecord{{Key: "citation-1", Span: evidence}},
			Mentions:     []MentionRecord{{Key: "mention-1", EvidenceKey: "citation-1", Surface: "Synthetic Person", Role: "speaker"}},
			Observations: []ObservationDraft{observationDraft},
			Signals: []SignalRecord{{
				ID: "22222222-2222-2222-2222-222222222222", ObservationID: string(observationDraft.ID),
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
