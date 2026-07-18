package knowledge

import (
	"math"
	"testing"
	"time"
)

func TestObservationKeepsValidTimeSeparateFromRecordedTime(t *testing.T) {
	validAt := time.Date(2024, time.January, 15, 9, 0, 0, 0, time.UTC)
	recordedAt := time.Date(2026, time.July, 18, 12, 0, 0, 0, time.UTC)
	validTime, err := AtTime(validAt)
	if err != nil {
		t.Fatalf("AtTime() error = %v", err)
	}
	confidence := 0.85

	observation, err := NewObservation(ObservationInput{
		ID: ObservationID("observation-1"),
		Statement: Statement{
			Subject:   " Stacks ",
			Predicate: " became ",
			Object:    " a temporal knowledge graph ",
		},
		ValidTime:   validTime,
		RecordedAt:  recordedAt,
		EvidenceIDs: []EvidenceID{"evidence-1"},
		Derivation: Derivation{
			Method:        "language-model",
			Version:       "observation-extractor-v1",
			Model:         "local-model",
			PromptVersion: "prompt-v1",
		},
		Status:     StatusInferred,
		Confidence: &confidence,
	})
	if err != nil {
		t.Fatalf("NewObservation() error = %v", err)
	}

	instant, ok := observation.ValidTime().Instant()
	if !ok || instant != validAt {
		t.Errorf("ValidTime().Instant() = (%v, %v), want (%v, true)", instant, ok, validAt)
	}
	if observation.RecordedAt() != recordedAt {
		t.Errorf("RecordedAt() = %v, want %v", observation.RecordedAt(), recordedAt)
	}
	if observation.Statement().Subject != "Stacks" {
		t.Errorf("Statement().Subject = %q, want %q", observation.Statement().Subject, "Stacks")
	}
	if got, ok := observation.Confidence(); !ok || got != confidence {
		t.Errorf("Confidence() = (%v, %v), want (%v, true)", got, ok, confidence)
	}
}

func TestUnknownValidTimeDoesNotUseRecordedTime(t *testing.T) {
	recordedAt := time.Date(2026, time.July, 18, 12, 0, 0, 0, time.UTC)

	observation, err := NewObservation(validObservationInput(UnknownTime(), recordedAt))
	if err != nil {
		t.Fatalf("NewObservation() error = %v", err)
	}

	if observation.ValidTime().Kind() != TemporalUnknown {
		t.Errorf("ValidTime().Kind() = %v, want %v", observation.ValidTime().Kind(), TemporalUnknown)
	}
	if observation.RecordedAt() != recordedAt {
		t.Errorf("RecordedAt() = %v, want %v", observation.RecordedAt(), recordedAt)
	}
}

func TestDuringRequiresIncreasingBounds(t *testing.T) {
	instant := time.Date(2026, time.July, 18, 12, 0, 0, 0, time.UTC)

	if _, err := During(instant, instant); err == nil {
		t.Fatal("During() error = nil, want invalid interval error")
	}
}

func TestOpenIntervalDistinguishesUnknownBound(t *testing.T) {
	start := time.Date(2026, time.July, 18, 12, 0, 0, 0, time.UTC)
	extent, err := Since(start)
	if err != nil {
		t.Fatalf("Since() error = %v", err)
	}

	gotStart, hasStart, gotEnd, hasEnd := extent.Bounds()
	if !hasStart || gotStart != start {
		t.Errorf("Bounds() start = (%v, %v), want (%v, true)", gotStart, hasStart, start)
	}
	if hasEnd || !gotEnd.IsZero() {
		t.Errorf("Bounds() end = (%v, %v), want (zero, false)", gotEnd, hasEnd)
	}
}

func TestUncertainInstantIsNotValidityInterval(t *testing.T) {
	start := time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(1, 0, 0)
	extent, err := Within(start, end)
	if err != nil {
		t.Fatalf("Within() error = %v", err)
	}

	if extent.Kind() != TemporalWindow {
		t.Errorf("Kind() = %v, want %v", extent.Kind(), TemporalWindow)
	}
}

func TestObservationDefensivelyCopiesEvidenceIDs(t *testing.T) {
	recordedAt := time.Date(2026, time.July, 18, 12, 0, 0, 0, time.UTC)
	input := validObservationInput(UnknownTime(), recordedAt)

	observation, err := NewObservation(input)
	if err != nil {
		t.Fatalf("NewObservation() error = %v", err)
	}

	input.EvidenceIDs[0] = EvidenceID("changed-input")
	returned := observation.EvidenceIDs()
	returned[0] = EvidenceID("changed-output")

	if got := observation.EvidenceIDs()[0]; got != EvidenceID("evidence-1") {
		t.Errorf("EvidenceIDs()[0] = %q, want %q", got, EvidenceID("evidence-1"))
	}
}

func TestNewObservationRejectsInvalidInput(t *testing.T) {
	recordedAt := time.Date(2026, time.July, 18, 12, 0, 0, 0, time.UTC)
	confidenceBelowRange := -0.1
	confidenceAboveRange := 1.1
	confidenceNotANumber := math.NaN()

	tests := []struct {
		name   string
		mutate func(*ObservationInput)
	}{
		{name: "missing ID", mutate: func(input *ObservationInput) { input.ID = "" }},
		{name: "missing subject", mutate: func(input *ObservationInput) { input.Statement.Subject = "" }},
		{name: "missing predicate", mutate: func(input *ObservationInput) { input.Statement.Predicate = "" }},
		{name: "missing object", mutate: func(input *ObservationInput) { input.Statement.Object = "" }},
		{name: "missing recorded time", mutate: func(input *ObservationInput) { input.RecordedAt = time.Time{} }},
		{name: "missing evidence", mutate: func(input *ObservationInput) { input.EvidenceIDs = nil }},
		{name: "duplicate evidence", mutate: func(input *ObservationInput) {
			input.EvidenceIDs = []EvidenceID{"evidence-1", "evidence-1"}
		}},
		{name: "missing derivation method", mutate: func(input *ObservationInput) { input.Derivation.Method = "" }},
		{name: "missing derivation version", mutate: func(input *ObservationInput) { input.Derivation.Version = "" }},
		{name: "model without prompt", mutate: func(input *ObservationInput) { input.Derivation.Model = "model" }},
		{name: "prompt without model", mutate: func(input *ObservationInput) { input.Derivation.PromptVersion = "prompt" }},
		{name: "invalid status", mutate: func(input *ObservationInput) { input.Status = EpistemicStatus("certain") }},
		{name: "confidence below range", mutate: func(input *ObservationInput) { input.Confidence = &confidenceBelowRange }},
		{name: "confidence above range", mutate: func(input *ObservationInput) { input.Confidence = &confidenceAboveRange }},
		{name: "confidence is not a number", mutate: func(input *ObservationInput) { input.Confidence = &confidenceNotANumber }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := validObservationInput(UnknownTime(), recordedAt)
			test.mutate(&input)

			if _, err := NewObservation(input); err == nil {
				t.Fatal("NewObservation() error = nil, want validation error")
			}
		})
	}
}

func validObservationInput(validTime TemporalExtent, recordedAt time.Time) ObservationInput {
	return ObservationInput{
		ID: ObservationID("observation-1"),
		Statement: Statement{
			Subject:   "Stacks",
			Predicate: "is-a",
			Object:    "temporal knowledge graph",
		},
		ValidTime:   validTime,
		RecordedAt:  recordedAt,
		EvidenceIDs: []EvidenceID{"evidence-1"},
		Derivation: Derivation{
			Method:  "manual",
			Version: "v1",
		},
		Status: StatusObserved,
	}
}
