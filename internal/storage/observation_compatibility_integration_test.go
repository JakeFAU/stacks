package storage

import (
	"context"
	"crypto/sha256"
	"math"
	"testing"
	"time"

	knowledge "github.com/JakeFAU/stacks/core/evidence"
	"github.com/JakeFAU/stacks/core/observation"
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

type legacyObservationFixture struct {
	extractionRunID  string
	runModelID       string
	runPromptVersion string
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

					decoded, err := loadLegacyObservation(ctx, pool, observationID)
					if err != nil {
						t.Fatalf("load legacy reference shape: %v", err)
					}
					assertLoadedLegacyTerm(t, "subject", decoded.Observation.Statement().Subject, subject, fixture.subjectEntityID, fixture.subjectMentionID)
					assertLoadedLegacyTerm(t, "object", decoded.Observation.Statement().Object, object, fixture.objectEntityID, fixture.objectMentionID)
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
						decoded, err := loadLegacyObservation(ctx, pool, observationID)
						if err != nil {
							t.Fatalf("load legacy temporal shape: %v", err)
						}
						if decoded.Observation.Status() != observation.EpistemicStatus(status) {
							t.Fatalf("canonical status = %q, want %q", decoded.Observation.Status(), status)
						}
						if confidence == nil {
							if _, ok := decoded.Observation.Confidence(); ok {
								t.Fatalf("canonical confidence = present, want absent")
							}
						} else if got, ok := decoded.Observation.Confidence(); !ok || got.Value() != *confidence {
							t.Fatalf("canonical confidence = %v/%v, want %v", got, ok, *confidence)
						}
						wantKind := observation.TemporalUnknown
						switch temporalCase.name {
						case "start_only", "increasing_bounds":
							wantKind = observation.TemporalInterval
						case "equal_bounds":
							wantKind = observation.TemporalInstant
						}
						if decoded.Observation.ValidTime().Kind() != wantKind {
							t.Fatalf("canonical valid time kind = %v, want %v", decoded.Observation.ValidTime().Kind(), wantKind)
						}
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
		updateLegacyObservationDigest(t, ctx, pool, observationID, []knowledge.EvidenceID{knowledge.EvidenceID(fixture.evidenceSpanID)})

		signalID := uuid.NewString()
		signalInput := SignalInput{
			ID: signalID, ObservationID: observationID, Category: "delegation_autonomy", Direction: "weakening",
			ExtractionModelID: "synthetic-compatibility-model", PromptVersion: "compatibility-v1",
			Rationale: "Synthetic source-grounded rationale.", Confidence: 0.75,
		}
		signalEvidence := []SignalEvidenceInput{
			{EvidenceSpanID: fixture.evidenceSpanID, Role: "supporting"},
			{EvidenceSpanID: fixture.evidenceSpanID, Role: "contradicting"},
		}
		signalDigest, err := ComputeSignalDigest(signalInput, signalEvidence)
		if err != nil {
			t.Fatalf("compute compatibility signal digest: %v", err)
		}
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
			signalID, observationID, signalDigest[:]); err != nil {
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

		decoded, err := loadLegacyObservation(ctx, pool, observationID)
		if err != nil {
			t.Fatalf("load legacy observation provenance: %v", err)
		}
		want := []observation.EvidenceLink{
			{EvidenceID: knowledge.EvidenceID(fixture.evidenceSpanID), Role: observation.EvidenceSupporting},
			{EvidenceID: knowledge.EvidenceID(fixture.evidenceSpanID), Role: observation.EvidenceContradicting},
		}
		if !sameEvidenceLinks(decoded.Observation.EvidenceLinks(), want) {
			t.Fatalf("canonical evidence links = %#v, want %#v", decoded.Observation.EvidenceLinks(), want)
		}
		if !sameEvidenceIDs(decoded.Compatibility.observationEvidenceOrigin, []knowledge.EvidenceID{knowledge.EvidenceID(fixture.evidenceSpanID)}) {
			t.Fatalf("private origin = %#v", decoded.Compatibility.observationEvidenceOrigin)
		}
		if decoded.Signal == nil || decoded.Signal.Input.ObservationID != observationID ||
			decoded.Signal.Input.Category != "delegation_autonomy" || decoded.Signal.Input.Direction != "weakening" ||
			decoded.Signal.Input.ExtractionModelID != "synthetic-compatibility-model" ||
			decoded.Signal.Input.PromptVersion != "compatibility-v1" || decoded.Signal.Input.Rationale != "Synthetic source-grounded rationale." ||
			decoded.Signal.Input.Confidence != 0.75 {
			t.Fatalf("canonical signal = %#v", decoded.Signal)
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
	predicate        string
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
		Document: version, SectionID: "tab-synthetic", StartOffset: 0, EndOffset: len("Synthetic"), Quote: "Synthetic", RecordedAt: version.RecordedAt(),
	})
	if err != nil {
		t.Fatalf("create legacy compatibility evidence span: %v", err)
	}
	storedSpan, err := documents.PutEvidenceSpan(ctx, span)
	if err != nil {
		t.Fatalf("put legacy compatibility evidence span: %v", err)
	}
	ingestionRepository := NewIngestionRepository(pool)
	state, err := ingestionRepository.prepareLegacyVersion(ctx, version, testExtractionDerivation(t, version), modelpolicy.DataModePersonal, 5*time.Minute)
	if err != nil {
		t.Fatalf("prepare legacy compatibility extraction run: %v", err)
	}
	var runModelID, runPromptVersion string
	if err := pool.QueryRow(ctx, `
		SELECT model_id, prompt_version
		FROM stacks.extraction_runs
		WHERE id = $1`, state.DerivationID).Scan(&runModelID, &runPromptVersion); err != nil {
		t.Fatalf("read legacy compatibility extraction run: %v", err)
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
		extractionRunID: state.DerivationID, runModelID: runModelID, runPromptVersion: runPromptVersion,
		subjectEntityID: subjectEntity.ID, objectEntityID: objectEntity.ID,
		subjectMentionID: subjectMention.ID, objectMentionID: objectMention.ID,
		evidenceSpanID: storedSpan.ID,
	}
}

func insertLegacyObservation(t *testing.T, ctx context.Context, pool *pgxpool.Pool, input legacyObservationInsert) {
	t.Helper()
	predicate := input.predicate
	if predicate == "" {
		predicate = "legacy_compatibility"
		if input.digestLabel != "" {
			predicate += "_" + input.digestLabel
		}
	}
	row := legacyObservationRow{
		ID: input.id, ExtractionRunID: input.extractionRunID, Predicate: predicate,
		ValidStart: input.validStart, ValidEnd: input.validEnd, RecordedAt: legacyRecordedAt,
		Derivation: "model_extraction", EpistemicStatus: input.epistemicStatus, Confidence: input.confidence,
	}
	if input.subjectEntityID != nil {
		row.SubjectEntityID = *input.subjectEntityID
	}
	if input.objectEntityID != nil {
		row.ObjectEntityID = *input.objectEntityID
	}
	if input.subjectMentionID != nil {
		row.SubjectMentionID = *input.subjectMentionID
	}
	if input.objectMentionID != nil {
		row.ObjectMentionID = *input.objectMentionID
	}
	digest, err := computeObservationDigestV1(legacyObservationWrite{Row: row, Origin: []knowledge.EvidenceID{}})
	if err != nil {
		t.Fatalf("compute legacy observation digest: %v", err)
	}
	var extractionRunID any
	if input.extractionRunID != "" {
		extractionRunID = input.extractionRunID
	}
	if _, err := pool.Exec(ctx, legacyObservationInsertSQL,
		input.id, extractionRunID, input.subjectEntityID, input.objectEntityID,
		input.subjectMentionID, input.objectMentionID, predicate, input.validStart,
		input.validEnd, legacyRecordedAt, "model_extraction", input.epistemicStatus,
		input.confidence, digest[:],
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
