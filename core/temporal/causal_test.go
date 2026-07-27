package temporal_test

import (
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/JakeFAU/stacks/core/evidence"
	"github.com/JakeFAU/stacks/core/observation"
	"github.com/JakeFAU/stacks/core/temporal"
)

func TestBuildCausalChainIncludesOnlyExplicitCausalObservations(t *testing.T) {
	selection := causalSelection(t)
	cause := causalText(t, "deployment")
	effect := causalText(t, "latency")
	explicit := causalCandidate(
		t, "explicit", temporal.CausalPredicate, cause, effect,
		causalInstant(t, 2), causalRecordedAt(2), observation.StatusObserved, nil,
		[]observation.EvidenceLink{
			{EvidenceID: "support-explicit", Role: observation.EvidenceSupporting},
			{EvidenceID: "counter-explicit", Role: observation.EvidenceContradicting},
		},
	)
	chronologyOnly := causalCandidate(
		t, "chronology-only", "project.precedes", cause, effect,
		causalInstant(t, 1), causalRecordedAt(1), observation.StatusObserved, nil,
		[]observation.EvidenceLink{{EvidenceID: "support-chronology", Role: observation.EvidenceSupporting}},
	)

	got, err := temporal.BuildCausalChain(
		selection,
		temporal.CurrentKnowledge(),
		[]temporal.StateCandidate{chronologyOnly, explicit},
	)
	if err != nil {
		t.Fatalf("BuildCausalChain() error = %v", err)
	}
	want := []temporal.CausalLink{{
		Cause:                    cause,
		Effect:                   effect,
		ObservationIDs:           []observation.ObservationID{"explicit"},
		SupportingEvidenceIDs:    []evidence.EvidenceID{"support-explicit"},
		ContradictingEvidenceIDs: []evidence.EvidenceID{"counter-explicit"},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BuildCausalChain() = %#v, want only exact explicit causal evidence %#v", got, want)
	}
}

func TestBuildCausalChainPreservesIsolatedLinksAndExactTypedTwoLinkChains(t *testing.T) {
	selection := causalSelection(t)
	first := causalText(t, "A")
	middle := causalEntity(t, "entity:middle")
	last := causalText(t, "C")
	isolatedCause := causalText(t, "isolated")
	isolatedEffect := causalText(t, "outcome")
	candidates := []temporal.StateCandidate{
		causalCandidate(
			t, "isolated", temporal.CausalPredicate, isolatedCause, isolatedEffect,
			causalInstant(t, 4), causalRecordedAt(4), observation.StatusObserved, nil,
			[]observation.EvidenceLink{{EvidenceID: "support-isolated", Role: observation.EvidenceSupporting}},
		),
		causalCandidate(
			t, "second", temporal.CausalPredicate, middle, last,
			causalInstant(t, 3), causalRecordedAt(3), observation.StatusObserved, nil,
			[]observation.EvidenceLink{{EvidenceID: "support-second", Role: observation.EvidenceSupporting}},
		),
		causalCandidate(
			t, "first", temporal.CausalPredicate, first, middle,
			causalInstant(t, 2), causalRecordedAt(2), observation.StatusObserved, nil,
			[]observation.EvidenceLink{{EvidenceID: "support-first", Role: observation.EvidenceSupporting}},
		),
	}

	got, err := temporal.BuildCausalChain(selection, temporal.CurrentKnowledge(), candidates)
	if err != nil {
		t.Fatalf("BuildCausalChain() error = %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("links = %#v, want exact two-link chain plus one isolated explicit link", got)
	}
	if temporal.CompareTerms(got[0].Effect, got[1].Cause) != 0 {
		t.Fatalf("first effect and second cause do not have exact typed equality: %#v %#v", got[0], got[1])
	}
	if got[0].Cause != first || got[0].Effect != middle ||
		got[1].Cause != middle || got[1].Effect != last ||
		got[2].Cause != isolatedCause || got[2].Effect != isolatedEffect {
		t.Fatalf("links = %#v, want exact explicit edges in chronology order", got)
	}
	for _, link := range got {
		if link.Cause == first && link.Effect == last {
			t.Fatalf("links = %#v, must not synthesize a transitive cause-to-effect edge", got)
		}
	}
}

func TestBuildCausalChainNeverConnectsVisiblyEqualTermsOfDifferentTypes(t *testing.T) {
	selection := causalSelection(t)
	first := causalText(t, "A")
	textMiddle := causalText(t, "entity:middle")
	entityMiddle := causalEntity(t, "entity:middle")
	last := causalText(t, "C")
	got, err := temporal.BuildCausalChain(
		selection,
		temporal.CurrentKnowledge(),
		[]temporal.StateCandidate{
			causalCandidate(
				t, "first", temporal.CausalPredicate, first, textMiddle,
				causalInstant(t, 2), causalRecordedAt(2), observation.StatusObserved, nil,
				[]observation.EvidenceLink{{EvidenceID: "support-first", Role: observation.EvidenceSupporting}},
			),
			causalCandidate(
				t, "second", temporal.CausalPredicate, entityMiddle, last,
				causalInstant(t, 3), causalRecordedAt(3), observation.StatusObserved, nil,
				[]observation.EvidenceLink{{EvidenceID: "support-second", Role: observation.EvidenceSupporting}},
			),
		},
	)
	if err != nil {
		t.Fatalf("BuildCausalChain() error = %v", err)
	}
	if len(got) != 2 || temporal.CompareTerms(got[0].Effect, got[1].Cause) == 0 {
		t.Fatalf("links = %#v, visibly equal text/entity terms must remain isolated typed links", got)
	}
	for _, link := range got {
		if link.Cause == first && link.Effect == last {
			t.Fatalf("links = %#v, type mismatch must not synthesize a chain edge", got)
		}
	}
}

func TestBuildCausalChainOrdersChronologicallyWithStableTies(t *testing.T) {
	selection := causalSelection(t)
	candidates := []temporal.StateCandidate{
		causalCandidate(
			t, "z-late-recorded", temporal.CausalPredicate, causalText(t, "z"), causalText(t, "z-effect"),
			causalInstant(t, 2), causalRecordedAt(3), observation.StatusObserved, nil,
			[]observation.EvidenceLink{{EvidenceID: "z-evidence", Role: observation.EvidenceSupporting}},
		),
		causalCandidate(
			t, "b-tied", temporal.CausalPredicate, causalText(t, "b"), causalText(t, "b-effect"),
			causalInstant(t, 2), causalRecordedAt(2), observation.StatusObserved, nil,
			[]observation.EvidenceLink{{EvidenceID: "b-evidence", Role: observation.EvidenceSupporting}},
		),
		causalCandidate(
			t, "a-tied", temporal.CausalPredicate, causalText(t, "a"), causalText(t, "a-effect"),
			causalInstant(t, 2), causalRecordedAt(2), observation.StatusObserved, nil,
			[]observation.EvidenceLink{{EvidenceID: "a-evidence", Role: observation.EvidenceSupporting}},
		),
		causalCandidate(
			t, "earliest", temporal.CausalPredicate, causalText(t, "early"), causalText(t, "early-effect"),
			causalInstant(t, 1), causalRecordedAt(4), observation.StatusObserved, nil,
			[]observation.EvidenceLink{{EvidenceID: "early-evidence", Role: observation.EvidenceSupporting}},
		),
	}
	reversed := append([]temporal.StateCandidate{}, candidates...)
	slices.Reverse(reversed)

	first, err := temporal.BuildCausalChain(selection, temporal.CurrentKnowledge(), candidates)
	if err != nil {
		t.Fatalf("BuildCausalChain(first) error = %v", err)
	}
	second, err := temporal.BuildCausalChain(selection, temporal.CurrentKnowledge(), reversed)
	if err != nil {
		t.Fatalf("BuildCausalChain(reversed) error = %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("input order changed causal result:\nfirst  %#v\nsecond %#v", first, second)
	}
	gotIDs := make([]observation.ObservationID, len(first))
	for index, link := range first {
		gotIDs[index] = link.ObservationIDs[0]
	}
	if !slices.Equal(gotIDs, []observation.ObservationID{"earliest", "a-tied", "b-tied", "z-late-recorded"}) {
		t.Fatalf("causal order = %v, want valid time, recorded time, observation ID", gotIDs)
	}
}

func TestBuildCausalChainAppliesRecordedCutoffIndependentlyFromValidTime(t *testing.T) {
	selection := causalSelection(t)
	earlyRecorded := causalCandidate(
		t, "early-recorded", temporal.CausalPredicate, causalText(t, "A"), causalText(t, "B"),
		causalInstant(t, 3), causalRecordedAt(1), observation.StatusObserved, nil,
		[]observation.EvidenceLink{{EvidenceID: "early-evidence", Role: observation.EvidenceSupporting}},
	)
	lateRecordedEarlierValid := causalCandidate(
		t, "late-recorded", temporal.CausalPredicate, causalText(t, "X"), causalText(t, "Y"),
		causalInstant(t, 1), causalRecordedAt(3), observation.StatusObserved, nil,
		[]observation.EvidenceLink{{EvidenceID: "late-evidence", Role: observation.EvidenceSupporting}},
	)
	recordedExactlyAtCutoff := causalCandidate(
		t, "at-cutoff", temporal.CausalPredicate, causalText(t, "M"), causalText(t, "N"),
		causalInstant(t, 4), causalRecordedAt(2), observation.StatusObserved, nil,
		[]observation.EvidenceLink{{EvidenceID: "cutoff-evidence", Role: observation.EvidenceSupporting}},
	)
	scope, err := temporal.KnownAsOf(causalRecordedAt(2))
	if err != nil {
		t.Fatalf("KnownAsOf() error = %v", err)
	}

	historical, err := temporal.BuildCausalChain(selection, scope, []temporal.StateCandidate{
		lateRecordedEarlierValid,
		recordedExactlyAtCutoff,
		earlyRecorded,
	})
	if err != nil {
		t.Fatalf("BuildCausalChain(as-of) error = %v", err)
	}
	current, err := temporal.BuildCausalChain(selection, temporal.CurrentKnowledge(), []temporal.StateCandidate{
		earlyRecorded,
		recordedExactlyAtCutoff,
		lateRecordedEarlierValid,
	})
	if err != nil {
		t.Fatalf("BuildCausalChain(current) error = %v", err)
	}
	if len(historical) != 2 ||
		historical[0].ObservationIDs[0] != "early-recorded" ||
		historical[1].ObservationIDs[0] != "at-cutoff" {
		t.Fatalf("historical links = %#v, want observations recorded before or exactly at inclusive cutoff ordered by valid time", historical)
	}
	if len(current) != 3 || current[0].ObservationIDs[0] != "late-recorded" {
		t.Fatalf("current links = %#v, want all observations ordered by valid time", current)
	}
}

func TestBuildCausalChainKeepsAdmittedRejectedClaimsExplicit(t *testing.T) {
	rejected := causalCandidate(
		t, "rejected", temporal.CausalPredicate, causalText(t, "A"), causalText(t, "B"),
		causalInstant(t, 2), causalRecordedAt(2), observation.StatusRejected, nil,
		[]observation.EvidenceLink{{EvidenceID: "rejected-evidence", Role: observation.EvidenceContradicting}},
	)

	got, err := temporal.BuildCausalChain(
		causalSelection(t),
		temporal.CurrentKnowledge(),
		[]temporal.StateCandidate{rejected},
	)
	if err != nil {
		t.Fatalf("BuildCausalChain() error = %v", err)
	}
	if len(got) != 1 || !slices.Equal(got[0].ObservationIDs, []observation.ObservationID{"rejected"}) {
		t.Fatalf("links = %#v, want admitted explicit claim preserved with rejected epistemic status", got)
	}
}

func TestBuildCausalChainMergesExactLinksWithoutConfidenceOrInputOrderSelection(t *testing.T) {
	selection := causalSelection(t)
	cause := causalText(t, "A")
	effect := causalText(t, "B")
	low := causalConfidence(t, 0.01)
	high := causalConfidence(t, 0.99)
	lowCandidate := causalCandidate(
		t, "low", temporal.CausalPredicate, cause, effect,
		causalInstant(t, 2), causalRecordedAt(2), observation.StatusObserved, &low,
		[]observation.EvidenceLink{
			{EvidenceID: "support-low", Role: observation.EvidenceSupporting},
			{EvidenceID: "counter-low", Role: observation.EvidenceContradicting},
		},
	)
	highCandidate := causalCandidate(
		t, "high", temporal.CausalPredicate, cause, effect,
		causalInstant(t, 3), causalRecordedAt(3), observation.StatusValidatedEmpirically, &high,
		[]observation.EvidenceLink{{EvidenceID: "support-high", Role: observation.EvidenceSupporting}},
	)

	first, err := temporal.BuildCausalChain(selection, temporal.CurrentKnowledge(), []temporal.StateCandidate{lowCandidate, highCandidate})
	if err != nil {
		t.Fatalf("BuildCausalChain(first) error = %v", err)
	}
	second, err := temporal.BuildCausalChain(selection, temporal.CurrentKnowledge(), []temporal.StateCandidate{highCandidate, lowCandidate})
	if err != nil {
		t.Fatalf("BuildCausalChain(second) error = %v", err)
	}
	if !reflect.DeepEqual(first, second) || len(first) != 1 {
		t.Fatalf("merged links differ or were confidence-selected:\nfirst  %#v\nsecond %#v", first, second)
	}
	want := temporal.CausalLink{
		Cause:                    cause,
		Effect:                   effect,
		ObservationIDs:           []observation.ObservationID{"high", "low"},
		SupportingEvidenceIDs:    []evidence.EvidenceID{"support-high", "support-low"},
		ContradictingEvidenceIDs: []evidence.EvidenceID{"counter-low"},
	}
	if !reflect.DeepEqual(first[0], want) {
		t.Fatalf("merged link = %#v, want all provenance independent of confidence %#v", first[0], want)
	}
}

func TestBuildCausalChainRejectsChronologyWithoutExplicitCausalEvidence(t *testing.T) {
	got, err := temporal.BuildCausalChain(
		causalSelection(t),
		temporal.CurrentKnowledge(),
		[]temporal.StateCandidate{causalCandidate(
			t, "chronology-only", "project.precedes", causalText(t, "A"), causalText(t, "B"),
			causalInstant(t, 2), causalRecordedAt(2), observation.StatusObserved, nil,
			[]observation.EvidenceLink{{EvidenceID: "chronology-evidence", Role: observation.EvidenceSupporting}},
		)},
	)
	if err != nil {
		t.Fatalf("BuildCausalChain() error = %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("links = %#v, want an empty non-nil result for chronology-only input", got)
	}
}

func TestBuildCausalChainRejectsInvalidInput(t *testing.T) {
	point, err := temporal.At("point", causalTime(2))
	if err != nil {
		t.Fatalf("temporal.At() error = %v", err)
	}
	validCandidate := causalCandidate(
		t, "valid", temporal.CausalPredicate, causalText(t, "A"), causalText(t, "B"),
		causalInstant(t, 2), causalRecordedAt(2), observation.StatusObserved, nil,
		[]observation.EvidenceLink{{EvidenceID: "valid-evidence", Role: observation.EvidenceSupporting}},
	)
	tests := []struct {
		name       string
		selection  temporal.TemporalSelection
		scope      temporal.KnowledgeScope
		candidates []temporal.StateCandidate
	}{
		{name: "point selection", selection: point, scope: temporal.CurrentKnowledge(), candidates: []temporal.StateCandidate{validCandidate}},
		{
			name:      "candidate statement mismatch",
			selection: causalSelection(t),
			scope:     temporal.CurrentKnowledge(),
			candidates: []temporal.StateCandidate{func() temporal.StateCandidate {
				invalid := validCandidate
				invalid.Value = causalText(t, "different")
				return invalid
			}()},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := temporal.BuildCausalChain(test.selection, test.scope, test.candidates); err == nil {
				t.Fatal("BuildCausalChain() error = nil, want invalid input failure")
			}
		})
	}
}

func causalSelection(t *testing.T) temporal.TemporalSelection {
	t.Helper()
	selection, err := temporal.Between("causal", causalTime(0), causalTime(5))
	if err != nil {
		t.Fatalf("temporal.Between() error = %v", err)
	}
	return selection
}

func causalCandidate(
	t *testing.T,
	id string,
	predicate observation.Predicate,
	cause observation.Term,
	effect observation.Term,
	validTime observation.TemporalExtent,
	recordedAt time.Time,
	status observation.EpistemicStatus,
	confidence *observation.Confidence,
	links []observation.EvidenceLink,
) temporal.StateCandidate {
	t.Helper()
	valueObservation, err := observation.NewObservation(observation.ObservationInput{
		ID:         observation.ObservationID(id),
		Statement:  observation.Statement{Subject: cause, Predicate: predicate, Object: effect},
		ValidTime:  validTime,
		RecordedAt: recordedAt,
		Evidence:   links,
		Derivation: observation.Derivation{Method: "synthetic", Version: "causal-v1"},
		Status:     status,
		Confidence: confidence,
	})
	if err != nil {
		t.Fatalf("observation.NewObservation(%q) error = %v", id, err)
	}
	key, err := temporal.NewStateKey(cause, predicate)
	if err != nil {
		t.Fatalf("temporal.NewStateKey() error = %v", err)
	}
	return temporal.StateCandidate{Key: key, Value: effect, Observation: valueObservation}
}

func causalText(t *testing.T, value string) observation.Term {
	t.Helper()
	term, err := observation.NewTextTerm(value)
	if err != nil {
		t.Fatalf("observation.NewTextTerm(%q) error = %v", value, err)
	}
	return term
}

func causalEntity(t *testing.T, entityID string) observation.Term {
	t.Helper()
	term, err := observation.NewEntityTerm(entityID, "")
	if err != nil {
		t.Fatalf("observation.NewEntityTerm(%q) error = %v", entityID, err)
	}
	return term
}

func causalInstant(t *testing.T, day int) observation.TemporalExtent {
	t.Helper()
	extent, err := observation.AtTime(causalTime(day))
	if err != nil {
		t.Fatalf("observation.AtTime() error = %v", err)
	}
	return extent
}

func causalConfidence(t *testing.T, value float64) observation.Confidence {
	t.Helper()
	confidence, err := observation.NewUnitIntervalConfidence(value)
	if err != nil {
		t.Fatalf("observation.NewUnitIntervalConfidence() error = %v", err)
	}
	return confidence
}

func causalTime(day int) time.Time {
	return time.Date(2035, time.January, 1+day, 12, 0, 0, 0, time.UTC)
}

func causalRecordedAt(day int) time.Time {
	return time.Date(2035, time.February, 1+day, 12, 0, 0, 0, time.UTC)
}
