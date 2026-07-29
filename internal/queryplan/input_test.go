package queryplan

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/JakeFAU/stacks/core/identity"
	"stacks/internal/query"
)

func TestNormalizeInputRejectsInvalidPreDisclosureValuesWithoutPrivateMarkers(t *testing.T) {
	limits := query.Limits{MaxEntities: 2, MaxPredicates: 8, MaxChronology: 20}
	referenceTime := time.Date(2026, time.July, 29, 12, 0, 0, 0, time.FixedZone("synthetic", -4*60*60))
	markers := []string{"private-question-marker", "entity-atlas-001", "entity-atlas-002"}

	tests := []struct {
		name             string
		input            Input
		limits           query.Limits
		maxQuestionBytes int
	}{
		{
			name:             "missing question",
			input:            Input{EntityIDs: []identity.EntityID{"entity-atlas-001"}, ReferenceTime: referenceTime},
			limits:           limits,
			maxQuestionBytes: 1024,
		},
		{
			name:             "blank question",
			input:            Input{Question: " \t\n", EntityIDs: []identity.EntityID{"entity-atlas-001"}, ReferenceTime: referenceTime},
			limits:           limits,
			maxQuestionBytes: 1024,
		},
		{
			name:             "oversized question",
			input:            Input{Question: "private-question-marker", EntityIDs: []identity.EntityID{"entity-atlas-001"}, ReferenceTime: referenceTime},
			limits:           limits,
			maxQuestionBytes: 4,
		},
		{
			name:             "invalid utf8 question",
			input:            Input{Question: string([]byte{0xff}), EntityIDs: []identity.EntityID{"entity-atlas-001"}, ReferenceTime: referenceTime},
			limits:           limits,
			maxQuestionBytes: 1024,
		},
		{
			name:             "missing reference time",
			input:            Input{Question: "private-question-marker", EntityIDs: []identity.EntityID{"entity-atlas-001"}},
			limits:           limits,
			maxQuestionBytes: 1024,
		},
		{
			name:             "missing entity ids",
			input:            Input{Question: "private-question-marker", ReferenceTime: referenceTime},
			limits:           limits,
			maxQuestionBytes: 1024,
		},
		{
			name:             "blank entity id",
			input:            Input{Question: "private-question-marker", EntityIDs: []identity.EntityID{" "}, ReferenceTime: referenceTime},
			limits:           limits,
			maxQuestionBytes: 1024,
		},
		{
			name:             "duplicate entity id",
			input:            Input{Question: "private-question-marker", EntityIDs: []identity.EntityID{"entity-atlas-001", " entity-atlas-001 "}, ReferenceTime: referenceTime},
			limits:           limits,
			maxQuestionBytes: 1024,
		},
		{
			name:             "over limit entity ids",
			input:            Input{Question: "private-question-marker", EntityIDs: []identity.EntityID{"entity-atlas-001", "entity-atlas-002", "entity-atlas-003"}, ReferenceTime: referenceTime},
			limits:           limits,
			maxQuestionBytes: 1024,
		},
		{
			name:             "invalid query limits",
			input:            Input{Question: "private-question-marker", EntityIDs: []identity.EntityID{"entity-atlas-001"}, ReferenceTime: referenceTime},
			limits:           query.Limits{MaxEntities: 0, MaxPredicates: 8, MaxChronology: 20},
			maxQuestionBytes: 1024,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NormalizeInput(test.input, test.limits, test.maxQuestionBytes)
			if err == nil {
				t.Fatal("NormalizeInput() error = nil")
			}
			for _, marker := range markers {
				if strings.Contains(err.Error(), marker) {
					t.Fatalf("NormalizeInput() error = %q, contains private marker %q", err, marker)
				}
			}
		})
	}
}

func TestNormalizeInputRejectsIDDisclosure(t *testing.T) {
	input := Input{
		Question:      "What changed for entity-atlas-001?",
		EntityIDs:     []identity.EntityID{"entity-atlas-001"},
		ReferenceTime: time.Date(2026, time.July, 29, 12, 0, 0, 0, time.FixedZone("synthetic", -4*60*60)),
	}
	_, err := NormalizeInput(input, query.Limits{MaxEntities: 4, MaxPredicates: 8, MaxChronology: 20}, 1024)
	if err == nil || strings.Contains(err.Error(), "entity-atlas-001") {
		t.Fatalf("NormalizeInput() error = %v", err)
	}
}

func TestNormalizeInputCanonicalizesPrivateValues(t *testing.T) {
	input := Input{
		Question:      "What changed last quarter?",
		EntityIDs:     []identity.EntityID{" entity-atlas-002 ", "entity-atlas-001"},
		ReferenceTime: time.Date(2026, time.July, 29, 12, 0, 0, 123456789, time.FixedZone("synthetic", -4*60*60)),
	}

	normalized, err := NormalizeInput(input, query.Limits{MaxEntities: 4, MaxPredicates: 8, MaxChronology: 20}, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := normalized.EntityIDs, []identity.EntityID{"entity-atlas-001", "entity-atlas-002"}; !equalEntityIDs(got, want) {
		t.Fatalf("NormalizeInput() entity IDs = %v, want %v", got, want)
	}
	wantReferenceTime := time.Date(2026, time.July, 29, 16, 0, 0, 123456000, time.UTC)
	if !normalized.ReferenceTime.Equal(wantReferenceTime) || normalized.ReferenceTime.Location() != time.UTC {
		t.Fatalf("NormalizeInput() reference time = %s, want %s UTC", normalized.ReferenceTime, wantReferenceTime)
	}
	input.EntityIDs[0] = "mutated"
	if normalized.EntityIDs[0] != "entity-atlas-001" {
		t.Fatalf("NormalizeInput() stored caller mutation: %v", normalized.EntityIDs)
	}
}

func TestModelRequestForOmitsCanonicalIDsAndIsDeterministic(t *testing.T) {
	input := normalizedSyntheticInput(t)
	limits := query.Limits{MaxEntities: 4, MaxPredicates: 8, MaxChronology: 20}

	first, err := modelRequestFor(input, limits)
	if err != nil {
		t.Fatal(err)
	}
	second, err := modelRequestFor(input, limits)
	if err != nil {
		t.Fatal(err)
	}
	if first.Input != second.Input || !bytes.Equal(first.JSONSchema, second.JSONSchema) {
		t.Fatalf("modelRequestFor() is not deterministic: first = %#v, second = %#v", first, second)
	}
	for _, entityID := range input.EntityIDs {
		if strings.Contains(first.Input, string(entityID)) {
			t.Fatalf("modelRequestFor() model input contains canonical entity ID %q", entityID)
		}
	}
	const want = `{"question":"What changed last quarter?","reference_time":"2026-07-29T16:00:00Z","entity_count":1,"entity_ids_attached_locally":true,"limits":{"max_entities":4,"max_predicates":8,"max_chronology":20}}`
	if first.Input != want {
		t.Fatalf("ModelRequest.Input = %s, want %s", first.Input, want)
	}
	if first.PromptVersion != PromptVersion || first.SchemaName != SchemaName || first.SystemPrompt == "" {
		t.Fatalf("ModelRequest contract = %#v", first)
	}
	first.JSONSchema[0] ^= 0xff
	if bytes.Equal(first.JSONSchema, second.JSONSchema) {
		t.Fatal("modelRequestFor() returned aliased schema bytes")
	}
}

func TestModelRequestForRejectsInvalidLimits(t *testing.T) {
	_, err := modelRequestFor(normalizedSyntheticInput(t), query.Limits{MaxEntities: 0, MaxPredicates: 8, MaxChronology: 20})
	if err == nil || strings.Contains(err.Error(), "entity-atlas-001") {
		t.Fatalf("modelRequestFor() error = %v", err)
	}
}

func normalizedSyntheticInput(t *testing.T) Input {
	t.Helper()
	input, err := NormalizeInput(Input{
		Question:      "What changed last quarter?",
		EntityIDs:     []identity.EntityID{"entity-atlas-001"},
		ReferenceTime: time.Date(2026, time.July, 29, 12, 0, 0, 0, time.FixedZone("synthetic", -4*60*60)),
	}, query.Limits{MaxEntities: 4, MaxPredicates: 8, MaxChronology: 20}, 1024)
	if err != nil {
		t.Fatal(err)
	}
	return input
}

func equalEntityIDs(got, want []identity.EntityID) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}
