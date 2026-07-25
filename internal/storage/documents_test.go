package storage

import (
	"context"
	"errors"
	"testing"

	"stacks/internal/ingest"
	"stacks/internal/modelpolicy"
)

func TestCompleteVersionRejectsPaddedLocalIdentifierBeforeOpeningTransaction(t *testing.T) {
	repository := &IngestionRepository{}
	err := repository.CompleteVersion(context.Background(), ingest.Completion{
		VersionID: "11111111-1111-1111-1111-111111111111",
		DataMode:  modelpolicy.DataModePersonal,
		Evidence:  []ingest.EvidenceRecord{{Key: " citation-1"}},
	})
	if !errors.Is(err, ingest.ErrPersistenceReference) {
		t.Fatalf("CompleteVersion() error = %v, want padded identifier rejection", err)
	}
}

func TestCompletedWriteSetMismatchIsBoundedToRunIdentity(t *testing.T) {
	runID := "22222222-2222-2222-2222-222222222222"
	err := completedWriteSetComparisonError(
		runID,
		newObservationBoundaryError(
			ErrObservationConflict,
			reasonObservationOriginMismatch,
			"33333333-3333-3333-3333-333333333333",
		),
	)
	if !errors.Is(err, ErrObservationConflict) {
		t.Fatalf("completedWriteSetComparisonError() error = %v, want ErrObservationConflict", err)
	}
	want := `observation boundary run "22222222-2222-2222-2222-222222222222": completion_write_set_mismatch`
	if err.Error() != want {
		t.Fatalf("completedWriteSetComparisonError() error = %q, want %q", err, want)
	}
}
