package postgres_test

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	postgres "github.com/JakeFAU/stacks/adapters/postgres"
	"github.com/JakeFAU/stacks/core/evidence"
	"github.com/JakeFAU/stacks/core/identity"
	"github.com/JakeFAU/stacks/core/observation"
)

var observationRecordedAt = time.Date(2026, time.July, 25, 21, 0, 0, 123456000, time.UTC)

type observationRepositoryFixture struct {
	identityRepositoryFixture
	run     postgres.ExtractionRunInput
	lease   postgres.LeaseRequest
	entity  identity.Entity
	mention identity.MentionRecord
}

func newObservationRepositoryFixture(t testing.TB) observationRepositoryFixture {
	t.Helper()
	identityFixture := newIdentityRepositoryFixture(t)
	var versionID string
	if err := identityFixture.admin.QueryRow(
		identityFixture.ctx,
		`SELECT document_version_id
		 FROM stacks_core.evidence_spans
		 WHERE id = $1`,
		identityFixture.evidence[0].ID(),
	).Scan(&versionID); err != nil {
		t.Fatalf("load observation fixture document version ID: %v", err)
	}
	run := canonicalExtractionRun(versionID)
	run.ID = "run:opaque/observation"
	run.DerivationDigest = syntheticDigest("observation-extraction-derivation")
	run.RecordedAt = observationRecordedAt.Add(-2 * time.Minute)
	lease := canonicalLease(
		"attempt:opaque/observation",
		"worker:opaque/observation",
		observationRecordedAt.Add(-time.Minute),
	)
	entity := canonicalEntity(t, "entity:opaque/observation", "Synthetic Observer")
	mention := canonicalMention(
		t,
		"mention:opaque/observation",
		identityFixture.evidence[0],
		"Synthetic Observer",
		"",
	)
	if err := identityFixture.database.InTransaction(
		identityFixture.ctx,
		func(transaction *postgres.Transaction) error {
			if _, err := transaction.PrepareExtraction(identityFixture.ctx, run, lease); err != nil {
				return err
			}
			if _, err := transaction.PutEntity(identityFixture.ctx, entity); err != nil {
				return err
			}
			if _, err := transaction.PutMention(identityFixture.ctx, mention); err != nil {
				return err
			}
			return nil
		},
	); err != nil {
		t.Fatalf("persist observation repository fixture: %v", err)
	}
	return observationRepositoryFixture{
		identityRepositoryFixture: identityFixture,
		run:                       run,
		lease:                     lease,
		entity:                    entity,
		mention:                   mention,
	}
}

func canonicalObservation(
	t testing.TB,
	id string,
	subject observation.Term,
	predicateValue string,
	object observation.Term,
	validTime observation.TemporalExtent,
	recordedAt time.Time,
	status observation.EpistemicStatus,
	links []observation.EvidenceLink,
	confidence *observation.Confidence,
	runID string,
) observation.Observation {
	t.Helper()
	predicate, err := observation.NewPredicate(predicateValue)
	if err != nil {
		t.Fatalf("observation.NewPredicate(%q) error = %v", predicateValue, err)
	}
	value, err := observation.NewObservation(observation.ObservationInput{
		ID: observation.ObservationID(id),
		Statement: observation.Statement{
			Subject:   subject,
			Predicate: predicate,
			Object:    object,
		},
		ValidTime:  validTime,
		RecordedAt: recordedAt,
		Evidence:   links,
		Derivation: observation.Derivation{
			Method:        "structured-extraction",
			Version:       "extractor-v3",
			RunID:         runID,
			Model:         "synthetic-model-v1",
			PromptVersion: "prompt-v7",
		},
		Status:     status,
		Confidence: confidence,
	})
	if err != nil {
		t.Fatalf("observation.NewObservation(%q) error = %v", id, err)
	}
	return value
}

func putObservations(
	t testing.TB,
	fixture observationRepositoryFixture,
	values ...observation.Observation,
) {
	t.Helper()
	if err := fixture.database.InTransaction(fixture.ctx, func(transaction *postgres.Transaction) error {
		for _, value := range values {
			created, err := transaction.PutObservation(fixture.ctx, value)
			if err != nil {
				return err
			}
			if !created {
				return fmt.Errorf("observation %q was not created", value.ID())
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("persist observations: %v", err)
	}
}

func assertCanonicalObservationEqual(
	t testing.TB,
	got observation.Observation,
	want observation.Observation,
) {
	t.Helper()
	if got.ID() != want.ID() ||
		got.Statement() != want.Statement() ||
		!reflect.DeepEqual(got.ValidTime(), want.ValidTime()) ||
		got.RecordedAt() != want.RecordedAt() ||
		!reflect.DeepEqual(got.EvidenceLinks(), want.EvidenceLinks()) ||
		got.Derivation() != want.Derivation() ||
		got.Status() != want.Status() ||
		got.LegacyUncited() != want.LegacyUncited() ||
		got.DigestVersion() != want.DigestVersion() ||
		got.Digest() != want.Digest() {
		t.Fatalf("loaded observation %q does not equal canonical payload", want.ID())
	}
	gotConfidence, gotHasConfidence := got.Confidence()
	wantConfidence, wantHasConfidence := want.Confidence()
	if gotHasConfidence != wantHasConfidence ||
		(gotHasConfidence && gotConfidence != wantConfidence) {
		t.Fatalf(
			"loaded observation %q confidence = (%#v, %v), want (%#v, %v)",
			want.ID(),
			gotConfidence,
			gotHasConfidence,
			wantConfidence,
			wantHasConfidence,
		)
	}
}

func supportingLink(span evidence.EvidenceSpan) observation.EvidenceLink {
	return observation.EvidenceLink{
		EvidenceID: span.ID(),
		Role:       observation.EvidenceSupporting,
	}
}
