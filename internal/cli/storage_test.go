package cli

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"stacks/internal/directory"
	"stacks/internal/entity"
	"stacks/internal/storage"
)

func TestStorageReviewStoreRejectsTransitionsWithoutRepository(t *testing.T) {
	store := NewStorageReviewStore(nil)
	cases := []struct {
		name string
		call func() error
	}{
		{name: "accept", call: func() error {
			_, err := store.AcceptReviewProposal(context.Background(), "proposal-1", "person-1")
			return err
		}},
		{name: "reject", call: func() error { _, err := store.RejectReviewProposal(context.Background(), "proposal-1"); return err }},
		{name: "correct", call: func() error {
			_, err := store.CorrectReviewDecision(context.Background(), "decision-1", "person-1")
			return err
		}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			err := testCase.call()
			if err == nil || !strings.Contains(err.Error(), "repository is not configured") {
				t.Fatalf("transition error = %v, want repository configuration error", err)
			}
		})
	}
}

func TestReviewProposalFromStoragePreservesOnlyBoundedDirectoryProjection(t *testing.T) {
	detail := storage.ResolutionProposalDetail{
		ID:      "proposal-directory",
		Context: "Synthetic cited context",
		Candidates: []storage.ResolutionCandidateDetail{{
			DirectoryProfileID: "profile-directory",
			DisplayName:        "Synthetic Directory Person",
			MaskedEmail:        "r***@corp.example",
			Source:             "domain_profile",
			Reason:             "directory name candidate requires review",
		}},
	}

	proposal := reviewProposalFromStorage(detail)
	if len(proposal.Candidates) != 1 {
		t.Fatalf("candidate count = %d, want 1", len(proposal.Candidates))
	}
	candidate := proposal.Candidates[0]
	if candidate.DirectoryProfileID != "profile-directory" ||
		candidate.DisplayName != "Synthetic Directory Person" ||
		candidate.MaskedEmail != "r***@corp.example" ||
		candidate.Source != "domain_profile" {
		t.Fatalf("directory candidate = %#v, want bounded projection", candidate)
	}
}

func TestReviewerEmailVerificationIsAdditive(t *testing.T) {
	malformed := directory.ReviewerVerification{
		Query: entity.DirectoryQuery{
			Kind:          entity.DirectoryQueryEmail,
			Email:         "reviewer@corp.example",
			EmailEvidence: entity.EmailEvidenceReviewerSupplied,
		},
	}
	verifier := &fakeReviewerEmailVerifier{verification: malformed}
	got, err := verifyReviewerEmail(
		context.Background(),
		verifier,
		"reviewer@corp.example",
	)
	if err != nil {
		t.Fatalf("verifyReviewerEmail() error = %v", err)
	}
	if got != nil ||
		verifier.calls != 1 ||
		verifier.email != "reviewer@corp.example" {
		t.Fatalf("malformed reviewer verification = %#v calls/email = %d/%q, want omitted metadata", got, verifier.calls, verifier.email)
	}

	verification := directory.ReviewerVerification{
		Query: malformed.Query,
		Lookup: directory.LookupResult{
			Outcome: entity.DirectoryNoMatch,
		},
		Evaluation: entity.DirectoryEvaluation{
			Outcome: entity.DirectoryNoMatch,
		},
		AttemptCount: 1,
		RecordedAt:   time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC),
	}
	verifier.verification = verification
	got, err = verifyReviewerEmail(
		context.Background(),
		verifier,
		"reviewer@corp.example",
	)
	if err != nil || got == nil || !reflect.DeepEqual(*got, verification) {
		t.Fatalf("valid reviewer verification = %#v, %v; want complete additive metadata", got, err)
	}

	verifier.err = errors.New("synthetic private provider failure")
	got, err = verifyReviewerEmail(
		context.Background(),
		verifier,
		"reviewer@corp.example",
	)
	if err != nil || got != nil {
		t.Fatalf("provider failure result = %#v, %v; want ignored additive failure", got, err)
	}

	verifier.err = nil
	verifier.verification = directory.ReviewerVerification{
		Evaluation: entity.DirectoryEvaluation{Outcome: entity.DirectoryDisabled},
	}
	got, err = verifyReviewerEmail(
		context.Background(),
		verifier,
		"reviewer@corp.example",
	)
	if err != nil || got != nil {
		t.Fatalf("disabled verification result = %#v, %v; want explicit review decision preserved", got, err)
	}
}

func TestStorageReviewStoreMalformedVerifierResultPreservesExplicitCreation(t *testing.T) {
	databaseURL := os.Getenv("STACKS_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("STACKS_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := storage.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open integration database: %v", err)
	}
	t.Cleanup(pool.Close)

	suffix := uuid.NewString()
	recordedAt := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	sourceID := uuid.NewString()
	versionID := uuid.NewString()
	tabID := uuid.NewString()
	spanID := uuid.NewString()
	mentionID := uuid.NewString()
	proposalID := uuid.NewString()
	email := "malformed." + strings.ReplaceAll(suffix, "-", "") + "@synthetic.example"
	quote := "Synthetic Malformed Reviewer <" + email + ">"
	versionDigest := sha256.Sum256([]byte("version-" + suffix))
	contentDigest := sha256.Sum256([]byte(quote))
	for _, statement := range []struct {
		operation string
		query     string
		arguments []any
	}{
		{
			operation: "source",
			query: `INSERT INTO stacks.source_documents
				(id, provider, provider_document_id, recorded_at)
				VALUES ($1, 'synthetic-drive', $2, $3)`,
			arguments: []any{sourceID, "malformed-" + suffix, recordedAt},
		},
		{
			operation: "version",
			query: `INSERT INTO stacks.document_versions
				(id, source_document_id, digest, recorded_at)
				VALUES ($1, $2, $3, $4)`,
			arguments: []any{versionID, sourceID, versionDigest[:], recordedAt},
		},
		{
			operation: "tab",
			query: `INSERT INTO stacks.document_tabs
				(id, document_version_id, provider_tab_id, title, title_path,
				 display_order, role, content, content_digest)
				VALUES ($1, $2, 'synthetic-tab', 'Synthetic Transcript',
				 ARRAY['Synthetic Transcript'], 0, 'transcript', $3, $4)`,
			arguments: []any{tabID, versionID, quote, contentDigest[:]},
		},
		{
			operation: "span",
			query: `INSERT INTO stacks.evidence_spans
				(id, document_tab_id, start_offset, end_offset, quote)
				VALUES ($1, $2, 0, $3, $4)`,
			arguments: []any{spanID, tabID, len(quote), quote},
		},
		{
			operation: "mention",
			query: `INSERT INTO stacks.mentions
				(id, evidence_span_id, surface, normalized_name, proposed_email,
				 proposed_email_evidence_span_id, role, recorded_at,
				 currently_admissible)
				VALUES ($1, $2, 'Synthetic Malformed Reviewer',
				 'synthetic malformed reviewer', $3, $2, 'speaker', $4, true)`,
			arguments: []any{mentionID, spanID, email, recordedAt},
		},
		{
			operation: "proposal",
			query: `INSERT INTO stacks.resolution_proposals
				(id, mention_id, status, derivation, recorded_at)
				VALUES ($1, $2, 'pending', 'synthetic_test', $3)`,
			arguments: []any{proposalID, mentionID, recordedAt},
		},
	} {
		if _, err := pool.Exec(ctx, statement.query, statement.arguments...); err != nil {
			t.Fatalf("seed malformed verifier %s: %v", statement.operation, err)
		}
	}

	verifier := &fakeReviewerEmailVerifier{
		verification: directory.ReviewerVerification{
			Query: entity.DirectoryQuery{
				Kind:          entity.DirectoryQueryEmail,
				Email:         email,
				EmailEvidence: entity.EmailEvidenceReviewerSupplied,
			},
		},
	}
	decision, err := NewStorageReviewStore(
		storage.NewEntityRepository(pool),
		verifier,
	).CreateReviewPerson(ctx, proposalID, CreatePersonInput{
		Name:  "Synthetic Malformed Reviewer",
		Email: email,
	})
	if err != nil {
		t.Fatalf("create explicit reviewer person with malformed verification: %v", err)
	}
	var attempts, identityAssertions, aliases int
	if err := pool.QueryRow(ctx, `
		SELECT
		    (SELECT count(*)
		     FROM stacks.directory_lookup_attempts
		     WHERE mention_id = $1),
		    (SELECT count(*)
		     FROM stacks.entity_directory_identity_assertions
		     WHERE decision_id = $2),
		    (SELECT count(*)
		     FROM stacks.entity_alias_assertions
		     WHERE decision_id = $2)`,
		mentionID,
		decision.ID,
	).Scan(&attempts, &identityAssertions, &aliases); err != nil {
		t.Fatalf("load malformed verifier creation result: %v", err)
	}
	if decision.Outcome != string(storage.ResolutionOutcomeCreated) ||
		attempts != 0 ||
		identityAssertions != 0 ||
		aliases != 2 {
		t.Fatalf(
			"malformed verifier outcome/attempts/identities/aliases = %q/%d/%d/%d, want created/0/0/2",
			decision.Outcome,
			attempts,
			identityAssertions,
			aliases,
		)
	}
}

type fakeReviewerEmailVerifier struct {
	verification directory.ReviewerVerification
	err          error
	calls        int
	email        string
}

func (verifier *fakeReviewerEmailVerifier) VerifyReviewerEmail(
	_ context.Context,
	email string,
) (directory.ReviewerVerification, error) {
	verifier.calls++
	verifier.email = email
	return verifier.verification, verifier.err
}
