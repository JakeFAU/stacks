package observation_test

import (
	"math"
	"slices"
	"testing"
	"time"

	"github.com/JakeFAU/stacks/core/evidence"
	"github.com/JakeFAU/stacks/core/observation"
)

func TestObservationPreservesEntityAndGroundingMentionTerms(t *testing.T) {
	subject, err := observation.NewEntityTerm("entity-subject", "mention-subject")
	if err != nil {
		t.Fatalf("NewEntityTerm(subject) error = %v", err)
	}
	object, err := observation.NewEntityTerm("entity-object", "mention-object")
	if err != nil {
		t.Fatalf("NewEntityTerm(object) error = %v", err)
	}
	input := validObservationInput(t)
	input.Statement.Subject = subject
	input.Statement.Object = object

	got, err := observation.NewObservation(input)
	if err != nil {
		t.Fatalf("NewObservation() error = %v", err)
	}
	gotSubject, gotSubjectMention, ok := got.Statement().Subject.Entity()
	if !ok || gotSubject != "entity-subject" || gotSubjectMention != "mention-subject" {
		t.Errorf("subject Entity() = (%q, %q, %v)", gotSubject, gotSubjectMention, ok)
	}
	gotObject, gotObjectMention, ok := got.Statement().Object.Entity()
	if !ok || gotObject != "entity-object" || gotObjectMention != "mention-object" {
		t.Errorf("object Entity() = (%q, %q, %v)", gotObject, gotObjectMention, ok)
	}
}

func TestObservationAllowsAbsentLegacyTerms(t *testing.T) {
	input := validObservationInput(t)
	input.Statement.Subject = observation.AbsentTerm()
	input.Statement.Object = observation.AbsentTerm()

	got, err := observation.NewObservation(input)
	if err != nil {
		t.Fatalf("NewObservation() error = %v", err)
	}
	if got.Statement().Subject.Kind() != observation.TermAbsent || got.Statement().Object.Kind() != observation.TermAbsent {
		t.Fatalf("statement term kinds = (%v, %v), want absent terms", got.Statement().Subject.Kind(), got.Statement().Object.Kind())
	}
}

func TestObservationPreservesSupportingAndContradictingEvidence(t *testing.T) {
	input := validObservationInput(t)
	input.Evidence = []observation.EvidenceLink{
		{EvidenceID: "evidence-z", Role: observation.EvidenceContradicting},
		{EvidenceID: "evidence-a", Role: observation.EvidenceSupporting},
		{EvidenceID: "evidence-z", Role: observation.EvidenceSupporting},
	}

	got, err := observation.NewObservation(input)
	if err != nil {
		t.Fatalf("NewObservation() error = %v", err)
	}
	want := []observation.EvidenceLink{
		{EvidenceID: "evidence-a", Role: observation.EvidenceSupporting},
		{EvidenceID: "evidence-z", Role: observation.EvidenceContradicting},
		{EvidenceID: "evidence-z", Role: observation.EvidenceSupporting},
	}
	if !slices.Equal(got.EvidenceLinks(), want) {
		t.Errorf("EvidenceLinks() = %#v, want %#v", got.EvidenceLinks(), want)
	}
}

func TestObservationAcceptsAllDurableEpistemicStatuses(t *testing.T) {
	statuses := []observation.EpistemicStatus{
		observation.StatusObserved,
		observation.StatusInferred,
		observation.StatusHypothesized,
		observation.StatusValidatedStructurally,
		observation.StatusValidatedEmpirically,
		observation.StatusRejected,
	}
	for _, status := range statuses {
		t.Run(string(status), func(t *testing.T) {
			input := validObservationInput(t)
			input.Status = status
			got, err := observation.NewObservation(input)
			if err != nil {
				t.Fatalf("NewObservation() error = %v", err)
			}
			if got.Status() != status {
				t.Errorf("Status() = %q, want %q", got.Status(), status)
			}
		})
	}
}

func TestObservationRequiresCallerRecordedTime(t *testing.T) {
	input := validObservationInput(t)
	input.RecordedAt = time.Time{}
	if _, err := observation.NewObservation(input); err == nil {
		t.Fatal("NewObservation() error = nil, want missing recorded time error")
	}
}

func TestObservationPreservesStructuredDerivationAndRunID(t *testing.T) {
	input := validObservationInput(t)
	input.Derivation = observation.Derivation{
		Method:        " language-model ",
		Version:       " extractor-v2 ",
		RunID:         " run-17 ",
		Model:         " synthetic-model ",
		PromptVersion: " prompt-v3 ",
	}
	got, err := observation.NewObservation(input)
	if err != nil {
		t.Fatalf("NewObservation() error = %v", err)
	}
	want := observation.Derivation{
		Method:        " language-model ",
		Version:       " extractor-v2 ",
		RunID:         " run-17 ",
		Model:         " synthetic-model ",
		PromptVersion: " prompt-v3 ",
	}
	if got.Derivation() != want {
		t.Errorf("Derivation() = %#v, want %#v", got.Derivation(), want)
	}

	legacy := validObservationInput(t)
	legacy.Derivation = observation.Derivation{
		Method:            "legacy-import",
		RunID:             "legacy-run",
		LegacyUnversioned: true,
	}
	if _, err := observation.NewObservation(legacy); err != nil {
		t.Fatalf("NewObservation(legacy) error = %v", err)
	}
}

func TestObservationPreservesPredicateBytesExactly(t *testing.T) {
	const predicateBytes = " \t source predicate \n"
	predicate, err := observation.NewPredicate(predicateBytes)
	if err != nil {
		t.Fatalf("NewPredicate() error = %v", err)
	}
	input := validObservationInput(t)
	input.Statement.Predicate = predicate

	got, err := observation.NewObservation(input)
	if err != nil {
		t.Fatalf("NewObservation() error = %v", err)
	}
	if got.Statement().Predicate != observation.Predicate(predicateBytes) {
		t.Errorf("Statement().Predicate = %q, want exact bytes %q", got.Statement().Predicate, predicateBytes)
	}
}

func TestObservationAllowsExplicitLegacyUncitedCompatibility(t *testing.T) {
	input := validObservationInput(t)
	input.Evidence = nil
	input.LegacyUncited = true

	got, err := observation.NewObservation(input)
	if err != nil {
		t.Fatalf("NewObservation() error = %v", err)
	}
	if !got.LegacyUncited() {
		t.Fatal("LegacyUncited() = false, want true")
	}
	if len(got.EvidenceLinks()) != 0 {
		t.Errorf("EvidenceLinks() = %v, want no invented evidence", got.EvidenceLinks())
	}
}

func TestObservationRejectsOrdinaryUncitedWrite(t *testing.T) {
	input := validObservationInput(t)
	input.Evidence = nil
	if _, err := observation.NewObservation(input); err == nil {
		t.Fatal("NewObservation() error = nil, want ordinary uncited write error")
	}
}

func TestObservationRejectsLegacyUncitedMarkerWithEvidence(t *testing.T) {
	input := validObservationInput(t)
	input.LegacyUncited = true
	if _, err := observation.NewObservation(input); err == nil {
		t.Fatal("NewObservation() error = nil, want ambiguous legacy-uncited marker error")
	}
}

func TestObservationDefensivelyCopiesEvidenceLinks(t *testing.T) {
	input := validObservationInput(t)
	got, err := observation.NewObservation(input)
	if err != nil {
		t.Fatalf("NewObservation() error = %v", err)
	}
	input.Evidence[0] = observation.EvidenceLink{EvidenceID: "changed-input", Role: observation.EvidenceContradicting}
	returned := got.EvidenceLinks()
	returned[0] = observation.EvidenceLink{EvidenceID: "changed-output", Role: observation.EvidenceContradicting}

	want := []observation.EvidenceLink{{EvidenceID: "evidence-1", Role: observation.EvidenceSupporting}}
	if !slices.Equal(got.EvidenceLinks(), want) {
		t.Errorf("EvidenceLinks() = %#v, want %#v", got.EvidenceLinks(), want)
	}
}

func TestObservationRejectsDuplicateEvidenceRolePairs(t *testing.T) {
	input := validObservationInput(t)
	input.Evidence = []observation.EvidenceLink{
		{EvidenceID: "evidence-1", Role: observation.EvidenceSupporting},
		{EvidenceID: "evidence-1", Role: observation.EvidenceSupporting},
	}
	if _, err := observation.NewObservation(input); err == nil {
		t.Fatal("NewObservation() error = nil, want duplicate evidence-role error")
	}
}

func TestObservationKeepsValidTimeSeparateFromRecordedTime(t *testing.T) {
	validAt := time.Date(2024, time.January, 15, 9, 0, 0, 0, time.UTC)
	recordedAt := time.Date(2026, time.July, 18, 12, 0, 0, 0, time.FixedZone("synthetic", -4*60*60))
	validTime, err := observation.AtTime(validAt)
	if err != nil {
		t.Fatalf("AtTime() error = %v", err)
	}
	confidence, err := observation.NewUnitIntervalConfidence(0.85)
	if err != nil {
		t.Fatalf("NewUnitIntervalConfidence() error = %v", err)
	}
	input := validObservationInput(t)
	input.ValidTime = validTime
	input.RecordedAt = recordedAt
	input.Confidence = &confidence

	got, err := observation.NewObservation(input)
	if err != nil {
		t.Fatalf("NewObservation() error = %v", err)
	}
	instant, ok := got.ValidTime().Instant()
	if !ok || instant != validAt {
		t.Errorf("ValidTime().Instant() = (%v, %v), want (%v, true)", instant, ok, validAt)
	}
	if !got.RecordedAt().Equal(recordedAt) || got.RecordedAt().Location() != time.UTC {
		t.Errorf("RecordedAt() = %v, want UTC equivalent of %v", got.RecordedAt(), recordedAt)
	}
	gotConfidence, ok := got.Confidence()
	if !ok || gotConfidence.Value() != 0.85 || gotConfidence.Scale() != observation.ConfidenceUnitInterval {
		t.Errorf("Confidence() = (%#v, %v), want unit interval 0.85", gotConfidence, ok)
	}
}

func TestObservationRejectsInvalidCanonicalInput(t *testing.T) {
	invalidConfidence := observation.Confidence{}
	tests := []struct {
		name   string
		mutate func(*observation.ObservationInput)
	}{
		{name: "missing ID", mutate: func(input *observation.ObservationInput) { input.ID = "" }},
		{name: "missing predicate", mutate: func(input *observation.ObservationInput) { input.Statement.Predicate = "" }},
		{name: "whitespace predicate", mutate: func(input *observation.ObservationInput) { input.Statement.Predicate = " \t" }},
		{name: "missing evidence", mutate: func(input *observation.ObservationInput) { input.Evidence = nil }},
		{name: "blank evidence ID", mutate: func(input *observation.ObservationInput) {
			input.Evidence = []observation.EvidenceLink{{Role: observation.EvidenceSupporting}}
		}},
		{name: "invalid evidence role", mutate: func(input *observation.ObservationInput) {
			input.Evidence = []observation.EvidenceLink{{EvidenceID: "evidence-1", Role: "neutral"}}
		}},
		{name: "missing derivation method", mutate: func(input *observation.ObservationInput) { input.Derivation.Method = "" }},
		{name: "whitespace derivation method", mutate: func(input *observation.ObservationInput) { input.Derivation.Method = " \t" }},
		{name: "missing derivation version", mutate: func(input *observation.ObservationInput) { input.Derivation.Version = "" }},
		{name: "whitespace derivation version", mutate: func(input *observation.ObservationInput) { input.Derivation.Version = " \t" }},
		{name: "whitespace run ID", mutate: func(input *observation.ObservationInput) { input.Derivation.RunID = " \t" }},
		{name: "legacy flag with version", mutate: func(input *observation.ObservationInput) { input.Derivation.LegacyUnversioned = true }},
		{name: "legacy flag with whitespace version", mutate: func(input *observation.ObservationInput) {
			input.Derivation.LegacyUnversioned = true
			input.Derivation.Version = " \t"
		}},
		{name: "model without prompt", mutate: func(input *observation.ObservationInput) { input.Derivation.Model = "model" }},
		{name: "prompt without model", mutate: func(input *observation.ObservationInput) { input.Derivation.PromptVersion = "prompt" }},
		{name: "whitespace model", mutate: func(input *observation.ObservationInput) {
			input.Derivation.Model = " \t"
			input.Derivation.PromptVersion = "prompt"
		}},
		{name: "whitespace prompt", mutate: func(input *observation.ObservationInput) {
			input.Derivation.Model = "model"
			input.Derivation.PromptVersion = " \t"
		}},
		{name: "whitespace model and prompt", mutate: func(input *observation.ObservationInput) {
			input.Derivation.Model = " \t"
			input.Derivation.PromptVersion = " \n"
		}},
		{name: "invalid status", mutate: func(input *observation.ObservationInput) { input.Status = "certain" }},
		{name: "invalid confidence", mutate: func(input *observation.ObservationInput) { input.Confidence = &invalidConfidence }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := validObservationInput(t)
			test.mutate(&input)
			if _, err := observation.NewObservation(input); err == nil {
				t.Fatal("NewObservation() error = nil, want validation error")
			}
		})
	}

	if _, err := observation.NewUnitIntervalConfidence(math.NaN()); err == nil {
		t.Fatal("NewUnitIntervalConfidence(NaN) error = nil, want validation error")
	}
}

func validObservationInput(t *testing.T) observation.ObservationInput {
	t.Helper()
	subject, err := observation.NewTextTerm("subject")
	if err != nil {
		t.Fatalf("NewTextTerm(subject) error = %v", err)
	}
	object, err := observation.NewTextTerm("object")
	if err != nil {
		t.Fatalf("NewTextTerm(object) error = %v", err)
	}
	predicate, err := observation.NewPredicate("predicate")
	if err != nil {
		t.Fatalf("NewPredicate() error = %v", err)
	}
	return observation.ObservationInput{
		ID: "observation-1",
		Statement: observation.Statement{
			Subject:   subject,
			Predicate: predicate,
			Object:    object,
		},
		ValidTime:  observation.UnknownTime(),
		RecordedAt: time.Date(2026, time.July, 18, 12, 0, 0, 0, time.UTC),
		Evidence: []observation.EvidenceLink{
			{EvidenceID: evidence.EvidenceID("evidence-1"), Role: observation.EvidenceSupporting},
		},
		Derivation: observation.Derivation{
			Method:  "manual",
			Version: "v1",
		},
		Status: observation.StatusObserved,
	}
}
