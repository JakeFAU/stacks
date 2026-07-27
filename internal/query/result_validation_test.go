package query

import (
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
