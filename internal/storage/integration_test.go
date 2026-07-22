package storage

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"stacks/internal/knowledge"
	"stacks/internal/source"
)

const testDatabaseURLEnvironmentVariable = "STACKS_TEST_DATABASE_URL"

func TestPutDocumentVersionIsIdempotent(t *testing.T) {
	pool := openIntegrationDatabase(t)
	documents := NewDocumentRepository(pool)
	version := testDocumentVersion(t, testIdentifier("document-idempotent"))

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
	version := testDocumentVersion(t, testIdentifier("document-rollback"))

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

	firstEntity, err := entities.CreateEntity(ctx, EntityInput{ID: uuid.NewString(), Kind: "person", DisplayName: "Synthetic Person A"})
	if err != nil {
		t.Fatalf("create first entity: %v", err)
	}
	if firstEntity.RecordedAt.IsZero() {
		t.Fatal("first entity recorded time is zero")
	}
	secondEntity, err := entities.CreateEntity(ctx, EntityInput{ID: uuid.NewString(), Kind: "person", DisplayName: "Synthetic Person B"})
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
	repeatedCorrection, err := entities.CorrectDecision(ctx, first.ID, ResolutionDecisionInput{
		Outcome:  ResolutionOutcomeAccepted,
		EntityID: secondEntity.ID,
	})
	if err != nil {
		t.Fatalf("repeat correction: %v", err)
	}
	if repeatedCorrection.ID != second.ID {
		t.Fatalf("repeated correction ID = %q, want %q", repeatedCorrection.ID, second.ID)
	}

	effective, err := entities.EffectiveDecision(ctx, proposal.ID)
	if err != nil {
		t.Fatalf("load effective decision: %v", err)
	}
	if effective.ID != second.ID {
		t.Fatalf("effective decision ID = %q, want %q", effective.ID, second.ID)
	}

	var decisionCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM stacks.resolution_decisions WHERE proposal_id = $1`, proposal.ID).Scan(&decisionCount); err != nil {
		t.Fatalf("count decision history: %v", err)
	}
	if decisionCount != 2 {
		t.Fatalf("decision history count = %d, want 2", decisionCount)
	}
	firstDetail, err := entities.ShowEntityDetail(ctx, firstEntity.ID)
	if err != nil {
		t.Fatalf("show superseded entity: %v", err)
	}
	if firstDetail.MentionCount != 0 || len(firstDetail.Evidence) != 0 {
		t.Fatalf("superseded entity detail = %#v, want no effective mentions or evidence", firstDetail)
	}
	secondDetail, err := entities.ShowEntityDetail(ctx, secondEntity.ID)
	if err != nil {
		t.Fatalf("show effective entity: %v", err)
	}
	if secondDetail.MentionCount != 1 || len(secondDetail.Evidence) != 1 {
		t.Fatalf("effective entity detail = %#v, want one effective mention and evidence", secondDetail)
	}
}

func TestInteractiveReviewActionsAppendHistoryAndRejectStaleState(t *testing.T) {
	pool := openIntegrationDatabase(t)
	repository := NewEntityRepository(pool)
	ctx := context.Background()

	firstEntity, err := repository.CreateEntity(ctx, EntityInput{ID: uuid.NewString(), Kind: "person", DisplayName: "Synthetic Review Person A"})
	if err != nil {
		t.Fatalf("create first review entity: %v", err)
	}
	secondEntity, err := repository.CreateEntity(ctx, EntityInput{ID: uuid.NewString(), Kind: "person", DisplayName: "Synthetic Review Person B"})
	if err != nil {
		t.Fatalf("create second review entity: %v", err)
	}
	proposal, err := repository.CreateResolutionProposal(ctx, ResolutionProposalInput{MentionID: createSyntheticMention(t, pool)})
	if err != nil {
		t.Fatalf("create review proposal: %v", err)
	}
	accepted, err := repository.RecordReviewDecision(ctx, ResolutionDecisionInput{
		ProposalID: proposal.ID,
		Outcome:    ResolutionOutcomeAccepted,
		EntityID:   firstEntity.ID,
	})
	if err != nil {
		t.Fatalf("accept review proposal: %v", err)
	}
	if _, err := repository.RecordReviewDecision(ctx, ResolutionDecisionInput{
		ProposalID: proposal.ID,
		Outcome:    ResolutionOutcomeAccepted,
		EntityID:   firstEntity.ID,
	}); err == nil {
		t.Fatal("repeat review acceptance error = nil, want effective-state error")
	}
	correction, err := repository.CorrectReviewDecision(ctx, accepted.ID, ResolutionDecisionInput{
		Outcome:  ResolutionOutcomeAccepted,
		EntityID: secondEntity.ID,
	})
	if err != nil {
		t.Fatalf("correct review decision: %v", err)
	}
	if correction.SupersedesID != accepted.ID {
		t.Fatalf("correction supersedes %q, want %q", correction.SupersedesID, accepted.ID)
	}
	if _, err := repository.CorrectReviewDecision(ctx, accepted.ID, ResolutionDecisionInput{
		Outcome:  ResolutionOutcomeAccepted,
		EntityID: firstEntity.ID,
	}); err == nil {
		t.Fatal("correct stale decision error = nil, want not effective error")
	}

	createProposal, err := repository.CreateResolutionProposal(ctx, ResolutionProposalInput{MentionID: createSyntheticMention(t, pool)})
	if err != nil {
		t.Fatalf("create new-person proposal: %v", err)
	}
	createdEntity, createdDecision, err := repository.CreateReviewPerson(ctx, CreateReviewPersonInput{
		ProposalID:  createProposal.ID,
		EntityID:    uuid.NewString(),
		Kind:        "person",
		DisplayName: "Synthetic Created Person",
		Aliases:     []AliasInput{{Type: "name", NormalizedValue: "synthetic created person"}},
	})
	if err != nil {
		t.Fatalf("create review person: %v", err)
	}
	if createdEntity.ID == "" || createdDecision.Outcome != ResolutionOutcomeCreated || createdDecision.EntityID != createdEntity.ID {
		t.Fatalf("created review result = (%#v, %#v), want created entity decision", createdEntity, createdDecision)
	}

	rejectedProposal, err := repository.CreateResolutionProposal(ctx, ResolutionProposalInput{MentionID: createSyntheticMention(t, pool)})
	if err != nil {
		t.Fatalf("create rejected proposal: %v", err)
	}
	if _, err := repository.RecordReviewDecision(ctx, ResolutionDecisionInput{ProposalID: rejectedProposal.ID, Outcome: ResolutionOutcomeRejected}); err != nil {
		t.Fatalf("reject review proposal: %v", err)
	}
}

func TestCompleteAnalysisDeduplicatesStableInput(t *testing.T) {
	pool := openIntegrationDatabase(t)
	entities := NewEntityRepository(pool)
	analysis := NewAnalysisRepository(pool)
	ctx := context.Background()

	employee, err := entities.CreateEntity(ctx, EntityInput{ID: uuid.NewString(), Kind: "person", DisplayName: "Synthetic Employee"})
	if err != nil {
		t.Fatalf("create employee: %v", err)
	}
	manager, err := entities.CreateEntity(ctx, EntityInput{ID: uuid.NewString(), Kind: "person", DisplayName: "Synthetic Manager"})
	if err != nil {
		t.Fatalf("create manager: %v", err)
	}
	version := testDocumentVersion(t, testIdentifier("document-analysis"))
	documents := NewDocumentRepository(pool)
	storedVersion, _, err := documents.PutDocumentVersion(ctx, version)
	if err != nil {
		t.Fatalf("put analysis document version: %v", err)
	}
	digest := version.Digest()
	input := AnalysisInput{
		EmployeeEntityID:      employee.ID,
		ManagerEntityID:       manager.ID,
		AnalysisPromptVersion: "analyze-test-v1",
		PolicyVersion:         "policy-test-v1",
		Inputs: []AnalysisInputReference{{
			Kind:   AnalysisInputKindDocumentVersion,
			ID:     storedVersion.ID,
			Digest: digest[:],
		}},
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

	var inputCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM stacks.analysis_inputs WHERE analysis_run_id = $1`, first.ID).Scan(&inputCount); err != nil {
		t.Fatalf("count stored analysis inputs: %v", err)
	}
	if inputCount != len(input.Inputs) {
		t.Fatalf("stored analysis input count = %d, want %d", inputCount, len(input.Inputs))
	}
	wrongDigestInput := input
	wrongDigestInput.Inputs = append([]AnalysisInputReference(nil), input.Inputs...)
	wrongDigestInput.Inputs[0].Digest = make([]byte, len(digest))
	if _, _, err := analysis.Complete(ctx, wrongDigestInput); err == nil {
		t.Fatal("analysis accepted an input digest that does not belong to its document version")
	}
	typeConfusedInput := input
	typeConfusedInput.Inputs = append([]AnalysisInputReference(nil), input.Inputs...)
	typeConfusedInput.Inputs[0].Kind = AnalysisInputKindSignal
	if _, _, err := analysis.Complete(ctx, typeConfusedInput); err == nil {
		t.Fatal("analysis accepted a document version ID as a signal input")
	}
}

func TestStorageRetriesDoNotDuplicateGraphRecords(t *testing.T) {
	pool := openIntegrationDatabase(t)
	ctx := context.Background()
	entities := NewEntityRepository(pool)
	graph := NewGraphRepository(pool)

	entity, err := entities.CreateEntity(ctx, EntityInput{ID: uuid.NewString(), Kind: "person", DisplayName: "Synthetic Retry Person"})
	if err != nil {
		t.Fatalf("create retry entity: %v", err)
	}
	aliasInput := AliasInput{EntityID: entity.ID, NormalizedValue: "synthetic retry person", Type: "name"}
	firstAlias, err := entities.PutAlias(ctx, aliasInput)
	if err != nil {
		t.Fatalf("put first alias: %v", err)
	}
	secondAlias, err := entities.PutAlias(ctx, aliasInput)
	if err != nil {
		t.Fatalf("put repeated alias: %v", err)
	}
	if secondAlias.ID != firstAlias.ID {
		t.Fatalf("repeated alias ID = %q, want %q", secondAlias.ID, firstAlias.ID)
	}

	mentionID, spanID := createSyntheticMentionAndSpan(t, pool)
	mentionInput := MentionInput{EvidenceSpanID: spanID, Surface: "Synthetic", Role: "speaker"}
	firstMention, err := entities.CreateMention(ctx, mentionInput)
	if err != nil {
		t.Fatalf("load first mention: %v", err)
	}
	secondMention, err := entities.CreateMention(ctx, mentionInput)
	if err != nil {
		t.Fatalf("load repeated mention: %v", err)
	}
	if firstMention.ID != mentionID || secondMention.ID != mentionID {
		t.Fatal("repeated mention did not return its stable ID")
	}

	proposalInput := ResolutionProposalInput{MentionID: mentionID}
	proposal, err := entities.CreateResolutionProposal(ctx, proposalInput)
	if err != nil {
		t.Fatalf("create proposal: %v", err)
	}
	repeatedProposal, err := entities.CreateResolutionProposal(ctx, proposalInput)
	if err != nil {
		t.Fatalf("create repeated proposal: %v", err)
	}
	if repeatedProposal.ID != proposal.ID {
		t.Fatalf("repeated proposal ID = %q, want %q", repeatedProposal.ID, proposal.ID)
	}
	confidence := 0.75
	candidateInput := ResolutionCandidateInput{ProposalID: proposal.ID, EntityID: entity.ID, Rank: 0, Confidence: &confidence, Reason: "synthetic"}
	firstCandidate, err := entities.PutCandidate(ctx, candidateInput)
	if err != nil {
		t.Fatalf("put candidate: %v", err)
	}
	secondCandidate, err := entities.PutCandidate(ctx, candidateInput)
	if err != nil {
		t.Fatalf("put repeated candidate: %v", err)
	}
	if secondCandidate.ID != firstCandidate.ID {
		t.Fatalf("repeated candidate ID = %q, want %q", secondCandidate.ID, firstCandidate.ID)
	}

	observationInput := ObservationInput{
		ID:              uuid.NewString(),
		SubjectEntityID: entity.ID,
		Predicate:       "interacted_with",
		Derivation:      "synthetic",
		EpistemicStatus: "inferred",
	}
	firstObservation, err := graph.CompleteObservation(ctx, observationInput, []string{spanID})
	if err != nil {
		t.Fatalf("complete observation: %v", err)
	}
	secondObservation, err := graph.CompleteObservation(ctx, observationInput, []string{spanID})
	if err != nil {
		t.Fatalf("complete repeated observation: %v", err)
	}
	if secondObservation.ID != firstObservation.ID {
		t.Fatalf("repeated observation ID = %q, want %q", secondObservation.ID, firstObservation.ID)
	}

	signalInput := SignalInput{
		ID:                uuid.NewString(),
		ObservationID:     firstObservation.ID,
		Category:          "delegation_autonomy",
		Direction:         "strengthening",
		ExtractionModelID: "synthetic-model",
		PromptVersion:     "synthetic-v1",
		Confidence:        0.8,
	}
	firstSignal, err := graph.CompleteSignal(ctx, signalInput, []SignalEvidenceInput{{EvidenceSpanID: spanID, Role: "supporting"}})
	if err != nil {
		t.Fatalf("complete signal: %v", err)
	}
	secondSignal, err := graph.CompleteSignal(ctx, signalInput, []SignalEvidenceInput{{EvidenceSpanID: spanID, Role: "supporting"}})
	if err != nil {
		t.Fatalf("complete repeated signal: %v", err)
	}
	if secondSignal.ID != firstSignal.ID {
		t.Fatalf("repeated signal ID = %q, want %q", secondSignal.ID, firstSignal.ID)
	}
	changedObservation := observationInput
	changedObservation.Predicate = "different_interaction"
	if _, err := graph.CompleteObservation(ctx, changedObservation, []string{spanID}); err == nil {
		t.Fatal("changed observation payload with existing ID succeeded")
	}
	changedSignal := signalInput
	changedSignal.Direction = "weakening"
	if _, err := graph.CompleteSignal(ctx, changedSignal, []SignalEvidenceInput{{EvidenceSpanID: spanID, Role: "supporting"}}); err == nil {
		t.Fatal("changed signal payload with existing ID succeeded")
	}
}

func TestPutEvidenceSpanRejectsImmutableQuoteConflict(t *testing.T) {
	pool := openIntegrationDatabase(t)
	version := testDocumentVersion(t, testIdentifier("document-evidence-conflict"))
	documents := NewDocumentRepository(pool)
	if _, _, err := documents.PutDocumentVersion(context.Background(), version); err != nil {
		t.Fatalf("put evidence document version: %v", err)
	}
	span, err := knowledge.NewEvidenceSpan(knowledge.EvidenceSpanInput{
		Document: version, TabID: "tab-synthetic", StartOffset: 0, EndOffset: len("Synthetic"), Quote: "Synthetic",
	})
	if err != nil {
		t.Fatalf("new evidence span: %v", err)
	}
	stored, err := documents.PutEvidenceSpan(context.Background(), span)
	if err != nil {
		t.Fatalf("put evidence span: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE stacks.evidence_spans SET quote = 'corrupted' WHERE id = $1`, stored.ID); err != nil {
		t.Fatalf("create synthetic immutable conflict: %v", err)
	}
	if _, err := documents.PutEvidenceSpan(context.Background(), span); err == nil {
		t.Fatal("repeated evidence span with conflicting stored quote succeeded")
	}
}

func TestCompleteSignalRejectsNotesOnlyEvidence(t *testing.T) {
	pool := openIntegrationDatabase(t)
	ctx := context.Background()
	version := testDocumentVersionWithRole(t, testIdentifier("document-notes-signal"), source.TabRoleGeminiNotes)
	documents := NewDocumentRepository(pool)
	if _, _, err := documents.PutDocumentVersion(ctx, version); err != nil {
		t.Fatalf("put notes document version: %v", err)
	}
	span, err := knowledge.NewEvidenceSpan(knowledge.EvidenceSpanInput{
		Document: version, TabID: "tab-synthetic", StartOffset: 0, EndOffset: len("Synthetic"), Quote: "Synthetic",
	})
	if err != nil {
		t.Fatalf("new notes evidence span: %v", err)
	}
	storedSpan, err := documents.PutEvidenceSpan(ctx, span)
	if err != nil {
		t.Fatalf("put notes evidence span: %v", err)
	}
	graph := NewGraphRepository(pool)
	observation, err := graph.CompleteObservation(ctx, ObservationInput{
		ID: uuid.NewString(), Predicate: "interacted_with", Derivation: "synthetic", EpistemicStatus: "inferred",
	}, []string{storedSpan.ID})
	if err != nil {
		t.Fatalf("complete notes observation: %v", err)
	}
	_, err = graph.CompleteSignal(ctx, SignalInput{
		ID: uuid.NewString(), ObservationID: observation.ID, Category: "delegation_autonomy", Direction: "strengthening",
		ExtractionModelID: "synthetic-model", PromptVersion: "synthetic-v1", Confidence: 0.8,
	}, []SignalEvidenceInput{{EvidenceSpanID: storedSpan.ID, Role: "supporting"}})
	if err == nil {
		t.Fatal("notes-only signal completion succeeded")
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
	mentionID, _ := createSyntheticMentionAndSpan(t, pool)
	return mentionID
}

func createSyntheticMentionAndSpan(t *testing.T, pool *pgxpool.Pool) (string, string) {
	t.Helper()
	documents := NewDocumentRepository(pool)
	version := testDocumentVersion(t, testIdentifier("document-mention"))
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
	return mention.ID, storedSpan.ID
}

func testIdentifier(prefix string) string {
	return prefix + "-" + uuid.NewString()
}

func testDocumentVersion(t *testing.T, providerDocumentID string) knowledge.DocumentVersion {
	return testDocumentVersionWithRole(t, providerDocumentID, source.TabRoleTranscript)
}

func testDocumentVersionWithRole(t *testing.T, providerDocumentID string, role source.TabRole) knowledge.DocumentVersion {
	t.Helper()
	version, err := knowledge.NewDocumentVersion(knowledge.DocumentVersionInput{
		Provider:           "synthetic-drive",
		ProviderDocumentID: providerDocumentID,
		RecordedAt:         time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC),
		Tabs: []source.Tab{{
			ID:    "tab-synthetic",
			Title: "Synthetic Transcript",
			Order: 0,
			Role:  role,
			Text:  "Synthetic meeting text.",
		}},
	})
	if err != nil {
		t.Fatalf("new synthetic document version: %v", err)
	}
	return version
}
