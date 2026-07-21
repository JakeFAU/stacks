package storage

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"stacks/internal/knowledge"
	"stacks/internal/source"
)

const testDatabaseURLEnvironmentVariable = "STACKS_TEST_DATABASE_URL"

func TestPutDocumentVersionIsIdempotent(t *testing.T) {
	pool := openIntegrationDatabase(t)
	documents := NewDocumentRepository(pool)
	version := testDocumentVersion(t, "document-idempotent")

	first, created, err := documents.PutDocumentVersion(context.Background(), version)
	if err != nil {
		t.Fatalf("put first document version: %v", err)
	}
	if !created {
		t.Fatal("first document version insertion created = false, want true")
	}

	second, created, err := documents.PutDocumentVersion(context.Background(), version)
	if err != nil {
		t.Fatalf("put repeated document version: %v", err)
	}
	if created {
		t.Fatal("repeated document version insertion created = true, want false")
	}
	if second.ID != first.ID {
		t.Fatalf("repeated document version ID = %q, want %q", second.ID, first.ID)
	}
}

func TestDocumentTransactionRollsBackVersion(t *testing.T) {
	pool := openIntegrationDatabase(t)
	documents := NewDocumentRepository(pool)
	version := testDocumentVersion(t, "document-rollback")

	err := documents.InTransaction(context.Background(), func(transaction *DocumentRepository) error {
		_, _, err := transaction.PutDocumentVersion(context.Background(), version)
		if err != nil {
			return err
		}
		return errors.New("rollback requested")
	})
	if err == nil {
		t.Fatal("document transaction error = nil, want rollback error")
	}

	_, created, err := documents.PutDocumentVersion(context.Background(), version)
	if err != nil {
		t.Fatalf("put document version after rollback: %v", err)
	}
	if !created {
		t.Fatal("document version after rollback created = false, want true")
	}
}

func TestCorrectionLeavesOneEffectiveDecision(t *testing.T) {
	pool := openIntegrationDatabase(t)
	entities := NewEntityRepository(pool)
	ctx := context.Background()

	firstEntity, err := entities.CreateEntity(ctx, EntityInput{Kind: "person", DisplayName: "Synthetic Person A"})
	if err != nil {
		t.Fatalf("create first entity: %v", err)
	}
	secondEntity, err := entities.CreateEntity(ctx, EntityInput{Kind: "person", DisplayName: "Synthetic Person B"})
	if err != nil {
		t.Fatalf("create second entity: %v", err)
	}
	proposal, err := entities.CreateResolutionProposal(ctx, ResolutionProposalInput{MentionID: createSyntheticMention(t, pool)})
	if err != nil {
		t.Fatalf("create resolution proposal: %v", err)
	}
	first, err := entities.RecordDecision(ctx, ResolutionDecisionInput{
		ProposalID: proposal.ID,
		Outcome:    ResolutionOutcomeAccepted,
		EntityID:   firstEntity.ID,
	})
	if err != nil {
		t.Fatalf("record initial decision: %v", err)
	}
	second, err := entities.CorrectDecision(ctx, first.ID, ResolutionDecisionInput{
		Outcome:  ResolutionOutcomeAccepted,
		EntityID: secondEntity.ID,
	})
	if err != nil {
		t.Fatalf("correct decision: %v", err)
	}
	if second.SupersedesID != first.ID {
		t.Fatalf("correction supersedes %q, want %q", second.SupersedesID, first.ID)
	}

	effective, err := entities.EffectiveDecision(ctx, proposal.ID)
	if err != nil {
		t.Fatalf("load effective decision: %v", err)
	}
	if effective.ID != second.ID {
		t.Fatalf("effective decision ID = %q, want %q", effective.ID, second.ID)
	}
}

func TestCompleteAnalysisDeduplicatesStableInput(t *testing.T) {
	pool := openIntegrationDatabase(t)
	entities := NewEntityRepository(pool)
	analysis := NewAnalysisRepository(pool)
	ctx := context.Background()

	employee, err := entities.CreateEntity(ctx, EntityInput{Kind: "person", DisplayName: "Synthetic Employee"})
	if err != nil {
		t.Fatalf("create employee: %v", err)
	}
	manager, err := entities.CreateEntity(ctx, EntityInput{Kind: "person", DisplayName: "Synthetic Manager"})
	if err != nil {
		t.Fatalf("create manager: %v", err)
	}
	digest := sha256.Sum256([]byte("synthetic-analysis-input"))
	input := AnalysisInput{
		EmployeeEntityID:      employee.ID,
		ManagerEntityID:       manager.ID,
		InputDigest:           digest[:],
		AnalysisPromptVersion: "analyze-test-v1",
		PolicyVersion:         "policy-test-v1",
	}

	first, created, err := analysis.Complete(ctx, input)
	if err != nil {
		t.Fatalf("complete first analysis: %v", err)
	}
	if !created {
		t.Fatal("first analysis completion created = false, want true")
	}
	second, created, err := analysis.Complete(ctx, input)
	if err != nil {
		t.Fatalf("complete repeated analysis: %v", err)
	}
	if created {
		t.Fatal("repeated analysis completion created = true, want false")
	}
	if second.ID != first.ID {
		t.Fatalf("repeated analysis ID = %q, want %q", second.ID, first.ID)
	}
}

func openIntegrationDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv(testDatabaseURLEnvironmentVariable)
	if databaseURL == "" {
		t.Skipf("%s is not set", testDatabaseURLEnvironmentVariable)
	}

	pool, err := Open(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("open integration database: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func createSyntheticMention(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	documents := NewDocumentRepository(pool)
	version := testDocumentVersion(t, "document-mention")
	if _, _, err := documents.PutDocumentVersion(context.Background(), version); err != nil {
		t.Fatalf("put mention document version: %v", err)
	}
	span, err := knowledge.NewEvidenceSpan(knowledge.EvidenceSpanInput{
		Document:    version,
		TabID:       "tab-synthetic",
		StartOffset: 0,
		EndOffset:   len("Synthetic"),
		Quote:       "Synthetic",
	})
	if err != nil {
		t.Fatalf("new synthetic evidence span: %v", err)
	}
	storedSpan, err := documents.PutEvidenceSpan(context.Background(), span)
	if err != nil {
		t.Fatalf("put synthetic evidence span: %v", err)
	}
	entities := NewEntityRepository(pool)
	mention, err := entities.CreateMention(context.Background(), MentionInput{
		EvidenceSpanID: storedSpan.ID,
		Surface:        "Synthetic",
		Role:           "speaker",
	})
	if err != nil {
		t.Fatalf("create synthetic mention: %v", err)
	}
	return mention.ID
}

func testDocumentVersion(t *testing.T, providerDocumentID string) knowledge.DocumentVersion {
	t.Helper()
	version, err := knowledge.NewDocumentVersion(knowledge.DocumentVersionInput{
		Provider:           "synthetic-drive",
		ProviderDocumentID: providerDocumentID,
		RecordedAt:         time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC),
		Tabs: []source.Tab{{
			ID:    "tab-synthetic",
			Title: "Synthetic Transcript",
			Order: 0,
			Role:  source.TabRoleTranscript,
			Text:  "Synthetic meeting text.",
		}},
	})
	if err != nil {
		t.Fatalf("new synthetic document version: %v", err)
	}
	return version
}
