package storage

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/JakeFAU/stacks/core/evidence"
	"github.com/JakeFAU/stacks/core/observation"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestLegacyObservationStorageErrorsRemainPrivateAndUnwrap(t *testing.T) {
	const observationID = "11111111-2222-3333-4444-555555555555"
	const privateMarker = "private driver predicate rationale prompt"
	cause := errors.New(privateMarker)
	for _, testCase := range []struct {
		name       string
		reason     string
		querier    legacyObservationQuerier
		invokeLoad func(legacyObservationQuerier) error
	}{
		{
			name:    "observation_query_row",
			reason:  "legacy_observation_query_failed",
			querier: legacyObservationFakeQuerier{row: legacyObservationFakeRow{err: cause}},
			invokeLoad: func(query legacyObservationQuerier) error {
				_, _, _, err := scanLegacyObservationState(context.Background(), query, observationID)
				return err
			},
		},
		{
			name:    "evidence_query",
			reason:  "legacy_observation_evidence_query_failed",
			querier: legacyObservationFakeQuerier{queryErr: cause},
			invokeLoad: func(query legacyObservationQuerier) error {
				_, err := loadObservationEvidenceOrigin(context.Background(), query, observationID)
				return err
			},
		},
		{
			name:    "evidence_scan",
			reason:  "legacy_observation_evidence_scan_failed",
			querier: legacyObservationFakeQuerier{rows: &legacyObservationFakeRows{next: true, scanErr: cause}},
			invokeLoad: func(query legacyObservationQuerier) error {
				_, err := loadObservationEvidenceOrigin(context.Background(), query, observationID)
				return err
			},
		},
		{
			name:    "evidence_iteration",
			reason:  "legacy_observation_evidence_iteration_failed",
			querier: legacyObservationFakeQuerier{rows: &legacyObservationFakeRows{err: cause}},
			invokeLoad: func(query legacyObservationQuerier) error {
				_, err := loadObservationEvidenceOrigin(context.Background(), query, observationID)
				return err
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			err := testCase.invokeLoad(testCase.querier)
			if err == nil || strings.Contains(err.Error(), privateMarker) ||
				!strings.Contains(err.Error(), testCase.reason) || !strings.Contains(err.Error(), observationID) ||
				!errors.Is(err, cause) {
				t.Fatalf("storage error = %v", err)
			}
		})
	}
}

type legacyObservationFakeQuerier struct {
	row      pgx.Row
	rows     pgx.Rows
	queryErr error
}

func (querier legacyObservationFakeQuerier) QueryRow(context.Context, string, ...any) pgx.Row {
	return querier.row
}

func (querier legacyObservationFakeQuerier) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return querier.rows, querier.queryErr
}

type legacyObservationFakeRow struct{ err error }

func (row legacyObservationFakeRow) Scan(...any) error { return row.err }

type legacyObservationFakeRows struct {
	next    bool
	scanErr error
	err     error
}

func (rows *legacyObservationFakeRows) Close() {}

func (rows *legacyObservationFakeRows) Err() error { return rows.err }

func (rows *legacyObservationFakeRows) CommandTag() pgconn.CommandTag { return pgconn.CommandTag{} }

func (rows *legacyObservationFakeRows) FieldDescriptions() []pgconn.FieldDescription { return nil }

func (rows *legacyObservationFakeRows) Next() bool {
	next := rows.next
	rows.next = false
	return next
}

func (rows *legacyObservationFakeRows) Scan(...any) error { return rows.scanErr }

func (rows *legacyObservationFakeRows) Values() ([]any, error) { return nil, nil }

func (rows *legacyObservationFakeRows) RawValues() [][]byte { return nil }

func (rows *legacyObservationFakeRows) Conn() *pgx.Conn { return nil }

func TestLoadLegacyObservationDecodesCanonicalReferenceAndTimeShapes(t *testing.T) {
	pool := openIntegrationDatabase(t)
	ctx := context.Background()
	fixture := createLegacyObservationFixture(t, pool)
	start := time.Date(2026, time.July, 1, 12, 0, 0, 0, time.UTC)

	for _, subject := range legacyTermShapes {
		for _, object := range legacyTermShapes {
			for _, temporalCase := range []struct {
				name               string
				start, end         *time.Time
				wantKind           observation.TemporalKind
				wantStart, wantEnd time.Time
				hasStart, hasEnd   bool
			}{
				{name: "unknown", wantKind: observation.TemporalUnknown},
				{name: "since", start: timePointer(start), wantKind: observation.TemporalInterval, wantStart: start, hasStart: true},
				{name: "instant", start: timePointer(start), end: timePointer(start), wantKind: observation.TemporalInstant, wantStart: start, hasStart: true},
				{name: "during", start: timePointer(start), end: timePointer(start.Add(time.Hour)), wantKind: observation.TemporalInterval, wantStart: start, wantEnd: start.Add(time.Hour), hasStart: true, hasEnd: true},
			} {
				t.Run(subject.name+"_to_"+object.name+"_"+temporalCase.name, func(t *testing.T) {
					observationID := uuid.NewString()
					subjectEntityID, subjectMentionID := legacyTermReferences(subject, fixture.subjectEntityID, fixture.subjectMentionID)
					objectEntityID, objectMentionID := legacyTermReferences(object, fixture.objectEntityID, fixture.objectMentionID)
					insertLegacyObservation(t, ctx, pool, legacyObservationInsert{
						id: observationID, extractionRunID: fixture.extractionRunID,
						subjectEntityID: subjectEntityID, subjectMentionID: subjectMentionID,
						objectEntityID: objectEntityID, objectMentionID: objectMentionID,
						validStart: temporalCase.start, validEnd: temporalCase.end,
						epistemicStatus: "observed", confidence: float64Pointer(0.75),
					})

					decoded, err := loadLegacyObservation(ctx, pool, observationID)
					if err != nil {
						t.Fatalf("load legacy observation: %v", err)
					}
					statement := decoded.Observation.Statement()
					assertLoadedLegacyTerm(t, "subject", statement.Subject, subject, fixture.subjectEntityID, fixture.subjectMentionID)
					assertLoadedLegacyTerm(t, "object", statement.Object, object, fixture.objectEntityID, fixture.objectMentionID)
					assertLoadedLegacyTime(t, decoded.Observation.ValidTime(), temporalCase.wantKind, temporalCase.wantStart, temporalCase.wantEnd, temporalCase.hasStart, temporalCase.hasEnd)
					assertLoadedLegacyCore(t, decoded, fixture, observationID, "observed", 0.75)
				})
			}
		}
	}
}

func TestLoadLegacyObservationPreservesEvidenceOriginAndRoles(t *testing.T) {
	pool := openIntegrationDatabase(t)
	ctx := context.Background()
	fixture := createLegacyObservationFixture(t, pool)
	observationID := uuid.NewString()
	insertLegacyObservation(t, ctx, pool, legacyObservationInsert{
		id: observationID, extractionRunID: fixture.extractionRunID, epistemicStatus: "inferred",
	})
	linkLegacyObservationEvidence(t, ctx, pool, observationID, fixture.evidenceSpanID)
	updateLegacyObservationDigest(t, ctx, pool, observationID, []evidence.EvidenceID{evidence.EvidenceID(fixture.evidenceSpanID)})
	insertLegacySignal(t, ctx, pool, observationID, fixture.evidenceSpanID, fixture.runModelID, fixture.runPromptVersion)

	decoded, err := loadLegacyObservation(ctx, pool, observationID)
	if err != nil {
		t.Fatalf("load legacy observation: %v", err)
	}
	want := []observation.EvidenceLink{
		{EvidenceID: evidence.EvidenceID(fixture.evidenceSpanID), Role: observation.EvidenceSupporting},
		{EvidenceID: evidence.EvidenceID(fixture.evidenceSpanID), Role: observation.EvidenceContradicting},
	}
	if !sameEvidenceLinks(decoded.Observation.EvidenceLinks(), want) {
		t.Fatalf("canonical evidence links = %#v, want %#v", decoded.Observation.EvidenceLinks(), want)
	}
	if !sameEvidenceIDs(decoded.Compatibility.observationEvidenceOrigin, []evidence.EvidenceID{evidence.EvidenceID(fixture.evidenceSpanID)}) {
		t.Fatalf("private evidence origin = %#v", decoded.Compatibility.observationEvidenceOrigin)
	}
}

func TestLoadLegacyObservationPreservesSignalVertical(t *testing.T) {
	pool := openIntegrationDatabase(t)
	ctx := context.Background()
	fixture := createLegacyObservationFixture(t, pool)
	observationID := uuid.NewString()
	insertLegacyObservation(t, ctx, pool, legacyObservationInsert{
		id: observationID, extractionRunID: fixture.extractionRunID, epistemicStatus: "inferred", confidence: float64Pointer(4.25),
	})
	signalDigest := insertLegacySignal(t, ctx, pool, observationID, fixture.evidenceSpanID, fixture.runModelID, fixture.runPromptVersion)

	decoded, err := loadLegacyObservation(ctx, pool, observationID)
	if err != nil {
		t.Fatalf("load legacy observation: %v", err)
	}
	assertLoadedLegacyCore(t, decoded, fixture, observationID, "inferred", 4.25)
	if decoded.Signal == nil || decoded.Signal.Digest != signalDigest ||
		decoded.Signal.Input.ObservationID != observationID || decoded.Signal.Input.Category != "delegation_autonomy" ||
		decoded.Signal.Input.Direction != "weakening" || decoded.Signal.Input.ExtractionModelID != fixture.runModelID ||
		decoded.Signal.Input.PromptVersion != fixture.runPromptVersion || decoded.Signal.Input.Rationale != "Synthetic source-grounded rationale." ||
		decoded.Signal.Input.Confidence != 0.75 {
		t.Fatalf("signal vertical = %#v", decoded.Signal)
	}
	wantEvidence := []SignalEvidenceInput{
		{EvidenceSpanID: fixture.evidenceSpanID, Role: "contradicting"},
		{EvidenceSpanID: fixture.evidenceSpanID, Role: "supporting"},
	}
	if len(decoded.Signal.Evidence) != len(wantEvidence) {
		t.Fatalf("signal evidence = %#v, want %#v", decoded.Signal.Evidence, wantEvidence)
	}
	for index, want := range wantEvidence {
		if decoded.Signal.Evidence[index] != want {
			t.Fatalf("signal evidence[%d] = %#v, want %#v", index, decoded.Signal.Evidence[index], want)
		}
	}
}

func TestLoadLegacyObservationRejectsSignalDigestMismatchPrivately(t *testing.T) {
	pool := openIntegrationDatabase(t)
	ctx := context.Background()
	fixture := createLegacyObservationFixture(t, pool)
	observationID := uuid.NewString()
	insertLegacyObservation(t, ctx, pool, legacyObservationInsert{
		id: observationID, extractionRunID: fixture.extractionRunID, epistemicStatus: "inferred",
	})
	insertLegacySignal(t, ctx, pool, observationID, fixture.evidenceSpanID, fixture.runModelID, fixture.runPromptVersion)
	corruptDigest := sha256.Sum256([]byte("private signal digest corruption " + observationID))
	if _, err := pool.Exec(ctx, `
		UPDATE stacks.interaction_signals
		SET digest = $2
		WHERE observation_id = $1`, observationID, corruptDigest[:]); err != nil {
		t.Fatalf("corrupt stored signal digest: %v", err)
	}
	_, err := loadLegacyObservation(ctx, pool, observationID)
	if !errors.Is(err, ErrObservationCompatibility) || !strings.Contains(err.Error(), "signal_digest_mismatch") ||
		strings.Contains(err.Error(), "private signal digest corruption") {
		t.Fatalf("signal digest corruption error = %v", err)
	}
}

func TestLoadLegacyObservationPreservesHistoricalSignalDerivationMismatch(t *testing.T) {
	pool := openIntegrationDatabase(t)
	ctx := context.Background()
	fixture := createLegacyObservationFixture(t, pool)
	observationID := uuid.NewString()
	insertLegacyObservation(t, ctx, pool, legacyObservationInsert{
		id: observationID, extractionRunID: fixture.extractionRunID, epistemicStatus: "observed",
	})
	insertLegacySignal(t, ctx, pool, observationID, fixture.evidenceSpanID, "historical-synthetic-model", "historical-synthetic-prompt")

	decoded, err := loadLegacyObservation(ctx, pool, observationID)
	if err != nil {
		t.Fatalf("load historical signal mismatch: %v", err)
	}
	if decoded.Observation.Derivation().Model != fixture.runModelID || decoded.Observation.Derivation().PromptVersion != fixture.runPromptVersion ||
		decoded.Signal == nil || decoded.Signal.Input.ExtractionModelID != "historical-synthetic-model" || decoded.Signal.Input.PromptVersion != "historical-synthetic-prompt" {
		t.Fatalf("decoded derivation/signal = %#v/%#v", decoded.Observation.Derivation(), decoded.Signal)
	}
}

func TestLoadLegacyObservationMarksHistoricalUncited(t *testing.T) {
	pool := openIntegrationDatabase(t)
	ctx := context.Background()
	_ = createLegacyObservationFixture(t, pool)
	observationID := uuid.NewString()
	insertLegacyObservation(t, ctx, pool, legacyObservationInsert{
		id: observationID, predicate: "legacy_historical_" + observationID, epistemicStatus: "observed",
	})

	decoded, err := loadLegacyObservation(ctx, pool, observationID)
	if err != nil {
		t.Fatalf("load historical observation: %v", err)
	}
	if !decoded.Observation.LegacyUncited() || len(decoded.Observation.EvidenceLinks()) != 0 || !decoded.Observation.Derivation().LegacyUnversioned {
		t.Fatalf("decoded historical observation = %#v", decoded.Observation)
	}
}

func TestLoadLegacyObservationRejectsDigestMismatchPrivately(t *testing.T) {
	pool := openIntegrationDatabase(t)
	ctx := context.Background()
	fixture := createLegacyObservationFixture(t, pool)
	observationID := uuid.NewString()
	insertLegacyObservation(t, ctx, pool, legacyObservationInsert{
		id: observationID, extractionRunID: fixture.extractionRunID, epistemicStatus: "observed",
	})
	wrongDigest := sha256.Sum256([]byte("wrong legacy digest " + observationID))
	if _, err := pool.Exec(ctx, `UPDATE stacks.observations SET digest = $2 WHERE id = $1`, observationID, wrongDigest[:]); err != nil {
		t.Fatalf("corrupt stored digest: %v", err)
	}

	_, err := loadLegacyObservation(ctx, pool, observationID)
	if !errors.Is(err, ErrObservationCompatibility) || !strings.Contains(err.Error(), reasonObservationDigestMismatch) ||
		strings.Contains(err.Error(), "legacy_compatibility") || strings.Contains(err.Error(), "Synthetic") {
		t.Fatalf("digest mismatch error = %v", err)
	}
}

func TestLoadLegacyObservationRejectsInvalidStoredPairingsPrivately(t *testing.T) {
	pool := openIntegrationDatabase(t)
	ctx := context.Background()
	fixture := createLegacyObservationFixture(t, pool)
	observationID := uuid.NewString()
	insertLegacyObservation(t, ctx, pool, legacyObservationInsert{
		id: observationID, extractionRunID: fixture.extractionRunID, epistemicStatus: "observed",
	})
	linkLegacyObservationEvidence(t, ctx, pool, observationID, fixture.evidenceSpanID)

	_, err := loadLegacyObservation(ctx, pool, observationID)
	if !errors.Is(err, ErrObservationCompatibility) || !strings.Contains(err.Error(), reasonObservationDigestMismatch) ||
		strings.Contains(err.Error(), "legacy_compatibility") {
		t.Fatalf("invalid stored pairing error = %v", err)
	}
}

func assertLoadedLegacyTerm(t *testing.T, name string, actual observation.Term, shape legacyTermShape, entityID, mentionID string) {
	t.Helper()
	wantKind := observation.TermAbsent
	if shape.entityID {
		wantKind = observation.TermEntity
	} else if shape.mentionID {
		wantKind = observation.TermMention
	}
	if actual.Kind() != wantKind {
		t.Fatalf("%s kind = %v, want %v", name, actual.Kind(), wantKind)
	}
	if shape.entityID {
		gotEntityID, gotMentionID, _ := actual.Entity()
		wantMentionID := ""
		if shape.mentionID {
			wantMentionID = mentionID
		}
		if gotEntityID != entityID || gotMentionID != wantMentionID {
			t.Fatalf("%s entity term = %q/%q", name, gotEntityID, gotMentionID)
		}
	} else if shape.mentionID {
		gotMentionID, _ := actual.MentionID()
		if gotMentionID != mentionID {
			t.Fatalf("%s mention term = %q, want %q", name, gotMentionID, mentionID)
		}
	}
}

func assertLoadedLegacyTime(t *testing.T, actual observation.TemporalExtent, wantKind observation.TemporalKind, wantStart, wantEnd time.Time, hasStart, hasEnd bool) {
	t.Helper()
	if actual.Kind() != wantKind {
		t.Fatalf("valid time kind = %v, want %v", actual.Kind(), wantKind)
	}
	if instant, ok := actual.Instant(); ok {
		if !instant.Equal(wantStart) {
			t.Fatalf("instant = %v, want %v", instant, wantStart)
		}
		return
	}
	start, gotStart, end, gotEnd := actual.Bounds()
	if gotStart != hasStart || gotEnd != hasEnd || gotStart && !start.Equal(wantStart) || gotEnd && !end.Equal(wantEnd) {
		t.Fatalf("valid bounds = (%v, %v, %v, %v)", start, gotStart, end, gotEnd)
	}
}

func assertLoadedLegacyCore(t *testing.T, decoded decodedLegacyObservation, fixture legacyObservationFixture, observationID, status string, confidenceValue float64) {
	t.Helper()
	confidence, ok := decoded.Observation.Confidence()
	if decoded.Observation.ID() != observation.ObservationID(observationID) || !decoded.Observation.RecordedAt().Equal(legacyRecordedAt) ||
		decoded.Observation.Status() != observation.EpistemicStatus(status) || !ok || confidence.Value() != confidenceValue ||
		confidence.Scale() != observation.ConfidenceUnspecifiedLegacy {
		t.Fatalf("canonical observation = %#v", decoded.Observation)
	}
	derivation := decoded.Observation.Derivation()
	if derivation.Method != "model_extraction" || derivation.RunID != fixture.extractionRunID || derivation.Model != fixture.runModelID ||
		derivation.Version != fixture.runPromptVersion || derivation.PromptVersion != fixture.runPromptVersion || derivation.LegacyUnversioned {
		t.Fatalf("canonical derivation = %#v", derivation)
	}
	if decoded.Compatibility.storedDigest == ([sha256.Size]byte{}) {
		t.Fatal("stored digest was not preserved")
	}
}

func linkLegacyObservationEvidence(t *testing.T, ctx context.Context, pool *pgxpool.Pool, observationID, evidenceID string) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		INSERT INTO stacks.observation_evidence (observation_id, evidence_span_id)
		VALUES ($1, $2)`, observationID, evidenceID); err != nil {
		t.Fatalf("link legacy observation evidence: %v", err)
	}
}

func updateLegacyObservationDigest(t *testing.T, ctx context.Context, pool *pgxpool.Pool, observationID string, origin []evidence.EvidenceID) {
	t.Helper()
	var row legacyObservationRow
	var digest []byte
	if err := pool.QueryRow(ctx, `
		SELECT observation.id::text, COALESCE(observation.extraction_run_id::text, ''),
		       COALESCE(observation.subject_entity_id::text, ''), COALESCE(observation.object_entity_id::text, ''),
		       COALESCE(observation.subject_mention_id::text, ''), COALESCE(observation.object_mention_id::text, ''),
		       observation.predicate, observation.valid_start, observation.valid_end, observation.recorded_at,
		       observation.derivation, observation.epistemic_status, observation.confidence, observation.digest
		FROM stacks.observations AS observation
		WHERE observation.id = $1`, observationID).Scan(
		&row.ID, &row.ExtractionRunID, &row.SubjectEntityID, &row.ObjectEntityID,
		&row.SubjectMentionID, &row.ObjectMentionID, &row.Predicate, &row.ValidStart, &row.ValidEnd,
		&row.RecordedAt, &row.Derivation, &row.EpistemicStatus, &row.Confidence, &digest,
	); err != nil {
		t.Fatalf("read legacy observation for digest: %v", err)
	}
	if len(digest) != sha256.Size {
		t.Fatalf("stored digest length = %d, want %d", len(digest), sha256.Size)
	}
	copy(row.Digest[:], digest)
	expected, err := computeObservationDigestV1(legacyObservationWrite{Row: row, Origin: origin})
	if err != nil {
		t.Fatalf("compute legacy observation digest: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE stacks.observations SET digest = $2 WHERE id = $1`, observationID, expected[:]); err != nil {
		t.Fatalf("update legacy observation digest: %v", err)
	}
}

func insertLegacySignal(t *testing.T, ctx context.Context, pool *pgxpool.Pool, observationID, evidenceID, modelID, promptVersion string) [sha256.Size]byte {
	t.Helper()
	signalID := uuid.NewString()
	input := SignalInput{
		ID: signalID, ObservationID: observationID, Category: "delegation_autonomy", Direction: "weakening",
		ExtractionModelID: modelID, PromptVersion: promptVersion, Rationale: "Synthetic source-grounded rationale.", Confidence: 0.75,
	}
	evidence := []SignalEvidenceInput{
		{EvidenceSpanID: evidenceID, Role: "supporting"},
		{EvidenceSpanID: evidenceID, Role: "contradicting"},
	}
	digest, err := ComputeSignalDigest(input, evidence)
	if err != nil {
		t.Fatalf("compute legacy signal digest: %v", err)
	}
	transaction, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("start legacy signal transaction: %v", err)
	}
	defer transaction.Rollback(ctx) //nolint:errcheck // Committed transactions are already closed.
	if _, err := transaction.Exec(ctx, `
		INSERT INTO stacks.interaction_signals
			(id, observation_id, category, direction, extraction_model_id, prompt_version, rationale, confidence, digest)
		VALUES ($1, $2, 'delegation_autonomy', 'weakening', $3, $4, 'Synthetic source-grounded rationale.', 0.75, $5)`,
		signalID, observationID, modelID, promptVersion, digest[:]); err != nil {
		t.Fatalf("insert legacy interaction signal: %v", err)
	}
	for _, role := range []string{"supporting", "contradicting"} {
		if _, err := transaction.Exec(ctx, `
			INSERT INTO stacks.signal_evidence (signal_id, evidence_span_id, role)
			VALUES ($1, $2, $3)`, signalID, evidenceID, role); err != nil {
			t.Fatalf("link %s legacy signal evidence: %v", role, err)
		}
	}
	if err := transaction.Commit(ctx); err != nil {
		t.Fatalf("commit legacy signal transaction: %v", err)
	}
	return digest
}
