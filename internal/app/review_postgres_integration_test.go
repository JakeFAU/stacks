package app

import (
	"context"
	"testing"
	"time"

	postgres "github.com/JakeFAU/stacks/adapters/postgres"
	"github.com/JakeFAU/stacks/adapters/postgres/coremigrations"
	"github.com/JakeFAU/stacks/adapters/postgres/directorymigrations"
	"github.com/JakeFAU/stacks/adapters/postgres/migration"
	"github.com/JakeFAU/stacks/adapters/postgres/postgrestest"
	"github.com/jackc/pgx/v5"

	"stacks/internal/cli"
	"stacks/internal/directory"
	"stacks/internal/entity"
)

const reviewRepositoryIntegrationTimeout = 15 * time.Second

var reviewRepositoryIntegrationTime = time.Date(
	2026,
	time.July,
	26,
	19,
	0,
	0,
	0,
	time.UTC,
)

type reviewRepositoryIntegrationFixture struct {
	ctx      context.Context
	database *postgres.Database
	admin    *pgx.Conn
}

func TestReviewRepositoryMalformedDirectoryEvidenceIsFailSoftInPostgreSQL(t *testing.T) {
	profile := entity.DirectoryProfile{
		Provider: "google_people", SubjectID: "people/reviewer-live",
		Source: entity.DirectorySourceDomainProfile, DisplayName: "Reviewer Person",
		Emails: []entity.DirectoryEmail{{
			Value: "reviewer.live@example.test", Primary: true,
		}},
		ObservedAt: reviewRepositoryIntegrationTime.Add(-time.Hour),
	}
	base := directory.ReviewerVerification{
		Query: entity.DirectoryQuery{
			Kind: entity.DirectoryQueryEmail, Email: "reviewer.live@example.test",
			EmailEvidence: entity.EmailEvidenceReviewerSupplied,
		},
		Lookup: directory.LookupResult{
			Provider: "google_people", Outcome: entity.DirectoryReview,
			Profiles: []entity.DirectoryProfile{profile},
		},
		Evaluation: entity.DirectoryEvaluation{
			Outcome:    entity.DirectoryReview,
			Candidates: []entity.DirectoryProfile{profile},
		},
		AttemptCount: 1,
		RecordedAt:   reviewRepositoryIntegrationTime.Add(-time.Minute),
	}
	tests := []struct {
		name         string
		mutate       func(*directory.ReviewerVerification)
		wantAttempts int
	}{
		{
			name: "valid optional evidence",
			mutate: func(*directory.ReviewerVerification) {
			},
			wantAttempts: 1,
		},
		{
			name: "malformed evaluation candidate",
			mutate: func(value *directory.ReviewerVerification) {
				value.Evaluation.Candidates = append(
					[]entity.DirectoryProfile(nil),
					value.Evaluation.Candidates...,
				)
				value.Evaluation.Candidates[0].SubjectID = ""
			},
		},
		{
			name: "noncanonical lookup observation time",
			mutate: func(value *directory.ReviewerVerification) {
				value.Lookup.Profiles = append(
					[]entity.DirectoryProfile(nil),
					value.Lookup.Profiles...,
				)
				value.Lookup.Profiles[0].ObservedAt =
					value.Lookup.Profiles[0].ObservedAt.Add(time.Nanosecond)
			},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newReviewRepositoryIntegrationFixture(t)
			verification := base
			testCase.mutate(&verification)
			repository := NewReviewRepository(
				postgres.ReviewerStore{
					Database:         fixture.database,
					IncludeDirectory: true,
				},
				&sequenceIDGenerator{values: []string{
					"entity:reviewer-live",
					"decision:reviewer-live",
					"alias:reviewer-live:1",
					"alias:reviewer-live:2",
					"alias:reviewer-live:3",
				}},
				&recordingClock{now: reviewRepositoryIntegrationTime},
			)
			result, err := repository.CreateReviewPerson(
				fixture.ctx,
				"proposal:reviewer-live",
				cli.CreatePersonInput{
					Name: "Reviewer Person", Email: "reviewer.live@example.test",
				},
				&verification,
			)
			if err != nil {
				t.Fatalf("CreateReviewPerson() error = %v", err)
			}
			if result.ID != "decision:reviewer-live" {
				t.Fatalf("review decision = %#v, want committed explicit decision", result)
			}
			var decisions, attempts int
			if err := fixture.admin.QueryRow(
				fixture.ctx,
				`SELECT
					(SELECT count(*) FROM stacks_core.resolution_decisions
					 WHERE proposal_id = 'proposal:reviewer-live'),
					(SELECT count(*) FROM stacks_directory.lookup_attempts
					 WHERE proposal_id = 'proposal:reviewer-live')`,
			).Scan(&decisions, &attempts); err != nil {
				t.Fatalf("inspect reviewer persistence: %v", err)
			}
			if decisions != 1 || attempts != testCase.wantAttempts {
				t.Fatalf(
					"review decisions/attempts = %d/%d, want 1/%d",
					decisions,
					attempts,
					testCase.wantAttempts,
				)
			}
		})
	}
}

func newReviewRepositoryIntegrationFixture(
	t testing.TB,
) reviewRepositoryIntegrationFixture {
	t.Helper()
	isolated := postgrestest.NewDatabase(t)
	core, err := coremigrations.Manifest()
	if err != nil {
		t.Fatalf("coremigrations.Manifest() error = %v", err)
	}
	directoryManifest, err := directorymigrations.Manifest()
	if err != nil {
		t.Fatalf("directorymigrations.Manifest() error = %v", err)
	}
	applicationConfig, err := pgx.ParseConfig(isolated.ApplicationURL())
	if err != nil {
		t.Fatalf("parse application URL: %v", err)
	}
	ctx, cancel := context.WithTimeout(
		context.Background(),
		reviewRepositoryIntegrationTimeout,
	)
	t.Cleanup(cancel)
	if _, err := (migration.Migrator{
		DatabaseURL:     isolated.AdminURL(),
		ApplicationRole: applicationConfig.User,
		Manifests:       []migration.Manifest{core, directoryManifest},
	}).Apply(ctx); err != nil {
		t.Fatalf("install reviewer integration scopes: %v", err)
	}
	database, err := postgres.Open(ctx, isolated.ApplicationURL())
	if err != nil {
		t.Fatalf("open reviewer integration database: %v", err)
	}
	t.Cleanup(database.Close)
	admin, err := pgx.Connect(ctx, isolated.AdminURL())
	if err != nil {
		t.Fatalf("connect reviewer integration admin: %v", err)
	}
	t.Cleanup(func() {
		_ = admin.Close(context.Background())
	})
	seedReviewRepositoryIntegrationProposal(t, ctx, admin)
	return reviewRepositoryIntegrationFixture{
		ctx: ctx, database: database, admin: admin,
	}
}

func seedReviewRepositoryIntegrationProposal(
	t testing.TB,
	ctx context.Context,
	admin *pgx.Conn,
) {
	t.Helper()
	if _, err := admin.Exec(ctx, `
		INSERT INTO stacks_core.source_documents (
			id, provider, provider_document_id, current_version_id, created_at
		)
		VALUES ('source:reviewer-live', 'synthetic', 'document:reviewer-live', NULL, $1);
		INSERT INTO stacks_core.document_versions (
			id, source_document_id, digest_version, content_digest, title,
			locator, provider_version, modified_at, source_time, recorded_at
		)
		VALUES (
			'version:reviewer-live', 'source:reviewer-live',
			'synthetic.document.v1', decode(repeat('11', 32), 'hex'),
			'Synthetic reviewer record', 'synthetic://reviewer-live', 'version-1',
			$1, NULL, $1
		);
		UPDATE stacks_core.source_documents
		SET current_version_id = 'version:reviewer-live'
		WHERE id = 'source:reviewer-live';
		INSERT INTO stacks_core.document_sections (
			document_version_id, section_id, title, parent_id, path,
			section_order, role, content
		)
		VALUES (
			'version:reviewer-live', 'section:reviewer-live', 'Synthetic section',
			'', ARRAY['Synthetic section'], 0, 'transcript', 'Reviewer Person'
		);
		INSERT INTO stacks_core.evidence_spans (
			id, document_version_id, section_id, digest_version, digest,
			start_offset, end_offset, quote, recorded_at
		)
		VALUES (
			'evidence:reviewer-live', 'version:reviewer-live',
			'section:reviewer-live', 'synthetic.evidence.v1',
			decode(repeat('22', 32), 'hex'), 0, 15, 'Reviewer Person', $1
		);
		INSERT INTO stacks_core.extraction_runs (
			id, document_version_id, derivation_digest_version,
			derivation_digest, method, version, provider, data_mode, model,
			prompt_version, schema_digest, max_output_tokens, recorded_at,
			state, completed_at, write_set_digest_version, write_set_digest
		)
		VALUES (
			'run:reviewer-live', 'version:reviewer-live', 'synthetic.derivation.v1',
			decode(repeat('33', 32), 'hex'), 'synthetic', 'v1', 'synthetic',
			'personal', 'synthetic-model', 'synthetic-prompt',
			decode(repeat('44', 32), 'hex'), 1, $1, 'completed', $1,
			'synthetic.write-set.v1', decode(repeat('55', 32), 'hex')
		);
		INSERT INTO stacks_core.mentions (
			id, evidence_id, derivation_run_id, surface, normalized_name,
			proposed_email, proposed_email_evidence_id, role, recorded_at
		)
		VALUES (
			'mention:reviewer-live', 'evidence:reviewer-live', 'run:reviewer-live',
			'Reviewer Person', 'reviewer person', 'reviewer.live@example.test',
			'evidence:reviewer-live', 'speaker', $1
		);
		INSERT INTO stacks_core.resolution_proposals (
			id, mention_id, reason_code, recorded_at
		)
		VALUES (
			'proposal:reviewer-live', 'mention:reviewer-live', 'identity_review', $1
		);
		INSERT INTO stacks_core.resolution_proposal_evidence (
			proposal_id, evidence_id, evidence_order
		)
		VALUES ('proposal:reviewer-live', 'evidence:reviewer-live', 0);
		INSERT INTO stacks_core.admission_targets (
			target_kind, target_id, recorded_at
		)
		VALUES
			('extraction_run', 'run:reviewer-live', $1),
			('mention', 'mention:reviewer-live', $1);
		INSERT INTO stacks_core.admission_decisions (
			id, target_kind, target_id, outcome, reason_code, authority,
			recorded_at, supersedes_id, digest_version, digest
		)
		VALUES
			('admission-run:reviewer-live', 'extraction_run', 'run:reviewer-live',
			 'admitted', 'validated', 'policy', $1, NULL,
			 'synthetic.admission.v1', decode(repeat('66', 32), 'hex')),
			('admission-mention:reviewer-live', 'mention', 'mention:reviewer-live',
			 'admitted', 'validated', 'policy', $1, NULL,
			 'synthetic.admission.v1', decode(repeat('77', 32), 'hex'))`,
		pgx.QueryExecModeSimpleProtocol,
		reviewRepositoryIntegrationTime.Add(-2*time.Hour),
	); err != nil {
		t.Fatalf("seed reviewer integration proposal: %v", err)
	}
}
