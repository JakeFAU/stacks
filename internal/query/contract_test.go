package query

import (
	"errors"
	"fmt"
	"reflect"
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

func TestValidateResultRequiresPayloadSelectionsToMatchRequest(t *testing.T) {
	point := mustPoint(t, "point", 2026, time.January, 1)
	otherPoint := mustPoint(t, "other-point", 2026, time.February, 1)
	before := mustWindow(t, "before", 2026, time.January, 1)
	after := mustWindow(t, "after", 2026, time.February, 1)
	otherWindow := mustWindow(t, "other", 2026, time.March, 1)

	pointPayload := mustPointPayload(t, otherPoint)
	trendPayload := mustTrendPayload(t, otherWindow, after)
	trajectoryPayload := mustTrajectoryPayload(t, otherWindow)
	causalPayload := mustCausalPayload(t, otherWindow)
	tests := []struct {
		name       string
		intent     temporal.Intent
		selections []temporal.TemporalSelection
		limit      int
		payload    IntentPayload
		predicates []observation.Predicate
	}{
		{"point", temporal.IntentPointInTime, []temporal.TemporalSelection{point}, 0, pointPayload, []observation.Predicate{}},
		{"trend", temporal.IntentTrendComparison, []temporal.TemporalSelection{before, after}, 0, trendPayload, []observation.Predicate{}},
		{"trajectory", temporal.IntentTrajectory, []temporal.TemporalSelection{before}, 1, trajectoryPayload, []observation.Predicate{}},
		{"causal", temporal.IntentCausalChain, []temporal.TemporalSelection{before}, 1, causalPayload, []observation.Predicate{CausalPredicate}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := Result{Intent: test.intent, EntityIDs: []identity.EntityID{"entity-a"}, EntityMatch: EntityMatchAll, Predicates: test.predicates, Selections: test.selections, KnowledgeScope: temporal.CurrentKnowledge(), Limit: test.limit, Payload: test.payload, Gaps: []Gap{}}
			if err := ValidateResult(result); err == nil {
				t.Fatal("ValidateResult() error = nil, want payload selection mismatch")
			}
		})
	}
}

func TestPayloadsRejectUnknownChangeKindsAndImpossibleShapes(t *testing.T) {
	before := mustWindow(t, "before", 2026, time.January, 1)
	after := mustWindow(t, "after", 2026, time.February, 1)
	key := mustStateKey(t, "entity-a", "predicate-a")
	fact := validFact(t, key, "value")
	tests := []struct {
		name   string
		change Change
	}{
		{"unknown kind", Change{Kind: "unknown", Key: key, After: &fact}},
		{"added with before", Change{Kind: temporal.ChangeAdded, Key: key, Before: &fact, After: &fact}},
		{"added without after", Change{Kind: temporal.ChangeAdded, Key: key}},
		{"removed with after", Change{Kind: temporal.ChangeRemoved, Key: key, Before: &fact, After: &fact}},
		{"removed without before", Change{Kind: temporal.ChangeRemoved, Key: key}},
		{"changed without before", Change{Kind: temporal.ChangeChanged, Key: key, After: &fact}},
		{"changed without after", Change{Kind: temporal.ChangeChanged, Key: key, Before: &fact}},
	}
	for _, test := range tests {
		t.Run("trend "+test.name, func(t *testing.T) {
			if _, err := NewTrendPayload(TrendResult{Before: WindowResult{Selection: before}, After: WindowResult{Selection: after}, Changes: []Change{test.change}}); err == nil {
				t.Fatal("NewTrendPayload() error = nil, want invalid change error")
			}
		})
		t.Run("trajectory "+test.name, func(t *testing.T) {
			transition := Transition{Kind: test.change.Kind, Key: test.change.Key, ValidTime: mustInstant(t), Before: test.change.Before, After: test.change.After}
			if _, err := NewTrajectoryPayload(TrajectoryResult{Selection: before, Transitions: []Transition{transition}}); err == nil {
				t.Fatal("NewTrajectoryPayload() error = nil, want invalid transition error")
			}
		})
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
	point := mustPoint(t, "at", 2026, time.January, 1)
	for _, request := range []Request{
		{Intent: temporal.IntentPointInTime, EntityIDs: []identity.EntityID{identity.EntityID(private), identity.EntityID(private)}, EntityMatch: EntityMatchAll, Selections: []temporal.TemporalSelection{point}, KnowledgeScope: temporal.CurrentKnowledge()},
		{Intent: temporal.IntentPointInTime, EntityIDs: []identity.EntityID{"entity-a"}, EntityMatch: EntityMatchAll, Predicates: []observation.Predicate{observation.Predicate(private), observation.Predicate(private)}, Selections: []temporal.TemporalSelection{point}, KnowledgeScope: temporal.CurrentKnowledge()},
	} {
		_, err := NormalizeRequest(request, validLimits())
		if err == nil {
			t.Fatal("NormalizeRequest() error = nil, want privacy-safe validation error")
		}
		if strings.Contains(err.Error(), private) {
			t.Errorf("NormalizeRequest() error %q contains supplied private payload", err)
		}
	}
}

func TestPayloadConstructorsAndAccessorsDefensivelyCopyNestedSlices(t *testing.T) {
	window := mustWindow(t, "window", 2026, time.January, 1)
	key := mustStateKey(t, "entity-a", "predicate-a")
	input := TrajectoryResult{Selection: window, Transitions: []Transition{{Kind: temporal.ChangeAdded, Key: key, ValidTime: mustInstant(t), After: &Fact{Key: key, Value: mustText(t, "value"), Contributions: []Contribution{}, SupportingCitations: []Citation{{EvidenceID: "evidence-a", Role: observation.EvidenceSupporting, SourceDocumentID: "document", DocumentVersionID: "version", SectionID: "section", SectionTitle: "title", SectionPath: []string{"parent"}, SectionOrder: 0, SectionRole: "body", StartOffset: 0, EndOffset: 1}}, ContradictingCitations: []Citation{}}}}}
	payload, err := NewTrajectoryPayload(input)
	if err != nil {
		t.Fatalf("NewTrajectoryPayload() error = %v", err)
	}
	input.Transitions[0].After.SupportingCitations[0].SectionPath[0] = "mutated-input"
	first, ok := payload.Trajectory()
	if !ok || first.Transitions[0].After.SupportingCitations[0].SectionPath[0] != "parent" {
		t.Fatalf("payload stored input mutation: %#v", first)
	}
	first.Transitions[0].After.SupportingCitations[0].SectionPath[0] = "mutated-output"
	second, ok := payload.Trajectory()
	if !ok || second.Transitions[0].After.SupportingCitations[0].SectionPath[0] != "parent" {
		t.Errorf("payload stored accessor mutation: %#v", second)
	}
}

func TestOrderingIsTotalAcrossEarlierKeyTies(t *testing.T) {
	key := mustStateKey(t, "entity-a", "predicate-a")
	firstFact := validFact(t, key, "value")
	secondFact := validFact(t, key, "value")
	firstFact.Contributions = []Contribution{{ObservationID: "observation", Status: observation.StatusObserved, ValidTime: mustInstant(t), RecordedAt: time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC), Derivation: observation.Derivation{Method: "method-a", Version: "v1"}}}
	secondFact.Contributions = append([]Contribution{}, firstFact.Contributions...)
	secondFact.Contributions[0].Derivation.Method = "method-b"
	first := []Fact{firstFact, secondFact}
	second := []Fact{secondFact, firstFact}
	orderFacts(first)
	orderFacts(second)
	if !slices.EqualFunc(first, second, func(left, right Fact) bool { return reflect.DeepEqual(left, right) }) {
		t.Errorf("orderFacts() differs after reverse: %#v and %#v", first, second)
	}

	firstContribution := Contribution{ObservationID: "observation", Status: observation.StatusObserved, ValidTime: mustInstant(t), RecordedAt: time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC), Derivation: observation.Derivation{Method: "method-a", Version: "v1"}}
	secondContribution := firstContribution
	secondContribution.Derivation.Method = "method-b"
	contributionsA, contributionsB := []Contribution{firstContribution, secondContribution}, []Contribution{secondContribution, firstContribution}
	orderContributions(contributionsA)
	orderContributions(contributionsB)
	if !slices.EqualFunc(contributionsA, contributionsB, func(left, right Contribution) bool { return reflect.DeepEqual(left, right) }) {
		t.Errorf("orderContributions() differs after reverse")
	}

	firstCitation := validCitation("evidence")
	secondCitation := firstCitation
	secondCitation.SectionTitle = "later title"
	citationsA, citationsB := []Citation{firstCitation, secondCitation}, []Citation{secondCitation, firstCitation}
	orderCitations(citationsA)
	orderCitations(citationsB)
	if !slices.EqualFunc(citationsA, citationsB, func(left, right Citation) bool { return reflect.DeepEqual(left, right) }) {
		t.Errorf("orderCitations() differs after reverse")
	}

	firstChange, secondChange := Change{Kind: temporal.ChangeAdded, Key: key, After: &firstFact}, Change{Kind: temporal.ChangeAdded, Key: key, After: &secondFact}
	changesA, changesB := []Change{firstChange, secondChange}, []Change{secondChange, firstChange}
	orderChanges(changesA)
	orderChanges(changesB)
	if !reflect.DeepEqual(changesA, changesB) {
		t.Errorf("orderChanges() differs after reverse")
	}

	firstUnresolved, secondUnresolved := UnresolvedItem{Key: key, Reason: temporal.UnresolvedHypothesis, Candidates: []Fact{firstFact}}, UnresolvedItem{Key: key, Reason: temporal.UnresolvedHypothesis, Candidates: []Fact{secondFact}}
	unresolvedA, unresolvedB := []UnresolvedItem{firstUnresolved, secondUnresolved}, []UnresolvedItem{secondUnresolved, firstUnresolved}
	orderUnresolvedItems(unresolvedA)
	orderUnresolvedItems(unresolvedB)
	if !reflect.DeepEqual(unresolvedA, unresolvedB) {
		t.Errorf("orderUnresolvedItems() differs after reverse")
	}

	firstTransition, secondTransition := Transition{Kind: temporal.ChangeAdded, Key: key, ValidTime: mustInstant(t), After: &firstFact}, Transition{Kind: temporal.ChangeAdded, Key: key, ValidTime: mustInstant(t), After: &secondFact}
	transitionsA, transitionsB := []Transition{firstTransition, secondTransition}, []Transition{secondTransition, firstTransition}
	orderTransitions(transitionsA)
	orderTransitions(transitionsB)
	if !reflect.DeepEqual(transitionsA, transitionsB) {
		t.Errorf("orderTransitions() differs after reverse")
	}

	firstLink, secondLink := CausalLink{Cause: mustText(t, "cause"), Effect: mustText(t, "effect"), Contributions: firstFact.Contributions, SupportingCitations: firstFact.SupportingCitations, ContradictingCitations: []Citation{}}, CausalLink{Cause: mustText(t, "cause"), Effect: mustText(t, "effect"), Contributions: secondFact.Contributions, SupportingCitations: secondFact.SupportingCitations, ContradictingCitations: []Citation{}}
	linksA, linksB := []CausalLink{firstLink, secondLink}, []CausalLink{secondLink, firstLink}
	orderCausalLinks(linksA)
	orderCausalLinks(linksB)
	if !reflect.DeepEqual(linksA, linksB) {
		t.Errorf("orderCausalLinks() differs after reverse")
	}

	gapsA, gapsB := []Gap{{Kind: GapNoEvidence, EntityID: "entity", SelectionLabel: "selection", Predicate: "predicate-a"}, {Kind: GapNoEvidence, EntityID: "entity", SelectionLabel: "selection", Predicate: "predicate-b"}}, []Gap{{Kind: GapNoEvidence, EntityID: "entity", SelectionLabel: "selection", Predicate: "predicate-b"}, {Kind: GapNoEvidence, EntityID: "entity", SelectionLabel: "selection", Predicate: "predicate-a"}}
	orderGaps(gapsA)
	orderGaps(gapsB)
	if !reflect.DeepEqual(gapsA, gapsB) {
		t.Errorf("orderGaps() differs after reverse")
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

func validFact(t *testing.T, key temporal.StateKey, value string) Fact {
	t.Helper()
	return Fact{Key: key, Value: mustText(t, value), Contributions: []Contribution{}, SupportingCitations: []Citation{validCitation("evidence")}, ContradictingCitations: []Citation{}}
}

func mustPointPayload(t *testing.T, selection temporal.TemporalSelection) IntentPayload {
	t.Helper()
	payload, err := NewPointPayload(PointInTimeResult{Selection: selection})
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func mustTrendPayload(t *testing.T, before, after temporal.TemporalSelection) IntentPayload {
	t.Helper()
	payload, err := NewTrendPayload(TrendResult{Before: WindowResult{Selection: before}, After: WindowResult{Selection: after}})
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func mustTrajectoryPayload(t *testing.T, selection temporal.TemporalSelection) IntentPayload {
	t.Helper()
	payload, err := NewTrajectoryPayload(TrajectoryResult{Selection: selection})
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func mustCausalPayload(t *testing.T, selection temporal.TemporalSelection) IntentPayload {
	t.Helper()
	payload, err := NewCausalPayload(CausalChainResult{Selection: selection})
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func equalRequest(left, right Request) bool {
	return left.Intent == right.Intent && left.EntityMatch == right.EntityMatch && left.Limit == right.Limit && slices.Equal(left.EntityIDs, right.EntityIDs) && slices.Equal(left.Predicates, right.Predicates) && slices.Equal(left.Selections, right.Selections) && left.KnowledgeScope == right.KnowledgeScope
}
