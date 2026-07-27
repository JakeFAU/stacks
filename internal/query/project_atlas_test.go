package query

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/JakeFAU/stacks/core/evidence"
	"github.com/JakeFAU/stacks/core/identity"
	"github.com/JakeFAU/stacks/core/observation"
	"github.com/JakeFAU/stacks/core/temporal"
)

func TestProjectAtlasTrendContractPreservesCitedTemporalEvidence(t *testing.T) {
	fixture := newProjectAtlasFixture(t)
	reader := &projectAtlasReader{
		current:    fixture.currentSnapshot,
		historical: fixture.historicalSnapshot,
		cutoff:     fixture.cutoff,
	}
	service := Service{Reader: reader, Limits: Limits{MaxEntities: 4, MaxPredicates: 8, MaxChronology: 20}}

	current, err := service.Query(context.Background(), fixture.currentRequest)
	if err != nil {
		t.Fatalf("current Query() error = %v", err)
	}
	if err := ValidateResult(current); err != nil {
		t.Fatalf("ValidateResult(current) error = %v", err)
	}
	wantPredicates := []observation.Predicate{
		"project.delivery_commitment",
		"project.delivery_risk",
		"project.priority",
		"project.responsible_party",
		"project.scope",
	}
	if current.KnowledgeScope.Kind() != temporal.KnowledgeCurrent ||
		!slices.Equal(current.EntityIDs, []identity.EntityID{"entity:project-atlas"}) ||
		!slices.Equal(current.Predicates, wantPredicates) {
		t.Fatalf("current normalized scope = entities %v predicates %v knowledge %v", current.EntityIDs, current.Predicates, current.KnowledgeScope.Kind())
	}
	if len(reader.selections) != 1 ||
		!slices.Equal(reader.selections[0].Predicates, wantPredicates) ||
		reader.selections[0].KnowledgeScope.Kind() != temporal.KnowledgeCurrent {
		t.Fatalf("current reader selection = %#v, want one normalized current selection", reader.selections)
	}

	currentTrend, ok := current.Payload.Trend()
	if !ok {
		t.Fatal("current Payload.Trend() = false")
	}
	if got := factPredicates(currentTrend.Before.Facts); !slices.Equal(got, []observation.Predicate{
		"project.delivery_commitment",
		"project.responsible_party",
	}) {
		t.Fatalf("current before fact order = %v", got)
	}
	if got := factPredicates(currentTrend.After.Facts); !slices.Equal(got, []observation.Predicate{
		"project.delivery_commitment",
		"project.responsible_party",
	}) {
		t.Fatalf("current after fact order = %v", got)
	}
	if got := unresolvedPredicates(currentTrend.Before.Unresolved); !slices.Equal(got, []observation.Predicate{
		"project.delivery_risk",
		"project.priority",
	}) {
		t.Fatalf("current before unresolved order = %v", got)
	}
	if got := unresolvedPredicates(currentTrend.After.Unresolved); !slices.Equal(got, []observation.Predicate{
		"project.delivery_risk",
		"project.scope",
	}) {
		t.Fatalf("current after unresolved order = %v", got)
	}
	if got := stateKeyPredicates(currentTrend.UnresolvedKeys); !slices.Equal(got, []observation.Predicate{
		"project.delivery_risk",
		"project.priority",
		"project.scope",
	}) {
		t.Fatalf("current unresolved key order = %v", got)
	}

	beforeCommitment := currentTrend.Before.Facts[0]
	afterCommitment := currentTrend.After.Facts[0]
	assertTextTerm(t, beforeCommitment.Value, "2032-06-15")
	assertTextTerm(t, afterCommitment.Value, "2032-07-01")
	if got := beforeCommitment.Contributions; len(got) != 1 ||
		got[0].ObservationID != "observation:atlas/initial-observed" ||
		got[0].Status != observation.StatusObserved {
		t.Fatalf("before commitment contributions = %#v", got)
	}
	if got := afterCommitment.Contributions; len(got) != 1 ||
		got[0].ObservationID != "observation:atlas/revised-observed" ||
		got[0].Status != observation.StatusObserved {
		t.Fatalf("after commitment contributions = %#v", got)
	}
	if !reflect.DeepEqual(beforeCommitment.SupportingCitations, []Citation{fixture.initialEvidence.citation(observation.EvidenceSupporting)}) ||
		!reflect.DeepEqual(beforeCommitment.ContradictingCitations, []Citation{fixture.revisionEvidence.citation(observation.EvidenceContradicting)}) {
		t.Fatalf("before commitment citations = supporting %#v contradicting %#v", beforeCommitment.SupportingCitations, beforeCommitment.ContradictingCitations)
	}
	if !reflect.DeepEqual(afterCommitment.SupportingCitations, []Citation{fixture.revisionEvidence.citation(observation.EvidenceSupporting)}) ||
		!reflect.DeepEqual(afterCommitment.ContradictingCitations, []Citation{fixture.initialEvidence.citation(observation.EvidenceContradicting)}) {
		t.Fatalf("after commitment citations = supporting %#v contradicting %#v", afterCommitment.SupportingCitations, afterCommitment.ContradictingCitations)
	}

	beforeOwner := currentTrend.Before.Facts[1]
	afterOwner := currentTrend.After.Facts[1]
	ownerKey, err := temporal.NewStateKey(
		atlasEntityTerm(t, "entity:project-atlas"),
		"project.responsible_party",
	)
	if err != nil {
		t.Fatalf("temporal.NewStateKey(responsibility) error = %v", err)
	}
	wantBeforeOwner := Fact{
		Key:   ownerKey,
		Value: atlasEntityTerm(t, "entity:delivery-unit-alpha"),
		Contributions: []Contribution{{
			ObservationID: "observation:atlas/owner-alpha",
			Status:        observation.StatusObserved,
			ValidTime: atlasInterval(
				t,
				time.Date(2032, time.January, 1, 0, 0, 0, 0, time.UTC),
				time.Date(2032, time.February, 1, 0, 0, 0, 0, time.UTC),
			),
			RecordedAt: time.Date(2032, time.January, 12, 10, 5, 0, 0, time.UTC),
			Derivation: observation.Derivation{
				Method:  "synthetic-acceptance",
				Version: "d1-v1",
				RunID:   "run:project-atlas",
			},
		}},
		SupportingCitations:    []Citation{fixture.ownerAlphaEvidence.citation(observation.EvidenceSupporting)},
		ContradictingCitations: []Citation{},
	}
	wantAfterOwner := Fact{
		Key:   ownerKey,
		Value: atlasEntityTerm(t, "entity:delivery-unit-beta"),
		Contributions: []Contribution{{
			ObservationID:            "observation:atlas/owner-beta",
			Status:                   observation.StatusObserved,
			ValidTime:                atlasSince(t, time.Date(2032, time.March, 1, 0, 0, 0, 0, time.UTC)),
			RecordedAt:               time.Date(2032, time.March, 3, 10, 5, 0, 0, time.UTC),
			Derivation:               observation.Derivation{Method: "synthetic-acceptance", Version: "d1-v1", RunID: "run:project-atlas"},
			ObjectGroundingMentionID: "mention:delivery-unit-beta",
		}},
		SupportingCitations:    []Citation{fixture.ownerBetaEvidence.citation(observation.EvidenceSupporting)},
		ContradictingCitations: []Citation{},
	}
	if !reflect.DeepEqual(beforeOwner, wantBeforeOwner) {
		t.Fatalf("before responsibility fact = %#v, want independent %#v", beforeOwner, wantBeforeOwner)
	}
	if !reflect.DeepEqual(afterOwner, wantAfterOwner) {
		t.Fatalf("after responsibility fact = %#v, want independent %#v", afterOwner, wantAfterOwner)
	}

	if got := changePredicates(currentTrend.Changes); !slices.Equal(got, []observation.Predicate{
		"project.delivery_commitment",
		"project.responsible_party",
	}) {
		t.Fatalf("current change order = %v", got)
	}
	for _, change := range currentTrend.Changes {
		if change.Kind != temporal.ChangeChanged || change.Before == nil || change.After == nil {
			t.Fatalf("current change = %#v, want a fully cited changed fact", change)
		}
	}
	if !reflect.DeepEqual(*currentTrend.Changes[0].Before, beforeCommitment) ||
		!reflect.DeepEqual(*currentTrend.Changes[0].After, afterCommitment) ||
		!reflect.DeepEqual(*currentTrend.Changes[1].Before, wantBeforeOwner) ||
		!reflect.DeepEqual(*currentTrend.Changes[1].After, wantAfterOwner) {
		t.Fatal("current changes did not retain the exact cited before/after facts")
	}

	beforeRisk := currentTrend.Before.Unresolved[0]
	beforePriority := currentTrend.Before.Unresolved[1]
	afterRisk := currentTrend.After.Unresolved[0]
	afterScope := currentTrend.After.Unresolved[1]
	if beforeRisk.Reason != temporal.UnresolvedTemporalUncertainty ||
		afterRisk.Reason != temporal.UnresolvedTemporalUncertainty ||
		afterScope.Reason != temporal.UnresolvedHypothesis {
		t.Fatalf("current uncertainty reasons = before-risk %q after-risk %q after-scope %q", beforeRisk.Reason, afterRisk.Reason, afterScope.Reason)
	}
	if beforePriority.Reason != temporal.UnresolvedConflict ||
		len(beforePriority.Candidates) != 2 {
		t.Fatalf("current priority = %#v, want both conflicting candidates", beforePriority)
	}
	if got := factTextValues(beforePriority.Candidates); !slices.Equal(got, []string{"critical", "routine"}) {
		t.Fatalf("priority candidates = %v, want stable value order without a confidence winner", got)
	}
	lowConfidence, _ := fixture.priorityRoutine.Confidence()
	highConfidence, _ := fixture.priorityCritical.Confidence()
	if lowConfidence.Value() >= highConfidence.Value() {
		t.Fatalf("synthetic confidence ordering = %v >= %v, fixture must oppose support ordering", lowConfidence.Value(), highConfidence.Value())
	}
	if got := factTextValues(afterRisk.Candidates); !slices.Equal(got, []string{"elevated", "watch"}) {
		t.Fatalf("after risk candidates = %v, want uncertainty-window and unknown-time evidence", got)
	}
	if len(afterScope.Candidates) != 1 ||
		len(afterScope.Candidates[0].SupportingCitations) != 1 ||
		len(afterScope.Candidates[0].Contributions) != 1 {
		t.Fatalf("after scope hypothesis lost cited provenance: %#v", afterScope)
	}

	wantCurrentGaps := []Gap{
		{Kind: GapUnresolvedMention, EntityID: "entity:project-atlas", Predicate: "project.partner"},
		{Kind: GapValidTimeExcluded, EntityID: "entity:project-atlas", Predicate: "project.delivery_commitment", SelectionLabel: "after-change"},
		{Kind: GapValidTimeExcluded, EntityID: "entity:project-atlas", Predicate: "project.priority", SelectionLabel: "after-change"},
		{Kind: GapValidTimeExcluded, EntityID: "entity:project-atlas", Predicate: "project.responsible_party", SelectionLabel: "after-change"},
		{Kind: GapValidTimeExcluded, EntityID: "entity:project-atlas", Predicate: "project.delivery_commitment", SelectionLabel: "before-change"},
		{Kind: GapValidTimeExcluded, EntityID: "entity:project-atlas", Predicate: "project.delivery_risk", SelectionLabel: "before-change"},
		{Kind: GapValidTimeExcluded, EntityID: "entity:project-atlas", Predicate: "project.responsible_party", SelectionLabel: "before-change"},
		{Kind: GapValidTimeExcluded, EntityID: "entity:project-atlas", Predicate: "project.scope", SelectionLabel: "before-change"},
	}
	if !reflect.DeepEqual(current.Gaps, wantCurrentGaps) {
		t.Fatalf("current gaps = %#v, want %#v", current.Gaps, wantCurrentGaps)
	}

	historicalRequest := fixture.currentRequest
	historicalRequest.KnowledgeScope = fixture.historicalScope
	historical, err := service.Query(context.Background(), historicalRequest)
	if err != nil {
		t.Fatalf("historical Query() error = %v", err)
	}
	if err := ValidateResult(historical); err != nil {
		t.Fatalf("ValidateResult(historical) error = %v", err)
	}
	historicalCutoff, hasHistoricalCutoff := historical.KnowledgeScope.AsOf()
	if !hasHistoricalCutoff || !historicalCutoff.Equal(fixture.cutoff) {
		t.Fatalf("historical knowledge scope = (%v, %v), want cutoff %v", historicalCutoff, hasHistoricalCutoff, fixture.cutoff)
	}
	if len(reader.selections) != 2 {
		t.Fatalf("reader selections = %d, want current and historical", len(reader.selections))
	}

	historicalTrend, ok := historical.Payload.Trend()
	if !ok {
		t.Fatal("historical Payload.Trend() = false")
	}
	if got := factPredicates(historicalTrend.Before.Facts); !slices.Equal(got, []observation.Predicate{"project.responsible_party"}) {
		t.Fatalf("historical before facts = %v", got)
	}
	if len(historicalTrend.After.Facts) != 0 {
		t.Fatalf("historical after facts = %#v, want none admitted by cutoff", historicalTrend.After.Facts)
	}
	if got := unresolvedPredicates(historicalTrend.Before.Unresolved); !slices.Equal(got, []observation.Predicate{
		"project.delivery_commitment",
		"project.delivery_risk",
		"project.priority",
	}) {
		t.Fatalf("historical before unresolved order = %v", got)
	}
	if historicalTrend.Before.Unresolved[0].Reason != temporal.UnresolvedHypothesis {
		t.Fatalf("historical initial commitment reason = %q, want hypothesis", historicalTrend.Before.Unresolved[0].Reason)
	}
	if len(historicalTrend.Changes) != 1 ||
		historicalTrend.Changes[0].Kind != temporal.ChangeRemoved ||
		historicalTrend.Changes[0].Key.Predicate != "project.responsible_party" ||
		historicalTrend.Changes[0].Before == nil ||
		historicalTrend.Changes[0].After != nil {
		t.Fatalf("historical changes = %#v, want admitted responsibility removed after the cutoff-visible interval", historicalTrend.Changes)
	}
	if !slices.Contains(historical.Gaps, Gap{
		Kind:      GapAuthorityExcluded,
		EntityID:  "entity:project-atlas",
		Predicate: "project.responsible_party",
	}) {
		t.Fatalf("historical gaps = %#v, want responsibility mention excluded by cutoff authority", historical.Gaps)
	}
	if slices.Contains(current.Gaps, Gap{
		Kind:      GapAuthorityExcluded,
		EntityID:  "entity:project-atlas",
		Predicate: "project.responsible_party",
	}) {
		t.Fatalf("current gaps = %#v, responsibility mention should be admitted currently", current.Gaps)
	}

	reordered := cloneReadSnapshot(fixture.currentSnapshot)
	slices.Reverse(reordered.Entities)
	slices.Reverse(reordered.Observations)
	slices.Reverse(reordered.Coverage)
	for index := range reordered.Observations {
		slices.Reverse(reordered.Observations[index].Evidence)
	}
	reorderedResult, err := (Service{
		Reader: &projectAtlasReader{
			current:    reordered,
			historical: fixture.historicalSnapshot,
			cutoff:     fixture.cutoff,
		},
		Limits: service.Limits,
	}).Query(context.Background(), fixture.currentRequest)
	if err != nil {
		t.Fatalf("reordered current Query() error = %v", err)
	}
	if !reflect.DeepEqual(current, reorderedResult) {
		t.Fatalf("reordered synthetic snapshot changed the result:\ncurrent   %#v\nreordered %#v", current, reorderedResult)
	}
}

func TestProjectAtlasPointContractReconstructsCitedState(t *testing.T) {
	fixture := newProjectAtlasFixture(t)
	at := time.Date(2032, time.January, 20, 12, 0, 0, 0, time.UTC)
	selection, err := temporal.At("at-boundary", at)
	if err != nil {
		t.Fatalf("temporal.At() error = %v", err)
	}
	request := fixture.currentRequest
	request.Intent = temporal.IntentPointInTime
	request.Selections = []temporal.TemporalSelection{selection}
	reader := &projectAtlasReader{
		current:    fixture.currentSnapshot,
		historical: fixture.historicalSnapshot,
		cutoff:     fixture.cutoff,
	}
	service := Service{Reader: reader, Limits: Limits{MaxEntities: 4, MaxPredicates: 8, MaxChronology: 20}}

	current, err := service.Query(context.Background(), request)
	if err != nil {
		t.Fatalf("current Query() error = %v", err)
	}
	if err := ValidateResult(current); err != nil {
		t.Fatalf("ValidateResult(current) error = %v", err)
	}
	point, ok := current.Payload.Point()
	if !ok {
		t.Fatal("current Payload.Point() = false")
	}
	project := atlasEntityTerm(t, "entity:project-atlas")
	commitmentKey, err := temporal.NewStateKey(project, "project.delivery_commitment")
	if err != nil {
		t.Fatalf("temporal.NewStateKey(commitment) error = %v", err)
	}
	responsibilityKey, err := temporal.NewStateKey(project, "project.responsible_party")
	if err != nil {
		t.Fatalf("temporal.NewStateKey(responsibility) error = %v", err)
	}
	riskKey, err := temporal.NewStateKey(project, "project.delivery_risk")
	if err != nil {
		t.Fatalf("temporal.NewStateKey(risk) error = %v", err)
	}
	priorityKey, err := temporal.NewStateKey(project, "project.priority")
	if err != nil {
		t.Fatalf("temporal.NewStateKey(priority) error = %v", err)
	}
	derivation := observation.Derivation{
		Method:  "synthetic-acceptance",
		Version: "d1-v1",
		RunID:   "run:project-atlas",
	}
	ownerAlpha := Fact{
		Key:   responsibilityKey,
		Value: atlasEntityTerm(t, "entity:delivery-unit-alpha"),
		Contributions: []Contribution{{
			ObservationID: "observation:atlas/owner-alpha",
			Status:        observation.StatusObserved,
			ValidTime: atlasInterval(
				t,
				time.Date(2032, time.January, 1, 0, 0, 0, 0, time.UTC),
				time.Date(2032, time.February, 1, 0, 0, 0, 0, time.UTC),
			),
			RecordedAt: time.Date(2032, time.January, 12, 10, 5, 0, 0, time.UTC),
			Derivation: derivation,
		}},
		SupportingCitations: []Citation{
			fixture.ownerAlphaEvidence.citation(observation.EvidenceSupporting),
		},
		ContradictingCitations: []Citation{},
	}
	risk := UnresolvedItem{
		Key:    riskKey,
		Reason: temporal.UnresolvedTemporalUncertainty,
		Candidates: []Fact{{
			Key:   riskKey,
			Value: atlasTextTerm(t, "watch"),
			Contributions: []Contribution{{
				ObservationID: "observation:atlas/risk-unknown",
				Status:        observation.StatusObserved,
				ValidTime:     observation.UnknownTime(),
				RecordedAt:    time.Date(2032, time.January, 18, 10, 5, 0, 0, time.UTC),
				Derivation:    derivation,
			}},
			SupportingCitations: []Citation{
				fixture.riskUnknownEvidence.citation(observation.EvidenceSupporting),
			},
			ContradictingCitations: []Citation{},
		}},
	}
	priorityCritical := Fact{
		Key:   priorityKey,
		Value: atlasTextTerm(t, "critical"),
		Contributions: []Contribution{{
			ObservationID: "observation:atlas/priority-critical",
			Status:        observation.StatusObserved,
			ValidTime:     atlasInstant(t, at),
			RecordedAt:    time.Date(2032, time.January, 20, 11, 5, 0, 0, time.UTC),
			Derivation:    derivation,
		}},
		SupportingCitations: []Citation{
			fixture.priorityCriticalEvidence.citation(observation.EvidenceSupporting),
		},
		ContradictingCitations: []Citation{},
	}
	priorityRoutine := Fact{
		Key:   priorityKey,
		Value: atlasTextTerm(t, "routine"),
		Contributions: []Contribution{{
			ObservationID: "observation:atlas/priority-routine",
			Status:        observation.StatusObserved,
			ValidTime:     atlasInstant(t, at),
			RecordedAt:    time.Date(2032, time.January, 20, 10, 5, 0, 0, time.UTC),
			Derivation:    derivation,
		}},
		SupportingCitations: []Citation{
			fixture.priorityRoutineEvidence.citation(observation.EvidenceSupporting),
		},
		ContradictingCitations: []Citation{},
	}
	priority := UnresolvedItem{
		Key:        priorityKey,
		Reason:     temporal.UnresolvedConflict,
		Candidates: []Fact{priorityCritical, priorityRoutine},
	}
	wantCurrentPoint := PointInTimeResult{
		Selection: selection,
		Facts: []Fact{
			{
				Key:   commitmentKey,
				Value: atlasTextTerm(t, "2032-06-15"),
				Contributions: []Contribution{{
					ObservationID: "observation:atlas/initial-observed",
					Status:        observation.StatusObserved,
					ValidTime: atlasInterval(
						t,
						time.Date(2032, time.January, 1, 0, 0, 0, 0, time.UTC),
						time.Date(2032, time.February, 1, 0, 0, 0, 0, time.UTC),
					),
					RecordedAt: time.Date(2032, time.April, 2, 10, 0, 0, 0, time.UTC),
					Derivation: derivation,
				}},
				SupportingCitations: []Citation{
					fixture.initialEvidence.citation(observation.EvidenceSupporting),
				},
				ContradictingCitations: []Citation{
					fixture.revisionEvidence.citation(observation.EvidenceContradicting),
				},
			},
			ownerAlpha,
		},
		Unresolved: []UnresolvedItem{risk, priority},
	}
	if !reflect.DeepEqual(point, wantCurrentPoint) {
		t.Fatalf("current point = %#v, want exact %#v", point, wantCurrentPoint)
	}
	wantCurrentGaps := []Gap{
		{Kind: GapUnresolvedMention, EntityID: "entity:project-atlas", Predicate: "project.partner"},
		{Kind: GapValidTimeExcluded, EntityID: "entity:project-atlas", Predicate: "project.delivery_commitment", SelectionLabel: "at-boundary"},
		{Kind: GapValidTimeExcluded, EntityID: "entity:project-atlas", Predicate: "project.delivery_risk", SelectionLabel: "at-boundary"},
		{Kind: GapValidTimeExcluded, EntityID: "entity:project-atlas", Predicate: "project.responsible_party", SelectionLabel: "at-boundary"},
		{Kind: GapValidTimeExcluded, EntityID: "entity:project-atlas", Predicate: "project.scope", SelectionLabel: "at-boundary"},
	}
	if !reflect.DeepEqual(current.Gaps, wantCurrentGaps) {
		t.Fatalf("current point gaps = %#v, want exact %#v", current.Gaps, wantCurrentGaps)
	}

	historicalRequest := request
	historicalRequest.KnowledgeScope = fixture.historicalScope
	historical, err := service.Query(context.Background(), historicalRequest)
	if err != nil {
		t.Fatalf("historical Query() error = %v", err)
	}
	historicalPoint, ok := historical.Payload.Point()
	if !ok {
		t.Fatal("historical Payload.Point() = false")
	}
	wantHistoricalPoint := PointInTimeResult{
		Selection:  selection,
		Facts:      []Fact{ownerAlpha},
		Unresolved: []UnresolvedItem{risk, priority},
	}
	if !reflect.DeepEqual(historicalPoint, wantHistoricalPoint) {
		t.Fatalf("historical point = %#v, want exact %#v", historicalPoint, wantHistoricalPoint)
	}
	wantHistoricalGaps := []Gap{{
		Kind:           GapValidTimeExcluded,
		EntityID:       "entity:project-atlas",
		Predicate:      "project.delivery_commitment",
		SelectionLabel: "at-boundary",
	}}
	if !reflect.DeepEqual(historical.Gaps, wantHistoricalGaps) {
		t.Fatalf("historical point gaps = %#v, want exact %#v", historical.Gaps, wantHistoricalGaps)
	}

	reordered := cloneReadSnapshot(fixture.currentSnapshot)
	slices.Reverse(reordered.Entities)
	slices.Reverse(reordered.Observations)
	slices.Reverse(reordered.Coverage)
	for index := range reordered.Observations {
		slices.Reverse(reordered.Observations[index].Evidence)
	}
	reorderedResult, err := (Service{
		Reader: &projectAtlasReader{
			current:    reordered,
			historical: fixture.historicalSnapshot,
			cutoff:     fixture.cutoff,
		},
		Limits: service.Limits,
	}).Query(context.Background(), request)
	if err != nil {
		t.Fatalf("reordered Query() error = %v", err)
	}
	if !reflect.DeepEqual(current, reorderedResult) {
		t.Fatalf("reordered point result differs:\ncurrent   %#v\nreordered %#v", current, reorderedResult)
	}
}

func TestProjectAtlasTrajectoryContractPreservesCitedTransitions(t *testing.T) {
	fixture := newProjectAtlasFixture(t)
	selection, err := temporal.Between(
		"atlas-trajectory",
		time.Date(2032, time.January, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2032, time.April, 1, 0, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("temporal.Between() error = %v", err)
	}
	request := fixture.currentRequest
	request.Intent = temporal.IntentTrajectory
	request.Selections = []temporal.TemporalSelection{selection}
	request.Limit = 12
	reader := &projectAtlasReader{
		current:    fixture.currentSnapshot,
		historical: fixture.historicalSnapshot,
		cutoff:     fixture.cutoff,
	}
	service := Service{
		Reader: reader,
		Limits: Limits{MaxEntities: 4, MaxPredicates: 8, MaxChronology: 20},
	}

	result, err := service.Query(context.Background(), request)
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	if err := ValidateResult(result); err != nil {
		t.Fatalf("ValidateResult() error = %v", err)
	}
	trajectory, ok := result.Payload.Trajectory()
	if !ok {
		t.Fatal("Payload.Trajectory() = false")
	}
	if trajectory.Selection != selection || len(trajectory.Transitions) != 6 {
		t.Fatalf("trajectory = %#v, want six bounded responsibility/commitment transitions", trajectory)
	}
	gotKinds := make([]temporal.ChangeKind, len(trajectory.Transitions))
	gotPredicates := make([]observation.Predicate, len(trajectory.Transitions))
	for index, transition := range trajectory.Transitions {
		gotKinds[index] = transition.Kind
		gotPredicates[index] = transition.Key.Predicate
	}
	if !slices.Equal(gotKinds, []temporal.ChangeKind{
		temporal.ChangeAdded,
		temporal.ChangeAdded,
		temporal.ChangeRemoved,
		temporal.ChangeRemoved,
		temporal.ChangeAdded,
		temporal.ChangeAdded,
	}) {
		t.Fatalf("trajectory kinds = %v", gotKinds)
	}
	if !slices.Equal(gotPredicates, []observation.Predicate{
		"project.responsible_party",
		"project.delivery_commitment",
		"project.responsible_party",
		"project.delivery_commitment",
		"project.delivery_commitment",
		"project.responsible_party",
	}) {
		t.Fatalf("trajectory predicate order = %v", gotPredicates)
	}
	firstOwner := trajectory.Transitions[0].After
	firstCommitment := trajectory.Transitions[1].After
	revisedCommitment := trajectory.Transitions[4].After
	revisedOwner := trajectory.Transitions[5].After
	if firstOwner == nil || firstCommitment == nil ||
		revisedCommitment == nil || revisedOwner == nil {
		t.Fatalf("trajectory facts = %#v, want complete added facts", trajectory.Transitions)
	}
	if !reflect.DeepEqual(firstOwner.SupportingCitations, []Citation{
		fixture.ownerAlphaEvidence.citation(observation.EvidenceSupporting),
	}) || !reflect.DeepEqual(revisedOwner.SupportingCitations, []Citation{
		fixture.ownerBetaEvidence.citation(observation.EvidenceSupporting),
	}) {
		t.Fatalf("responsibility transitions lost exact citations: %#v", trajectory.Transitions)
	}
	if !reflect.DeepEqual(firstCommitment.SupportingCitations, []Citation{
		fixture.initialEvidence.citation(observation.EvidenceSupporting),
	}) || !reflect.DeepEqual(firstCommitment.ContradictingCitations, []Citation{
		fixture.revisionEvidence.citation(observation.EvidenceContradicting),
	}) || !reflect.DeepEqual(revisedCommitment.SupportingCitations, []Citation{
		fixture.revisionEvidence.citation(observation.EvidenceSupporting),
	}) || !reflect.DeepEqual(revisedCommitment.ContradictingCitations, []Citation{
		fixture.initialEvidence.citation(observation.EvidenceContradicting),
	}) {
		t.Fatalf("commitment transitions lost exact evidence roles: %#v", trajectory.Transitions)
	}
	project := atlasEntityTerm(t, "entity:project-atlas")
	derivation := observation.Derivation{
		Method:  "synthetic-acceptance",
		Version: "d1-v1",
		RunID:   "run:project-atlas",
	}
	wantContribution := map[observation.ObservationID]Contribution{
		"observation:atlas/initial-hypothesis": {
			ObservationID: "observation:atlas/initial-hypothesis",
			Status:        observation.StatusHypothesized,
			ValidTime:     atlasInstant(t, time.Date(2032, time.January, 15, 12, 0, 0, 0, time.UTC)),
			RecordedAt:    time.Date(2032, time.January, 16, 10, 5, 0, 0, time.UTC),
			Derivation:    derivation,
		},
		"observation:atlas/initial-observed": {
			ObservationID: "observation:atlas/initial-observed",
			Status:        observation.StatusObserved,
			ValidTime: atlasInterval(
				t,
				time.Date(2032, time.January, 1, 0, 0, 0, 0, time.UTC),
				time.Date(2032, time.February, 1, 0, 0, 0, 0, time.UTC),
			),
			RecordedAt: time.Date(2032, time.April, 2, 10, 0, 0, 0, time.UTC),
			Derivation: derivation,
		},
		"observation:atlas/revised-observed": {
			ObservationID: "observation:atlas/revised-observed",
			Status:        observation.StatusObserved,
			ValidTime:     atlasSince(t, time.Date(2032, time.March, 1, 0, 0, 0, 0, time.UTC)),
			RecordedAt:    time.Date(2032, time.March, 2, 10, 5, 0, 0, time.UTC),
			Derivation:    derivation,
		},
		"observation:atlas/risk-window": {
			ObservationID: "observation:atlas/risk-window",
			Status:        observation.StatusObserved,
			ValidTime: atlasWindow(
				t,
				time.Date(2032, time.March, 15, 0, 0, 0, 0, time.UTC),
				time.Date(2032, time.March, 20, 0, 0, 0, 0, time.UTC),
			),
			RecordedAt: time.Date(2032, time.March, 16, 10, 5, 0, 0, time.UTC),
			Derivation: derivation,
		},
		"observation:atlas/risk-unknown": {
			ObservationID: "observation:atlas/risk-unknown",
			Status:        observation.StatusObserved,
			ValidTime:     observation.UnknownTime(),
			RecordedAt:    time.Date(2032, time.January, 18, 10, 5, 0, 0, time.UTC),
			Derivation:    derivation,
		},
		"observation:atlas/priority-critical": {
			ObservationID: "observation:atlas/priority-critical",
			Status:        observation.StatusObserved,
			ValidTime:     atlasInstant(t, time.Date(2032, time.January, 20, 12, 0, 0, 0, time.UTC)),
			RecordedAt:    time.Date(2032, time.January, 20, 11, 5, 0, 0, time.UTC),
			Derivation:    derivation,
		},
		"observation:atlas/priority-routine": {
			ObservationID: "observation:atlas/priority-routine",
			Status:        observation.StatusObserved,
			ValidTime:     atlasInstant(t, time.Date(2032, time.January, 20, 12, 0, 0, 0, time.UTC)),
			RecordedAt:    time.Date(2032, time.January, 20, 10, 5, 0, 0, time.UTC),
			Derivation:    derivation,
		},
		"observation:atlas/owner-alpha": {
			ObservationID: "observation:atlas/owner-alpha",
			Status:        observation.StatusObserved,
			ValidTime: atlasInterval(
				t,
				time.Date(2032, time.January, 1, 0, 0, 0, 0, time.UTC),
				time.Date(2032, time.February, 1, 0, 0, 0, 0, time.UTC),
			),
			RecordedAt: time.Date(2032, time.January, 12, 10, 5, 0, 0, time.UTC),
			Derivation: derivation,
		},
		"observation:atlas/owner-beta": {
			ObservationID:            "observation:atlas/owner-beta",
			Status:                   observation.StatusObserved,
			ValidTime:                atlasSince(t, time.Date(2032, time.March, 1, 0, 0, 0, 0, time.UTC)),
			RecordedAt:               time.Date(2032, time.March, 3, 10, 5, 0, 0, time.UTC),
			Derivation:               derivation,
			ObjectGroundingMentionID: "mention:delivery-unit-beta",
		},
		"observation:atlas/scope-hypothesis": {
			ObservationID: "observation:atlas/scope-hypothesis",
			Status:        observation.StatusHypothesized,
			ValidTime:     atlasInstant(t, time.Date(2032, time.March, 18, 12, 0, 0, 0, time.UTC)),
			RecordedAt:    time.Date(2032, time.March, 18, 10, 5, 0, 0, time.UTC),
			Derivation:    derivation,
		},
	}
	type expectedUnresolvedCandidate struct {
		value         observation.Term
		observationID []observation.ObservationID
		supporting    []Citation
		contradicting []Citation
	}
	type expectedUnresolvedItem struct {
		predicate  observation.Predicate
		reason     temporal.UnresolvedReason
		candidates []expectedUnresolvedCandidate
	}
	wantUnresolved := []expectedUnresolvedItem{
		{
			predicate: "project.delivery_commitment",
			reason:    temporal.UnresolvedTransition,
			candidates: []expectedUnresolvedCandidate{
				{
					value:         atlasTextTerm(t, "2032-06-15"),
					observationID: []observation.ObservationID{"observation:atlas/initial-hypothesis", "observation:atlas/initial-observed"},
					supporting:    []Citation{fixture.initialEvidence.citation(observation.EvidenceSupporting)},
					contradicting: []Citation{fixture.revisionEvidence.citation(observation.EvidenceContradicting)},
				},
				{
					value:         atlasTextTerm(t, "2032-07-01"),
					observationID: []observation.ObservationID{"observation:atlas/revised-observed"},
					supporting:    []Citation{fixture.revisionEvidence.citation(observation.EvidenceSupporting)},
					contradicting: []Citation{fixture.initialEvidence.citation(observation.EvidenceContradicting)},
				},
			},
		},
		{
			predicate: "project.delivery_risk",
			reason:    temporal.UnresolvedTemporalUncertainty,
			candidates: []expectedUnresolvedCandidate{
				{
					value:         atlasTextTerm(t, "elevated"),
					observationID: []observation.ObservationID{"observation:atlas/risk-window"},
					supporting:    []Citation{fixture.riskWindowEvidence.citation(observation.EvidenceSupporting)},
					contradicting: []Citation{},
				},
				{
					value:         atlasTextTerm(t, "watch"),
					observationID: []observation.ObservationID{"observation:atlas/risk-unknown"},
					supporting:    []Citation{fixture.riskUnknownEvidence.citation(observation.EvidenceSupporting)},
					contradicting: []Citation{},
				},
			},
		},
		{
			predicate: "project.priority",
			reason:    temporal.UnresolvedConflict,
			candidates: []expectedUnresolvedCandidate{
				{
					value:         atlasTextTerm(t, "critical"),
					observationID: []observation.ObservationID{"observation:atlas/priority-critical"},
					supporting:    []Citation{fixture.priorityCriticalEvidence.citation(observation.EvidenceSupporting)},
					contradicting: []Citation{},
				},
				{
					value:         atlasTextTerm(t, "routine"),
					observationID: []observation.ObservationID{"observation:atlas/priority-routine"},
					supporting:    []Citation{fixture.priorityRoutineEvidence.citation(observation.EvidenceSupporting)},
					contradicting: []Citation{},
				},
			},
		},
		{
			predicate: "project.responsible_party",
			reason:    temporal.UnresolvedTransition,
			candidates: []expectedUnresolvedCandidate{
				{
					value:         atlasEntityTerm(t, "entity:delivery-unit-alpha"),
					observationID: []observation.ObservationID{"observation:atlas/owner-alpha"},
					supporting:    []Citation{fixture.ownerAlphaEvidence.citation(observation.EvidenceSupporting)},
					contradicting: []Citation{},
				},
				{
					value:         atlasEntityTerm(t, "entity:delivery-unit-beta"),
					observationID: []observation.ObservationID{"observation:atlas/owner-beta"},
					supporting:    []Citation{fixture.ownerBetaEvidence.citation(observation.EvidenceSupporting)},
					contradicting: []Citation{},
				},
			},
		},
		{
			predicate: "project.scope",
			reason:    temporal.UnresolvedHypothesis,
			candidates: []expectedUnresolvedCandidate{{
				value:         atlasTextTerm(t, "expanded"),
				observationID: []observation.ObservationID{"observation:atlas/scope-hypothesis"},
				supporting:    []Citation{fixture.scopeEvidence.citation(observation.EvidenceSupporting)},
				contradicting: []Citation{},
			}},
		},
	}
	if len(trajectory.Unresolved) != len(wantUnresolved) {
		t.Fatalf("trajectory unresolved = %#v, want %d exact selected-window items", trajectory.Unresolved, len(wantUnresolved))
	}
	for itemIndex, wantItem := range wantUnresolved {
		item := trajectory.Unresolved[itemIndex]
		if item.Key.Subject != project || item.Key.Predicate != wantItem.predicate ||
			item.Reason != wantItem.reason || len(item.Candidates) != len(wantItem.candidates) {
			t.Fatalf("trajectory unresolved[%d] = %#v, want predicate %q reason %q", itemIndex, item, wantItem.predicate, wantItem.reason)
		}
		for candidateIndex, wantCandidate := range wantItem.candidates {
			candidate := item.Candidates[candidateIndex]
			gotObservationIDs := make([]observation.ObservationID, len(candidate.Contributions))
			for index, contribution := range candidate.Contributions {
				gotObservationIDs[index] = contribution.ObservationID
				if !reflect.DeepEqual(contribution, wantContribution[contribution.ObservationID]) {
					t.Fatalf("trajectory unresolved[%d].Candidates[%d].Contributions[%d] = %#v, want exact Project Atlas provenance", itemIndex, candidateIndex, index, contribution)
				}
			}
			if candidate.Value != wantCandidate.value ||
				!slices.Equal(gotObservationIDs, wantCandidate.observationID) ||
				!reflect.DeepEqual(candidate.SupportingCitations, wantCandidate.supporting) ||
				!reflect.DeepEqual(candidate.ContradictingCitations, wantCandidate.contradicting) {
				t.Fatalf("trajectory unresolved[%d].Candidates[%d] = %#v, want exact value, provenance, and citations", itemIndex, candidateIndex, candidate)
			}
		}
	}
	if !reflect.DeepEqual(result.Gaps, []Gap{{
		Kind:      GapUnresolvedMention,
		EntityID:  "entity:project-atlas",
		Predicate: "project.partner",
	}}) {
		t.Fatalf("trajectory gaps = %#v, want exact unresolved mention coverage", result.Gaps)
	}

	reordered := cloneReadSnapshot(fixture.currentSnapshot)
	slices.Reverse(reordered.Entities)
	slices.Reverse(reordered.Observations)
	slices.Reverse(reordered.Coverage)
	for index := range reordered.Observations {
		slices.Reverse(reordered.Observations[index].Evidence)
	}
	reorderedResult, err := (Service{
		Reader: &projectAtlasReader{
			current:    reordered,
			historical: fixture.historicalSnapshot,
			cutoff:     fixture.cutoff,
		},
		Limits: service.Limits,
	}).Query(context.Background(), request)
	if err != nil {
		t.Fatalf("reordered Query() error = %v", err)
	}
	if !reflect.DeepEqual(result, reorderedResult) {
		t.Fatalf("reordered trajectory result differs:\nresult    %#v\nreordered %#v", result, reorderedResult)
	}
}

type projectAtlasFixture struct {
	currentRequest           Request
	currentSnapshot          ReadSnapshot
	historicalSnapshot       ReadSnapshot
	historicalScope          temporal.KnowledgeScope
	cutoff                   time.Time
	initialEvidence          projectAtlasEvidence
	revisionEvidence         projectAtlasEvidence
	ownerAlphaEvidence       projectAtlasEvidence
	ownerBetaEvidence        projectAtlasEvidence
	priorityRoutineEvidence  projectAtlasEvidence
	priorityCriticalEvidence projectAtlasEvidence
	riskUnknownEvidence      projectAtlasEvidence
	riskWindowEvidence       projectAtlasEvidence
	scopeEvidence            projectAtlasEvidence
	priorityRoutine          observation.Observation
	priorityCritical         observation.Observation
}

type projectAtlasEvidence struct {
	document evidence.DocumentVersion
	section  evidence.Section
	span     evidence.EvidenceSpan
}

func (value projectAtlasEvidence) citation(role observation.EvidenceRole) Citation {
	return Citation{
		EvidenceID:        value.span.ID(),
		Role:              role,
		SourceDocumentID:  value.document.ProviderDocumentID(),
		DocumentVersionID: value.document.Digest().String(),
		SectionID:         value.section.ID(),
		SectionTitle:      value.section.Title(),
		SectionPath:       value.section.Path(),
		SectionOrder:      value.section.Order(),
		SectionRole:       value.section.Role(),
		StartOffset:       value.span.StartOffset(),
		EndOffset:         value.span.EndOffset(),
		Locator:           value.span.Locator(),
		Text:              value.span.Text(),
	}
}

func newProjectAtlasFixture(t *testing.T) projectAtlasFixture {
	t.Helper()
	before, err := temporal.Between(
		"before-change",
		time.Date(2032, time.January, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2032, time.February, 1, 0, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("temporal.Between(before-change) error = %v", err)
	}
	after, err := temporal.Between(
		"after-change",
		time.Date(2032, time.March, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2032, time.April, 1, 0, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("temporal.Between(after-change) error = %v", err)
	}
	cutoff := time.Date(2032, time.February, 15, 12, 0, 0, 0, time.UTC)
	historicalScope, err := temporal.KnownAsOf(cutoff)
	if err != nil {
		t.Fatalf("temporal.KnownAsOf() error = %v", err)
	}

	fixture := projectAtlasFixture{
		currentRequest: Request{
			Intent:      temporal.IntentTrendComparison,
			EntityIDs:   []identity.EntityID{" entity:project-atlas "},
			EntityMatch: EntityMatchAll,
			Predicates: []observation.Predicate{
				" project.scope ",
				"project.responsible_party",
				"project.delivery_commitment",
				"project.priority",
				"project.delivery_risk",
			},
			Selections:     []temporal.TemporalSelection{before, after},
			KnowledgeScope: temporal.CurrentKnowledge(),
		},
		historicalScope: historicalScope,
		cutoff:          cutoff,
	}

	fixture.initialEvidence = newProjectAtlasEvidence(
		t,
		"initial",
		"Initial synthetic planning note records a provisional delivery commitment for 2032-06-15.",
		time.Date(2032, time.January, 16, 10, 0, 0, 0, time.UTC),
	)
	fixture.revisionEvidence = newProjectAtlasEvidence(
		t,
		"revision",
		"Later synthetic planning note records the changed delivery commitment for 2032-07-01.",
		time.Date(2032, time.March, 2, 10, 0, 0, 0, time.UTC),
	)
	fixture.ownerAlphaEvidence = newProjectAtlasEvidence(
		t,
		"owner-alpha",
		"Synthetic responsibility is assigned to delivery unit alpha.",
		time.Date(2032, time.January, 12, 10, 0, 0, 0, time.UTC),
	)
	fixture.ownerBetaEvidence = newProjectAtlasEvidence(
		t,
		"owner-beta",
		"Synthetic responsibility transfers to delivery unit beta.",
		time.Date(2032, time.March, 3, 10, 0, 0, 0, time.UTC),
	)
	fixture.priorityRoutineEvidence = newProjectAtlasEvidence(
		t,
		"priority-routine",
		"Synthetic note supports routine priority.",
		time.Date(2032, time.January, 20, 10, 0, 0, 0, time.UTC),
	)
	fixture.priorityCriticalEvidence = newProjectAtlasEvidence(
		t,
		"priority-critical",
		"Synthetic note independently supports critical priority.",
		time.Date(2032, time.January, 20, 11, 0, 0, 0, time.UTC),
	)
	fixture.riskUnknownEvidence = newProjectAtlasEvidence(
		t,
		"risk-unknown",
		"Synthetic note records delivery risk with unknown valid time.",
		time.Date(2032, time.January, 18, 10, 0, 0, 0, time.UTC),
	)
	fixture.riskWindowEvidence = newProjectAtlasEvidence(
		t,
		"risk-window",
		"Synthetic note bounds elevated risk to an uncertainty window.",
		time.Date(2032, time.March, 16, 10, 0, 0, 0, time.UTC),
	)
	fixture.scopeEvidence = newProjectAtlasEvidence(
		t,
		"scope",
		"Synthetic note hypothesizes an expanded scope.",
		time.Date(2032, time.March, 18, 10, 0, 0, 0, time.UTC),
	)

	project := atlasEntityTerm(t, "entity:project-atlas")
	ownerAlpha := atlasEntityTerm(t, "entity:delivery-unit-alpha")
	ownerBeta := atlasEntityTerm(t, "entity:delivery-unit-beta")
	ownerBetaMention := atlasMentionTerm(t, "mention:delivery-unit-beta")
	initialDate := atlasTextTerm(t, "2032-06-15")
	revisedDate := atlasTextTerm(t, "2032-07-01")
	initialInstant := atlasInstant(t, time.Date(2032, time.January, 15, 12, 0, 0, 0, time.UTC))
	initialInterval := atlasInterval(
		t,
		time.Date(2032, time.January, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2032, time.February, 1, 0, 0, 0, 0, time.UTC),
	)
	revisedInterval := atlasSince(t, time.Date(2032, time.March, 1, 0, 0, 0, 0, time.UTC))
	lowConfidence := atlasConfidence(t, 0.08)
	highConfidence := atlasConfidence(t, 0.97)

	initialHypothesis := atlasReadObservation(t, atlasObservationInput{
		id:              "observation:atlas/initial-hypothesis",
		predicate:       "project.delivery_commitment",
		sourceSubject:   project,
		sourceObject:    initialDate,
		resolvedSubject: project,
		resolvedObject:  initialDate,
		validTime:       initialInstant,
		recordedAt:      time.Date(2032, time.January, 16, 10, 5, 0, 0, time.UTC),
		status:          observation.StatusHypothesized,
		confidence:      &highConfidence,
		citations:       []Citation{fixture.initialEvidence.citation(observation.EvidenceSupporting)},
	})
	initialObserved := atlasReadObservation(t, atlasObservationInput{
		id:              "observation:atlas/initial-observed",
		predicate:       "project.delivery_commitment",
		sourceSubject:   project,
		sourceObject:    initialDate,
		resolvedSubject: project,
		resolvedObject:  initialDate,
		validTime:       initialInterval,
		recordedAt:      time.Date(2032, time.April, 2, 10, 0, 0, 0, time.UTC),
		status:          observation.StatusObserved,
		confidence:      &lowConfidence,
		citations: []Citation{
			fixture.initialEvidence.citation(observation.EvidenceSupporting),
			fixture.revisionEvidence.citation(observation.EvidenceContradicting),
		},
	})
	revisedObserved := atlasReadObservation(t, atlasObservationInput{
		id:              "observation:atlas/revised-observed",
		predicate:       "project.delivery_commitment",
		sourceSubject:   project,
		sourceObject:    revisedDate,
		resolvedSubject: project,
		resolvedObject:  revisedDate,
		validTime:       revisedInterval,
		recordedAt:      time.Date(2032, time.March, 2, 10, 5, 0, 0, time.UTC),
		status:          observation.StatusObserved,
		confidence:      &lowConfidence,
		citations: []Citation{
			fixture.revisionEvidence.citation(observation.EvidenceSupporting),
			fixture.initialEvidence.citation(observation.EvidenceContradicting),
		},
	})
	ownerAlphaObservation := atlasReadObservation(t, atlasObservationInput{
		id:              "observation:atlas/owner-alpha",
		predicate:       "project.responsible_party",
		sourceSubject:   project,
		sourceObject:    ownerAlpha,
		resolvedSubject: project,
		resolvedObject:  ownerAlpha,
		validTime:       initialInterval,
		recordedAt:      time.Date(2032, time.January, 12, 10, 5, 0, 0, time.UTC),
		status:          observation.StatusObserved,
		confidence:      &lowConfidence,
		citations:       []Citation{fixture.ownerAlphaEvidence.citation(observation.EvidenceSupporting)},
	})
	ownerBetaObservation := atlasReadObservation(t, atlasObservationInput{
		id:                       "observation:atlas/owner-beta",
		predicate:                "project.responsible_party",
		sourceSubject:            project,
		sourceObject:             ownerBetaMention,
		resolvedSubject:          project,
		resolvedObject:           ownerBeta,
		objectGroundingMentionID: "mention:delivery-unit-beta",
		validTime:                revisedInterval,
		recordedAt:               time.Date(2032, time.March, 3, 10, 5, 0, 0, time.UTC),
		status:                   observation.StatusObserved,
		confidence:               &lowConfidence,
		citations:                []Citation{fixture.ownerBetaEvidence.citation(observation.EvidenceSupporting)},
	})
	fixture.priorityRoutine = atlasReadObservation(t, atlasObservationInput{
		id:              "observation:atlas/priority-routine",
		predicate:       "project.priority",
		sourceSubject:   project,
		sourceObject:    atlasTextTerm(t, "routine"),
		resolvedSubject: project,
		resolvedObject:  atlasTextTerm(t, "routine"),
		validTime:       atlasInstant(t, time.Date(2032, time.January, 20, 12, 0, 0, 0, time.UTC)),
		recordedAt:      time.Date(2032, time.January, 20, 10, 5, 0, 0, time.UTC),
		status:          observation.StatusObserved,
		confidence:      &lowConfidence,
		citations:       []Citation{fixture.priorityRoutineEvidence.citation(observation.EvidenceSupporting)},
	}).Observation
	fixture.priorityCritical = atlasReadObservation(t, atlasObservationInput{
		id:              "observation:atlas/priority-critical",
		predicate:       "project.priority",
		sourceSubject:   project,
		sourceObject:    atlasTextTerm(t, "critical"),
		resolvedSubject: project,
		resolvedObject:  atlasTextTerm(t, "critical"),
		validTime:       atlasInstant(t, time.Date(2032, time.January, 20, 12, 0, 0, 0, time.UTC)),
		recordedAt:      time.Date(2032, time.January, 20, 11, 5, 0, 0, time.UTC),
		status:          observation.StatusObserved,
		confidence:      &highConfidence,
		citations:       []Citation{fixture.priorityCriticalEvidence.citation(observation.EvidenceSupporting)},
	}).Observation
	priorityRoutine := atlasReadObservationFromCanonical(t, fixture.priorityRoutine, project, atlasTextTerm(t, "routine"), "", []Citation{
		fixture.priorityRoutineEvidence.citation(observation.EvidenceSupporting),
	})
	priorityCritical := atlasReadObservationFromCanonical(t, fixture.priorityCritical, project, atlasTextTerm(t, "critical"), "", []Citation{
		fixture.priorityCriticalEvidence.citation(observation.EvidenceSupporting),
	})
	riskUnknown := atlasReadObservation(t, atlasObservationInput{
		id:              "observation:atlas/risk-unknown",
		predicate:       "project.delivery_risk",
		sourceSubject:   project,
		sourceObject:    atlasTextTerm(t, "watch"),
		resolvedSubject: project,
		resolvedObject:  atlasTextTerm(t, "watch"),
		validTime:       observation.UnknownTime(),
		recordedAt:      time.Date(2032, time.January, 18, 10, 5, 0, 0, time.UTC),
		status:          observation.StatusObserved,
		confidence:      &lowConfidence,
		citations:       []Citation{fixture.riskUnknownEvidence.citation(observation.EvidenceSupporting)},
	})
	riskWindow := atlasReadObservation(t, atlasObservationInput{
		id:              "observation:atlas/risk-window",
		predicate:       "project.delivery_risk",
		sourceSubject:   project,
		sourceObject:    atlasTextTerm(t, "elevated"),
		resolvedSubject: project,
		resolvedObject:  atlasTextTerm(t, "elevated"),
		validTime: atlasWindow(
			t,
			time.Date(2032, time.March, 15, 0, 0, 0, 0, time.UTC),
			time.Date(2032, time.March, 20, 0, 0, 0, 0, time.UTC),
		),
		recordedAt: time.Date(2032, time.March, 16, 10, 5, 0, 0, time.UTC),
		status:     observation.StatusObserved,
		confidence: &highConfidence,
		citations:  []Citation{fixture.riskWindowEvidence.citation(observation.EvidenceSupporting)},
	})
	scopeHypothesis := atlasReadObservation(t, atlasObservationInput{
		id:              "observation:atlas/scope-hypothesis",
		predicate:       "project.scope",
		sourceSubject:   project,
		sourceObject:    atlasTextTerm(t, "expanded"),
		resolvedSubject: project,
		resolvedObject:  atlasTextTerm(t, "expanded"),
		validTime:       atlasInstant(t, time.Date(2032, time.March, 18, 12, 0, 0, 0, time.UTC)),
		recordedAt:      time.Date(2032, time.March, 18, 10, 5, 0, 0, time.UTC),
		status:          observation.StatusHypothesized,
		confidence:      &highConfidence,
		citations:       []Citation{fixture.scopeEvidence.citation(observation.EvidenceSupporting)},
	})

	fixture.currentSnapshot = ReadSnapshot{
		Entities: []EntityAuthority{{EntityID: "entity:project-atlas", Known: true}},
		Observations: []ReadObservation{
			scopeHypothesis,
			riskWindow,
			riskUnknown,
			priorityCritical,
			priorityRoutine,
			ownerBetaObservation,
			ownerAlphaObservation,
			revisedObserved,
			initialObserved,
			initialHypothesis,
		},
		Coverage: []Coverage{{
			Reason:        CoverageUnresolvedMention,
			EntityID:      "entity:project-atlas",
			Predicate:     "project.partner",
			ObservationID: "observation:atlas/partner-mention",
			ValidTime:     observation.UnknownTime(),
		}},
	}
	fixture.historicalSnapshot = ReadSnapshot{
		Entities: []EntityAuthority{{EntityID: "entity:project-atlas", Known: true}},
		Observations: []ReadObservation{
			riskUnknown,
			priorityCritical,
			priorityRoutine,
			ownerAlphaObservation,
			initialHypothesis,
		},
		Coverage: []Coverage{{
			Reason:        CoverageAuthorityExcluded,
			EntityID:      "entity:project-atlas",
			Predicate:     "project.responsible_party",
			ObservationID: "observation:atlas/owner-beta",
			ValidTime:     revisedInterval,
		}},
	}
	return fixture
}

type atlasObservationInput struct {
	id                       observation.ObservationID
	predicate                observation.Predicate
	sourceSubject            observation.Term
	sourceObject             observation.Term
	resolvedSubject          observation.Term
	resolvedObject           observation.Term
	objectGroundingMentionID string
	validTime                observation.TemporalExtent
	recordedAt               time.Time
	status                   observation.EpistemicStatus
	confidence               *observation.Confidence
	citations                []Citation
}

func atlasReadObservation(t *testing.T, input atlasObservationInput) ReadObservation {
	t.Helper()
	links := make([]observation.EvidenceLink, len(input.citations))
	for index, citation := range input.citations {
		links[index] = observation.EvidenceLink{EvidenceID: citation.EvidenceID, Role: citation.Role}
	}
	value, err := observation.NewObservation(observation.ObservationInput{
		ID: input.id,
		Statement: observation.Statement{
			Subject:   input.sourceSubject,
			Predicate: input.predicate,
			Object:    input.sourceObject,
		},
		ValidTime:  input.validTime,
		RecordedAt: input.recordedAt,
		Evidence:   links,
		Derivation: observation.Derivation{
			Method:  "synthetic-acceptance",
			Version: "d1-v1",
			RunID:   "run:project-atlas",
		},
		Status:     input.status,
		Confidence: input.confidence,
	})
	if err != nil {
		t.Fatalf("observation.NewObservation(%q) error = %v", input.id, err)
	}
	return atlasReadObservationFromCanonical(
		t,
		value,
		input.resolvedSubject,
		input.resolvedObject,
		input.objectGroundingMentionID,
		input.citations,
	)
}

func atlasReadObservationFromCanonical(
	t *testing.T,
	value observation.Observation,
	subject observation.Term,
	object observation.Term,
	objectGroundingMentionID string,
	citations []Citation,
) ReadObservation {
	t.Helper()
	return ReadObservation{
		Observation:              value,
		Subject:                  subject,
		Object:                   object,
		ObjectGroundingMentionID: objectGroundingMentionID,
		Evidence:                 cloneCitations(citations),
	}
}

func newProjectAtlasEvidence(t *testing.T, id, text string, recordedAt time.Time) projectAtlasEvidence {
	t.Helper()
	section, err := evidence.NewSection(evidence.SectionInput{
		ID:    "section:atlas/" + id,
		Title: "Synthetic Project Atlas evidence",
		Path:  []string{"Synthetic acceptance", "Project Atlas"},
		Order: 1,
		Role:  "synthetic-note",
		Text:  text,
	})
	if err != nil {
		t.Fatalf("evidence.NewSection(%q) error = %v", id, err)
	}
	document, err := evidence.NewDocumentVersion(evidence.DocumentVersionInput{
		Provider:           "synthetic-acceptance",
		ProviderDocumentID: "document:atlas/" + id,
		Title:              "Synthetic Project Atlas note",
		Locator:            "synthetic://project-atlas/" + id,
		ProviderVersion:    "synthetic-v1",
		ModifiedAt:         recordedAt.Add(-time.Minute),
		RecordedAt:         recordedAt,
		Sections:           []evidence.Section{section},
	})
	if err != nil {
		t.Fatalf("evidence.NewDocumentVersion(%q) error = %v", id, err)
	}
	span, err := evidence.NewEvidenceSpan(evidence.EvidenceSpanInput{
		Document:    document,
		SectionID:   section.ID(),
		StartOffset: 0,
		EndOffset:   len(text),
		Quote:       text,
		RecordedAt:  recordedAt,
	})
	if err != nil {
		t.Fatalf("evidence.NewEvidenceSpan(%q) error = %v", id, err)
	}
	return projectAtlasEvidence{document: document, section: section, span: span}
}

type projectAtlasReader struct {
	current    ReadSnapshot
	historical ReadSnapshot
	cutoff     time.Time
	selections []ReadSelection
}

func (reader *projectAtlasReader) Read(_ context.Context, selection ReadSelection) (ReadSnapshot, error) {
	reader.selections = append(reader.selections, selection)
	switch selection.KnowledgeScope.Kind() {
	case temporal.KnowledgeCurrent:
		return cloneReadSnapshot(reader.current), nil
	case temporal.KnowledgeAsOf:
		cutoff, ok := selection.KnowledgeScope.AsOf()
		if !ok || !cutoff.Equal(reader.cutoff) {
			return ReadSnapshot{}, fmt.Errorf("synthetic historical cutoff is unexpected")
		}
		return cloneReadSnapshot(reader.historical), nil
	default:
		return ReadSnapshot{}, fmt.Errorf("synthetic knowledge scope is invalid")
	}
}

func atlasTextTerm(t *testing.T, value string) observation.Term {
	t.Helper()
	term, err := observation.NewTextTerm(value)
	if err != nil {
		t.Fatalf("observation.NewTextTerm(%q) error = %v", value, err)
	}
	return term
}

func atlasEntityTerm(t *testing.T, id string) observation.Term {
	t.Helper()
	term, err := observation.NewEntityTerm(id, "")
	if err != nil {
		t.Fatalf("observation.NewEntityTerm(%q) error = %v", id, err)
	}
	return term
}

func atlasMentionTerm(t *testing.T, id string) observation.Term {
	t.Helper()
	term, err := observation.NewMentionTerm(id)
	if err != nil {
		t.Fatalf("observation.NewMentionTerm(%q) error = %v", id, err)
	}
	return term
}

func atlasInstant(t *testing.T, value time.Time) observation.TemporalExtent {
	t.Helper()
	extent, err := observation.AtTime(value)
	if err != nil {
		t.Fatalf("observation.AtTime() error = %v", err)
	}
	return extent
}

func atlasInterval(t *testing.T, start, end time.Time) observation.TemporalExtent {
	t.Helper()
	extent, err := observation.During(start, end)
	if err != nil {
		t.Fatalf("observation.During() error = %v", err)
	}
	return extent
}

func atlasSince(t *testing.T, start time.Time) observation.TemporalExtent {
	t.Helper()
	extent, err := observation.Since(start)
	if err != nil {
		t.Fatalf("observation.Since() error = %v", err)
	}
	return extent
}

func atlasWindow(t *testing.T, start, end time.Time) observation.TemporalExtent {
	t.Helper()
	extent, err := observation.Within(start, end)
	if err != nil {
		t.Fatalf("observation.Within() error = %v", err)
	}
	return extent
}

func atlasConfidence(t *testing.T, value float64) observation.Confidence {
	t.Helper()
	confidence, err := observation.NewUnitIntervalConfidence(value)
	if err != nil {
		t.Fatalf("observation.NewUnitIntervalConfidence(%v) error = %v", value, err)
	}
	return confidence
}

func factPredicates(values []Fact) []observation.Predicate {
	result := make([]observation.Predicate, len(values))
	for index, value := range values {
		result[index] = value.Key.Predicate
	}
	return result
}

func unresolvedPredicates(values []UnresolvedItem) []observation.Predicate {
	result := make([]observation.Predicate, len(values))
	for index, value := range values {
		result[index] = value.Key.Predicate
	}
	return result
}

func stateKeyPredicates(values []temporal.StateKey) []observation.Predicate {
	result := make([]observation.Predicate, len(values))
	for index, value := range values {
		result[index] = value.Predicate
	}
	return result
}

func changePredicates(values []Change) []observation.Predicate {
	result := make([]observation.Predicate, len(values))
	for index, value := range values {
		result[index] = value.Key.Predicate
	}
	return result
}

func factTextValues(values []Fact) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index], _ = value.Value.Text()
	}
	return result
}

func assertTextTerm(t *testing.T, term observation.Term, want string) {
	t.Helper()
	got, ok := term.Text()
	if !ok || got != want {
		t.Fatalf("term.Text() = (%q, %v), want (%q, true)", got, ok, want)
	}
}
