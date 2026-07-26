package postgres_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	postgres "github.com/JakeFAU/stacks/adapters/postgres"
	"github.com/JakeFAU/stacks/core/timepoint"
)

func TestExtractionFirstClaimCreatesAttemptOne(t *testing.T) {
	fixture := newExtractionRepositoryFixture(t)
	run := canonicalExtractionRun(fixture.versionID)
	lease := canonicalLease("attempt:opaque/one", "worker:opaque/a", extractionClaimedAt)

	var state postgres.ExtractionState
	if err := fixture.database.InTransaction(fixture.ctx, func(transaction *postgres.Transaction) error {
		var err error
		state, err = transaction.PrepareExtraction(fixture.ctx, run, lease)
		return err
	}); err != nil {
		t.Fatalf("PrepareExtraction() error = %v", err)
	}
	if state.Status != postgres.ExtractionClaimed ||
		state.RunID != run.ID ||
		state.AttemptID != lease.AttemptID ||
		state.AttemptNumber != 1 ||
		state.LeaseExpiresAt != lease.ClaimedAt.Add(lease.LeaseDuration) {
		t.Fatalf("PrepareExtraction() = %#v, want first claimed attempt", state)
	}
	var runState, attemptState string
	if err := fixture.admin.QueryRow(fixture.ctx, `
		SELECT run.state, attempt.state
		FROM stacks_core.extraction_runs AS run
		JOIN stacks_core.extraction_attempts AS attempt
		  ON attempt.run_id = run.id
		WHERE run.id = $1 AND attempt.id = $2`,
		run.ID,
		lease.AttemptID,
	).Scan(&runState, &attemptState); err != nil {
		t.Fatalf("inspect first extraction claim: %v", err)
	}
	if runState != "active" || attemptState != "active" {
		t.Fatalf("stored run/attempt states = %q/%q, want active/active", runState, attemptState)
	}
}

func TestExtractionLiveLeaseReturnsBusyWithoutNewAttempt(t *testing.T) {
	fixture := newExtractionRepositoryFixture(t)
	run := canonicalExtractionRun(fixture.versionID)
	first := canonicalLease("attempt:opaque/live", "worker:opaque/a", extractionClaimedAt)
	if err := fixture.database.InTransaction(fixture.ctx, func(transaction *postgres.Transaction) error {
		_, err := transaction.PrepareExtraction(fixture.ctx, run, first)
		return err
	}); err != nil {
		t.Fatalf("first PrepareExtraction() error = %v", err)
	}
	runXID := extractionRowXID(t, fixture.ctx, fixture.admin, "extraction_runs", run.ID)
	attemptXID := extractionRowXID(t, fixture.ctx, fixture.admin, "extraction_attempts", first.AttemptID)

	second := canonicalLease(
		"attempt:opaque/must-not-be-created",
		"worker:opaque/b",
		first.ClaimedAt.Add(time.Minute),
	)
	var state postgres.ExtractionState
	if err := fixture.database.InTransaction(fixture.ctx, func(transaction *postgres.Transaction) error {
		var err error
		state, err = transaction.PrepareExtraction(fixture.ctx, run, second)
		return err
	}); err != nil {
		t.Fatalf("busy PrepareExtraction() error = %v", err)
	}
	if state.Status != postgres.ExtractionBusy ||
		state.AttemptID != first.AttemptID ||
		state.AttemptNumber != 1 ||
		state.LeaseExpiresAt != first.ClaimedAt.Add(first.LeaseDuration) {
		t.Fatalf("busy PrepareExtraction() = %#v, want original live attempt", state)
	}
	var attempts int
	if err := fixture.admin.QueryRow(
		fixture.ctx,
		`SELECT count(*) FROM stacks_core.extraction_attempts WHERE run_id = $1`,
		run.ID,
	).Scan(&attempts); err != nil {
		t.Fatalf("count live extraction attempts: %v", err)
	}
	if attempts != 1 {
		t.Fatalf("attempt count = %d, want 1", attempts)
	}
	if got := extractionRowXID(t, fixture.ctx, fixture.admin, "extraction_runs", run.ID); got != runXID {
		t.Fatalf("busy retry rewrote run xmin from %s to %s", runXID, got)
	}
	if got := extractionRowXID(t, fixture.ctx, fixture.admin, "extraction_attempts", first.AttemptID); got != attemptXID {
		t.Fatalf("busy retry rewrote attempt xmin from %s to %s", attemptXID, got)
	}
}

func TestExtractionExpiredLeaseAppendsReclaimedAttempt(t *testing.T) {
	fixture := newExtractionRepositoryFixture(t)
	run := canonicalExtractionRun(fixture.versionID)
	first := canonicalLease("attempt:opaque/expired", "worker:opaque/a", extractionClaimedAt)
	if err := fixture.database.InTransaction(fixture.ctx, func(transaction *postgres.Transaction) error {
		_, err := transaction.PrepareExtraction(fixture.ctx, run, first)
		return err
	}); err != nil {
		t.Fatalf("first PrepareExtraction() error = %v", err)
	}
	second := canonicalLease(
		"attempt:opaque/reclaimed",
		"worker:opaque/b",
		first.ClaimedAt.Add(first.LeaseDuration),
	)
	var state postgres.ExtractionState
	if err := fixture.database.InTransaction(fixture.ctx, func(transaction *postgres.Transaction) error {
		var err error
		state, err = transaction.PrepareExtraction(fixture.ctx, run, second)
		return err
	}); err != nil {
		t.Fatalf("reclaim PrepareExtraction() error = %v", err)
	}
	if state.Status != postgres.ExtractionClaimed ||
		state.AttemptID != second.AttemptID ||
		state.AttemptNumber != 2 {
		t.Fatalf("reclaimed PrepareExtraction() = %#v, want claimed attempt two", state)
	}
	rows, err := fixture.admin.Query(fixture.ctx, `
		SELECT id, attempt_number, state
		FROM stacks_core.extraction_attempts
		WHERE run_id = $1
		ORDER BY attempt_number`,
		run.ID,
	)
	if err != nil {
		t.Fatalf("list reclaimed extraction attempts: %v", err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var id, state string
		var number int
		if err := rows.Scan(&id, &number, &state); err != nil {
			t.Fatalf("scan reclaimed extraction attempt: %v", err)
		}
		got = append(got, id+"/"+state)
		if number != len(got) {
			t.Errorf("attempt %q number = %d, want %d", id, number, len(got))
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate reclaimed extraction attempts: %v", err)
	}
	if !reflect.DeepEqual(got, []string{
		first.AttemptID + "/expired",
		second.AttemptID + "/active",
	}) {
		t.Fatalf("attempt history = %v, want expired then active", got)
	}
}

func TestExtractionFailureRetainsAttemptAndPermitsRetry(t *testing.T) {
	fixture := newExtractionRepositoryFixture(t)
	run := canonicalExtractionRun(fixture.versionID)
	first := canonicalLease("attempt:opaque/failed", "worker:opaque/a", extractionClaimedAt)
	if err := fixture.database.InTransaction(fixture.ctx, func(transaction *postgres.Transaction) error {
		if _, err := transaction.PrepareExtraction(fixture.ctx, run, first); err != nil {
			return err
		}
		return transaction.RecordExtractionFailure(fixture.ctx, postgres.ExtractionFailureInput{
			RunID:       run.ID,
			AttemptID:   first.AttemptID,
			Owner:       first.Owner,
			FailedAt:    first.ClaimedAt.Add(time.Minute),
			FailureCode: "synthetic_parse_failure",
		})
	}); err != nil {
		t.Fatalf("record extraction failure: %v", err)
	}

	second := canonicalLease(
		"attempt:opaque/retry",
		"worker:opaque/b",
		first.ClaimedAt.Add(2*time.Minute),
	)
	var state postgres.ExtractionState
	if err := fixture.database.InTransaction(fixture.ctx, func(transaction *postgres.Transaction) error {
		var err error
		state, err = transaction.PrepareExtraction(fixture.ctx, run, second)
		return err
	}); err != nil {
		t.Fatalf("retry PrepareExtraction() error = %v", err)
	}
	if state.Status != postgres.ExtractionClaimed || state.AttemptNumber != 2 {
		t.Fatalf("retry PrepareExtraction() = %#v, want claimed attempt two", state)
	}
	var firstState, firstFailure, secondState string
	if err := fixture.admin.QueryRow(fixture.ctx, `
		SELECT first.state, first.failure_code, second.state
		FROM stacks_core.extraction_attempts AS first
		JOIN stacks_core.extraction_attempts AS second
		  ON second.run_id = first.run_id
		WHERE first.id = $1 AND second.id = $2`,
		first.AttemptID,
		second.AttemptID,
	).Scan(&firstState, &firstFailure, &secondState); err != nil {
		t.Fatalf("inspect failed extraction history: %v", err)
	}
	if firstState != "failed" ||
		firstFailure != "synthetic_parse_failure" ||
		secondState != "active" {
		t.Fatalf(
			"failure history = %q/%q then %q, want failed/code then active",
			firstState,
			firstFailure,
			secondState,
		)
	}
}

func TestExtractionCompletedResumeCreatesNoAttempt(t *testing.T) {
	fixture := newExtractionRepositoryFixture(t)
	run := canonicalExtractionRun(fixture.versionID)
	first := canonicalLease("attempt:opaque/completed", "worker:opaque/a", extractionClaimedAt)
	completion := postgres.ExtractionCompletionInput{
		RunID:                 run.ID,
		AttemptID:             first.AttemptID,
		Owner:                 first.Owner,
		CompletedAt:           first.ClaimedAt.Add(time.Minute),
		WriteSetDigestVersion: "stacks.extraction-write-set.v1.canonical",
		WriteSetDigest:        syntheticDigest("canonical-write-set"),
	}
	if err := fixture.database.InTransaction(fixture.ctx, func(transaction *postgres.Transaction) error {
		if _, err := transaction.PrepareExtraction(fixture.ctx, run, first); err != nil {
			return err
		}
		return transaction.CompleteExtraction(fixture.ctx, completion)
	}); err != nil {
		t.Fatalf("complete extraction: %v", err)
	}
	runXID := extractionRowXID(t, fixture.ctx, fixture.admin, "extraction_runs", run.ID)
	attemptXID := extractionRowXID(t, fixture.ctx, fixture.admin, "extraction_attempts", first.AttemptID)

	resume := canonicalLease(
		"attempt:opaque/must-not-resume",
		"worker:opaque/b",
		first.ClaimedAt.Add(2*time.Minute),
	)
	var state postgres.ExtractionState
	if err := fixture.database.InTransaction(fixture.ctx, func(transaction *postgres.Transaction) error {
		var err error
		state, err = transaction.PrepareExtraction(fixture.ctx, run, resume)
		return err
	}); err != nil {
		t.Fatalf("completed PrepareExtraction() error = %v", err)
	}
	if state.Status != postgres.ExtractionCompleted ||
		state.AttemptID != first.AttemptID ||
		state.AttemptNumber != 1 ||
		!state.HasWriteSetDigest ||
		state.WriteSetDigestVersion != completion.WriteSetDigestVersion ||
		state.WriteSetDigest != completion.WriteSetDigest {
		t.Fatalf("completed PrepareExtraction() = %#v, want durable completed result", state)
	}
	var attempts int
	if err := fixture.admin.QueryRow(
		fixture.ctx,
		`SELECT count(*) FROM stacks_core.extraction_attempts WHERE run_id = $1`,
		run.ID,
	).Scan(&attempts); err != nil {
		t.Fatalf("count completed extraction attempts: %v", err)
	}
	if attempts != 1 {
		t.Fatalf("completed attempt count = %d, want 1", attempts)
	}
	if got := extractionRowXID(t, fixture.ctx, fixture.admin, "extraction_runs", run.ID); got != runXID {
		t.Fatalf("completed resume rewrote run xmin from %s to %s", runXID, got)
	}
	if got := extractionRowXID(t, fixture.ctx, fixture.admin, "extraction_attempts", first.AttemptID); got != attemptXID {
		t.Fatalf("completed resume rewrote attempt xmin from %s to %s", attemptXID, got)
	}
}

func TestExtractionCompletionExactRetryMatchesWriteSetDigest(t *testing.T) {
	fixture := newExtractionRepositoryFixture(t)
	run := canonicalExtractionRun(fixture.versionID)
	lease := canonicalLease("attempt:opaque/exact-completion", "worker:opaque/a", extractionClaimedAt)
	completion := postgres.ExtractionCompletionInput{
		RunID:                 run.ID,
		AttemptID:             lease.AttemptID,
		Owner:                 lease.Owner,
		CompletedAt:           lease.ClaimedAt.Add(time.Minute),
		WriteSetDigestVersion: "stacks.extraction-write-set.v1.canonical",
		WriteSetDigest:        syntheticDigest("exact-completion-write-set"),
	}
	if err := fixture.database.InTransaction(fixture.ctx, func(transaction *postgres.Transaction) error {
		if _, err := transaction.PrepareExtraction(fixture.ctx, run, lease); err != nil {
			return err
		}
		return transaction.CompleteExtraction(fixture.ctx, completion)
	}); err != nil {
		t.Fatalf("initial CompleteExtraction() error = %v", err)
	}
	before := []string{
		extractionRowXID(t, fixture.ctx, fixture.admin, "extraction_runs", run.ID),
		extractionRowXID(t, fixture.ctx, fixture.admin, "extraction_attempts", lease.AttemptID),
	}
	if err := fixture.database.InTransaction(fixture.ctx, func(transaction *postgres.Transaction) error {
		return transaction.CompleteExtraction(fixture.ctx, completion)
	}); err != nil {
		t.Fatalf("exact retry CompleteExtraction() error = %v", err)
	}
	after := []string{
		extractionRowXID(t, fixture.ctx, fixture.admin, "extraction_runs", run.ID),
		extractionRowXID(t, fixture.ctx, fixture.admin, "extraction_attempts", lease.AttemptID),
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("exact completion retry changed row identities from %v to %v", before, after)
	}
}

func TestExtractionCompletionRejectsDifferentWriteSetDigest(t *testing.T) {
	fixture := newExtractionRepositoryFixture(t)
	run := canonicalExtractionRun(fixture.versionID)
	lease := canonicalLease("attempt:opaque/digest-conflict", "worker:opaque/a", extractionClaimedAt)
	completion := postgres.ExtractionCompletionInput{
		RunID:                 run.ID,
		AttemptID:             lease.AttemptID,
		Owner:                 lease.Owner,
		CompletedAt:           lease.ClaimedAt.Add(time.Minute),
		WriteSetDigestVersion: "stacks.extraction-write-set.v1.canonical",
		WriteSetDigest:        syntheticDigest("first-write-set"),
	}
	if err := fixture.database.InTransaction(fixture.ctx, func(transaction *postgres.Transaction) error {
		if _, err := transaction.PrepareExtraction(fixture.ctx, run, lease); err != nil {
			return err
		}
		return transaction.CompleteExtraction(fixture.ctx, completion)
	}); err != nil {
		t.Fatalf("initial completion: %v", err)
	}
	completion.WriteSetDigest = syntheticDigest("different-write-set")
	err := fixture.database.InTransaction(fixture.ctx, func(transaction *postgres.Transaction) error {
		return transaction.CompleteExtraction(fixture.ctx, completion)
	})
	if !errors.Is(err, postgres.ErrConflict) {
		t.Fatalf("different completion digest error = %v, want ErrConflict", err)
	}
}

func TestExtractionWrongOwnerCannotFailOrComplete(t *testing.T) {
	fixture := newExtractionRepositoryFixture(t)
	run := canonicalExtractionRun(fixture.versionID)
	lease := canonicalLease("attempt:opaque/owned", "worker:opaque/a", extractionClaimedAt)
	if err := fixture.database.InTransaction(fixture.ctx, func(transaction *postgres.Transaction) error {
		_, err := transaction.PrepareExtraction(fixture.ctx, run, lease)
		return err
	}); err != nil {
		t.Fatalf("PrepareExtraction() error = %v", err)
	}
	for name, operation := range map[string]func(*postgres.Transaction) error{
		"failure": func(transaction *postgres.Transaction) error {
			return transaction.RecordExtractionFailure(fixture.ctx, postgres.ExtractionFailureInput{
				RunID:       run.ID,
				AttemptID:   lease.AttemptID,
				Owner:       "worker:opaque/wrong",
				FailedAt:    lease.ClaimedAt.Add(time.Minute),
				FailureCode: "must_not_persist",
			})
		},
		"completion": func(transaction *postgres.Transaction) error {
			return transaction.CompleteExtraction(fixture.ctx, postgres.ExtractionCompletionInput{
				RunID:                 run.ID,
				AttemptID:             lease.AttemptID,
				Owner:                 "worker:opaque/wrong",
				CompletedAt:           lease.ClaimedAt.Add(time.Minute),
				WriteSetDigestVersion: "stacks.extraction-write-set.v1.canonical",
				WriteSetDigest:        syntheticDigest("must-not-persist"),
			})
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := fixture.database.InTransaction(fixture.ctx, operation)
			if !errors.Is(err, postgres.ErrConflict) {
				t.Fatalf("wrong-owner %s error = %v, want ErrConflict", name, err)
			}
		})
	}
	var state, owner string
	if err := fixture.admin.QueryRow(
		fixture.ctx,
		`SELECT state, owner FROM stacks_core.extraction_attempts WHERE id = $1`,
		lease.AttemptID,
	).Scan(&state, &owner); err != nil {
		t.Fatalf("inspect owned attempt: %v", err)
	}
	if state != "active" || owner != lease.Owner {
		t.Fatalf("attempt state/owner = %q/%q, want active/%q", state, owner, lease.Owner)
	}
}

func TestExtractionCancellationPreservesErrorsIs(t *testing.T) {
	fixture := newExtractionRepositoryFixture(t)
	canceled, cancel := context.WithCancel(fixture.ctx)
	cancel()
	err := fixture.database.InTransaction(fixture.ctx, func(transaction *postgres.Transaction) error {
		_, err := transaction.PrepareExtraction(
			canceled,
			canonicalExtractionRun(fixture.versionID),
			canonicalLease("attempt:opaque/canceled", "worker:opaque/a", extractionClaimedAt),
		)
		return err
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled PrepareExtraction() error = %v, want context.Canceled", err)
	}
}

func TestExtractionRejectsNonCanonicalLifecycleTimesBeforeSQL(t *testing.T) {
	fixture := newExtractionRepositoryFixture(t)
	nonUTC := extractionRecordedAt.In(time.FixedZone("synthetic-offset", -4*60*60))
	subMicrosecond := extractionRecordedAt.Add(time.Nanosecond)
	monotonic := time.Now()
	for name, invalidTime := range map[string]time.Time{
		"non-UTC":         nonUTC,
		"sub-microsecond": subMicrosecond,
		"monotonic":       monotonic,
	} {
		if timepoint.IsCanonical(invalidTime) {
			t.Fatalf("%s fixture unexpectedly canonical: %v", name, invalidTime)
		}
		t.Run("run-"+name, func(t *testing.T) {
			run := canonicalExtractionRun(fixture.versionID)
			run.ID = "run:opaque/invalid-" + strings.ReplaceAll(name, " ", "-")
			run.RecordedAt = invalidTime
			err := fixture.database.InTransaction(fixture.ctx, func(transaction *postgres.Transaction) error {
				_, gotErr := transaction.PrepareExtraction(
					fixture.ctx,
					run,
					canonicalLease("attempt:opaque/invalid-run-"+name, "worker:opaque/a", extractionClaimedAt),
				)
				if gotErr == nil {
					return errors.New("PrepareExtraction() error = nil")
				}
				var count int
				if err := transaction.QueryRow(
					fixture.ctx,
					`SELECT count(*) FROM stacks_core.extraction_runs WHERE id = $1`,
					run.ID,
				).Scan(&count); err != nil {
					return err
				}
				if count != 0 {
					return errors.New("invalid run reached SQL write")
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
		t.Run("claim-"+name, func(t *testing.T) {
			run := canonicalExtractionRun(fixture.versionID)
			run.ID = "run:opaque/invalid-claim-" + strings.ReplaceAll(name, " ", "-")
			lease := canonicalLease("attempt:opaque/invalid-claim-"+name, "worker:opaque/a", invalidTime)
			err := fixture.database.InTransaction(fixture.ctx, func(transaction *postgres.Transaction) error {
				_, gotErr := transaction.PrepareExtraction(fixture.ctx, run, lease)
				if gotErr == nil {
					return errors.New("PrepareExtraction() error = nil")
				}
				var count int
				if err := transaction.QueryRow(
					fixture.ctx,
					`SELECT count(*) FROM stacks_core.extraction_runs WHERE id = $1`,
					run.ID,
				).Scan(&count); err != nil {
					return err
				}
				if count != 0 {
					return errors.New("invalid claim reached SQL write")
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}

	run := canonicalExtractionRun(fixture.versionID)
	run.ID = "run:opaque/invalid-terminal-times"
	lease := canonicalLease("attempt:opaque/invalid-terminal-times", "worker:opaque/a", extractionClaimedAt)
	if err := fixture.database.InTransaction(fixture.ctx, func(transaction *postgres.Transaction) error {
		_, err := transaction.PrepareExtraction(fixture.ctx, run, lease)
		return err
	}); err != nil {
		t.Fatalf("prepare terminal-time fixture: %v", err)
	}
	for name, operation := range map[string]func(*postgres.Transaction) error{
		"failure": func(transaction *postgres.Transaction) error {
			return transaction.RecordExtractionFailure(fixture.ctx, postgres.ExtractionFailureInput{
				RunID:       run.ID,
				AttemptID:   lease.AttemptID,
				Owner:       lease.Owner,
				FailedAt:    subMicrosecond,
				FailureCode: "invalid_time",
			})
		},
		"completion": func(transaction *postgres.Transaction) error {
			return transaction.CompleteExtraction(fixture.ctx, postgres.ExtractionCompletionInput{
				RunID:                 run.ID,
				AttemptID:             lease.AttemptID,
				Owner:                 lease.Owner,
				CompletedAt:           subMicrosecond,
				WriteSetDigestVersion: "stacks.extraction-write-set.v1.canonical",
				WriteSetDigest:        syntheticDigest("invalid-terminal-time"),
			})
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := fixture.database.InTransaction(fixture.ctx, func(transaction *postgres.Transaction) error {
				if gotErr := operation(transaction); gotErr == nil {
					return errors.New("terminal operation error = nil")
				}
				var state string
				if err := transaction.QueryRow(
					fixture.ctx,
					`SELECT state FROM stacks_core.extraction_attempts WHERE id = $1`,
					lease.AttemptID,
				).Scan(&state); err != nil {
					return err
				}
				if state != "active" {
					return errors.New("invalid terminal time changed attempt")
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
