package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/JakeFAU/stacks/core/admission"
	"github.com/jackc/pgx/v5"
)

func canonicalAdmissionDecision(
	t testing.TB,
	id string,
	targetKind admission.TargetKind,
	targetID string,
	outcome admission.Outcome,
	authority admission.Authority,
	supersedesID string,
	recordedAt time.Time,
) admission.Decision {
	t.Helper()
	decision, err := admission.NewDecision(admission.DecisionInput{
		ID:           id,
		TargetKind:   targetKind,
		TargetID:     targetID,
		Outcome:      outcome,
		ReasonCode:   "admission_authority",
		Authority:    authority,
		RecordedAt:   recordedAt,
		SupersedesID: supersedesID,
	})
	if err != nil {
		t.Fatalf("admission.NewDecision(%q) error = %v", id, err)
	}
	return decision
}

func admissionRowXID(
	t testing.TB,
	ctx context.Context,
	connection *pgx.Conn,
	decisionID string,
) string {
	t.Helper()
	var xid string
	if err := connection.QueryRow(
		ctx,
		`SELECT xmin::text
		 FROM stacks_core.admission_decisions
		 WHERE id = $1`,
		decisionID,
	).Scan(&xid); err != nil {
		t.Fatalf("read admission decision xmin: %v", err)
	}
	return xid
}
