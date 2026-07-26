package postgres_test

import (
	"context"
	"crypto/sha256"
	"testing"
	"time"

	postgres "github.com/JakeFAU/stacks/adapters/postgres"
	"github.com/JakeFAU/stacks/core/evidence"
	"github.com/jackc/pgx/v5"
)

var (
	extractionRecordedAt = time.Date(2026, time.July, 25, 20, 0, 0, 123456000, time.UTC)
	extractionClaimedAt  = extractionRecordedAt.Add(time.Minute)
)

type extractionRepositoryFixture struct {
	documentRepositoryFixture
	versionID string
}

func newExtractionRepositoryFixture(t testing.TB) extractionRepositoryFixture {
	t.Helper()
	fixture := newDocumentRepositoryFixture(t)
	document := canonicalDocument(
		t,
		"extraction-source:opaque/1",
		"revision-extraction",
		documentRecordedAt,
	)
	put, err := fixture.database.PutDocumentVersion(fixture.ctx, document)
	if err != nil {
		t.Fatalf("persist extraction document version: %v", err)
	}
	return extractionRepositoryFixture{
		documentRepositoryFixture: fixture,
		versionID:                 put.Ref.VersionID,
	}
}

func canonicalExtractionRun(versionID string) postgres.ExtractionRunInput {
	return postgres.ExtractionRunInput{
		ID:                      "run:opaque/extraction-1",
		DocumentVersionID:       versionID,
		DerivationDigestVersion: "stacks.extraction-derivation.v1.canonical",
		DerivationDigest:        syntheticDigest("extraction-derivation"),
		Method:                  "structured-extraction",
		Version:                 "extractor-v3",
		Provider:                "synthetic-model-boundary",
		DataMode:                "synthetic",
		Model:                   "synthetic-model-v1",
		PromptVersion:           "prompt-v7",
		SchemaDigest:            syntheticDigest("extraction-schema"),
		MaxOutputTokens:         512,
		RecordedAt:              extractionRecordedAt,
	}
}

func canonicalLease(attemptID, owner string, claimedAt time.Time) postgres.LeaseRequest {
	return postgres.LeaseRequest{
		AttemptID:     attemptID,
		Owner:         owner,
		ClaimedAt:     claimedAt,
		LeaseDuration: 5 * time.Minute,
	}
}

func syntheticDigest(value string) evidence.ContentDigest {
	return evidence.ContentDigest(sha256.Sum256([]byte(value)))
}

func extractionRowXID(
	t testing.TB,
	ctx context.Context,
	connection *pgx.Conn,
	table string,
	id string,
) string {
	t.Helper()
	if table != "extraction_runs" && table != "extraction_attempts" {
		t.Fatalf("unsupported extraction xmin table %q", table)
	}
	var xid string
	query := "SELECT xmin::text FROM stacks_core." + table + " WHERE id = $1"
	if err := connection.QueryRow(ctx, query, id).Scan(&xid); err != nil {
		t.Fatalf("read %s xmin: %v", table, err)
	}
	return xid
}
