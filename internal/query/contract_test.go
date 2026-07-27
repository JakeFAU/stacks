package query

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/JakeFAU/stacks/core/evidence"
	"github.com/JakeFAU/stacks/core/identity"
	"github.com/JakeFAU/stacks/core/observation"
	"github.com/JakeFAU/stacks/core/temporal"
)

func TestNormalizeRequestAcceptsEveryApprovedIntentShape(t *testing.T) {
	point := mustPoint(t, "at", 2026, time.January, 1)
	before := mustWindow(t, "before", 2026, time.January, 1)
	after := mustWindow(t, "after", 2026, time.February, 1)
	window := mustWindow(t, "window", 2026, time.March, 1)

	tests := []struct {
		name    string
		request Request
		limits  Limits
		want    Request
	}{
		{
			name:    "point",
			request: Request{Intent: temporal.IntentPointInTime, EntityIDs: []identity.EntityID{" entity-b ", "entity-a"}, EntityMatch: EntityMatchAny, Predicates: []observation.Predicate{" predicate-b ", "predicate-a"}, Selections: []temporal.TemporalSelection{point}, KnowledgeScope: temporal.CurrentKnowledge()},
			limits:  Limits{MaxEntities: 2, MaxPredicates: 2, MaxChronology: 3},
			want:    Request{Intent: temporal.IntentPointInTime, EntityIDs: []identity.EntityID{"entity-a", "entity-b"}, EntityMatch: EntityMatchAny, Predicates: []observation.Predicate{"predicate-a", "predicate-b"}, Selections: []temporal.TemporalSelection{point}, KnowledgeScope: temporal.CurrentKnowledge(), Limit: 0},
		},
		{
			name:    "trend",
			request: Request{Intent: temporal.IntentTrendComparison, EntityIDs: []identity.EntityID{"entity-a"}, EntityMatch: EntityMatchAll, Selections: []temporal.TemporalSelection{before, after}, KnowledgeScope: temporal.CurrentKnowledge()},
			limits:  Limits{MaxEntities: 1, MaxPredicates: 1, MaxChronology: 3},
			want:    Request{Intent: temporal.IntentTrendComparison, EntityIDs: []identity.EntityID{"entity-a"}, EntityMatch: EntityMatchAll, Predicates: []observation.Predicate{}, Selections: []temporal.TemporalSelection{before, after}, KnowledgeScope: temporal.CurrentKnowledge(), Limit: 0},
		},
		{
			name:    "trajectory",
			request: Request{Intent: temporal.IntentTrajectory, EntityIDs: []identity.EntityID{"entity-a"}, EntityMatch: EntityMatchAll, Selections: []temporal.TemporalSelection{window}, KnowledgeScope: temporal.CurrentKnowledge(), Limit: 2},
			limits:  Limits{MaxEntities: 1, MaxPredicates: 1, MaxChronology: 3},
			want:    Request{Intent: temporal.IntentTrajectory, EntityIDs: []identity.EntityID{"entity-a"}, EntityMatch: EntityMatchAll, Predicates: []observation.Predicate{}, Selections: []temporal.TemporalSelection{window}, KnowledgeScope: temporal.CurrentKnowledge(), Limit: 2},
		},
		{
			name:    "causal",
			request: Request{Intent: temporal.IntentCausalChain, EntityIDs: []identity.EntityID{"entity-a"}, EntityMatch: EntityMatchAll, Predicates: []observation.Predicate{causalPredicate}, Selections: []temporal.TemporalSelection{window}, KnowledgeScope: temporal.CurrentKnowledge(), Limit: 2},
			limits:  Limits{MaxEntities: 1, MaxPredicates: 1, MaxChronology: 3},
			want:    Request{Intent: temporal.IntentCausalChain, EntityIDs: []identity.EntityID{"entity-a"}, EntityMatch: EntityMatchAll, Predicates: []observation.Predicate{causalPredicate}, Selections: []temporal.TemporalSelection{window}, KnowledgeScope: temporal.CurrentKnowledge(), Limit: 2},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := NormalizeRequest(test.request, test.limits)
			if err != nil {
				t.Fatalf("NormalizeRequest() error = %v", err)
			}
			if !equalRequest(got, test.want) {
				t.Errorf("NormalizeRequest() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestNormalizeRequestRejectsInvalidIntentSelectionsBeforeReaderAccess(t *testing.T) {
	window := mustWindow(t, "window", 2026, time.January, 1)
	request := Request{Intent: temporal.IntentPointInTime, EntityIDs: []identity.EntityID{"entity-a"}, EntityMatch: EntityMatchAll, Selections: []temporal.TemporalSelection{window}, KnowledgeScope: temporal.CurrentKnowledge()}
	if _, err := NormalizeRequest(request, validLimits()); err == nil {
		t.Fatal("NormalizeRequest() error = nil, want invalid point selection error")
	}
}

func TestNormalizeRequestRejectsBlankDuplicateOrOverLimitEntitiesAndPredicates(t *testing.T) {
	point := mustPoint(t, "at", 2026, time.January, 1)
	tests := []struct {
		name    string
		request Request
		limits  Limits
	}{
		{"blank entity", Request{Intent: temporal.IntentPointInTime, EntityIDs: []identity.EntityID{" "}, EntityMatch: EntityMatchAll, Selections: []temporal.TemporalSelection{point}, KnowledgeScope: temporal.CurrentKnowledge()}, validLimits()},
		{"duplicate entity", Request{Intent: temporal.IntentPointInTime, EntityIDs: []identity.EntityID{"entity-a", " entity-a "}, EntityMatch: EntityMatchAll, Selections: []temporal.TemporalSelection{point}, KnowledgeScope: temporal.CurrentKnowledge()}, validLimits()},
		{"over limit entity", Request{Intent: temporal.IntentPointInTime, EntityIDs: []identity.EntityID{"entity-a", "entity-b", "entity-c"}, EntityMatch: EntityMatchAll, Selections: []temporal.TemporalSelection{point}, KnowledgeScope: temporal.CurrentKnowledge()}, Limits{MaxEntities: 2, MaxPredicates: 2, MaxChronology: 2}},
		{"blank predicate", Request{Intent: temporal.IntentPointInTime, EntityIDs: []identity.EntityID{"entity-a"}, EntityMatch: EntityMatchAll, Predicates: []observation.Predicate{" "}, Selections: []temporal.TemporalSelection{point}, KnowledgeScope: temporal.CurrentKnowledge()}, validLimits()},
		{"duplicate predicate", Request{Intent: temporal.IntentPointInTime, EntityIDs: []identity.EntityID{"entity-a"}, EntityMatch: EntityMatchAll, Predicates: []observation.Predicate{"predicate-a", " predicate-a "}, Selections: []temporal.TemporalSelection{point}, KnowledgeScope: temporal.CurrentKnowledge()}, validLimits()},
		{"over limit predicate", Request{Intent: temporal.IntentPointInTime, EntityIDs: []identity.EntityID{"entity-a"}, EntityMatch: EntityMatchAll, Predicates: []observation.Predicate{"predicate-a", "predicate-b", "predicate-c"}, Selections: []temporal.TemporalSelection{point}, KnowledgeScope: temporal.CurrentKnowledge()}, Limits{MaxEntities: 2, MaxPredicates: 2, MaxChronology: 2}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NormalizeRequest(test.request, test.limits); err == nil {
				t.Fatal("NormalizeRequest() error = nil, want validation error")
			}
		})
	}
}

func TestNormalizeRequestEnforcesIntentSpecificLimitRules(t *testing.T) {
	point := mustPoint(t, "at", 2026, time.January, 1)
	window := mustWindow(t, "window", 2026, time.January, 1)
	tests := []struct {
		name    string
		request Request
	}{
		{"point rejects limit", Request{Intent: temporal.IntentPointInTime, EntityIDs: []identity.EntityID{"entity-a"}, EntityMatch: EntityMatchAll, Selections: []temporal.TemporalSelection{point}, KnowledgeScope: temporal.CurrentKnowledge(), Limit: 1}},
		{"trend rejects limit", Request{Intent: temporal.IntentTrendComparison, EntityIDs: []identity.EntityID{"entity-a"}, EntityMatch: EntityMatchAll, Selections: []temporal.TemporalSelection{window, mustWindow(t, "after", 2026, time.February, 1)}, KnowledgeScope: temporal.CurrentKnowledge(), Limit: 1}},
		{"trajectory rejects zero", Request{Intent: temporal.IntentTrajectory, EntityIDs: []identity.EntityID{"entity-a"}, EntityMatch: EntityMatchAll, Selections: []temporal.TemporalSelection{window}, KnowledgeScope: temporal.CurrentKnowledge()}},
		{"causal rejects over limit", Request{Intent: temporal.IntentCausalChain, EntityIDs: []identity.EntityID{"entity-a"}, EntityMatch: EntityMatchAll, Predicates: []observation.Predicate{causalPredicate}, Selections: []temporal.TemporalSelection{window}, KnowledgeScope: temporal.CurrentKnowledge(), Limit: 3}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NormalizeRequest(test.request, Limits{MaxEntities: 2, MaxPredicates: 2, MaxChronology: 2}); err == nil {
				t.Fatal("NormalizeRequest() error = nil, want limit validation error")
			}
		})
	}
}

func TestNormalizeRequestRestrictsCausalPredicate(t *testing.T) {
	window := mustWindow(t, "window", 2026, time.January, 1)
	for _, predicates := range [][]observation.Predicate{nil, {"other"}, {causalPredicate, "other"}} {
		request := Request{Intent: temporal.IntentCausalChain, EntityIDs: []identity.EntityID{"entity-a"}, EntityMatch: EntityMatchAll, Predicates: predicates, Selections: []temporal.TemporalSelection{window}, KnowledgeScope: temporal.CurrentKnowledge(), Limit: 1}
		if _, err := NormalizeRequest(request, validLimits()); err == nil {
			t.Fatalf("NormalizeRequest(%v) error = nil, want exact causal predicate error", predicates)
		}
	}
}

func TestValidateResultRequiresExactlyOneMatchingPayload(t *testing.T) {
	point := mustPoint(t, "at", 2026, time.January, 1)
	payload, err := NewPointPayload(PointInTimeResult{Selection: point})
	if err != nil {
		t.Fatalf("NewPointPayload() error = %v", err)
	}
	result := Result{Intent: temporal.IntentPointInTime, EntityIDs: []identity.EntityID{"entity-a"}, EntityMatch: EntityMatchAll, Predicates: []observation.Predicate{}, Selections: []temporal.TemporalSelection{point}, KnowledgeScope: temporal.CurrentKnowledge(), Payload: payload, Gaps: []Gap{}}
	if err := ValidateResult(result); err != nil {
		t.Fatalf("ValidateResult() error = %v", err)
	}
	result.Payload = IntentPayload{intent: temporal.IntentPointInTime, point: &PointInTimeResult{}, trend: &TrendResult{}}
	if err := ValidateResult(result); err == nil {
		t.Fatal("ValidateResult() error = nil, want malformed union error")
	}
}

func TestResultCollectionsAreNonNilAndCanonicallyOrdered(t *testing.T) {
	keyA := mustStateKey(t, "entity-a", "predicate-a")
	keyB := mustStateKey(t, "entity-b", "predicate-b")
	valueA := mustText(t, "a")
	valueB := mustText(t, "b")
	window := mustWindow(t, "window", 2026, time.January, 1)
	payload, err := NewTrajectoryPayload(TrajectoryResult{Selection: window, Transitions: []Transition{{Kind: temporal.ChangeAdded, Key: keyB, ValidTime: mustInstant(t), After: &Fact{Key: keyB, Value: valueB, Contributions: []Contribution{}, SupportingCitations: []Citation{validCitation("evidence-b")}, ContradictingCitations: []Citation{}}}, {Kind: temporal.ChangeAdded, Key: keyA, ValidTime: mustInstant(t), After: &Fact{Key: keyA, Value: valueA, Contributions: []Contribution{}, SupportingCitations: []Citation{validCitation("evidence-a")}, ContradictingCitations: []Citation{}}}}})
	if err != nil {
		t.Fatalf("NewTrajectoryPayload() error = %v", err)
	}
	result := Result{Intent: temporal.IntentTrajectory, EntityIDs: []identity.EntityID{"entity-b", "entity-a"}, EntityMatch: EntityMatchAll, Predicates: []observation.Predicate{"predicate-b", "predicate-a"}, Selections: []temporal.TemporalSelection{window}, KnowledgeScope: temporal.CurrentKnowledge(), Limit: 2, Payload: payload, Gaps: []Gap{{Kind: GapNoEvidence, EntityID: "entity-b"}, {Kind: GapNoEvidence, EntityID: "entity-a"}}}
	normalized, err := NormalizeResult(result)
	if err != nil {
		t.Fatalf("NormalizeResult() error = %v", err)
	}
	if !slices.Equal(normalized.EntityIDs, []identity.EntityID{"entity-a", "entity-b"}) || !slices.Equal(normalized.Predicates, []observation.Predicate{"predicate-a", "predicate-b"}) {
		t.Errorf("NormalizeResult() metadata = %#v, want lexical ordering", normalized)
	}
	if len(normalized.Gaps) != 2 || normalized.Gaps[0].EntityID != "entity-a" {
		t.Errorf("NormalizeResult() gaps = %#v, want ordered non-nil gaps", normalized.Gaps)
	}
	trajectory, ok := normalized.Payload.Trajectory()
	if !ok || trajectory.Transitions == nil || trajectory.Transitions[0].Key != keyA {
		t.Errorf("NormalizeResult() trajectory = %#v, want ordered non-nil transitions", trajectory)
	}
}

func TestTypedErrorsDoNotContainSuppliedEntityIDsOrPrivatePayloads(t *testing.T) {
	private := "entity-private: private source text"
	for _, err := range []error{ErrEntityNotFound, ErrLimitExceeded, fmt.Errorf("repository failure: %w", ErrEntityNotFound), fmt.Errorf("repository failure: %w", ErrLimitExceeded)} {
		if !errors.Is(err, ErrEntityNotFound) && !errors.Is(err, ErrLimitExceeded) {
			t.Errorf("errors.Is(%v, typed errors) = false", err)
		}
		if strings.Contains(err.Error(), private) {
			t.Errorf("error %q contains supplied private payload", err)
		}
	}
}

func validLimits() Limits { return Limits{MaxEntities: 2, MaxPredicates: 2, MaxChronology: 2} }

func mustPoint(t *testing.T, label string, year int, month time.Month, day int) temporal.TemporalSelection {
	t.Helper()
	selection, err := temporal.At(label, time.Date(year, month, day, 0, 0, 0, 123456789, time.FixedZone("EST", -5*60*60)))
	if err != nil {
		t.Fatal(err)
	}
	return selection
}

func mustWindow(t *testing.T, label string, year int, month time.Month, day int) temporal.TemporalSelection {
	t.Helper()
	start := time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
	selection, err := temporal.Between(label, start, start.AddDate(0, 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	return selection
}

func mustStateKey(t *testing.T, entityID, predicate string) temporal.StateKey {
	t.Helper()
	term, err := observation.NewEntityTerm(entityID, "")
	if err != nil {
		t.Fatal(err)
	}
	key, err := temporal.NewStateKey(term, observation.Predicate(predicate))
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func mustText(t *testing.T, value string) observation.Term {
	t.Helper()
	term, err := observation.NewTextTerm(value)
	if err != nil {
		t.Fatal(err)
	}
	return term
}

func mustInstant(t *testing.T) observation.TemporalExtent {
	t.Helper()
	extent, err := observation.AtTime(time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return extent
}

func validCitation(id evidence.EvidenceID) Citation {
	return Citation{EvidenceID: id, Role: observation.EvidenceSupporting, SourceDocumentID: "document", DocumentVersionID: "version", SectionID: "section", SectionTitle: "section", SectionPath: []string{}, SectionOrder: 0, SectionRole: "body", StartOffset: 0, EndOffset: 1}
}

func equalRequest(left, right Request) bool {
	return left.Intent == right.Intent && left.EntityMatch == right.EntityMatch && left.Limit == right.Limit && slices.Equal(left.EntityIDs, right.EntityIDs) && slices.Equal(left.Predicates, right.Predicates) && slices.Equal(left.Selections, right.Selections) && left.KnowledgeScope == right.KnowledgeScope
}
