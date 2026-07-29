package postgres_test

import (
	"reflect"
	"testing"
	"time"

	postgres "github.com/JakeFAU/stacks/adapters/postgres"
	"github.com/JakeFAU/stacks/core/admission"
	"github.com/JakeFAU/stacks/core/evidence"
	"github.com/JakeFAU/stacks/core/observation"
	"github.com/JakeFAU/stacks/core/temporal"
)

func TestProjectCommitmentChronologyPreservesChangeUncertaintyAndCounterevidence(t *testing.T) {
	fixture := newDocumentRepositoryFixture(t)
	noteDates := []time.Time{
		time.Date(2026, time.July, 1, 9, 0, 0, 0, time.UTC),
		time.Date(2026, time.July, 10, 9, 0, 0, 0, time.UTC),
		time.Date(2026, time.July, 20, 9, 0, 0, 0, time.UTC),
	}
	noteTexts := []string{
		"Project Atlas may deliver on 2026-08-15.",
		"Project Atlas delivery moved to 2026-09-01.",
		"Project Atlas delivery remains 2026-09-01.",
	}
	var (
		spans []evidence.EvidenceSpan
		runs  []postgres.ExtractionRunInput
	)
	for index := range noteDates {
		section, err := evidence.NewSection(evidence.SectionInput{
			ID:    "section-project-note",
			Title: "Synthetic project note",
			Path:  []string{"Synthetic project notes"},
			Order: 0,
			Role:  "project-note",
			Text:  noteTexts[index],
		})
		if err != nil {
			t.Fatalf("evidence.NewSection(note %d) error = %v", index+1, err)
		}
		sourceTime := noteDates[index]
		recordedAt := noteDates[index].Add(time.Hour)
		document, err := evidence.NewDocumentVersion(evidence.DocumentVersionInput{
			Provider:           "synthetic-project-notes",
			ProviderDocumentID: "project-atlas-note-" + string(rune('1'+index)),
			Title:              "Synthetic Project Atlas note",
			Locator:            "synthetic://project-atlas/note-" + string(rune('1'+index)),
			ProviderVersion:    "synthetic-v1",
			ModifiedAt:         noteDates[index],
			RecordedAt:         recordedAt,
			SourceTime:         &sourceTime,
			Sections:           []evidence.Section{section},
		})
		if err != nil {
			t.Fatalf("evidence.NewDocumentVersion(note %d) error = %v", index+1, err)
		}
		put, err := fixture.database.PutDocumentVersion(fixture.ctx, document)
		if err != nil {
			t.Fatalf("PutDocumentVersion(note %d) error = %v", index+1, err)
		}
		span, err := evidence.NewEvidenceSpan(evidence.EvidenceSpanInput{
			Document:    document,
			SectionID:   section.ID(),
			StartOffset: 0,
			EndOffset:   len(noteTexts[index]),
			Quote:       noteTexts[index],
			RecordedAt:  recordedAt,
		})
		if err != nil {
			t.Fatalf("evidence.NewEvidenceSpan(note %d) error = %v", index+1, err)
		}
		run := canonicalExtractionRun(put.Ref.VersionID)
		run.ID = "run:project-atlas/note-" + string(rune('1'+index))
		run.DerivationDigest = syntheticDigest("project-atlas-run-" + string(rune('1'+index)))
		run.RecordedAt = recordedAt
		lease := canonicalLease(
			"attempt:project-atlas/note-"+string(rune('1'+index)),
			"worker:project-atlas",
			recordedAt.Add(time.Minute),
		)
		if err := fixture.database.InTransaction(fixture.ctx, func(transaction *postgres.Transaction) error {
			if _, err := transaction.PrepareExtraction(fixture.ctx, run, lease); err != nil {
				return err
			}
			_, err := transaction.PutEvidenceSpan(fixture.ctx, span)
			return err
		}); err != nil {
			t.Fatalf("persist Project Atlas note %d extraction input: %v", index+1, err)
		}
		spans = append(spans, span)
		runs = append(runs, run)
	}

	projectTerm, err := observation.NewTextTerm("Project Atlas")
	if err != nil {
		t.Fatalf("observation.NewTextTerm(project) error = %v", err)
	}
	initialDateTerm, err := observation.NewTextTerm("2026-08-15")
	if err != nil {
		t.Fatalf("observation.NewTextTerm(initial date) error = %v", err)
	}
	revisedDateTerm, err := observation.NewTextTerm("2026-09-01")
	if err != nil {
		t.Fatalf("observation.NewTextTerm(revised date) error = %v", err)
	}
	initialInstant, err := observation.AtTime(noteDates[0])
	if err != nil {
		t.Fatalf("observation.AtTime(initial) error = %v", err)
	}
	initialInterval, err := observation.During(noteDates[0], noteDates[1])
	if err != nil {
		t.Fatalf("observation.During(initial) error = %v", err)
	}
	revisedInterval, err := observation.Since(noteDates[1])
	if err != nil {
		t.Fatalf("observation.Since(revised) error = %v", err)
	}
	confirmationInstant, err := observation.AtTime(noteDates[2])
	if err != nil {
		t.Fatalf("observation.AtTime(confirmation) error = %v", err)
	}
	values := []observation.Observation{
		canonicalObservation(
			t,
			"observation:project-atlas/initial-hypothesis",
			projectTerm,
			"project.delivery_commitment",
			initialDateTerm,
			initialInstant,
			noteDates[0].Add(time.Hour),
			observation.StatusHypothesized,
			[]observation.EvidenceLink{supportingLink(spans[0])},
			nil,
			runs[0].ID,
		),
		canonicalObservation(
			t,
			"observation:project-atlas/initial-retrospective",
			projectTerm,
			"project.delivery_commitment",
			initialDateTerm,
			initialInterval,
			noteDates[1].Add(time.Hour),
			observation.StatusValidatedStructurally,
			[]observation.EvidenceLink{
				supportingLink(spans[0]),
				{EvidenceID: spans[1].ID(), Role: observation.EvidenceContradicting},
			},
			nil,
			runs[1].ID,
		),
		canonicalObservation(
			t,
			"observation:project-atlas/revised",
			projectTerm,
			"project.delivery_commitment",
			revisedDateTerm,
			revisedInterval,
			noteDates[1].Add(time.Hour).Add(time.Microsecond),
			observation.StatusObserved,
			[]observation.EvidenceLink{
				{EvidenceID: spans[0].ID(), Role: observation.EvidenceContradicting},
				supportingLink(spans[1]),
			},
			nil,
			runs[1].ID,
		),
		canonicalObservation(
			t,
			"observation:project-atlas/confirmed",
			projectTerm,
			"project.delivery_commitment",
			revisedDateTerm,
			confirmationInstant,
			noteDates[2].Add(time.Hour),
			observation.StatusValidatedEmpirically,
			[]observation.EvidenceLink{supportingLink(spans[2])},
			nil,
			runs[2].ID,
		),
	}
	if err := fixture.database.InTransaction(fixture.ctx, func(transaction *postgres.Transaction) error {
		for _, value := range values {
			if _, err := transaction.PutObservation(fixture.ctx, value); err != nil {
				return err
			}
			if err := transaction.AppendAdmissionDecision(
				fixture.ctx,
				canonicalAdmissionDecision(
					t,
					"admission:"+string(value.ID()),
					admission.TargetObservation,
					string(value.ID()),
					admission.Admitted,
					admission.AuthorityPolicy,
					"",
					value.RecordedAt().Add(time.Microsecond),
				),
			); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("persist Project Atlas observations: %v", err)
	}

	var loaded []observation.Observation
	for _, want := range values {
		got, err := fixture.database.LoadObservation(fixture.ctx, want.ID())
		if err != nil {
			t.Fatalf("LoadObservation(%q) error = %v", want.ID(), err)
		}
		assertCanonicalObservationEqual(t, got, want)
		loaded = append(loaded, got)
	}
	if loaded[0].Status() != observation.StatusHypothesized ||
		loaded[2].Status() != observation.StatusObserved ||
		loaded[3].Status() != observation.StatusValidatedEmpirically {
		t.Fatalf(
			"Project Atlas epistemic chronology = %q/%q/%q, want hypothesized/observed/validated",
			loaded[0].Status(),
			loaded[2].Status(),
			loaded[3].Status(),
		)
	}

	before, err := temporal.Between("initial", noteDates[0], noteDates[1])
	if err != nil {
		t.Fatalf("temporal.Between(initial) error = %v", err)
	}
	after, err := temporal.Between("revised", noteDates[1], noteDates[2].Add(24*time.Hour))
	if err != nil {
		t.Fatalf("temporal.Between(revised) error = %v", err)
	}
	plan, err := temporal.NewPlan(temporal.PlanInput{
		Intent:         temporal.IntentTrajectory,
		EntityIDs:      []string{"project-atlas:opaque"},
		Selections:     []temporal.TemporalSelection{after},
		KnowledgeScope: temporal.CurrentKnowledge(),
	})
	if err != nil {
		t.Fatalf("temporal.NewPlan(trajectory) error = %v", err)
	}
	if !reflect.DeepEqual(plan.Operations(), []temporal.RetrievalOperation{
		temporal.OperationPartitionTimeline,
		temporal.OperationAggregateWindows,
		temporal.OperationDiffWindows,
		temporal.OperationOrderTransitions,
	}) {
		t.Fatalf("trajectory operations = %v, want deterministic chronology operators", plan.Operations())
	}

	candidates := make([]temporal.StateCandidate, len(loaded))
	for index, value := range loaded {
		key, err := temporal.NewStateKey(
			value.Statement().Subject,
			value.Statement().Predicate,
		)
		if err != nil {
			t.Fatalf("temporal.NewStateKey(%q) error = %v", value.ID(), err)
		}
		candidates[index] = temporal.StateCandidate{
			Key:         key,
			Value:       value.Statement().Object,
			Observation: value,
		}
	}
	currentBefore, err := temporal.AggregateWindow(
		before,
		temporal.CurrentKnowledge(),
		candidates,
	)
	if err != nil {
		t.Fatalf("temporal.AggregateWindow(initial current) error = %v", err)
	}
	currentAfter, err := temporal.AggregateWindow(
		after,
		temporal.CurrentKnowledge(),
		candidates,
	)
	if err != nil {
		t.Fatalf("temporal.AggregateWindow(revised current) error = %v", err)
	}
	comparison, err := temporal.CompareWindowSummaries(currentBefore, currentAfter)
	if err != nil {
		t.Fatalf("temporal.CompareWindowSummaries() error = %v", err)
	}
	if len(comparison.Changes) != 1 ||
		comparison.Changes[0].Kind != temporal.ChangeChanged ||
		comparison.Changes[0].Before == nil ||
		comparison.Changes[0].After == nil {
		t.Fatalf("Project Atlas comparison = %#v, want one changed commitment", comparison)
	}
	beforeValue, beforeOK := comparison.Changes[0].Before.Value.Text()
	afterValue, afterOK := comparison.Changes[0].After.Value.Text()
	if !beforeOK ||
		beforeValue != "2026-08-15" ||
		!afterOK ||
		afterValue != "2026-09-01" {
		t.Fatalf("Project Atlas comparison = %#v, want one changed commitment", comparison)
	}
	if !sameSortedEvidenceIDs(
		comparison.Changes[0].Before.SupportingEvidenceIDs,
		spans[0].ID(),
	) || !sameSortedEvidenceIDs(
		comparison.Changes[0].Before.ContradictingEvidenceIDs,
		spans[1].ID(),
	) {
		t.Fatalf(
			"initial commitment provenance = supporting %v contradicting %v",
			comparison.Changes[0].Before.SupportingEvidenceIDs,
			comparison.Changes[0].Before.ContradictingEvidenceIDs,
		)
	}
	if !sameSortedEvidenceIDs(
		comparison.Changes[0].After.SupportingEvidenceIDs,
		spans[1].ID(),
		spans[2].ID(),
	) || !sameSortedEvidenceIDs(
		comparison.Changes[0].After.ContradictingEvidenceIDs,
		spans[0].ID(),
	) {
		t.Fatalf(
			"revised commitment provenance = supporting %v contradicting %v",
			comparison.Changes[0].After.SupportingEvidenceIDs,
			comparison.Changes[0].After.ContradictingEvidenceIDs,
		)
	}

	earlyScope, err := temporal.KnownAsOf(noteDates[0].Add(time.Hour))
	if err != nil {
		t.Fatalf("temporal.KnownAsOf() error = %v", err)
	}
	earlyBefore, err := temporal.AggregateWindow(before, earlyScope, candidates)
	if err != nil {
		t.Fatalf("temporal.AggregateWindow(initial early) error = %v", err)
	}
	if len(earlyBefore.Facts) != 0 ||
		len(earlyBefore.Unresolved) != 1 ||
		earlyBefore.Unresolved[0].Reason != temporal.UnresolvedHypothesis {
		t.Fatalf(
			"early recorded-time state = facts %#v unresolved %#v, want hypothesis only",
			earlyBefore.Facts,
			earlyBefore.Unresolved,
		)
	}
}

func sameSortedEvidenceIDs(
	got []evidence.EvidenceID,
	want ...evidence.EvidenceID,
) bool {
	if len(got) != len(want) {
		return false
	}
	wantSet := make(map[evidence.EvidenceID]struct{}, len(want))
	for _, id := range want {
		wantSet[id] = struct{}{}
	}
	for index, id := range got {
		if _, exists := wantSet[id]; !exists {
			return false
		}
		if index > 0 && got[index-1] > id {
			return false
		}
	}
	return true
}
