package storage

import (
	"context"
	"crypto/sha256"
	"math"
	"testing"
	"time"

	knowledge "github.com/JakeFAU/stacks/core/evidence"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"stacks/internal/modelpolicy"
)

type legacyTermShape struct {
	name      string
	entityID  bool
	mentionID bool
}

var legacyTermShapes = []legacyTermShape{
	{name: "absent"},
	{name: "entity", entityID: true},
	{name: "mention", mentionID: true},
	{name: "entity_with_grounding_mention", entityID: true, mentionID: true},
}

type legacyObservationProjection struct {
	extractionRunID  string
	recordedAt       time.Time
	derivation       string
	modelID          string
	promptVersion    string
	supportingIDs    []string
	contradictingIDs []string
}

type legacyObservationFixture struct {
	extractionRunID  string
	subjectEntityID  string
	objectEntityID   string
	subjectMentionID string
	objectMentionID  string
	evidenceSpanID   string
}

func TestLegacyObservationCompatibilityShapes(t *testing.T) {
	pool := openIntegrationDatabase(t)
	ctx := context.Background()
	fixture := createLegacyObservationFixture(t, pool)

	t.Run("accepted_reference_shapes", func(t *testing.T) {
		for _, subject := range legacyTermShapes {
			for _, object := range legacyTermShapes {
				t.Run(subject.name+"_to_"+object.name, func(t *testing.T) {
					observationID := uuid.NewString()
					subjectEntityID, subjectMentionID := legacyTermReferences(subject, fixture.subjectEntityID, fixture.subjectMentionID)
					objectEntityID, objectMentionID := legacyTermReferences(object, fixture.objectEntityID, fixture.objectMentionID)
					insertLegacyObservation(t, ctx, pool, legacyObservationInsert{
						id: observationID, extractionRunID: fixture.extractionRunID,
						subjectEntityID: subjectEntityID, objectEntityID: objectEntityID,
						subjectMentionID: subjectMentionID, objectMentionID: objectMentionID,
						epistemicStatus: "observed", digestLabel: observationID,
					})

					var storedSubjectEntityID, storedObjectEntityID, storedSubjectMentionID, storedObjectMentionID *string
					if err := pool.QueryRow(ctx, `
						SELECT subject_entity_id::text, object_entity_id::text,
						       subject_mention_id::text, object_mention_id::text
						FROM stacks.observations
						WHERE id = $1`, observationID).Scan(
						&storedSubjectEntityID, &storedObjectEntityID, &storedSubjectMentionID, &storedObjectMentionID,
					); err != nil {
						t.Fatalf("read legacy observation references: %v", err)
					}
					assertLegacyNullableReference(t, "subject entity", storedSubjectEntityID, subjectEntityID)
					assertLegacyNullableReference(t, "object entity", storedObjectEntityID, objectEntityID)
					assertLegacyNullableReference(t, "subject mention", storedSubjectMentionID, subjectMentionID)
					assertLegacyNullableReference(t, "object mention", storedObjectMentionID, objectMentionID)
				})
			}
		}
	})

	t.Run("accepted_epistemic_confidence_and_temporal_values", func(t *testing.T) {
		statuses := []string{
			"observed",
			"inferred",
			"hypothesized",
			"validated_structurally",
			"validated_empirically",
			"rejected",
		}
		confidences := []*float64{
			nil,
			float64Pointer(-2.5),
			float64Pointer(0),
			float64Pointer(0.75),
			float64Pointer(1),
			float64Pointer(4.25),
		}
		start := time.Date(2026, time.July, 1, 12, 0, 0, 0, time.UTC)
		temporalCases := []struct {
			name  string
			start *time.Time
			end   *time.Time
		}{
			{name: "no_bounds"},
			{name: "start_only", start: &start},
			{name: "equal_bounds", start: &start, end: &start},
			{name: "increasing_bounds", start: &start, end: timePointer(start.Add(time.Hour))},
		}
		for _, status := range statuses {
			for confidenceIndex, confidence := range confidences {
				for _, temporalCase := range temporalCases {
					t.Run(status+"_confidence_"+string(rune('0'+confidenceIndex))+"_"+temporalCase.name, func(t *testing.T) {
						observationID := uuid.NewString()
						insertLegacyObservation(t, ctx, pool, legacyObservationInsert{
							id: observationID, extractionRunID: fixture.extractionRunID,
							epistemicStatus: status, confidence: confidence,
							validStart: temporalCase.start, validEnd: temporalCase.end, digestLabel: observationID,
						})
					})
				}
			}
		}
	})

	t.Run("rejects_invalid_temporal_and_confidence_values", func(t *testing.T) {
		start := time.Date(2026, time.July, 1, 12, 0, 0, 0, time.UTC)
		end := start.Add(time.Hour)
		invalidCases := []struct {
			name       string
			start      *time.Time
			end        *time.Time
			confidence *float64
		}{
			{name: "end_only", end: &end},
			{name: "decreasing_bounds", start: &end, end: &start},
			{name: "nan_confidence", confidence: float64Pointer(math.NaN())},
			{name: "positive_infinity_confidence", confidence: float64Pointer(math.Inf(1))},
			{name: "negative_infinity_confidence", confidence: float64Pointer(math.Inf(-1))},
		}
		for _, invalidCase := range invalidCases {
			t.Run(invalidCase.name, func(t *testing.T) {
				transaction, err := pool.Begin(ctx)
				if err != nil {
					t.Fatalf("start invalid compatibility transaction: %v", err)
				}
				defer transaction.Rollback(ctx) //nolint:errcheck // Invalid rows must not be committed.
				observationID := uuid.NewString()
				if _, err := transaction.Exec(ctx, legacyObservationInsertSQL,
					observationID, fixture.extractionRunID, nil, nil, nil, nil,
					"legacy_compatibility", invalidCase.start, invalidCase.end, legacyRecordedAt,
					"model_extraction", "observed", invalidCase.confidence, legacyObservationDigest(observationID),
				); err == nil {
					t.Fatal("invalid legacy observation insert succeeded")
				}
			})
		}
	})

	t.Run("provenance_and_evidence_roles", func(t *testing.T) {
		observationID := uuid.NewString()
		insertLegacyObservation(t, ctx, pool, legacyObservationInsert{
			id: observationID, extractionRunID: fixture.extractionRunID,
			subjectEntityID: stringPointer(fixture.subjectEntityID), subjectMentionID: stringPointer(fixture.subjectMentionID),
			epistemicStatus: "inferred", digestLabel: observationID,
		})
		if _, err := pool.Exec(ctx, `
			INSERT INTO stacks.observation_evidence (observation_id, evidence_span_id)
			VALUES ($1, $2)`, observationID, fixture.evidenceSpanID); err != nil {
			t.Fatalf("link legacy observation evidence: %v", err)
		}

		signalID := uuid.NewString()
		transaction, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("start legacy signal transaction: %v", err)
		}
		defer transaction.Rollback(ctx) //nolint:errcheck // Committed transactions are already closed.
		if _, err := transaction.Exec(ctx, `
			INSERT INTO stacks.interaction_signals
				(id, observation_id, category, direction, extraction_model_id, prompt_version, rationale, confidence, digest)
			VALUES ($1, $2, 'delegation_autonomy', 'weakening', 'synthetic-compatibility-model',
			        'compatibility-v1', 'Synthetic source-grounded rationale.', 0.75, $3)`,
			signalID, observationID, legacyObservationDigest(signalID)); err != nil {
			t.Fatalf("insert legacy interaction signal: %v", err)
		}
		for _, role := range []string{"supporting", "contradicting"} {
			if _, err := transaction.Exec(ctx, `
				INSERT INTO stacks.signal_evidence (signal_id, evidence_span_id, role)
				VALUES ($1, $2, $3)`, signalID, fixture.evidenceSpanID, role); err != nil {
				t.Fatalf("link %s legacy signal evidence: %v", role, err)
			}
		}
		if err := transaction.Commit(ctx); err != nil {
			t.Fatalf("commit legacy signal transaction: %v", err)
		}

		var projection legacyObservationProjection
		if err := pool.QueryRow(ctx, `
			SELECT observation.extraction_run_id::text, observation.recorded_at, observation.derivation,
			       signal.extraction_model_id, signal.prompt_version
			FROM stacks.observations AS observation
			JOIN stacks.interaction_signals AS signal ON signal.observation_id = observation.id
			WHERE observation.id = $1`, observationID).Scan(
			&projection.extractionRunID, &projection.recordedAt, &projection.derivation,
			&projection.modelID, &projection.promptVersion,
		); err != nil {
			t.Fatalf("read legacy observation provenance: %v", err)
		}
		projection.supportingIDs = legacyEvidenceIDs(t, pool, observationID, `
			SELECT evidence_span_id::text
			FROM stacks.observation_evidence
			WHERE observation_id = $1`, observationID)
		projection.contradictingIDs = legacyEvidenceIDs(t, pool, observationID, `
			SELECT evidence_span_id::text
			FROM stacks.signal_evidence
			WHERE signal_id = $1 AND role = 'contradicting'`, signalID)
		signalSupportingIDs := legacyEvidenceIDs(t, pool, observationID, `
			SELECT evidence_span_id::text
			FROM stacks.signal_evidence
			WHERE signal_id = $1 AND role = 'supporting'`, signalID)

		if projection.extractionRunID != fixture.extractionRunID || !projection.recordedAt.Equal(legacyRecordedAt) ||
			projection.derivation != "model_extraction" || projection.modelID != "synthetic-compatibility-model" ||
			projection.promptVersion != "compatibility-v1" {
			t.Fatalf("legacy observation projection = %#v", projection)
		}
		assertLegacyEvidenceIDs(t, "observation supporting", projection.supportingIDs, fixture.evidenceSpanID)
		assertLegacyEvidenceIDs(t, "signal supporting", signalSupportingIDs, fixture.evidenceSpanID)
		assertLegacyEvidenceIDs(t, "signal contradicting", projection.contradictingIDs, fixture.evidenceSpanID)

		var category, direction, rationale string
		var confidence float64
		if err := pool.QueryRow(ctx, `
			SELECT category, direction, rationale, confidence
			FROM stacks.interaction_signals
			WHERE observation_id = $1`, observationID).Scan(&category, &direction, &rationale, &confidence); err != nil {
			t.Fatalf("read legacy interaction signal: %v", err)
		}
		if category != "delegation_autonomy" || direction != "weakening" ||
			rationale != "Synthetic source-grounded rationale." || confidence != 0.75 {
			t.Fatalf("legacy interaction signal = %q/%q/%q/%v", category, direction, rationale, confidence)
		}
	})

	t.Run("active_writer_keeps_entities_and_grounding_mentions", func(t *testing.T) {
		entities := NewEntityRepository(pool)
		manager, err := entities.CreateEntity(ctx, EntityInput{
			ID: uuid.NewString(), Kind: "person", DisplayName: "Synthetic Compatibility Manager",
		})
		if err != nil {
			t.Fatalf("create synthetic manager: %v", err)
		}
		employee, err := entities.CreateEntity(ctx, EntityInput{
			ID: uuid.NewString(), Kind: "person", DisplayName: "Synthetic Compatibility Employee",
		})
		if err != nil {
			t.Fatalf("create synthetic employee: %v", err)
		}
		providerDocumentID := testIdentifier("legacy-observation-writer")
		completeVersionedPairSignal(t, pool, providerDocumentID, "compatibility-revision",
			"Alex Manager assigned follow-up work to Jordan Employee.", employee.ID, manager.ID)

		var subjectEntityID, subjectMentionID, objectEntityID, objectMentionID *string
		if err := pool.QueryRow(ctx, `
			SELECT observation.subject_entity_id::text, observation.subject_mention_id::text,
			       observation.object_entity_id::text, observation.object_mention_id::text
			FROM stacks.observations AS observation
			JOIN stacks.extraction_runs AS run ON run.id = observation.extraction_run_id
			JOIN stacks.document_versions AS version ON version.id = run.document_version_id
			JOIN stacks.source_documents AS document ON document.id = version.source_document_id
			WHERE document.provider_document_id = $1`, providerDocumentID).Scan(
			&subjectEntityID, &subjectMentionID, &objectEntityID, &objectMentionID,
		); err != nil {
			t.Fatalf("read active writer observation: %v", err)
		}
		assertLegacyNullableReference(t, "active writer subject entity", subjectEntityID, stringPointer(manager.ID))
		assertLegacyNullableReference(t, "active writer object entity", objectEntityID, stringPointer(employee.ID))
		if subjectMentionID == nil || objectMentionID == nil {
			t.Fatalf("active writer grounding mentions = %v/%v, want both populated", subjectMentionID, objectMentionID)
		}
	})
}

const legacyObservationInsertSQL = `
	INSERT INTO stacks.observations
		(id, extraction_run_id, subject_entity_id, object_entity_id, subject_mention_id, object_mention_id,
		 predicate, valid_start, valid_end, recorded_at, derivation, epistemic_status, confidence, digest)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)`

var legacyRecordedAt = time.Date(2026, time.July, 25, 12, 0, 0, 0, time.UTC)

type legacyObservationInsert struct {
	id               string
	extractionRunID  string
	subjectEntityID  *string
	objectEntityID   *string
	subjectMentionID *string
	objectMentionID  *string
	validStart       *time.Time
	validEnd         *time.Time
	epistemicStatus  string
	confidence       *float64
	digestLabel      string
}

func createLegacyObservationFixture(t *testing.T, pool *pgxpool.Pool) legacyObservationFixture {
	t.Helper()
	ctx := context.Background()
	documents := NewDocumentRepository(pool)
	version := testDocumentVersion(t, testIdentifier("legacy-observation-compatibility"))
	if _, _, err := documents.PutDocumentVersion(ctx, version); err != nil {
		t.Fatalf("put legacy compatibility document version: %v", err)
	}
	span, err := knowledge.NewEvidenceSpan(knowledge.EvidenceSpanInput{
		Document: version, SectionID: "tab-synthetic", StartOffset: 0, EndOffset: len("Synthetic"), Quote: "Synthetic",
	})
	if err != nil {
		t.Fatalf("create legacy compatibility evidence span: %v", err)
	}
	storedSpan, err := documents.PutEvidenceSpan(ctx, span)
	if err != nil {
		t.Fatalf("put legacy compatibility evidence span: %v", err)
	}
	ingestionRepository := NewIngestionRepository(pool)
	state, err := ingestionRepository.PrepareVersion(ctx, version, testExtractionDerivation(t, version), modelpolicy.DataModePersonal, 5*time.Minute)
	if err != nil {
		t.Fatalf("prepare legacy compatibility extraction run: %v", err)
	}
	entities := NewEntityRepository(pool)
	subjectEntity, err := entities.CreateEntity(ctx, EntityInput{ID: uuid.NewString(), Kind: "person", DisplayName: "Synthetic Compatibility Subject"})
	if err != nil {
		t.Fatalf("create legacy compatibility subject entity: %v", err)
	}
	objectEntity, err := entities.CreateEntity(ctx, EntityInput{ID: uuid.NewString(), Kind: "person", DisplayName: "Synthetic Compatibility Object"})
	if err != nil {
		t.Fatalf("create legacy compatibility object entity: %v", err)
	}
	subjectMention, err := entities.CreateMention(ctx, MentionInput{EvidenceSpanID: storedSpan.ID, Surface: "Synthetic Subject", Role: "speaker"})
	if err != nil {
		t.Fatalf("create legacy compatibility subject mention: %v", err)
	}
	objectMention, err := entities.CreateMention(ctx, MentionInput{EvidenceSpanID: storedSpan.ID, Surface: "Synthetic Object", Role: "reference"})
	if err != nil {
		t.Fatalf("create legacy compatibility object mention: %v", err)
	}
	return legacyObservationFixture{
		extractionRunID: state.DerivationID,
		subjectEntityID: subjectEntity.ID, objectEntityID: objectEntity.ID,
		subjectMentionID: subjectMention.ID, objectMentionID: objectMention.ID,
		evidenceSpanID: storedSpan.ID,
	}
}

func insertLegacyObservation(t *testing.T, ctx context.Context, pool *pgxpool.Pool, input legacyObservationInsert) {
	t.Helper()
	if _, err := pool.Exec(ctx, legacyObservationInsertSQL,
		input.id, input.extractionRunID, input.subjectEntityID, input.objectEntityID,
		input.subjectMentionID, input.objectMentionID, "legacy_compatibility", input.validStart,
		input.validEnd, legacyRecordedAt, "model_extraction", input.epistemicStatus,
		input.confidence, legacyObservationDigest(input.digestLabel),
	); err != nil {
		t.Fatalf("insert legacy observation: %v", err)
	}
}

func legacyTermReferences(shape legacyTermShape, entityID, mentionID string) (*string, *string) {
	var entityReference, mentionReference *string
	if shape.entityID {
		entityReference = &entityID
	}
	if shape.mentionID {
		mentionReference = &mentionID
	}
	return entityReference, mentionReference
}

func assertLegacyNullableReference(t *testing.T, name string, actual, expected *string) {
	t.Helper()
	if (actual == nil) != (expected == nil) || actual != nil && *actual != *expected {
		t.Fatalf("%s = %v, want %v", name, actual, expected)
	}
}

func legacyObservationDigest(label string) []byte {
	digest := sha256.Sum256([]byte(label))
	return digest[:]
}

func float64Pointer(value float64) *float64 {
	return &value
}

func timePointer(value time.Time) *time.Time {
	return &value
}

func stringPointer(value string) *string {
	return &value
}

func legacyEvidenceIDs(t *testing.T, pool *pgxpool.Pool, observationID, query string, arguments ...any) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(), query, arguments...)
	if err != nil {
		t.Fatalf("query legacy evidence for observation %q: %v", observationID, err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan legacy evidence for observation %q: %v", observationID, err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate legacy evidence for observation %q: %v", observationID, err)
	}
	return ids
}

func assertLegacyEvidenceIDs(t *testing.T, name string, actual []string, expected string) {
	t.Helper()
	if len(actual) != 1 || actual[0] != expected {
		t.Fatalf("%s IDs = %#v, want [%q]", name, actual, expected)
	}
}
