package storage

import (
	"context"
	"errors"
	"testing"

	"stacks/internal/ingest"
)

func TestCompleteVersionRejectsPaddedLocalIdentifierBeforeOpeningTransaction(t *testing.T) {
	repository := &IngestionRepository{}
	err := repository.CompleteVersion(context.Background(), ingest.Completion{
		VersionID: "11111111-1111-1111-1111-111111111111",
		Evidence:  []ingest.EvidenceRecord{{Key: " citation-1"}},
	})
	if !errors.Is(err, ingest.ErrPersistenceReference) {
		t.Fatalf("CompleteVersion() error = %v, want padded identifier rejection", err)
	}
}
