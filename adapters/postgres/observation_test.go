package postgres_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	postgres "github.com/JakeFAU/stacks/adapters/postgres"
	"github.com/JakeFAU/stacks/core/observation"
)

func TestCanonicalObservationMatrixRoundTripsWithoutInference(t *testing.T) {
	fixture := newObservationRepositoryFixture(t)
	textTerm, err := observation.NewTextTerm("  Project Atlas / delivery  ")
	if err != nil {
		t.Fatalf("observation.NewTextTerm() error = %v", err)
	}
	mentionTerm, err := observation.NewMentionTerm(string(fixture.mention.ID()))
	if err != nil {
		t.Fatalf("observation.NewMentionTerm() error = %v", err)
	}
	entityTerm, err := observation.NewEntityTerm(string(fixture.entity.ID()), "")
	if err != nil {
		t.Fatalf("observation.NewEntityTerm() error = %v", err)
	}
	groundedEntityTerm, err := observation.NewEntityTerm(
		string(fixture.entity.ID()),
		string(fixture.mention.ID()),
	)
	if err != nil {
		t.Fatalf("observation.NewEntityTerm(grounded) error = %v", err)
	}
	terms := []observation.Term{
		observation.AbsentTerm(),
		textTerm,
		mentionTerm,
		entityTerm,
		groundedEntityTerm,
	}
	instant, err := observation.AtTime(time.Date(2026, time.July, 1, 12, 0, 0, 111111000, time.UTC))
	if err != nil {
		t.Fatalf("observation.AtTime() error = %v", err)
	}
	during, err := observation.During(
		time.Date(2026, time.July, 2, 0, 0, 0, 222222000, time.UTC),
		time.Date(2026, time.July, 3, 0, 0, 0, 333333000, time.UTC),
	)
	if err != nil {
		t.Fatalf("observation.During() error = %v", err)
	}
	since, err := observation.Since(time.Date(2026, time.July, 4, 0, 0, 0, 444444000, time.UTC))
	if err != nil {
		t.Fatalf("observation.Since() error = %v", err)
	}
	until, err := observation.Until(time.Date(2026, time.July, 5, 0, 0, 0, 555555000, time.UTC))
	if err != nil {
		t.Fatalf("observation.Until() error = %v", err)
	}
	window, err := observation.Within(
		time.Date(2026, time.July, 6, 0, 0, 0, 666666000, time.UTC),
		time.Date(2026, time.July, 7, 0, 0, 0, 777777000, time.UTC),
	)
	if err != nil {
		t.Fatalf("observation.Within() error = %v", err)
	}
	temporalExtents := []observation.TemporalExtent{
		observation.UnknownTime(),
		instant,
		during,
		since,
		until,
		window,
	}
	statuses := []observation.EpistemicStatus{
		observation.StatusObserved,
		observation.StatusInferred,
		observation.StatusHypothesized,
		observation.StatusValidatedStructurally,
		observation.StatusValidatedEmpirically,
		observation.StatusRejected,
	}
	zeroConfidence, err := observation.NewUnitIntervalConfidence(0)
	if err != nil {
		t.Fatalf("observation.NewUnitIntervalConfidence(0) error = %v", err)
	}
	oneConfidence, err := observation.NewUnitIntervalConfidence(1)
	if err != nil {
		t.Fatalf("observation.NewUnitIntervalConfidence(1) error = %v", err)
	}
	confidences := []*observation.Confidence{nil, &zeroConfidence, &oneConfidence}
	links := []observation.EvidenceLink{
		{
			EvidenceID: fixture.evidence[1].ID(),
			Role:       observation.EvidenceSupporting,
		},
		{
			EvidenceID: fixture.evidence[0].ID(),
			Role:       observation.EvidenceContradicting,
		},
		{
			EvidenceID: fixture.evidence[0].ID(),
			Role:       observation.EvidenceSupporting,
		},
	}

	var values []observation.Observation
	for subjectIndex, subject := range terms {
		for objectIndex, object := range terms {
			index := subjectIndex*len(terms) + objectIndex
			values = append(values, canonicalObservation(
				t,
				fmt.Sprintf("observation:matrix/%d-%d", subjectIndex, objectIndex),
				subject,
				"  project.delivery/commitment  ",
				object,
				temporalExtents[index%len(temporalExtents)],
				observationRecordedAt.Add(time.Duration(index)*time.Microsecond),
				statuses[index%len(statuses)],
				links,
				confidences[index%len(confidences)],
				fixture.run.ID,
			))
		}
	}
	putObservations(t, fixture, values...)

	for _, want := range values {
		got, err := fixture.database.LoadObservation(fixture.ctx, want.ID())
		if err != nil {
			t.Fatalf("LoadObservation(%q) error = %v", want.ID(), err)
		}
		assertCanonicalObservationEqual(t, got, want)
		if got.Statement().Predicate != "  project.delivery/commitment  " {
			t.Fatalf("predicate bytes = %q, want exact surrounding spaces", got.Statement().Predicate)
		}
		if got.Statement().Subject.Kind() == observation.TermText {
			text, _ := got.Statement().Subject.Text()
			if text != "  Project Atlas / delivery  " {
				t.Fatalf("subject text bytes = %q, want exact surrounding spaces", text)
			}
		}
	}
	gotLinks := values[0].EvidenceLinks()
	if len(gotLinks) != 3 {
		t.Fatalf("canonical evidence-role count = %d, want 3", len(gotLinks))
	}
	wantPairs := map[observation.EvidenceLink]bool{
		{EvidenceID: fixture.evidence[0].ID(), Role: observation.EvidenceContradicting}: true,
		{EvidenceID: fixture.evidence[0].ID(), Role: observation.EvidenceSupporting}:    true,
		{EvidenceID: fixture.evidence[1].ID(), Role: observation.EvidenceSupporting}:    true,
	}
	for index, link := range gotLinks {
		if !wantPairs[link] {
			t.Fatalf("unexpected canonical evidence-role pair = %#v", link)
		}
		if index > 0 {
			previous := gotLinks[index-1]
			if previous.EvidenceID > link.EvidenceID ||
				(previous.EvidenceID == link.EvidenceID && previous.Role > link.Role) {
				t.Fatalf("canonical evidence-role order is not ascending: %v", gotLinks)
			}
		}
	}
}

func TestCanonicalObservationExactRetryIsReadOnlyAndConflictIsBounded(t *testing.T) {
	fixture := newObservationRepositoryFixture(t)
	subject, err := observation.NewTextTerm("Project Atlas")
	if err != nil {
		t.Fatalf("observation.NewTextTerm(subject) error = %v", err)
	}
	object, err := observation.NewTextTerm("2026-08-15")
	if err != nil {
		t.Fatalf("observation.NewTextTerm(object) error = %v", err)
	}
	value := canonicalObservation(
		t,
		"observation:opaque/retry",
		subject,
		"project.delivery_commitment",
		object,
		observation.UnknownTime(),
		observationRecordedAt,
		observation.StatusObserved,
		[]observation.EvidenceLink{supportingLink(fixture.evidence[0])},
		nil,
		fixture.run.ID,
	)
	if err := fixture.database.InTransaction(fixture.ctx, func(transaction *postgres.Transaction) error {
		created, err := transaction.PutObservation(fixture.ctx, value)
		if err != nil {
			return err
		}
		if !created {
			return errors.New("first PutObservation() created = false")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var beforeObservation, beforeEvidence string
	if err := fixture.admin.QueryRow(
		fixture.ctx,
		`SELECT xmin::text FROM stacks_core.observations WHERE id = $1`,
		value.ID(),
	).Scan(&beforeObservation); err != nil {
		t.Fatalf("read observation xmin: %v", err)
	}
	if err := fixture.admin.QueryRow(
		fixture.ctx,
		`SELECT xmin::text
		 FROM stacks_core.observation_evidence
		 WHERE observation_id = $1`,
		value.ID(),
	).Scan(&beforeEvidence); err != nil {
		t.Fatalf("read observation evidence xmin: %v", err)
	}
	if err := fixture.database.InTransaction(fixture.ctx, func(transaction *postgres.Transaction) error {
		created, err := transaction.PutObservation(fixture.ctx, value)
		if err != nil {
			return err
		}
		if created {
			return errors.New("repeated PutObservation() created = true")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var afterObservation, afterEvidence string
	if err := fixture.admin.QueryRow(
		fixture.ctx,
		`SELECT xmin::text FROM stacks_core.observations WHERE id = $1`,
		value.ID(),
	).Scan(&afterObservation); err != nil {
		t.Fatalf("read repeated observation xmin: %v", err)
	}
	if err := fixture.admin.QueryRow(
		fixture.ctx,
		`SELECT xmin::text
		 FROM stacks_core.observation_evidence
		 WHERE observation_id = $1`,
		value.ID(),
	).Scan(&afterEvidence); err != nil {
		t.Fatalf("read repeated observation evidence xmin: %v", err)
	}
	if beforeObservation != afterObservation || beforeEvidence != afterEvidence {
		t.Fatalf(
			"exact retry changed row identities observation/evidence %s/%s to %s/%s",
			beforeObservation,
			beforeEvidence,
			afterObservation,
			afterEvidence,
		)
	}

	conflicting := canonicalObservation(
		t,
		string(value.ID()),
		subject,
		"project.revised_delivery_commitment",
		object,
		observation.UnknownTime(),
		observationRecordedAt,
		observation.StatusObserved,
		[]observation.EvidenceLink{supportingLink(fixture.evidence[0])},
		nil,
		fixture.run.ID,
	)
	err = fixture.database.InTransaction(fixture.ctx, func(transaction *postgres.Transaction) error {
		_, err := transaction.PutObservation(fixture.ctx, conflicting)
		return err
	})
	if !errors.Is(err, postgres.ErrConflict) {
		t.Fatalf("stable observation ID conflict error = %v, want ErrConflict", err)
	}
}

func TestCanonicalObservationRejectsLegacyStorageShapes(t *testing.T) {
	fixture := newObservationRepositoryFixture(t)
	subject, err := observation.NewTextTerm("Project Atlas")
	if err != nil {
		t.Fatalf("observation.NewTextTerm(subject) error = %v", err)
	}
	object, err := observation.NewTextTerm("synthetic commitment")
	if err != nil {
		t.Fatalf("observation.NewTextTerm(object) error = %v", err)
	}
	predicate, err := observation.NewPredicate("project.commitment")
	if err != nil {
		t.Fatalf("observation.NewPredicate() error = %v", err)
	}
	legacyConfidence, err := observation.NewLegacyConfidence(0.75)
	if err != nil {
		t.Fatalf("observation.NewLegacyConfidence() error = %v", err)
	}
	legacyValues := []observation.Observation{
		mustObservation(t, observation.ObservationInput{
			ID:         "observation:legacy/uncited",
			Statement:  observation.Statement{Subject: subject, Predicate: predicate, Object: object},
			ValidTime:  observation.UnknownTime(),
			RecordedAt: observationRecordedAt,
			Derivation: observation.Derivation{
				Method:  "legacy",
				Version: "legacy-v1",
			},
			Status:        observation.StatusObserved,
			LegacyUncited: true,
		}),
		mustObservation(t, observation.ObservationInput{
			ID:         "observation:legacy/derivation",
			Statement:  observation.Statement{Subject: subject, Predicate: predicate, Object: object},
			ValidTime:  observation.UnknownTime(),
			RecordedAt: observationRecordedAt,
			Evidence:   []observation.EvidenceLink{supportingLink(fixture.evidence[0])},
			Derivation: observation.Derivation{
				Method:            "legacy",
				LegacyUnversioned: true,
			},
			Status: observation.StatusObserved,
		}),
		mustObservation(t, observation.ObservationInput{
			ID:         "observation:legacy/confidence",
			Statement:  observation.Statement{Subject: subject, Predicate: predicate, Object: object},
			ValidTime:  observation.UnknownTime(),
			RecordedAt: observationRecordedAt,
			Evidence:   []observation.EvidenceLink{supportingLink(fixture.evidence[0])},
			Derivation: observation.Derivation{
				Method:  "structured-extraction",
				Version: "extractor-v3",
			},
			Status:     observation.StatusObserved,
			Confidence: &legacyConfidence,
		}),
	}
	for _, value := range legacyValues {
		t.Run(string(value.ID()), func(t *testing.T) {
			err := fixture.database.InTransaction(fixture.ctx, func(transaction *postgres.Transaction) error {
				_, err := transaction.PutObservation(fixture.ctx, value)
				return err
			})
			if err == nil {
				t.Fatal("PutObservation() error = nil, want legacy-shape rejection")
			}
			var count int
			if err := fixture.admin.QueryRow(
				fixture.ctx,
				`SELECT count(*) FROM stacks_core.observations WHERE id = $1`,
				value.ID(),
			).Scan(&count); err != nil {
				t.Fatalf("count rejected legacy observation: %v", err)
			}
			if count != 0 {
				t.Fatalf("rejected legacy observation row count = %d, want 0", count)
			}
		})
	}
}

func TestCanonicalObservationLoadRejectsCorruptDigest(t *testing.T) {
	fixture := newObservationRepositoryFixture(t)
	subject, err := observation.NewTextTerm("Project Atlas")
	if err != nil {
		t.Fatalf("observation.NewTextTerm(subject) error = %v", err)
	}
	object, err := observation.NewTextTerm("2026-08-15")
	if err != nil {
		t.Fatalf("observation.NewTextTerm(object) error = %v", err)
	}
	value := canonicalObservation(
		t,
		"observation:opaque/corrupt",
		subject,
		"project.delivery_commitment",
		object,
		observation.UnknownTime(),
		observationRecordedAt,
		observation.StatusObserved,
		[]observation.EvidenceLink{supportingLink(fixture.evidence[0])},
		nil,
		fixture.run.ID,
	)
	putObservations(t, fixture, value)
	corruptDigest := syntheticDigest("corrupt-observation-payload")
	if _, err := fixture.admin.Exec(
		fixture.ctx,
		`UPDATE stacks_core.observations SET digest = $2 WHERE id = $1`,
		value.ID(),
		corruptDigest[:],
	); err != nil {
		t.Fatalf("corrupt observation digest fixture: %v", err)
	}
	if _, err := fixture.database.LoadObservation(
		fixture.ctx,
		value.ID(),
	); !errors.Is(err, postgres.ErrConflict) {
		t.Fatalf("LoadObservation() error = %v, want ErrConflict", err)
	}
}

func TestCanonicalObservationUncitedTransactionRollsBack(t *testing.T) {
	fixture := newObservationRepositoryFixture(t)
	uncitedID := "observation:opaque/uncited-raw"
	err := fixture.database.InTransaction(fixture.ctx, func(transaction *postgres.Transaction) error {
		digest := syntheticDigest("uncited-raw-payload")
		_, err := transaction.Exec(fixture.ctx, `
			INSERT INTO stacks_core.observations (
				id,
				subject_kind,
				predicate,
				object_kind,
				object_text,
				temporal_kind,
				has_start,
				has_end,
				recorded_at,
				derivation_method,
				derivation_version,
				derivation_run_id,
				derivation_model,
				derivation_prompt_version,
				epistemic_status,
				digest_version,
				digest
			)
			VALUES (
				$1,
				'absent',
				'project.delivery_commitment',
				'text',
				'2026-08-15',
				'unknown',
				false,
				false,
				$2,
				'structured-extraction',
				'extractor-v3',
				$3,
				'synthetic-model-v1',
				'prompt-v7',
				'observed',
				'stacks.observation.v2.canonical',
				$4
			)`,
			uncitedID,
			observationRecordedAt,
			fixture.run.ID,
			digest[:],
		)
		return err
	})
	if err == nil {
		t.Fatal("uncited observation transaction error = nil, want deferred constraint failure")
	}
	var count int
	if err := fixture.admin.QueryRow(
		fixture.ctx,
		`SELECT count(*) FROM stacks_core.observations WHERE id = $1`,
		uncitedID,
	).Scan(&count); err != nil {
		t.Fatalf("count rolled-back uncited observation: %v", err)
	}
	if count != 0 {
		t.Fatalf("uncited observation row count = %d, want 0 after rollback", count)
	}
}

func mustObservation(
	t testing.TB,
	input observation.ObservationInput,
) observation.Observation {
	t.Helper()
	value, err := observation.NewObservation(input)
	if err != nil {
		t.Fatalf("observation.NewObservation(%q) error = %v", input.ID, err)
	}
	return value
}
