package postgres

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/JakeFAU/stacks/core/admission"
	"github.com/JakeFAU/stacks/core/evidence"
	"github.com/JakeFAU/stacks/core/identity"
	"github.com/JakeFAU/stacks/core/observation"
	"github.com/JakeFAU/stacks/core/temporal"
	"github.com/JakeFAU/stacks/core/timepoint"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	temporalTestEntityID       = identity.EntityID("entity:synthetic/alpha")
	temporalTestSecondEntityID = identity.EntityID("entity:synthetic/beta")
	temporalTestPredicate      = observation.Predicate("project.synthetic/state")
	temporalTestObservationID  = observation.ObservationID("observation:synthetic/temporal")
	temporalTestEvidenceID     = evidence.EvidenceID("evidence:synthetic/temporal")
)

var temporalTestRecordedAt = time.Date(2026, time.July, 26, 12, 0, 0, 0, time.UTC)

func TestTemporalQuerySnapshotRequiresNormalizedBoundedSelection(t *testing.T) {
	window := mustTemporalTestWindow(t)
	base := TemporalQuerySelection{
		EntityIDs:   []identity.EntityID{temporalTestEntityID},
		EntityMatch: TemporalEntityMatchAll,
		Predicates:  []observation.Predicate{temporalTestPredicate},
		Selections:  []temporal.TemporalSelection{window},
	}

	tests := []struct {
		name      string
		context   context.Context
		selection func() TemporalQuerySelection
	}{
		{
			name:    "nil context",
			context: nil,
			selection: func() TemporalQuerySelection {
				return base
			},
		},
		{
			name:    "missing entity",
			context: context.Background(),
			selection: func() TemporalQuerySelection {
				value := base
				value.EntityIDs = nil
				return value
			},
		},
		{
			name:    "unnormalized entity",
			context: context.Background(),
			selection: func() TemporalQuerySelection {
				value := base
				value.EntityIDs = []identity.EntityID{" entity:synthetic/alpha "}
				return value
			},
		},
		{
			name:    "unordered entity",
			context: context.Background(),
			selection: func() TemporalQuerySelection {
				value := base
				value.EntityIDs = []identity.EntityID{
					temporalTestSecondEntityID,
					temporalTestEntityID,
				}
				return value
			},
		},
		{
			name:    "duplicate entity",
			context: context.Background(),
			selection: func() TemporalQuerySelection {
				value := base
				value.EntityIDs = []identity.EntityID{
					temporalTestEntityID,
					temporalTestEntityID,
				}
				return value
			},
		},
		{
			name:    "too many entities",
			context: context.Background(),
			selection: func() TemporalQuerySelection {
				value := base
				value.EntityIDs = make([]identity.EntityID, 65)
				for index := range value.EntityIDs {
					value.EntityIDs[index] = identity.EntityID(
						fmt.Sprintf("entity:synthetic/%03d", index),
					)
				}
				return value
			},
		},
		{
			name:    "invalid entity match",
			context: context.Background(),
			selection: func() TemporalQuerySelection {
				value := base
				value.EntityMatch = 0
				return value
			},
		},
		{
			name:    "unnormalized predicate",
			context: context.Background(),
			selection: func() TemporalQuerySelection {
				value := base
				value.Predicates = []observation.Predicate{" project.synthetic/state "}
				return value
			},
		},
		{
			name:    "unordered predicates",
			context: context.Background(),
			selection: func() TemporalQuerySelection {
				value := base
				value.Predicates = []observation.Predicate{
					"project.synthetic/z",
					"project.synthetic/a",
				}
				return value
			},
		},
		{
			name:    "duplicate predicates",
			context: context.Background(),
			selection: func() TemporalQuerySelection {
				value := base
				value.Predicates = []observation.Predicate{
					temporalTestPredicate,
					temporalTestPredicate,
				}
				return value
			},
		},
		{
			name:    "too many predicates",
			context: context.Background(),
			selection: func() TemporalQuerySelection {
				value := base
				value.Predicates = make([]observation.Predicate, 257)
				for index := range value.Predicates {
					value.Predicates[index] = observation.Predicate(
						fmt.Sprintf("project.synthetic/%03d", index),
					)
				}
				return value
			},
		},
		{
			name:    "missing temporal selection",
			context: context.Background(),
			selection: func() TemporalQuerySelection {
				value := base
				value.Selections = nil
				return value
			},
		},
		{
			name:    "too many temporal selections",
			context: context.Background(),
			selection: func() TemporalQuerySelection {
				value := base
				value.Selections = []temporal.TemporalSelection{
					window,
					window,
					window,
				}
				return value
			},
		},
		{
			name:    "invalid temporal selection",
			context: context.Background(),
			selection: func() TemporalQuerySelection {
				value := base
				value.Selections = []temporal.TemporalSelection{{}}
				return value
			},
		},
		{
			name:    "zero knowledge cutoff",
			context: context.Background(),
			selection: func() TemporalQuerySelection {
				value := base
				cutoff := time.Time{}
				value.KnowledgeAsOf = &cutoff
				return value
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pool := newTemporalQueryFakePool()
			_, err := loadTemporalQuerySnapshot(
				test.context,
				pool,
				test.selection(),
				nil,
			)
			if err == nil {
				t.Fatal("loadTemporalQuerySnapshot() error = nil, want validation error")
			}
			if len(pool.options) != 0 {
				t.Fatalf("BeginTx calls = %d, want 0 before validation", len(pool.options))
			}
		})
	}
}

func TestTemporalQuerySnapshotBeginsRepeatableReadOnlyTransaction(t *testing.T) {
	pool := newTemporalQueryFakePool(
		temporalQueryRowsResult([][]any{{string(temporalTestEntityID), true}}),
		temporalQueryRowsResult(nil),
		temporalQueryRowsResult(nil),
		temporalQueryRowsResult(nil),
		temporalQueryRowsResult(nil),
		temporalQueryRowsResult(nil),
	)
	inputCutoff := time.Date(
		2026,
		time.July,
		26,
		8,
		30,
		0,
		987654321,
		time.FixedZone("synthetic-zone", -4*60*60),
	)
	selection := temporalTestSelection(t)
	selection.KnowledgeAsOf = &inputCutoff

	_, err := loadTemporalQuerySnapshot(
		context.Background(),
		pool,
		selection,
		nil,
	)
	if err != nil {
		t.Fatalf("loadTemporalQuerySnapshot() error = %v", err)
	}
	wantOptions := pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	}
	if len(pool.options) != 1 || pool.options[0] != wantOptions {
		t.Fatalf("BeginTx options = %#v, want %#v", pool.options, wantOptions)
	}
	if len(pool.transaction.queries) != 6 {
		t.Fatalf("transaction queries = %d, want 6", len(pool.transaction.queries))
	}
	wantCutoff := timepoint.Normalize(inputCutoff)
	for index, query := range pool.transaction.queries {
		if len(query.arguments) == 0 {
			t.Fatalf("query %d arguments = nil, want normalized cutoff", index)
		}
		gotCutoff, ok := query.arguments[0].(time.Time)
		if !ok || !gotCutoff.Equal(wantCutoff) || gotCutoff.Location() != time.UTC {
			t.Fatalf(
				"query %d cutoff = %#v, want canonical %s",
				index,
				query.arguments[0],
				wantCutoff,
			)
		}
		if strings.Contains(query.sql, string(temporalTestEntityID)) ||
			strings.Contains(query.sql, string(temporalTestPredicate)) {
			t.Fatalf("query %d interpolated a selected private value", index)
		}
	}
	canonicalAuthoritySQL := pool.transaction.queries[1].sql
	for _, required := range []string{
		"relevant_observations AS",
		"relevant_resolution_decisions AS",
		"relevant_admission_decisions AS",
		"resolution_successor.supersedes_id = resolution_predecessor.id",
		"admission_successor.supersedes_id = admission_predecessor.id",
		"decision.digest_version",
		"decision.digest",
	} {
		if !strings.Contains(canonicalAuthoritySQL, required) {
			t.Fatalf(
				"canonical authority SQL does not contain required boundary %q",
				required,
			)
		}
	}
	for _, forbidden := range []string{
		"AND resolution_successor.proposal_id = resolution_predecessor.proposal_id",
		"AND resolution_successor.recorded_at >= resolution_predecessor.recorded_at",
		"AND admission_successor.target_kind = admission_predecessor.target_kind",
		"AND admission_successor.target_id = admission_predecessor.target_id",
		"AND admission_successor.recorded_at >= admission_predecessor.recorded_at",
	} {
		if strings.Contains(canonicalAuthoritySQL, forbidden) {
			t.Fatalf(
				"canonical authority SQL filters malformed successor before validation with %q",
				forbidden,
			)
		}
	}
	authoritySQL := pool.transaction.queries[2].sql
	for _, required := range []string{
		"WITH RECURSIVE",
		"visible_entities AS",
		"reachable_resolution_decisions AS",
		"root_resolution.supersedes_id IS NULL",
		"resolution_successor.supersedes_id = resolution_predecessor.id",
		"resolution_successor.proposal_id = resolution_predecessor.proposal_id",
		"resolution_successor.recorded_at >= resolution_predecessor.recorded_at",
		"effective_resolution_decisions AS",
		"FROM reachable_resolution_decisions AS resolution_successor",
		"resolution_successor.proposal_id = decision.proposal_id",
		"visible_admission_targets AS",
		"target.recorded_at <= parameters.cutoff",
		"JOIN visible_admission_targets AS target",
		"target.target_kind = decision.target_kind",
		"target.target_id = decision.target_id",
		"reachable_admission_decisions AS",
		"root_admission.supersedes_id IS NULL",
		"admission_successor.supersedes_id = admission_predecessor.id",
		"admission_successor.target_kind = admission_predecessor.target_kind",
		"admission_successor.target_id = admission_predecessor.target_id",
		"admission_successor.recorded_at >= admission_predecessor.recorded_at",
		"effective_admissions AS",
		"FROM reachable_admission_decisions AS admission_successor",
		"admission_successor.target_kind = decision.target_kind",
		"admission_successor.target_id = decision.target_id",
		"mention_admission.target_kind = 'mention'",
		"decision_admission.target_kind = 'identity_decision'",
		"observation_admission.target_kind = 'observation'",
		"cardinality(parameters.predicates)",
		"parameters.entity_match = 1",
		"parameters.entity_match = 2",
	} {
		if !strings.Contains(authoritySQL, required) {
			t.Fatalf("authority SQL does not contain required boundary %q", required)
		}
	}
	for _, required := range []string{
		"JOIN stacks_core.source_documents AS source",
		"source.created_at > parameters.cutoff",
	} {
		if !strings.Contains(authoritySQL, required) {
			t.Fatalf(
				"authority evidence SQL does not contain required cutoff boundary %q",
				required,
			)
		}
	}
	evidenceSQL := pool.transaction.queries[4].sql
	for _, required := range []string{
		"JOIN stacks_core.source_documents AS source",
		"source.created_at <= parameters.cutoff",
		"version.content_digest",
		"span.digest_version",
		"span.recorded_at",
	} {
		if !strings.Contains(evidenceSQL, required) {
			t.Fatalf(
				"evidence projection SQL does not contain required cutoff boundary %q",
				required,
			)
		}
	}
	sectionSQL := pool.transaction.queries[5].sql
	for _, required := range []string{
		"section.content",
		"section.parent_id",
		"section.document_version_id = ANY($2::text[])",
	} {
		if !strings.Contains(sectionSQL, required) {
			t.Fatalf(
				"canonical section SQL does not contain required boundary %q",
				required,
			)
		}
	}
	if strings.Contains(authoritySQL, "extraction_run") {
		t.Fatal("observation projection introduced an extraction-run admission gate")
	}
}

func TestTemporalQuerySnapshotCommitsAfterAllAuthorityObservationAndEvidenceReads(t *testing.T) {
	evidenceRows, sectionRows := temporalCanonicalEvidenceRows(
		t,
		temporalTestObservationID,
	)
	evidenceID := evidence.EvidenceID(evidenceRows[0][14].(string))
	value := temporalTestObservationWithEvidenceID(
		t,
		observation.StatusObserved,
		nil,
		evidenceID,
	)
	selection := temporalTestSelection(t)
	selection.EntityIDs = []identity.EntityID{
		temporalTestEntityID,
		temporalTestSecondEntityID,
	}
	selection.EntityMatch = TemporalEntityMatchAny
	pool := newTemporalQueryFakePool(
		temporalQueryRowsResult([][]any{
			{string(temporalTestEntityID), true},
			{string(temporalTestSecondEntityID), false},
		}),
		temporalQueryRowsResult(nil),
		temporalQueryRowsResult([][]any{
			temporalQualificationRow(value, "retained"),
			temporalExcludedQualificationRow(
				"observation:synthetic/authority-excluded",
				string(TemporalCoverageAuthorityExcluded),
			),
			temporalExcludedQualificationRow(
				"observation:synthetic/admission-target-after-cutoff",
				string(TemporalCoverageAuthorityExcluded),
			),
			temporalExcludedQualificationRow(
				"observation:synthetic/unresolved",
				string(TemporalCoverageUnresolvedMention),
			),
			temporalExcludedQualificationRow(
				"observation:synthetic/entity-filtered",
				string(TemporalCoverageEntityFiltered),
			),
			temporalExcludedQualificationRow(
				"observation:synthetic/predicate-filtered",
				string(TemporalCoveragePredicateFiltered),
			),
		}),
		temporalQueryRowsResult([][]any{temporalObservationRow(t, value)}),
		temporalQueryRowsResult(evidenceRows),
		temporalQueryRowsResult(sectionRows),
	)

	snapshot, err := loadTemporalQuerySnapshot(
		context.Background(),
		pool,
		selection,
		nil,
	)
	if err != nil {
		t.Fatalf("loadTemporalQuerySnapshot() error = %v", err)
	}
	wantEvents := []string{
		"begin",
		"query-1",
		"query-2",
		"query-3",
		"query-4",
		"query-5",
		"query-6",
		"commit",
		"rollback",
	}
	if !slices.Equal(pool.events, wantEvents) {
		t.Fatalf("transaction events = %v, want %v", pool.events, wantEvents)
	}
	if len(snapshot.Entities) != 2 ||
		snapshot.Entities[0] != (TemporalEntityRecord{
			EntityID: temporalTestEntityID,
			Known:    true,
		}) ||
		snapshot.Entities[1] != (TemporalEntityRecord{
			EntityID: temporalTestSecondEntityID,
			Known:    false,
		}) {
		t.Fatalf("snapshot entities = %#v", snapshot.Entities)
	}
	if len(snapshot.Observations) != 1 {
		t.Fatalf("snapshot observations = %#v, want one", snapshot.Observations)
	}
	record := snapshot.Observations[0]
	if record.Observation.ID() != value.ID() ||
		record.SubjectGroundingMentionID != "" ||
		record.ObjectGroundingMentionID != "" ||
		len(record.Evidence) != 1 ||
		record.Evidence[0].EvidenceID != evidenceID {
		t.Fatalf("snapshot observation = %#v", record)
	}
	subjectID, _, subjectIsEntity := record.Subject.Entity()
	if !subjectIsEntity || identity.EntityID(subjectID) != temporalTestEntityID {
		t.Fatalf("resolved subject = %#v, want selected direct entity", record.Subject)
	}
	wantCoverageReasons := []TemporalCoverageReason{
		TemporalCoverageAuthorityExcluded,
		TemporalCoverageAuthorityExcluded,
		TemporalCoverageUnresolvedMention,
		TemporalCoverageEntityFiltered,
		TemporalCoveragePredicateFiltered,
	}
	wantCoverageObservationIDs := []observation.ObservationID{
		"observation:synthetic/authority-excluded",
		"observation:synthetic/admission-target-after-cutoff",
		"observation:synthetic/unresolved",
		"observation:synthetic/entity-filtered",
		"observation:synthetic/predicate-filtered",
	}
	if len(snapshot.Coverage) != len(wantCoverageReasons) {
		t.Fatalf("snapshot coverage = %#v, want five closed exclusions", snapshot.Coverage)
	}
	for index, reason := range wantCoverageReasons {
		if snapshot.Coverage[index].Reason != reason ||
			snapshot.Coverage[index].ObservationID != wantCoverageObservationIDs[index] ||
			snapshot.Coverage[index].EntityID != temporalTestEntityID ||
			snapshot.Coverage[index].Predicate != temporalTestPredicate {
			t.Fatalf(
				"snapshot coverage %d = %#v, want reason %q",
				index,
				snapshot.Coverage[index],
				reason,
			)
		}
	}
}

func TestTemporalQuerySnapshotRejectsDuplicateRetainedAuthorityRows(t *testing.T) {
	value := temporalTestObservation(t, observation.StatusObserved, nil)
	duplicate := temporalQualificationRow(value, temporalCoverageRetained)
	pool := newTemporalQueryFakePool(
		temporalQueryRowsResult([][]any{{string(temporalTestEntityID), true}}),
		temporalQueryRowsResult(nil),
		temporalQueryRowsResult([][]any{duplicate, duplicate}),
	)

	_, err := loadTemporalQuerySnapshot(
		context.Background(),
		pool,
		temporalTestSelection(t),
		nil,
	)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("loadTemporalQuerySnapshot() error = %v, want ErrConflict", err)
	}
	if err.Error() != "load temporal query snapshot: validate observation authority failed" {
		t.Fatalf("loadTemporalQuerySnapshot() error = %q, want bounded conflict", err)
	}
	if pool.transaction.commitCalls != 0 || pool.transaction.rollbackCalls != 1 {
		t.Fatalf(
			"commit/rollback calls = %d/%d, want 0/1",
			pool.transaction.commitCalls,
			pool.transaction.rollbackCalls,
		)
	}
}

func TestTemporalQuerySnapshotRejectsMalformedCanonicalAuthorityChain(t *testing.T) {
	resolutionRows := temporalResolutionAuthorityRows(t)
	admissionRows := temporalAdmissionAuthorityRows(t)
	tests := []struct {
		name       string
		baseRows   [][]any
		rowIndex   int
		fieldIndex int
		corrupt    any
	}{
		{
			name:       "resolution predecessor digest version",
			baseRows:   resolutionRows,
			rowIndex:   0,
			fieldIndex: 11,
			corrupt:    "stacks.identity-resolution-decision.invalid",
		},
		{
			name:       "resolution predecessor digest",
			baseRows:   resolutionRows,
			rowIndex:   0,
			fieldIndex: 12,
			corrupt:    temporalCorruptDigest("corrupt-resolution-predecessor"),
		},
		{
			name:       "resolution successor digest version",
			baseRows:   resolutionRows,
			rowIndex:   1,
			fieldIndex: 11,
			corrupt:    "stacks.identity-resolution-decision.invalid",
		},
		{
			name:       "resolution successor digest",
			baseRows:   resolutionRows,
			rowIndex:   1,
			fieldIndex: 12,
			corrupt:    temporalCorruptDigest("corrupt-resolution-successor"),
		},
		{
			name:       "admission predecessor digest version",
			baseRows:   admissionRows,
			rowIndex:   0,
			fieldIndex: 11,
			corrupt:    "stacks.admission-decision.invalid",
		},
		{
			name:       "admission predecessor digest",
			baseRows:   admissionRows,
			rowIndex:   0,
			fieldIndex: 12,
			corrupt:    temporalCorruptDigest("corrupt-admission-predecessor"),
		},
		{
			name:       "admission successor digest version",
			baseRows:   admissionRows,
			rowIndex:   1,
			fieldIndex: 11,
			corrupt:    "stacks.admission-decision.invalid",
		},
		{
			name:       "admission successor digest",
			baseRows:   admissionRows,
			rowIndex:   1,
			fieldIndex: 12,
			corrupt:    temporalCorruptDigest("corrupt-admission-successor"),
		},
		{
			name: "resolution successor proposal mismatch",
			baseRows: temporalResolutionAuthorityRowsWithSuccessor(
				t,
				"proposal:synthetic/other",
				temporalTestRecordedAt.Add(time.Minute),
			),
			rowIndex: -1,
		},
		{
			name: "resolution successor precedes predecessor",
			baseRows: temporalResolutionAuthorityRowsWithSuccessor(
				t,
				"proposal:synthetic/temporal",
				temporalTestRecordedAt.Add(-time.Minute),
			),
			rowIndex: -1,
		},
		{
			name: "admission successor target mismatch",
			baseRows: temporalAdmissionAuthorityRowsWithSuccessor(
				t,
				"observation:synthetic/other",
				temporalTestRecordedAt.Add(time.Minute),
			),
			rowIndex: -1,
		},
		{
			name: "admission successor precedes predecessor",
			baseRows: temporalAdmissionAuthorityRowsWithSuccessor(
				t,
				string(temporalTestObservationID),
				temporalTestRecordedAt.Add(-time.Minute),
			),
			rowIndex: -1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rows := cloneTemporalQueryRows(test.baseRows)
			if test.rowIndex >= 0 {
				rows[test.rowIndex][test.fieldIndex] = test.corrupt
			}
			pool := newTemporalQueryFakePool(
				temporalQueryRowsResult([][]any{{
					string(temporalTestEntityID),
					true,
				}}),
				temporalQueryRowsResult(rows),
				temporalQueryRowsResult(nil),
				temporalQueryRowsResult(nil),
				temporalQueryRowsResult(nil),
			)

			_, err := loadTemporalQuerySnapshot(
				context.Background(),
				pool,
				temporalTestSelection(t),
				nil,
			)
			if !errors.Is(err, ErrConflict) {
				t.Fatalf("loadTemporalQuerySnapshot() error = %v, want ErrConflict", err)
			}
			if err.Error() != "load temporal query snapshot: validate canonical authority failed" {
				t.Fatalf("loadTemporalQuerySnapshot() error = %q, want bounded authority conflict", err)
			}
			if len(pool.transaction.queries) != 2 {
				t.Fatalf(
					"transaction queries = %d, want authority validation to stop qualification",
					len(pool.transaction.queries),
				)
			}
			if pool.transaction.commitCalls != 0 ||
				pool.transaction.rollbackCalls != 1 {
				t.Fatalf(
					"commit/rollback calls = %d/%d, want 0/1",
					pool.transaction.commitCalls,
					pool.transaction.rollbackCalls,
				)
			}
		})
	}
}

func TestTemporalQuerySnapshotVerifiesCanonicalAuthorityBeforeQualification(t *testing.T) {
	authorityRows := append(
		temporalResolutionAuthorityRows(t),
		temporalAdmissionAuthorityRows(t)...,
	)
	pool := newTemporalQueryFakePool(
		temporalQueryRowsResult([][]any{{
			string(temporalTestEntityID),
			true,
		}}),
		temporalQueryRowsResult(authorityRows),
		temporalQueryRowsResult(nil),
		temporalQueryRowsResult(nil),
		temporalQueryRowsResult(nil),
		temporalQueryRowsResult(nil),
	)

	_, err := loadTemporalQuerySnapshot(
		context.Background(),
		pool,
		temporalTestSelection(t),
		nil,
	)
	if err != nil {
		t.Fatalf("loadTemporalQuerySnapshot() error = %v", err)
	}
	if len(pool.transaction.queries) != 6 ||
		pool.transaction.commitCalls != 1 ||
		pool.transaction.rollbackCalls != 1 {
		t.Fatalf(
			"queries/commit/rollback = %d/%d/%d, want 6/1/1",
			len(pool.transaction.queries),
			pool.transaction.commitCalls,
			pool.transaction.rollbackCalls,
		)
	}
}

func TestTemporalQuerySnapshotRejectsMalformedCanonicalEvidence(t *testing.T) {
	tests := []struct {
		name    string
		corrupt func(evidenceRows, sectionRows [][]any)
	}{
		{
			name: "document version ID",
			corrupt: func(evidenceRows, _ [][]any) {
				evidenceRows[0][5] = "document-version:synthetic/corrupt"
			},
		},
		{
			name: "document version digest",
			corrupt: func(evidenceRows, _ [][]any) {
				evidenceRows[0][7] = temporalCorruptDigest("corrupt-document")
			},
		},
		{
			name: "document version recorded time",
			corrupt: func(evidenceRows, _ [][]any) {
				evidenceRows[0][13] = temporalTestRecordedAt.Add(time.Nanosecond)
			},
		},
		{
			name: "evidence span ID",
			corrupt: func(evidenceRows, _ [][]any) {
				evidenceRows[0][14] = "evidence:synthetic/corrupt"
			},
		},
		{
			name: "evidence span digest",
			corrupt: func(evidenceRows, _ [][]any) {
				evidenceRows[0][18] = temporalCorruptDigest("corrupt-span")
			},
		},
		{
			name: "evidence span recorded time",
			corrupt: func(evidenceRows, _ [][]any) {
				evidenceRows[0][22] = temporalTestRecordedAt.Add(time.Nanosecond)
			},
		},
		{
			name: "section content",
			corrupt: func(_ [][]any, sectionRows [][]any) {
				sectionRows[0][7] = "synthetic quote was altered"
			},
		},
		{
			name: "quote bounds",
			corrupt: func(evidenceRows, _ [][]any) {
				evidenceRows[0][20] = len("synthetic quot")
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			evidenceRows, sectionRows := temporalCanonicalEvidenceRows(
				t,
				temporalTestObservationID,
			)
			value := temporalTestObservationWithEvidenceID(
				t,
				observation.StatusObserved,
				nil,
				evidence.EvidenceID(evidenceRows[0][14].(string)),
			)
			test.corrupt(evidenceRows, sectionRows)
			pool := newTemporalQueryFakePool(
				temporalQueryRowsResult([][]any{{
					string(temporalTestEntityID),
					true,
				}}),
				temporalQueryRowsResult(nil),
				temporalQueryRowsResult([][]any{
					temporalQualificationRow(value, temporalCoverageRetained),
				}),
				temporalQueryRowsResult([][]any{
					temporalObservationRow(t, value),
				}),
				temporalQueryRowsResult(evidenceRows),
				temporalQueryRowsResult(sectionRows),
			)

			_, err := loadTemporalQuerySnapshot(
				context.Background(),
				pool,
				temporalTestSelection(t),
				nil,
			)
			if !errors.Is(err, ErrConflict) {
				t.Fatalf("loadTemporalQuerySnapshot() error = %v, want ErrConflict", err)
			}
			if err.Error() != "load temporal query snapshot: validate canonical evidence failed" {
				t.Fatalf("loadTemporalQuerySnapshot() error = %q, want bounded evidence conflict", err)
			}
			if len(pool.transaction.queries) != 6 {
				t.Fatalf(
					"transaction queries = %d, want complete batch evidence validation",
					len(pool.transaction.queries),
				)
			}
			if pool.transaction.commitCalls != 0 ||
				pool.transaction.rollbackCalls != 1 {
				t.Fatalf(
					"commit/rollback calls = %d/%d, want 0/1",
					pool.transaction.commitCalls,
					pool.transaction.rollbackCalls,
				)
			}
		})
	}
}

func TestTemporalQuerySnapshotRollsBackAndPreservesCancellation(t *testing.T) {
	privateFailure := fmt.Errorf(
		"private-id predicate SQL https://private.invalid: %w",
		context.Canceled,
	)
	pool := newTemporalQueryFakePool(
		temporalQueryRowsResult([][]any{{string(temporalTestEntityID), true}}),
		temporalQueryRowsResult(nil),
		temporalQueryRowsError(privateFailure),
	)
	ctx := context.WithValue(
		context.Background(),
		temporalObserverContextKey{},
		"caller",
	)

	_, err := loadTemporalQuerySnapshot(
		ctx,
		pool,
		temporalTestSelection(t),
		nil,
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("loadTemporalQuerySnapshot() error = %v, want context.Canceled", err)
	}
	assertTemporalSnapshotErrorIsBounded(t, err)
	if pool.transaction.commitCalls != 0 || pool.transaction.rollbackCalls != 1 {
		t.Fatalf(
			"commit/rollback calls = %d/%d, want 0/1",
			pool.transaction.commitCalls,
			pool.transaction.rollbackCalls,
		)
	}
	for index, query := range pool.transaction.queries {
		if query.context != ctx {
			t.Fatalf("query %d context was replaced", index)
		}
	}
	if pool.transaction.rollbackContexts[0] != ctx {
		t.Fatal("rollback context was replaced")
	}
}

func TestTemporalQuerySnapshotDoesNotApplyValidTimeOrConfidencePolicy(t *testing.T) {
	confidence, err := observation.NewUnitIntervalConfidence(0.01)
	if err != nil {
		t.Fatalf("observation.NewUnitIntervalConfidence() error = %v", err)
	}
	evidenceRows, sectionRows := temporalCanonicalEvidenceRows(
		t,
		temporalTestObservationID,
	)
	value := temporalTestObservationWithEvidenceID(
		t,
		observation.StatusRejected,
		&confidence,
		evidence.EvidenceID(evidenceRows[0][14].(string)),
	)
	pool := newTemporalQueryFakePool(
		temporalQueryRowsResult([][]any{{string(temporalTestEntityID), true}}),
		temporalQueryRowsResult(nil),
		temporalQueryRowsResult([][]any{temporalQualificationRow(
			value,
			"retained",
		)}),
		temporalQueryRowsResult([][]any{temporalObservationRow(t, value)}),
		temporalQueryRowsResult(evidenceRows),
		temporalQueryRowsResult(sectionRows),
	)

	snapshot, err := loadTemporalQuerySnapshot(
		context.Background(),
		pool,
		temporalTestSelection(t),
		nil,
	)
	if err != nil {
		t.Fatalf("loadTemporalQuerySnapshot() error = %v", err)
	}
	if len(snapshot.Observations) != 1 {
		t.Fatalf("snapshot observations = %#v, want rejected out-of-window candidate", snapshot.Observations)
	}
	got := snapshot.Observations[0].Observation
	gotConfidence, hasConfidence := got.Confidence()
	if got.Status() != observation.StatusRejected ||
		!hasConfidence ||
		gotConfidence.Value() != confidence.Value() {
		t.Fatalf(
			"retained status/confidence = %q/(%v,%v), want rejected/(0.01,true)",
			got.Status(),
			gotConfidence.Value(),
			hasConfidence,
		)
	}
	for _, query := range pool.transaction.queries {
		for _, argument := range query.arguments {
			if argument == temporalTestRecordedAt.Add(365*24*time.Hour) {
				t.Fatal("valid-time selection leaked into PostgreSQL query arguments")
			}
		}
	}
	authoritySQL := pool.transaction.queries[2].sql
	for _, forbidden := range []string{
		"valid_start",
		"valid_end",
		"confidence_value",
		"epistemic_status",
	} {
		if strings.Contains(authoritySQL, forbidden) {
			t.Fatalf("authority eligibility SQL contains core policy field %q", forbidden)
		}
	}
	observationSQL := pool.transaction.queries[3].sql
	for _, projected := range []string{
		"value.valid_start",
		"value.valid_end",
		"value.confidence_value",
		"value.epistemic_status",
	} {
		if !strings.Contains(observationSQL, projected) {
			t.Fatalf("canonical observation projection omitted %q", projected)
		}
	}
}

func TestTemporalQuerySnapshotFinishesBoundedObservationOnSuccessAndFailure(t *testing.T) {
	selection := temporalTestSelection(t)
	selection.EntityIDs = []identity.EntityID{
		temporalTestEntityID,
		temporalTestSecondEntityID,
	}
	selection.Predicates = []observation.Predicate{
		"project.synthetic/a",
		"project.synthetic/b",
		"project.synthetic/c",
	}
	cutoff := temporalTestRecordedAt.Add(time.Minute)
	selection.KnowledgeAsOf = &cutoff
	wantAttributes := TemporalSnapshotAttributes{
		HasKnowledgeCutoff:   true,
		EntityCountBucket:    TemporalSnapshotCountTwoToFive,
		PredicateCountBucket: TemporalSnapshotCountTwoToFive,
		SelectionCountBucket: TemporalSnapshotCountOne,
	}

	t.Run("success", func(t *testing.T) {
		pool := newTemporalQueryFakePool(
			temporalQueryRowsResult([][]any{
				{string(temporalTestEntityID), true},
				{string(temporalTestSecondEntityID), true},
			}),
			temporalQueryRowsResult(nil),
			temporalQueryRowsResult(nil),
			temporalQueryRowsResult(nil),
			temporalQueryRowsResult(nil),
			temporalQueryRowsResult(nil),
		)
		observer := &recordingTemporalSnapshotObserver{}

		_, err := loadTemporalQuerySnapshot(
			context.Background(),
			pool,
			selection,
			observer,
		)
		if err != nil {
			t.Fatalf("loadTemporalQuerySnapshot() error = %v", err)
		}
		if observer.starts != 1 ||
			observer.finishes != 1 ||
			observer.finishErrors[0] != nil ||
			observer.attributes[0] != wantAttributes {
			t.Fatalf("observer = %#v, want one bounded successful observation", observer)
		}
		if pool.beginContexts[0].Value(temporalObserverContextKey{}) != "observed" {
			t.Fatal("observer context was not propagated into transaction")
		}
	})

	t.Run("failure", func(t *testing.T) {
		pool := newTemporalQueryFakePool()
		pool.beginErr = fmt.Errorf("private database URL: %w", context.DeadlineExceeded)
		observer := &recordingTemporalSnapshotObserver{}

		_, err := loadTemporalQuerySnapshot(
			context.Background(),
			pool,
			selection,
			observer,
		)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("loadTemporalQuerySnapshot() error = %v, want deadline", err)
		}
		assertTemporalSnapshotErrorIsBounded(t, err)
		if observer.starts != 1 ||
			observer.finishes != 1 ||
			!errors.Is(observer.finishErrors[0], context.DeadlineExceeded) ||
			observer.attributes[0] != wantAttributes {
			t.Fatalf("observer = %#v, want one bounded failed observation", observer)
		}
		assertTemporalSnapshotErrorIsBounded(t, observer.finishErrors[0])
	})
}

func assertTemporalSnapshotErrorIsBounded(t *testing.T, err error) {
	t.Helper()
	for current := err; current != nil; current = errors.Unwrap(current) {
		message := current.Error()
		for _, privateValue := range []string{
			"private-id",
			"predicate",
			"SQL",
			"https://",
			"private database URL",
		} {
			if strings.Contains(message, privateValue) {
				t.Fatalf(
					"bounded error chain leaked private failure %q: %q",
					privateValue,
					message,
				)
			}
		}
	}
}

func temporalTestSelection(t *testing.T) TemporalQuerySelection {
	t.Helper()
	return TemporalQuerySelection{
		EntityIDs:   []identity.EntityID{temporalTestEntityID},
		EntityMatch: TemporalEntityMatchAll,
		Predicates:  []observation.Predicate{temporalTestPredicate},
		Selections:  []temporal.TemporalSelection{mustTemporalTestWindow(t)},
	}
}

func mustTemporalTestWindow(t *testing.T) temporal.TemporalSelection {
	t.Helper()
	selection, err := temporal.Between(
		"synthetic-window",
		temporalTestRecordedAt.Add(365*24*time.Hour),
		temporalTestRecordedAt.Add(366*24*time.Hour),
	)
	if err != nil {
		t.Fatalf("temporal.Between() error = %v", err)
	}
	return selection
}

func temporalTestObservation(
	t *testing.T,
	status observation.EpistemicStatus,
	confidence *observation.Confidence,
) observation.Observation {
	t.Helper()
	return temporalTestObservationWithEvidenceID(
		t,
		status,
		confidence,
		temporalTestEvidenceID,
	)
}

func temporalTestObservationWithEvidenceID(
	t *testing.T,
	status observation.EpistemicStatus,
	confidence *observation.Confidence,
	evidenceID evidence.EvidenceID,
) observation.Observation {
	t.Helper()
	subject, err := observation.NewEntityTerm(string(temporalTestEntityID), "")
	if err != nil {
		t.Fatalf("observation.NewEntityTerm() error = %v", err)
	}
	object, err := observation.NewTextTerm("synthetic-state")
	if err != nil {
		t.Fatalf("observation.NewTextTerm() error = %v", err)
	}
	validTime, err := observation.AtTime(temporalTestRecordedAt)
	if err != nil {
		t.Fatalf("observation.AtTime() error = %v", err)
	}
	value, err := observation.NewObservation(observation.ObservationInput{
		ID: temporalTestObservationID,
		Statement: observation.Statement{
			Subject:   subject,
			Predicate: temporalTestPredicate,
			Object:    object,
		},
		ValidTime:  validTime,
		RecordedAt: temporalTestRecordedAt,
		Evidence: []observation.EvidenceLink{{
			EvidenceID: evidenceID,
			Role:       observation.EvidenceSupporting,
		}},
		Derivation: observation.Derivation{
			Method:  "synthetic",
			Version: "synthetic-v1",
		},
		Status:     status,
		Confidence: confidence,
	})
	if err != nil {
		t.Fatalf("observation.NewObservation() error = %v", err)
	}
	return value
}

func temporalQualificationRow(
	value observation.Observation,
	classification string,
) []any {
	subjectID, _, _ := value.Statement().Subject.Entity()
	objectText, _ := value.Statement().Object.Text()
	return []any{
		string(value.ID()),
		string(value.Statement().Predicate),
		termKindEntity,
		nil,
		subjectID,
		nil,
		termKindText,
		objectText,
		nil,
		nil,
		classification,
		string(temporalTestEntityID),
	}
}

func temporalExcludedQualificationRow(
	id string,
	classification string,
) []any {
	return []any{
		id,
		string(temporalTestPredicate),
		termKindEntity,
		nil,
		string(temporalTestEntityID),
		nil,
		termKindText,
		"synthetic-state",
		nil,
		nil,
		classification,
		string(temporalTestEntityID),
	}
}

func temporalObservationRow(t *testing.T, value observation.Observation) []any {
	t.Helper()
	subject, err := encodeObservationTerm(value.Statement().Subject)
	if err != nil {
		t.Fatalf("encodeObservationTerm(subject) error = %v", err)
	}
	object, err := encodeObservationTerm(value.Statement().Object)
	if err != nil {
		t.Fatalf("encodeObservationTerm(object) error = %v", err)
	}
	validTime, err := encodeTemporalExtent(value.ValidTime())
	if err != nil {
		t.Fatalf("encodeTemporalExtent() error = %v", err)
	}
	derivation := value.Derivation()
	var derivationRunID, derivationModel, derivationPromptVersion any
	if derivation.RunID != "" {
		derivationRunID = derivation.RunID
	}
	if derivation.Model != "" {
		derivationModel = derivation.Model
		derivationPromptVersion = derivation.PromptVersion
	}
	var confidenceValue, confidenceScale any
	if confidence, ok := value.Confidence(); ok {
		confidenceValueValue := confidence.Value()
		confidenceScaleValue := string(confidence.Scale())
		confidenceValue = &confidenceValueValue
		confidenceScale = &confidenceScaleValue
	}
	digest := value.Digest()
	return []any{
		string(value.ID()),
		subject.kind,
		subject.text,
		subject.mentionID,
		subject.entityID,
		subject.groundingMentionID,
		string(value.Statement().Predicate),
		object.kind,
		object.text,
		object.mentionID,
		object.entityID,
		object.groundingMentionID,
		validTime.kind,
		validTime.hasStart,
		validTime.start,
		validTime.hasEnd,
		validTime.end,
		value.RecordedAt(),
		derivation.Method,
		derivation.Version,
		derivationRunID,
		derivationModel,
		derivationPromptVersion,
		string(value.Status()),
		confidenceValue,
		confidenceScale,
		value.DigestVersion(),
		digest[:],
	}
}

func temporalCanonicalEvidenceRows(
	t *testing.T,
	observationID observation.ObservationID,
) ([][]any, [][]any) {
	t.Helper()
	section, err := evidence.NewSection(evidence.SectionInput{
		ID:    "section:synthetic/temporal",
		Title: "Synthetic Section",
		Path:  []string{"Synthetic Document", "Synthetic Section"},
		Order: 1,
		Role:  "body",
		Text:  "synthetic quote plus context",
	})
	if err != nil {
		t.Fatalf("evidence.NewSection() error = %v", err)
	}
	document, err := evidence.NewDocumentVersion(evidence.DocumentVersionInput{
		Provider:           "synthetic",
		ProviderDocumentID: "temporal",
		Title:              "Synthetic Document",
		Locator:            "synthetic://temporal",
		ProviderVersion:    "synthetic-v1",
		ModifiedAt:         temporalTestRecordedAt,
		RecordedAt:         temporalTestRecordedAt,
		Sections:           []evidence.Section{section},
	})
	if err != nil {
		t.Fatalf("evidence.NewDocumentVersion() error = %v", err)
	}
	span, err := evidence.NewEvidenceSpan(evidence.EvidenceSpanInput{
		Document:    document,
		SectionID:   section.ID(),
		StartOffset: 0,
		EndOffset:   len("synthetic quote"),
		Quote:       "synthetic quote",
		RecordedAt:  temporalTestRecordedAt,
	})
	if err != nil {
		t.Fatalf("evidence.NewEvidenceSpan() error = %v", err)
	}
	sourceDocumentID := deriveOpaqueID(
		sourceDocumentIDVersion,
		[]byte(document.Provider()),
		[]byte(document.ProviderDocumentID()),
	)
	documentDigest := document.Digest()
	documentVersionID := deriveOpaqueID(
		documentVersionIDVersion,
		[]byte(sourceDocumentID),
		[]byte(document.DigestVersion()),
		documentDigest[:],
	)
	spanDigest := span.Digest()
	return [][]any{{
			string(observationID),
			string(observation.EvidenceSupporting),
			sourceDocumentID,
			document.Provider(),
			document.ProviderDocumentID(),
			documentVersionID,
			document.DigestVersion(),
			documentDigest[:],
			document.Title(),
			document.Locator(),
			document.ProviderVersion(),
			document.ModifiedAt(),
			document.SourceTime(),
			document.RecordedAt(),
			string(span.ID()),
			documentVersionID,
			span.SectionID(),
			span.DigestVersion(),
			spanDigest[:],
			span.StartOffset(),
			span.EndOffset(),
			span.Text(),
			span.RecordedAt(),
		}}, [][]any{{
			documentVersionID,
			section.ID(),
			section.Title(),
			section.ParentID(),
			section.Path(),
			section.Order(),
			section.Role(),
			section.Text(),
		}}
}

func temporalResolutionAuthorityRows(t *testing.T) [][]any {
	t.Helper()
	predecessor, err := identity.NewResolutionDecision(identity.ResolutionDecisionInput{
		ID:         "decision:synthetic/temporal-predecessor",
		ProposalID: "proposal:synthetic/temporal",
		Outcome:    identity.DecisionAccepted,
		EntityID:   temporalTestEntityID,
		Authority:  identity.AuthorityReviewer,
		ReasonCode: "synthetic-review",
		RecordedAt: temporalTestRecordedAt,
	})
	if err != nil {
		t.Fatalf("identity.NewResolutionDecision(predecessor) error = %v", err)
	}
	successor, err := identity.NewResolutionDecision(identity.ResolutionDecisionInput{
		ID:           "decision:synthetic/temporal-successor",
		ProposalID:   predecessor.ProposalID(),
		Outcome:      identity.DecisionAccepted,
		EntityID:     temporalTestSecondEntityID,
		Authority:    identity.AuthorityReviewer,
		ReasonCode:   "synthetic-correction",
		RecordedAt:   temporalTestRecordedAt.Add(time.Minute),
		SupersedesID: predecessor.ID(),
	})
	if err != nil {
		t.Fatalf("identity.NewResolutionDecision(successor) error = %v", err)
	}
	return [][]any{
		temporalResolutionAuthorityRow(predecessor),
		temporalResolutionAuthorityRow(successor),
	}
}

func temporalResolutionAuthorityRow(value identity.ResolutionDecision) []any {
	var entityID any
	if value.EntityID() != "" {
		entityID = string(value.EntityID())
	}
	var supersedesID any
	if value.SupersedesID() != "" {
		supersedesID = string(value.SupersedesID())
	}
	digest := value.Digest()
	return []any{
		"resolution",
		string(value.ID()),
		string(value.ProposalID()),
		nil,
		nil,
		string(value.Outcome()),
		entityID,
		string(value.Authority()),
		value.ReasonCode(),
		value.RecordedAt(),
		supersedesID,
		value.DigestVersion(),
		digest[:],
	}
}

func temporalResolutionAuthorityRowsWithSuccessor(
	t *testing.T,
	proposalID identity.ProposalID,
	recordedAt time.Time,
) [][]any {
	t.Helper()
	rows := temporalResolutionAuthorityRows(t)
	successor, err := identity.NewResolutionDecision(
		identity.ResolutionDecisionInput{
			ID:           "decision:synthetic/temporal-successor",
			ProposalID:   proposalID,
			Outcome:      identity.DecisionAccepted,
			EntityID:     temporalTestSecondEntityID,
			Authority:    identity.AuthorityReviewer,
			ReasonCode:   "synthetic-correction",
			RecordedAt:   recordedAt,
			SupersedesID: identity.DecisionID(rows[0][1].(string)),
		},
	)
	if err != nil {
		t.Fatalf("identity.NewResolutionDecision(successor) error = %v", err)
	}
	rows[1] = temporalResolutionAuthorityRow(successor)
	return rows
}

func temporalAdmissionAuthorityRows(t *testing.T) [][]any {
	t.Helper()
	predecessor, err := admission.NewDecision(admission.DecisionInput{
		ID:         "admission:synthetic/temporal-predecessor",
		TargetKind: admission.TargetObservation,
		TargetID:   string(temporalTestObservationID),
		Outcome:    admission.Admitted,
		ReasonCode: "synthetic-review",
		Authority:  admission.AuthorityReviewer,
		RecordedAt: temporalTestRecordedAt,
	})
	if err != nil {
		t.Fatalf("admission.NewDecision(predecessor) error = %v", err)
	}
	successor, err := admission.NewDecision(admission.DecisionInput{
		ID:           "admission:synthetic/temporal-successor",
		TargetKind:   predecessor.TargetKind(),
		TargetID:     predecessor.TargetID(),
		Outcome:      admission.Retired,
		ReasonCode:   "synthetic-correction",
		Authority:    admission.AuthorityReviewer,
		RecordedAt:   temporalTestRecordedAt.Add(time.Minute),
		SupersedesID: predecessor.ID(),
	})
	if err != nil {
		t.Fatalf("admission.NewDecision(successor) error = %v", err)
	}
	return [][]any{
		temporalAdmissionAuthorityRow(predecessor),
		temporalAdmissionAuthorityRow(successor),
	}
}

func temporalAdmissionAuthorityRow(value admission.Decision) []any {
	var supersedesID any
	if value.SupersedesID() != "" {
		supersedesID = value.SupersedesID()
	}
	digest := value.Digest()
	return []any{
		"admission",
		value.ID(),
		nil,
		string(value.TargetKind()),
		value.TargetID(),
		string(value.Outcome()),
		nil,
		string(value.Authority()),
		value.ReasonCode(),
		value.RecordedAt(),
		supersedesID,
		value.DigestVersion(),
		digest[:],
	}
}

func temporalAdmissionAuthorityRowsWithSuccessor(
	t *testing.T,
	targetID string,
	recordedAt time.Time,
) [][]any {
	t.Helper()
	rows := temporalAdmissionAuthorityRows(t)
	successor, err := admission.NewDecision(admission.DecisionInput{
		ID:           "admission:synthetic/temporal-successor",
		TargetKind:   admission.TargetObservation,
		TargetID:     targetID,
		Outcome:      admission.Retired,
		ReasonCode:   "synthetic-correction",
		Authority:    admission.AuthorityReviewer,
		RecordedAt:   recordedAt,
		SupersedesID: rows[0][1].(string),
	})
	if err != nil {
		t.Fatalf("admission.NewDecision(successor) error = %v", err)
	}
	rows[1] = temporalAdmissionAuthorityRow(successor)
	return rows
}

func cloneTemporalQueryRows(values [][]any) [][]any {
	result := make([][]any, len(values))
	for index, value := range values {
		result[index] = append([]any(nil), value...)
	}
	return result
}

func temporalCorruptDigest(label string) []byte {
	value := sha256.Sum256([]byte(label))
	return value[:]
}

type temporalObserverContextKey struct{}

type recordingTemporalSnapshotObserver struct {
	starts       int
	finishes     int
	attributes   []TemporalSnapshotAttributes
	finishErrors []error
}

func (observer *recordingTemporalSnapshotObserver) StartTemporalSnapshot(
	ctx context.Context,
	attributes TemporalSnapshotAttributes,
) (context.Context, func(error)) {
	observer.starts++
	observer.attributes = append(observer.attributes, attributes)
	observedContext := context.WithValue(ctx, temporalObserverContextKey{}, "observed")
	return observedContext, func(err error) {
		observer.finishes++
		observer.finishErrors = append(observer.finishErrors, err)
	}
}

type temporalQueryResult struct {
	rows pgx.Rows
	err  error
}

func temporalQueryRowsResult(values [][]any) temporalQueryResult {
	return temporalQueryResult{rows: &temporalQueryFakeRows{values: values}}
}

func temporalQueryRowsError(err error) temporalQueryResult {
	return temporalQueryResult{rows: &temporalQueryFakeRows{err: err}}
}

type temporalQueryFakePool struct {
	transaction   *temporalQueryFakeTransaction
	options       []pgx.TxOptions
	beginContexts []context.Context
	beginErr      error
	events        []string
}

func newTemporalQueryFakePool(results ...temporalQueryResult) *temporalQueryFakePool {
	pool := &temporalQueryFakePool{}
	pool.transaction = &temporalQueryFakeTransaction{
		results: results,
		events:  &pool.events,
	}
	return pool
}

func (pool *temporalQueryFakePool) BeginTx(
	ctx context.Context,
	options pgx.TxOptions,
) (pgx.Tx, error) {
	pool.beginContexts = append(pool.beginContexts, ctx)
	pool.options = append(pool.options, options)
	pool.events = append(pool.events, "begin")
	if pool.beginErr != nil {
		return nil, pool.beginErr
	}
	return pool.transaction, nil
}

type temporalQueryCall struct {
	context   context.Context
	sql       string
	arguments []any
}

type temporalQueryFakeTransaction struct {
	results          []temporalQueryResult
	queries          []temporalQueryCall
	commitCalls      int
	rollbackCalls    int
	commitContexts   []context.Context
	rollbackContexts []context.Context
	events           *[]string
}

func (transaction *temporalQueryFakeTransaction) Begin(context.Context) (pgx.Tx, error) {
	return nil, errors.New("synthetic nested transaction is unsupported")
}

func (transaction *temporalQueryFakeTransaction) Commit(ctx context.Context) error {
	transaction.commitCalls++
	transaction.commitContexts = append(transaction.commitContexts, ctx)
	*transaction.events = append(*transaction.events, "commit")
	return nil
}

func (transaction *temporalQueryFakeTransaction) Rollback(ctx context.Context) error {
	transaction.rollbackCalls++
	transaction.rollbackContexts = append(transaction.rollbackContexts, ctx)
	*transaction.events = append(*transaction.events, "rollback")
	return pgx.ErrTxClosed
}

func (transaction *temporalQueryFakeTransaction) CopyFrom(
	context.Context,
	pgx.Identifier,
	[]string,
	pgx.CopyFromSource,
) (int64, error) {
	return 0, errors.New("synthetic copy is unsupported")
}

func (transaction *temporalQueryFakeTransaction) SendBatch(
	context.Context,
	*pgx.Batch,
) pgx.BatchResults {
	return temporalQueryFakeBatchResults{}
}

func (transaction *temporalQueryFakeTransaction) LargeObjects() pgx.LargeObjects {
	return pgx.LargeObjects{}
}

func (transaction *temporalQueryFakeTransaction) Prepare(
	context.Context,
	string,
	string,
) (*pgconn.StatementDescription, error) {
	return nil, errors.New("synthetic prepare is unsupported")
}

func (transaction *temporalQueryFakeTransaction) Exec(
	context.Context,
	string,
	...any,
) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errors.New("synthetic exec is unsupported")
}

func (transaction *temporalQueryFakeTransaction) Query(
	ctx context.Context,
	sql string,
	arguments ...any,
) (pgx.Rows, error) {
	transaction.queries = append(transaction.queries, temporalQueryCall{
		context:   ctx,
		sql:       sql,
		arguments: append([]any(nil), arguments...),
	})
	queryNumber := len(transaction.queries)
	*transaction.events = append(
		*transaction.events,
		fmt.Sprintf("query-%d", queryNumber),
	)
	if queryNumber > len(transaction.results) {
		return nil, errors.New("synthetic query result is missing")
	}
	result := transaction.results[queryNumber-1]
	return result.rows, result.err
}

func (transaction *temporalQueryFakeTransaction) QueryRow(
	context.Context,
	string,
	...any,
) pgx.Row {
	return temporalQueryFakeRow{err: errors.New("synthetic query row is unsupported")}
}

func (transaction *temporalQueryFakeTransaction) Conn() *pgx.Conn {
	return nil
}

type temporalQueryFakeBatchResults struct{}

func (temporalQueryFakeBatchResults) Exec() (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errors.New("synthetic batch is unsupported")
}

func (temporalQueryFakeBatchResults) Query() (pgx.Rows, error) {
	return nil, errors.New("synthetic batch is unsupported")
}

func (temporalQueryFakeBatchResults) QueryRow() pgx.Row {
	return temporalQueryFakeRow{err: errors.New("synthetic batch is unsupported")}
}

func (temporalQueryFakeBatchResults) Close() error {
	return nil
}

type temporalQueryFakeRow struct {
	err error
}

func (row temporalQueryFakeRow) Scan(...any) error {
	return row.err
}

type temporalQueryFakeRows struct {
	values  [][]any
	index   int
	err     error
	closed  bool
	current []any
}

func (rows *temporalQueryFakeRows) Close() {
	rows.closed = true
}

func (rows *temporalQueryFakeRows) Err() error {
	return rows.err
}

func (rows *temporalQueryFakeRows) CommandTag() pgconn.CommandTag {
	return pgconn.CommandTag{}
}

func (rows *temporalQueryFakeRows) FieldDescriptions() []pgconn.FieldDescription {
	return nil
}

func (rows *temporalQueryFakeRows) Next() bool {
	if rows.closed || rows.index >= len(rows.values) {
		return false
	}
	rows.current = rows.values[rows.index]
	rows.index++
	return true
}

func (rows *temporalQueryFakeRows) Scan(destinations ...any) error {
	if rows.current == nil {
		return errors.New("synthetic row has no current values")
	}
	if len(destinations) != len(rows.current) {
		return fmt.Errorf(
			"synthetic scan destinations = %d, want %d",
			len(destinations),
			len(rows.current),
		)
	}
	for index, destination := range destinations {
		if err := assignTemporalQueryValue(destination, rows.current[index]); err != nil {
			return fmt.Errorf("synthetic scan column %d: %w", index, err)
		}
	}
	return nil
}

func (rows *temporalQueryFakeRows) Values() ([]any, error) {
	return append([]any(nil), rows.current...), nil
}

func (rows *temporalQueryFakeRows) RawValues() [][]byte {
	return nil
}

func (rows *temporalQueryFakeRows) Conn() *pgx.Conn {
	return nil
}

func assignTemporalQueryValue(destination, source any) error {
	target := reflect.ValueOf(destination)
	if target.Kind() != reflect.Pointer || target.IsNil() {
		return errors.New("scan destination must be a non-nil pointer")
	}
	target = target.Elem()
	if source == nil {
		target.Set(reflect.Zero(target.Type()))
		return nil
	}
	value := reflect.ValueOf(source)
	if value.Type().AssignableTo(target.Type()) {
		target.Set(value)
		return nil
	}
	if value.Type().ConvertibleTo(target.Type()) {
		target.Set(value.Convert(target.Type()))
		return nil
	}
	return fmt.Errorf(
		"value type %s cannot assign to %s",
		value.Type(),
		target.Type(),
	)
}
