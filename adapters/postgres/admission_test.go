package postgres_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	postgres "github.com/JakeFAU/stacks/adapters/postgres"
	"github.com/JakeFAU/stacks/core/admission"
)

func TestAdmissionQuarantineThenAdmissionPreservesHistory(t *testing.T) {
	fixture := newIdentityRepositoryFixture(t)
	quarantine := canonicalAdmissionDecision(
		t,
		"admission:mention/opaque#quarantine",
		admission.TargetMention,
		"mention:opaque/admission",
		admission.Quarantined,
		admission.AuthorityPolicy,
		"",
		identityRecordedAt,
	)
	admit := canonicalAdmissionDecision(
		t,
		"admission:mention/opaque#admit",
		admission.TargetMention,
		"mention:opaque/admission",
		admission.Admitted,
		admission.AuthorityReviewer,
		quarantine.ID(),
		identityRecordedAt.Add(time.Minute),
	)

	if err := fixture.database.InTransaction(fixture.ctx, func(transaction *postgres.Transaction) error {
		if err := transaction.AppendAdmissionDecision(fixture.ctx, quarantine); err != nil {
			return err
		}
		return transaction.AppendAdmissionDecision(fixture.ctx, quarantine)
	}); err != nil {
		t.Fatalf("append and exactly retry quarantine: %v", err)
	}
	quarantineXID := admissionRowXID(t, fixture.ctx, fixture.admin, quarantine.ID())
	if err := fixture.database.InTransaction(fixture.ctx, func(transaction *postgres.Transaction) error {
		return transaction.AppendAdmissionDecision(fixture.ctx, admit)
	}); err != nil {
		t.Fatalf("append admission correction: %v", err)
	}
	if got := admissionRowXID(t, fixture.ctx, fixture.admin, quarantine.ID()); got != quarantineXID {
		t.Fatalf("admission correction rewrote quarantine xmin from %s to %s", quarantineXID, got)
	}
	effective, err := fixture.database.EffectiveAdmissionDecision(
		fixture.ctx,
		admission.TargetMention,
		"mention:opaque/admission",
	)
	if err != nil {
		t.Fatalf("EffectiveAdmissionDecision() error = %v", err)
	}
	if effective.ID() != admit.ID() || effective.Outcome() != admission.Admitted {
		t.Fatalf("effective admission = %#v, want admitted successor %q", effective, admit.ID())
	}
	var history int
	if err := fixture.admin.QueryRow(fixture.ctx, `
		SELECT count(*)
		FROM stacks_core.admission_decisions
		WHERE target_kind = $1
		  AND target_id = $2`,
		admission.TargetMention,
		"mention:opaque/admission",
	).Scan(&history); err != nil {
		t.Fatalf("count admission history: %v", err)
	}
	if history != 2 {
		t.Fatalf("admission history rows = %d, want 2", history)
	}
}

func TestAdmissionRetryPayloadConflictFails(t *testing.T) {
	fixture := newIdentityRepositoryFixture(t)
	first := canonicalAdmissionDecision(
		t,
		"admission:conflict",
		admission.TargetObservation,
		"observation:opaque/conflict",
		admission.Quarantined,
		admission.AuthorityPolicy,
		"",
		identityRecordedAt,
	)
	conflicting := canonicalAdmissionDecision(
		t,
		first.ID(),
		admission.TargetObservation,
		"observation:opaque/conflict",
		admission.Admitted,
		admission.AuthorityPolicy,
		"",
		identityRecordedAt,
	)
	if err := fixture.database.InTransaction(fixture.ctx, func(transaction *postgres.Transaction) error {
		return transaction.AppendAdmissionDecision(fixture.ctx, first)
	}); err != nil {
		t.Fatalf("append initial admission decision: %v", err)
	}
	err := fixture.database.InTransaction(fixture.ctx, func(transaction *postgres.Transaction) error {
		return transaction.AppendAdmissionDecision(fixture.ctx, conflicting)
	})
	if !errors.Is(err, postgres.ErrConflict) {
		t.Fatalf("conflicting admission retry error = %v, want ErrConflict", err)
	}
}

func TestConcurrentAdmissionCorrectionsCannotBranch(t *testing.T) {
	fixture := newIdentityRepositoryFixture(t)
	initial := canonicalAdmissionDecision(
		t,
		"admission:concurrent/initial",
		admission.TargetExtractionRun,
		"run:opaque/concurrent",
		admission.Quarantined,
		admission.AuthorityPolicy,
		"",
		identityRecordedAt,
	)
	if err := fixture.database.InTransaction(fixture.ctx, func(transaction *postgres.Transaction) error {
		return transaction.AppendAdmissionDecision(fixture.ctx, initial)
	}); err != nil {
		t.Fatalf("append initial concurrent admission: %v", err)
	}
	corrections := []admission.Decision{
		canonicalAdmissionDecision(
			t, "admission:concurrent/a", admission.TargetExtractionRun,
			"run:opaque/concurrent", admission.Admitted, admission.AuthorityReviewer,
			initial.ID(), identityRecordedAt.Add(time.Minute),
		),
		canonicalAdmissionDecision(
			t, "admission:concurrent/b", admission.TargetExtractionRun,
			"run:opaque/concurrent", admission.Retired, admission.AuthorityReviewer,
			initial.ID(), identityRecordedAt.Add(2*time.Minute),
		),
	}
	start := make(chan struct{})
	results := make(chan error, len(corrections))
	var ready sync.WaitGroup
	ready.Add(len(corrections))
	for _, correction := range corrections {
		correction := correction
		go func() {
			ready.Done()
			<-start
			results <- fixture.database.InTransaction(fixture.ctx, func(transaction *postgres.Transaction) error {
				return transaction.AppendAdmissionDecision(fixture.ctx, correction)
			})
		}()
	}
	ready.Wait()
	close(start)
	var succeeded, conflicted int
	for range corrections {
		err := <-results
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, postgres.ErrConflict):
			conflicted++
		default:
			t.Fatalf("concurrent admission correction error = %v, want nil or ErrConflict", err)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("concurrent admission outcomes success/conflict = %d/%d, want 1/1", succeeded, conflicted)
	}
}

func TestAdmissionDirectoryAbsenceIsNeutral(t *testing.T) {
	fixture := newIdentityRepositoryFixture(t)
	decision := canonicalAdmissionDecision(
		t,
		"admission:directory-neutral",
		admission.TargetIdentityDecision,
		"identity-decision:opaque/directory-neutral",
		admission.Admitted,
		admission.AuthorityReviewer,
		"",
		identityRecordedAt,
	)
	if err := fixture.database.InTransaction(fixture.ctx, func(transaction *postgres.Transaction) error {
		return transaction.AppendAdmissionDecision(fixture.ctx, decision)
	}); err != nil {
		t.Fatalf("append core-only admission decision: %v", err)
	}
	var directorySchemaExists bool
	if err := fixture.admin.QueryRow(
		fixture.ctx,
		`SELECT EXISTS (
			SELECT 1 FROM pg_namespace WHERE nspname = 'stacks_directory'
		)`,
	).Scan(&directorySchemaExists); err != nil {
		t.Fatalf("inspect optional directory schema: %v", err)
	}
	if directorySchemaExists {
		t.Fatal("directory schema exists in core-only fixture")
	}
}

func TestAdmissionRepositoryPreservesCancellation(t *testing.T) {
	fixture := newIdentityRepositoryFixture(t)
	canceled, cancel := context.WithCancel(fixture.ctx)
	cancel()
	if _, err := fixture.database.EffectiveAdmissionDecision(
		canceled,
		admission.TargetMention,
		"mention:opaque/canceled",
	); !errors.Is(err, context.Canceled) {
		t.Fatalf("EffectiveAdmissionDecision() error = %v, want context.Canceled", err)
	}
}
