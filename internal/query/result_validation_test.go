package query

import (
	"strings"
	"testing"
	"time"

	"github.com/JakeFAU/stacks/core/evidence"
	"github.com/JakeFAU/stacks/core/identity"
	"github.com/JakeFAU/stacks/core/observation"
	"github.com/JakeFAU/stacks/core/temporal"
)

func TestPayloadConstructorsRejectNoncanonicalNestedStateTerms(t *testing.T) {
	point := mustPoint(t, "point", 2026, time.January, 1)
	before := mustWindow(t, "before", 2026, time.January, 1)
	after := mustWindow(t, "after", 2026, time.February, 1)
	key := mustStateKey(t, "entity-a", "predicate-a")
	groundedEntity := mustGroundedEntityTerm(t, "entity-a", "mention-a")

	pointFact := resultValidationFact(t, key, mustText(t, "point-value"), "point")
	pointFact.Key = temporal.StateKey{Subject: groundedEntity, Predicate: key.Predicate}

	trendFact := resultValidationFact(t, key, groundedEntity, "trend")

	trajectoryFact := resultValidationFact(t, key, mustText(t, "trajectory-value"), "trajectory")
	trajectoryFact.Key.Predicate = ""

	causalContribution := resultValidationContribution(t, "causal")
	tests := []struct {
		name      string
		construct func() error
	}{
		{
			name: "point grounded state key",
			construct: func() error {
				_, err := NewPointPayload(PointInTimeResult{
					Selection:  point,
					Facts:      []Fact{pointFact},
					Unresolved: []UnresolvedItem{},
				})
				return err
			},
		},
		{
			name: "trend grounded state value",
			construct: func() error {
				_, err := NewTrendPayload(TrendResult{
					Before:         WindowResult{Selection: before, Facts: []Fact{trendFact}, Unresolved: []UnresolvedItem{}},
					After:          WindowResult{Selection: after, Facts: []Fact{}, Unresolved: []UnresolvedItem{}},
					Changes:        []Change{{Kind: temporal.ChangeRemoved, Key: key, Before: &trendFact}},
					UnresolvedKeys: []temporal.StateKey{},
				})
				return err
			},
		},
		{
			name: "trajectory blank transition key",
			construct: func() error {
				_, err := NewTrajectoryPayload(TrajectoryResult{
					Selection: before,
					Transitions: []Transition{{
						Kind:       temporal.ChangeAdded,
						Key:        trajectoryFact.Key,
						ValidTime:  mustInstant(t),
						After:      &trajectoryFact,
						Unresolved: []UnresolvedItem{},
					}},
				})
				return err
			},
		},
		{
			name: "causal grounded cause",
			construct: func() error {
				_, err := NewCausalPayload(CausalChainResult{
					Selection: before,
					Links: []CausalLink{{
						Cause:                  groundedEntity,
						Effect:                 mustText(t, "effect"),
						Contributions:          []Contribution{causalContribution},
						SupportingCitations:    []Citation{validCitation("causal-evidence")},
						ContradictingCitations: []Citation{},
					}},
				})
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.construct(); err == nil {
				t.Fatal("payload constructor error = nil, want invalid canonical state error")
			}
		})
	}
}

func TestPayloadConstructorsRejectInvalidNestedReasonsStatusesAndProvenance(t *testing.T) {
	point := mustPoint(t, "point", 2026, time.January, 1)
	key := mustStateKey(t, "entity-a", "predicate-a")

	tests := []struct {
		name  string
		value PointInTimeResult
	}{
		{
			name: "unknown unresolved reason",
			value: PointInTimeResult{
				Selection: point,
				Facts:     []Fact{},
				Unresolved: []UnresolvedItem{{
					Key:        key,
					Reason:     temporal.UnresolvedReason("unknown"),
					Candidates: []Fact{resultValidationFact(t, key, mustText(t, "candidate"), "unknown-reason")},
				}},
			},
		},
		{
			name:  "missing contributions",
			value: pointResultWithContribution(t, point, key, []Contribution{}),
		},
		{
			name: "blank observation ID",
			value: pointResultWithContribution(t, point, key, []Contribution{
				mutateResultValidationContribution(t, "blank-id", func(value *Contribution) {
					value.ObservationID = ""
				}),
			}),
		},
		{
			name: "unknown epistemic status",
			value: pointResultWithContribution(t, point, key, []Contribution{
				mutateResultValidationContribution(t, "unknown-status", func(value *Contribution) {
					value.Status = observation.EpistemicStatus("unknown")
				}),
			}),
		},
		{
			name: "zero recorded time",
			value: pointResultWithContribution(t, point, key, []Contribution{
				mutateResultValidationContribution(t, "zero-recorded", func(value *Contribution) {
					value.RecordedAt = time.Time{}
				}),
			}),
		},
		{
			name: "blank derivation method",
			value: pointResultWithContribution(t, point, key, []Contribution{
				mutateResultValidationContribution(t, "blank-method", func(value *Contribution) {
					value.Derivation.Method = ""
				}),
			}),
		},
		{
			name: "model without prompt version",
			value: pointResultWithContribution(t, point, key, []Contribution{
				mutateResultValidationContribution(t, "partial-model", func(value *Contribution) {
					value.Derivation.Model = "model"
				}),
			}),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewPointPayload(test.value); err == nil {
				t.Fatal("NewPointPayload() error = nil, want invalid nested provenance error")
			}
		})
	}
}

func TestPayloadConstructorsRejectInvalidContributionsAcrossEveryIntent(t *testing.T) {
	point := mustPoint(t, "point", 2026, time.January, 1)
	before := mustWindow(t, "before", 2026, time.January, 1)
	after := mustWindow(t, "after", 2026, time.February, 1)
	key := mustStateKey(t, "entity-a", "predicate-a")
	invalid := resultValidationFact(t, key, mustText(t, "invalid"), "invalid")
	invalid.Contributions[0].RecordedAt = time.Time{}
	valid := resultValidationFact(t, key, mustText(t, "valid"), "valid")

	tests := []struct {
		name      string
		construct func() error
	}{
		{
			name: "point fact",
			construct: func() error {
				_, err := NewPointPayload(PointInTimeResult{
					Selection:  point,
					Facts:      []Fact{invalid},
					Unresolved: []UnresolvedItem{},
				})
				return err
			},
		},
		{
			name: "trend fact and change",
			construct: func() error {
				_, err := NewTrendPayload(TrendResult{
					Before:         WindowResult{Selection: before, Facts: []Fact{invalid}, Unresolved: []UnresolvedItem{}},
					After:          WindowResult{Selection: after, Facts: []Fact{}, Unresolved: []UnresolvedItem{}},
					Changes:        []Change{{Kind: temporal.ChangeRemoved, Key: key, Before: &invalid}},
					UnresolvedKeys: []temporal.StateKey{},
				})
				return err
			},
		},
		{
			name: "trajectory unresolved candidate",
			construct: func() error {
				_, err := NewTrajectoryPayload(TrajectoryResult{
					Selection: before,
					Transitions: []Transition{{
						Kind:      temporal.ChangeAdded,
						Key:       key,
						ValidTime: mustInstant(t),
						After:     &valid,
						Unresolved: []UnresolvedItem{{
							Key:        key,
							Reason:     temporal.UnresolvedHypothesis,
							Candidates: []Fact{invalid},
						}},
					}},
				})
				return err
			},
		},
		{
			name: "causal link",
			construct: func() error {
				_, err := NewCausalPayload(CausalChainResult{
					Selection: before,
					Links: []CausalLink{{
						Cause:                  mustText(t, "cause"),
						Effect:                 mustText(t, "effect"),
						Contributions:          invalid.Contributions,
						SupportingCitations:    []Citation{validCitation("causal-evidence")},
						ContradictingCitations: []Citation{},
					}},
				})
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.construct(); err == nil {
				t.Fatal("payload constructor error = nil, want invalid nested contribution error")
			}
		})
	}
}

func TestPayloadConstructorsRejectIncoherentOrDuplicateNestedMaterial(t *testing.T) {
	point := mustPoint(t, "point", 2026, time.January, 1)
	key := mustStateKey(t, "entity-a", "predicate-a")
	otherKey := mustStateKey(t, "entity-b", "predicate-a")
	first := resultValidationFact(t, key, mustText(t, "first"), "first")
	second := resultValidationFact(t, key, mustText(t, "second"), "second")

	duplicateContribution := resultValidationContribution(t, "duplicate-contribution")
	factWithDuplicateContributions := resultValidationFact(t, key, mustText(t, "duplicate-contributions"), "ignored")
	factWithDuplicateContributions.Contributions = []Contribution{duplicateContribution, duplicateContribution}

	duplicateCitation := validCitation("duplicate-evidence")
	factWithDuplicateCitations := resultValidationFact(t, key, mustText(t, "duplicate-citations"), "duplicate-citations")
	factWithDuplicateCitations.SupportingCitations = []Citation{duplicateCitation, duplicateCitation}

	tests := []struct {
		name  string
		value PointInTimeResult
	}{
		{
			name:  "duplicate resolved keys",
			value: PointInTimeResult{Selection: point, Facts: []Fact{first, second}, Unresolved: []UnresolvedItem{}},
		},
		{
			name: "resolved and unresolved key overlap",
			value: PointInTimeResult{
				Selection: point,
				Facts:     []Fact{first},
				Unresolved: []UnresolvedItem{{
					Key:        key,
					Reason:     temporal.UnresolvedHypothesis,
					Candidates: []Fact{second},
				}},
			},
		},
		{
			name: "duplicate unresolved keys",
			value: PointInTimeResult{
				Selection: point,
				Facts:     []Fact{},
				Unresolved: []UnresolvedItem{
					{Key: key, Reason: temporal.UnresolvedHypothesis, Candidates: []Fact{first}},
					{Key: key, Reason: temporal.UnresolvedCounterevidenceOnly, Candidates: []Fact{second}},
				},
			},
		},
		{
			name: "unresolved candidate key mismatch",
			value: PointInTimeResult{
				Selection: point,
				Facts:     []Fact{},
				Unresolved: []UnresolvedItem{{
					Key:        otherKey,
					Reason:     temporal.UnresolvedHypothesis,
					Candidates: []Fact{first},
				}},
			},
		},
		{
			name: "duplicate unresolved candidate values",
			value: PointInTimeResult{
				Selection: point,
				Facts:     []Fact{},
				Unresolved: []UnresolvedItem{{
					Key:        key,
					Reason:     temporal.UnresolvedConflict,
					Candidates: []Fact{first, first},
				}},
			},
		},
		{
			name:  "duplicate contribution observation IDs",
			value: PointInTimeResult{Selection: point, Facts: []Fact{factWithDuplicateContributions}, Unresolved: []UnresolvedItem{}},
		},
		{
			name:  "duplicate citation evidence IDs in one role",
			value: PointInTimeResult{Selection: point, Facts: []Fact{factWithDuplicateCitations}, Unresolved: []UnresolvedItem{}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewPointPayload(test.value); err == nil {
				t.Fatal("NewPointPayload() error = nil, want incoherent or duplicate nested material error")
			}
		})
	}
}

func TestTrendPayloadRequiresChangesAndUnresolvedKeysToMatchWindows(t *testing.T) {
	beforeSelection := mustWindow(t, "before", 2026, time.January, 1)
	afterSelection := mustWindow(t, "after", 2026, time.February, 1)
	key := mustStateKey(t, "entity-a", "predicate-a")
	otherKey := mustStateKey(t, "entity-b", "predicate-a")
	beforeFact := resultValidationFact(t, key, mustText(t, "before"), "before")
	afterFact := resultValidationFact(t, key, mustText(t, "after"), "after")
	differentAfterProvenance := resultValidationFact(t, key, mustText(t, "after"), "different-after-provenance")
	unresolved := UnresolvedItem{
		Key:        key,
		Reason:     temporal.UnresolvedHypothesis,
		Candidates: []Fact{beforeFact},
	}

	tests := []struct {
		name  string
		value TrendResult
	}{
		{
			name: "change key differs from nested facts",
			value: TrendResult{
				Before:         WindowResult{Selection: beforeSelection, Facts: []Fact{beforeFact}, Unresolved: []UnresolvedItem{}},
				After:          WindowResult{Selection: afterSelection, Facts: []Fact{afterFact}, Unresolved: []UnresolvedItem{}},
				Changes:        []Change{{Kind: temporal.ChangeChanged, Key: otherKey, Before: &beforeFact, After: &afterFact}},
				UnresolvedKeys: []temporal.StateKey{},
			},
		},
		{
			name: "changed values are equal",
			value: TrendResult{
				Before:         WindowResult{Selection: beforeSelection, Facts: []Fact{beforeFact}, Unresolved: []UnresolvedItem{}},
				After:          WindowResult{Selection: afterSelection, Facts: []Fact{beforeFact}, Unresolved: []UnresolvedItem{}},
				Changes:        []Change{{Kind: temporal.ChangeChanged, Key: key, Before: &beforeFact, After: &beforeFact}},
				UnresolvedKeys: []temporal.StateKey{},
			},
		},
		{
			name: "changed windows omit required change",
			value: TrendResult{
				Before:         WindowResult{Selection: beforeSelection, Facts: []Fact{beforeFact}, Unresolved: []UnresolvedItem{}},
				After:          WindowResult{Selection: afterSelection, Facts: []Fact{afterFact}, Unresolved: []UnresolvedItem{}},
				Changes:        []Change{},
				UnresolvedKeys: []temporal.StateKey{},
			},
		},
		{
			name: "change facts do not match window facts",
			value: TrendResult{
				Before:         WindowResult{Selection: beforeSelection, Facts: []Fact{beforeFact}, Unresolved: []UnresolvedItem{}},
				After:          WindowResult{Selection: afterSelection, Facts: []Fact{afterFact}, Unresolved: []UnresolvedItem{}},
				Changes:        []Change{{Kind: temporal.ChangeChanged, Key: key, Before: &beforeFact, After: &differentAfterProvenance}},
				UnresolvedKeys: []temporal.StateKey{},
			},
		},
		{
			name: "unresolved window omits unresolved key",
			value: TrendResult{
				Before:         WindowResult{Selection: beforeSelection, Facts: []Fact{}, Unresolved: []UnresolvedItem{unresolved}},
				After:          WindowResult{Selection: afterSelection, Facts: []Fact{}, Unresolved: []UnresolvedItem{}},
				Changes:        []Change{},
				UnresolvedKeys: []temporal.StateKey{},
			},
		},
		{
			name: "duplicate unresolved keys",
			value: TrendResult{
				Before:         WindowResult{Selection: beforeSelection, Facts: []Fact{}, Unresolved: []UnresolvedItem{unresolved}},
				After:          WindowResult{Selection: afterSelection, Facts: []Fact{}, Unresolved: []UnresolvedItem{unresolved}},
				Changes:        []Change{},
				UnresolvedKeys: []temporal.StateKey{key, key},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewTrendPayload(test.value); err == nil {
				t.Fatal("NewTrendPayload() error = nil, want incoherent trend relationship error")
			}
		})
	}
}

func TestTrajectoryPayloadRequiresTransitionFactsToMatchTransition(t *testing.T) {
	window := mustWindow(t, "window", 2026, time.January, 1)
	key := mustStateKey(t, "entity-a", "predicate-a")
	otherKey := mustStateKey(t, "entity-b", "predicate-a")
	before := resultValidationFact(t, key, mustText(t, "before"), "before")
	after := resultValidationFact(t, key, mustText(t, "after"), "after")
	other := resultValidationFact(t, otherKey, mustText(t, "other"), "other")

	tests := []struct {
		name       string
		transition Transition
	}{
		{
			name:       "transition key differs from added fact",
			transition: Transition{Kind: temporal.ChangeAdded, Key: key, ValidTime: mustInstant(t), After: &other, Unresolved: []UnresolvedItem{}},
		},
		{
			name:       "changed transition values are equal",
			transition: Transition{Kind: temporal.ChangeChanged, Key: key, ValidTime: mustInstant(t), Before: &before, After: &before, Unresolved: []UnresolvedItem{}},
		},
		{
			name:       "changed transition fact key differs",
			transition: Transition{Kind: temporal.ChangeChanged, Key: key, ValidTime: mustInstant(t), Before: &before, After: &other, Unresolved: []UnresolvedItem{}},
		},
		{
			name:       "changed transition before and after keys differ",
			transition: Transition{Kind: temporal.ChangeChanged, Key: otherKey, ValidTime: mustInstant(t), Before: &other, After: &after, Unresolved: []UnresolvedItem{}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewTrajectoryPayload(TrajectoryResult{
				Selection:   window,
				Transitions: []Transition{test.transition},
			}); err == nil {
				t.Fatal("NewTrajectoryPayload() error = nil, want incoherent transition relationship error")
			}
		})
	}
}

func TestTrajectoryPayloadRequiresTransitionsAtUniqueSelectedBoundaries(t *testing.T) {
	start := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	window, err := temporal.Between("window", start, end)
	if err != nil {
		t.Fatalf("temporal.Between() error = %v", err)
	}
	key := mustStateKey(t, "entity-a", "predicate-a")
	otherKey := mustStateKey(t, "entity-a", "predicate-b")
	after := resultValidationFact(t, key, mustText(t, "after"), "selected-boundary-after")
	before := resultValidationFact(t, key, mustText(t, "before"), "selected-boundary-before")
	other := resultValidationFact(t, otherKey, mustText(t, "other"), "selected-boundary-other")

	at := func(value time.Time) observation.TemporalExtent {
		t.Helper()
		extent, err := observation.AtTime(value)
		if err != nil {
			t.Fatalf("observation.AtTime() error = %v", err)
		}
		return extent
	}
	during, err := observation.During(start, start.Add(time.Hour))
	if err != nil {
		t.Fatalf("observation.During() error = %v", err)
	}
	uncertain, err := observation.Within(start, start.Add(time.Hour))
	if err != nil {
		t.Fatalf("observation.Within() error = %v", err)
	}

	tests := []struct {
		name        string
		transitions []Transition
	}{
		{
			name: "transition unresolved key differs from transition key",
			transitions: []Transition{{
				Kind:      temporal.ChangeAdded,
				Key:       key,
				ValidTime: at(start),
				After:     &after,
				Unresolved: []UnresolvedItem{{
					Key:        otherKey,
					Reason:     temporal.UnresolvedHypothesis,
					Candidates: []Fact{other},
				}},
			}},
		},
		{
			name: "duplicate key and boundary",
			transitions: []Transition{
				{Kind: temporal.ChangeAdded, Key: key, ValidTime: at(start), After: &after, Unresolved: []UnresolvedItem{}},
				{Kind: temporal.ChangeRemoved, Key: key, ValidTime: at(start), Before: &before, Unresolved: []UnresolvedItem{}},
			},
		},
		{
			name:        "boundary before selected start",
			transitions: []Transition{{Kind: temporal.ChangeAdded, Key: key, ValidTime: at(start.Add(-time.Microsecond)), After: &after, Unresolved: []UnresolvedItem{}}},
		},
		{
			name:        "boundary at selected end",
			transitions: []Transition{{Kind: temporal.ChangeAdded, Key: key, ValidTime: at(end), After: &after, Unresolved: []UnresolvedItem{}}},
		},
		{
			name:        "boundary after selected end",
			transitions: []Transition{{Kind: temporal.ChangeAdded, Key: key, ValidTime: at(end.Add(time.Microsecond)), After: &after, Unresolved: []UnresolvedItem{}}},
		},
		{
			name:        "interval is not a transition boundary",
			transitions: []Transition{{Kind: temporal.ChangeAdded, Key: key, ValidTime: during, After: &after, Unresolved: []UnresolvedItem{}}},
		},
		{
			name:        "uncertainty window is not a transition boundary",
			transitions: []Transition{{Kind: temporal.ChangeAdded, Key: key, ValidTime: uncertain, After: &after, Unresolved: []UnresolvedItem{}}},
		},
		{
			name:        "unknown time is not a transition boundary",
			transitions: []Transition{{Kind: temporal.ChangeAdded, Key: key, ValidTime: observation.UnknownTime(), After: &after, Unresolved: []UnresolvedItem{}}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewTrajectoryPayload(TrajectoryResult{
				Selection:   window,
				Transitions: test.transitions,
			}); err == nil {
				t.Fatal("NewTrajectoryPayload() error = nil, want invalid transition boundary relationship error")
			}
		})
	}

	if _, err := NewTrajectoryPayload(TrajectoryResult{
		Selection: window,
		Transitions: []Transition{{
			Kind:       temporal.ChangeAdded,
			Key:        key,
			ValidTime:  at(start),
			After:      &after,
			Unresolved: []UnresolvedItem{},
		}},
	}); err != nil {
		t.Fatalf("NewTrajectoryPayload() selected-start transition error = %v", err)
	}
}

func TestValidateResultEnforcesChronologyPayloadLimits(t *testing.T) {
	window := mustWindow(t, "window", 2026, time.January, 1)
	firstKey := mustStateKey(t, "entity-a", "predicate-a")
	secondKey := mustStateKey(t, "entity-a", "predicate-b")
	first := resultValidationFact(t, firstKey, mustText(t, "first"), "limit-first")
	second := resultValidationFact(t, secondKey, mustText(t, "second"), "limit-second")
	trajectoryPayload, err := NewTrajectoryPayload(TrajectoryResult{
		Selection: window,
		Transitions: []Transition{
			{Kind: temporal.ChangeAdded, Key: firstKey, ValidTime: mustInstant(t), After: &first, Unresolved: []UnresolvedItem{}},
			{Kind: temporal.ChangeAdded, Key: secondKey, ValidTime: mustInstant(t), After: &second, Unresolved: []UnresolvedItem{}},
		},
	})
	if err != nil {
		t.Fatalf("NewTrajectoryPayload() error = %v", err)
	}
	causalPayload, err := NewCausalPayload(CausalChainResult{
		Selection: window,
		Links: []CausalLink{
			{
				Cause:                  mustText(t, "first-cause"),
				Effect:                 mustText(t, "first-effect"),
				Contributions:          []Contribution{resultValidationContribution(t, "limit-causal-first")},
				SupportingCitations:    []Citation{validCitation("limit-causal-first")},
				ContradictingCitations: []Citation{},
			},
			{
				Cause:                  mustText(t, "second-cause"),
				Effect:                 mustText(t, "second-effect"),
				Contributions:          []Contribution{resultValidationContribution(t, "limit-causal-second")},
				SupportingCitations:    []Citation{validCitation("limit-causal-second")},
				ContradictingCitations: []Citation{},
			},
		},
	})
	if err != nil {
		t.Fatalf("NewCausalPayload() error = %v", err)
	}

	tests := []struct {
		name       string
		intent     temporal.Intent
		predicates []observation.Predicate
		payload    IntentPayload
	}{
		{
			name:       "trajectory",
			intent:     temporal.IntentTrajectory,
			predicates: []observation.Predicate{},
			payload:    trajectoryPayload,
		},
		{
			name:       "causal",
			intent:     temporal.IntentCausalChain,
			predicates: []observation.Predicate{CausalPredicate},
			payload:    causalPayload,
		},
	}
	for _, test := range tests {
		t.Run(test.name+" rejects overflow", func(t *testing.T) {
			result := resultValidationEnvelope(
				test.intent,
				test.predicates,
				[]temporal.TemporalSelection{window},
				1,
				test.payload,
			)
			if err := ValidateResult(result); err == nil {
				t.Fatal("ValidateResult() error = nil, want chronology limit association error")
			}
		})
		t.Run(test.name+" accepts exact limit", func(t *testing.T) {
			result := resultValidationEnvelope(
				test.intent,
				test.predicates,
				[]temporal.TemporalSelection{window},
				2,
				test.payload,
			)
			if err := ValidateResult(result); err != nil {
				t.Fatalf("ValidateResult() exact-limit error = %v", err)
			}
		})
	}
}

func TestTrajectoryPayloadRejectsInvalidNestedUnresolvedMaterial(t *testing.T) {
	window := mustWindow(t, "window", 2026, time.January, 1)
	key := mustStateKey(t, "entity-a", "predicate-a")
	otherKey := mustStateKey(t, "entity-b", "predicate-a")
	after := resultValidationFact(t, key, mustText(t, "after"), "trajectory-after")
	other := resultValidationFact(t, otherKey, mustText(t, "other"), "trajectory-other")

	tests := []struct {
		name       string
		unresolved UnresolvedItem
	}{
		{
			name: "unknown unresolved reason",
			unresolved: UnresolvedItem{
				Key:        otherKey,
				Reason:     temporal.UnresolvedReason("unknown"),
				Candidates: []Fact{other},
			},
		},
		{
			name: "candidate key differs from unresolved key",
			unresolved: UnresolvedItem{
				Key:        otherKey,
				Reason:     temporal.UnresolvedHypothesis,
				Candidates: []Fact{after},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewTrajectoryPayload(TrajectoryResult{
				Selection: window,
				Transitions: []Transition{{
					Kind:       temporal.ChangeAdded,
					Key:        key,
					ValidTime:  mustInstant(t),
					After:      &after,
					Unresolved: []UnresolvedItem{test.unresolved},
				}},
			}); err == nil {
				t.Fatal("NewTrajectoryPayload() error = nil, want invalid nested unresolved material error")
			}
		})
	}
}

func TestTrajectoryPayloadRejectsInvalidTopLevelUnresolvedMaterial(t *testing.T) {
	window := mustWindow(t, "window", 2026, time.January, 1)
	key := mustStateKey(t, "entity-a", "predicate-a")
	otherKey := mustStateKey(t, "entity-b", "predicate-a")
	fact := resultValidationFact(t, key, mustText(t, "value"), "trajectory-top-level")

	tests := []struct {
		name       string
		unresolved UnresolvedItem
	}{
		{
			name: "unknown unresolved reason",
			unresolved: UnresolvedItem{
				Key:        key,
				Reason:     temporal.UnresolvedReason("unknown"),
				Candidates: []Fact{fact},
			},
		},
		{
			name: "candidate key differs from unresolved key",
			unresolved: UnresolvedItem{
				Key:        otherKey,
				Reason:     temporal.UnresolvedHypothesis,
				Candidates: []Fact{fact},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewTrajectoryPayload(TrajectoryResult{
				Selection:  window,
				Unresolved: []UnresolvedItem{test.unresolved},
			}); err == nil {
				t.Fatal("NewTrajectoryPayload() error = nil, want invalid top-level unresolved material error")
			}
		})
	}
}

func TestCausalPayloadRejectsInvalidNestedContributionAndCitationMaterial(t *testing.T) {
	window := mustWindow(t, "window", 2026, time.January, 1)
	tests := []struct {
		name   string
		mutate func(*CausalLink)
	}{
		{
			name: "unknown contribution status",
			mutate: func(link *CausalLink) {
				link.Contributions[0].Status = observation.EpistemicStatus("unknown")
			},
		},
		{
			name: "duplicate supporting citation",
			mutate: func(link *CausalLink) {
				link.SupportingCitations = append(link.SupportingCitations, link.SupportingCitations[0])
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			link := CausalLink{
				Cause:                  mustText(t, "cause"),
				Effect:                 mustText(t, "effect"),
				Contributions:          []Contribution{resultValidationContribution(t, "causal-nested")},
				SupportingCitations:    []Citation{validCitation("causal-nested-evidence")},
				ContradictingCitations: []Citation{},
			}
			test.mutate(&link)
			if _, err := NewCausalPayload(CausalChainResult{
				Selection: window,
				Links:     []CausalLink{link},
			}); err == nil {
				t.Fatal("NewCausalPayload() error = nil, want invalid nested causal material error")
			}
		})
	}
}

func TestValidateResultRevalidatesNestedPayload(t *testing.T) {
	point := mustPoint(t, "point", 2026, time.January, 1)
	key := mustStateKey(t, "entity-a", "predicate-a")
	payload, err := NewPointPayload(PointInTimeResult{
		Selection:  point,
		Facts:      []Fact{},
		Unresolved: []UnresolvedItem{},
	})
	if err != nil {
		t.Fatalf("NewPointPayload() error = %v", err)
	}
	invalidFact := resultValidationFact(t, key, mustGroundedEntityTerm(t, "entity-b", "mention-b"), "invalid")
	payload.point.Facts = []Fact{invalidFact}

	result := Result{
		Intent:         temporal.IntentPointInTime,
		EntityIDs:      []identity.EntityID{"entity-a"},
		EntityMatch:    EntityMatchAll,
		Predicates:     []observation.Predicate{},
		Selections:     []temporal.TemporalSelection{point},
		KnowledgeScope: temporal.CurrentKnowledge(),
		Payload:        payload,
		Gaps:           []Gap{},
	}
	if err := ValidateResult(result); err == nil {
		t.Fatal("ValidateResult() error = nil, want nested payload validation error")
	}
}

func TestValidateResultRevalidatesMutatedNestedPayloadForEveryNonPointIntent(t *testing.T) {
	before := mustWindow(t, "before", 2026, time.January, 1)
	after := mustWindow(t, "after", 2026, time.February, 1)
	key := mustStateKey(t, "entity-a", "predicate-a")

	tests := []struct {
		name  string
		build func(*testing.T) Result
	}{
		{
			name: "trend",
			build: func(t *testing.T) Result {
				beforeFact := resultValidationFact(t, key, mustText(t, "before"), "validate-trend-before")
				afterFact := resultValidationFact(t, key, mustText(t, "after"), "validate-trend-after")
				payload, err := NewTrendPayload(TrendResult{
					Before:         WindowResult{Selection: before, Facts: []Fact{beforeFact}, Unresolved: []UnresolvedItem{}},
					After:          WindowResult{Selection: after, Facts: []Fact{afterFact}, Unresolved: []UnresolvedItem{}},
					Changes:        []Change{{Kind: temporal.ChangeChanged, Key: key, Before: &beforeFact, After: &afterFact}},
					UnresolvedKeys: []temporal.StateKey{},
				})
				if err != nil {
					t.Fatalf("NewTrendPayload() error = %v", err)
				}
				payload.trend.Before.Facts[0].Contributions[0].RecordedAt = time.Time{}
				return resultValidationEnvelope(
					temporal.IntentTrendComparison,
					[]observation.Predicate{},
					[]temporal.TemporalSelection{before, after},
					0,
					payload,
				)
			},
		},
		{
			name: "trajectory",
			build: func(t *testing.T) Result {
				fact := resultValidationFact(t, key, mustText(t, "after"), "validate-trajectory")
				payload, err := NewTrajectoryPayload(TrajectoryResult{
					Selection: before,
					Transitions: []Transition{{
						Kind:       temporal.ChangeAdded,
						Key:        key,
						ValidTime:  mustInstant(t),
						After:      &fact,
						Unresolved: []UnresolvedItem{},
					}},
				})
				if err != nil {
					t.Fatalf("NewTrajectoryPayload() error = %v", err)
				}
				payload.trajectory.Unresolved = []UnresolvedItem{{
					Key:        key,
					Reason:     temporal.UnresolvedReason("unknown"),
					Candidates: []Fact{fact},
				}}
				return resultValidationEnvelope(
					temporal.IntentTrajectory,
					[]observation.Predicate{},
					[]temporal.TemporalSelection{before},
					1,
					payload,
				)
			},
		},
		{
			name: "causal",
			build: func(t *testing.T) Result {
				payload, err := NewCausalPayload(CausalChainResult{
					Selection: before,
					Links: []CausalLink{{
						Cause:                  mustText(t, "cause"),
						Effect:                 mustText(t, "effect"),
						Contributions:          []Contribution{resultValidationContribution(t, "validate-causal")},
						SupportingCitations:    []Citation{validCitation("validate-causal-evidence")},
						ContradictingCitations: []Citation{},
					}},
				})
				if err != nil {
					t.Fatalf("NewCausalPayload() error = %v", err)
				}
				payload.causal.Links[0].Contributions[0].Status = observation.EpistemicStatus("unknown")
				return resultValidationEnvelope(
					temporal.IntentCausalChain,
					[]observation.Predicate{CausalPredicate},
					[]temporal.TemporalSelection{before},
					1,
					payload,
				)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateResult(test.build(t)); err == nil {
				t.Fatal("ValidateResult() error = nil, want mutated nested payload validation error")
			}
		})
	}
}

func TestValidateResultRejectsDuplicateGaps(t *testing.T) {
	point := mustPoint(t, "point", 2026, time.January, 1)
	payload, err := NewPointPayload(PointInTimeResult{
		Selection:  point,
		Facts:      []Fact{},
		Unresolved: []UnresolvedItem{},
	})
	if err != nil {
		t.Fatalf("NewPointPayload() error = %v", err)
	}
	duplicateGap := Gap{Kind: GapNoEvidence, EntityID: "entity-a"}
	result := Result{
		Intent:         temporal.IntentPointInTime,
		EntityIDs:      []identity.EntityID{"entity-a"},
		EntityMatch:    EntityMatchAll,
		Predicates:     []observation.Predicate{},
		Selections:     []temporal.TemporalSelection{point},
		KnowledgeScope: temporal.CurrentKnowledge(),
		Payload:        payload,
		Gaps:           []Gap{duplicateGap, duplicateGap},
	}
	if err := ValidateResult(result); err == nil {
		t.Fatal("ValidateResult() error = nil, want duplicate gap error")
	}
}

func TestValidateResultRejectsUnauthorizedGapContexts(t *testing.T) {
	privateEntity := identity.EntityID("private-entity")
	privatePredicate := observation.Predicate("private-predicate")
	privateLabel := "private-label"
	point := mustPoint(t, "point", 2026, time.January, 1)
	before := mustWindow(t, "before", 2026, time.January, 1)
	after := mustWindow(t, "after", 2026, time.February, 1)

	tests := []struct {
		name   string
		result Result
		gap    Gap
	}{
		{
			name: "entity is not requested",
			result: resultValidationEnvelope(
				temporal.IntentPointInTime,
				[]observation.Predicate{},
				[]temporal.TemporalSelection{point},
				0,
				mustPointPayload(t, point),
			),
			gap: Gap{Kind: GapNoEvidence, EntityID: privateEntity},
		},
		{
			name: "selection label is not requested",
			result: resultValidationEnvelope(
				temporal.IntentTrendComparison,
				[]observation.Predicate{},
				[]temporal.TemporalSelection{before, after},
				0,
				mustTrendPayload(t, before, after),
			),
			gap: Gap{Kind: GapValidTimeExcluded, SelectionLabel: privateLabel},
		},
		{
			name: "predicate is invalid",
			result: resultValidationEnvelope(
				temporal.IntentTrajectory,
				[]observation.Predicate{},
				[]temporal.TemporalSelection{before},
				1,
				mustTrajectoryPayload(t, before),
			),
			gap: Gap{Kind: GapNoEvidence, Predicate: " "},
		},
		{
			name: "predicate is not requested by explicit filter",
			result: resultValidationEnvelope(
				temporal.IntentPointInTime,
				[]observation.Predicate{"requested-predicate"},
				[]temporal.TemporalSelection{point},
				0,
				mustPointPayload(t, point),
			),
			gap: Gap{Kind: GapNoEvidence, Predicate: privatePredicate},
		},
		{
			name: "no causal evidence is not a point gap",
			result: resultValidationEnvelope(
				temporal.IntentPointInTime,
				[]observation.Predicate{},
				[]temporal.TemporalSelection{point},
				0,
				mustPointPayload(t, point),
			),
			gap: Gap{Kind: GapNoCausalEvidence},
		},
		{
			name: "no causal evidence predicate is not the causal predicate",
			result: resultValidationEnvelope(
				temporal.IntentCausalChain,
				[]observation.Predicate{CausalPredicate},
				[]temporal.TemporalSelection{before},
				1,
				mustCausalPayload(t, before),
			),
			gap: Gap{Kind: GapNoCausalEvidence, Predicate: privatePredicate},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.result.Gaps = []Gap{test.gap}
			err := ValidateResult(test.result)
			if err == nil {
				t.Fatal("ValidateResult() error = nil, want unauthorized gap context error")
			}
			for _, private := range []string{string(privateEntity), string(privatePredicate), privateLabel} {
				if strings.Contains(err.Error(), private) {
					t.Fatalf("ValidateResult() error exposed private gap context: %q", err)
				}
			}
		})
	}
}

func TestValidateResultAllowsAuthorizedAndContextualGapPredicates(t *testing.T) {
	point := mustPoint(t, "point", 2026, time.January, 1)
	tests := []Result{
		func() Result {
			result := resultValidationEnvelope(
				temporal.IntentPointInTime,
				[]observation.Predicate{"requested-predicate"},
				[]temporal.TemporalSelection{point},
				0,
				mustPointPayload(t, point),
			)
			result.Gaps = []Gap{{
				Kind:           GapValidTimeExcluded,
				EntityID:       "entity-a",
				Predicate:      "requested-predicate",
				SelectionLabel: "point",
			}}
			return result
		}(),
		func() Result {
			result := resultValidationEnvelope(
				temporal.IntentPointInTime,
				[]observation.Predicate{},
				[]temporal.TemporalSelection{point},
				0,
				mustPointPayload(t, point),
			)
			result.Gaps = []Gap{{Kind: GapNoEvidence, Predicate: "contextual-predicate"}}
			return result
		}(),
	}

	for _, result := range tests {
		if err := ValidateResult(result); err != nil {
			t.Fatalf("ValidateResult() authorized gap error = %v", err)
		}
	}
}

func resultValidationEnvelope(
	intent temporal.Intent,
	predicates []observation.Predicate,
	selections []temporal.TemporalSelection,
	limit int,
	payload IntentPayload,
) Result {
	return Result{
		Intent:         intent,
		EntityIDs:      []identity.EntityID{"entity-a"},
		EntityMatch:    EntityMatchAll,
		Predicates:     predicates,
		Selections:     selections,
		KnowledgeScope: temporal.CurrentKnowledge(),
		Limit:          limit,
		Payload:        payload,
		Gaps:           []Gap{},
	}
}

func pointResultWithContribution(
	t *testing.T,
	selection temporal.TemporalSelection,
	key temporal.StateKey,
	contributions []Contribution,
) PointInTimeResult {
	t.Helper()
	fact := resultValidationFact(t, key, mustText(t, "value"), "point")
	fact.Contributions = contributions
	return PointInTimeResult{Selection: selection, Facts: []Fact{fact}, Unresolved: []UnresolvedItem{}}
}

func resultValidationFact(
	t *testing.T,
	key temporal.StateKey,
	value observation.Term,
	suffix string,
) Fact {
	t.Helper()
	return Fact{
		Key:                    key,
		Value:                  value,
		Contributions:          []Contribution{resultValidationContribution(t, suffix)},
		SupportingCitations:    []Citation{validCitation(evidence.EvidenceID("evidence-" + suffix))},
		ContradictingCitations: []Citation{},
	}
}

func resultValidationContribution(t *testing.T, suffix string) Contribution {
	t.Helper()
	return Contribution{
		ObservationID: observation.ObservationID("observation-" + suffix),
		Status:        observation.StatusObserved,
		ValidTime:     mustInstant(t),
		RecordedAt:    time.Date(2026, time.January, 1, 0, 0, 0, 123456000, time.UTC),
		Derivation:    observation.Derivation{Method: "synthetic", Version: "v1"},
	}
}

func mutateResultValidationContribution(
	t *testing.T,
	suffix string,
	mutate func(*Contribution),
) Contribution {
	t.Helper()
	value := resultValidationContribution(t, suffix)
	mutate(&value)
	return value
}

func mustGroundedEntityTerm(t *testing.T, entityID, mentionID string) observation.Term {
	t.Helper()
	value, err := observation.NewEntityTerm(entityID, mentionID)
	if err != nil {
		t.Fatalf("observation.NewEntityTerm() error = %v", err)
	}
	return value
}
