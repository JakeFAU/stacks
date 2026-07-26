package postgres_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	postgres "github.com/JakeFAU/stacks/adapters/postgres"
	"github.com/JakeFAU/stacks/adapters/postgres/coremigrations"
	"github.com/JakeFAU/stacks/adapters/postgres/migration"
	"github.com/JakeFAU/stacks/adapters/postgres/postgrestest"
	"github.com/JakeFAU/stacks/core/evidence"
	"github.com/jackc/pgx/v5"
)

const (
	documentRepositoryTimeout = 10 * time.Second
	syntheticProvider         = "synthetic-source"
	syntheticProviderVersion  = "provider-version-opaque"
)

var (
	documentModifiedAt = time.Date(2026, time.July, 25, 14, 0, 0, 123456000, time.UTC)
	documentRecordedAt = time.Date(2026, time.July, 25, 14, 5, 0, 654321000, time.UTC)
)

type documentRepositoryFixture struct {
	ctx            context.Context
	database       *postgres.Database
	admin          *pgx.Conn
	applicationURL string
}

func TestEvidenceSpanRoundTripsExactUTF8RangeAndDigest(t *testing.T) {
	fixture := newDocumentRepositoryFixture(t)
	document := canonicalDocument(t, "source-opaque-a", "revision-a", documentRecordedAt)
	put, err := fixture.database.PutDocumentVersion(fixture.ctx, document)
	if err != nil {
		t.Fatalf("PutDocumentVersion() error = %v", err)
	}
	span, err := evidence.NewEvidenceSpan(evidence.EvidenceSpanInput{
		Document:    document,
		SectionID:   "section-transcript",
		StartOffset: 6,
		EndOffset:   11,
		Quote:       "café",
		RecordedAt: time.Date(
			2026,
			time.July,
			25,
			10,
			7,
			0,
			987654999,
			time.FixedZone("synthetic", -4*60*60),
		),
	})
	if err != nil {
		t.Fatalf("NewEvidenceSpan() error = %v", err)
	}
	if err := fixture.database.InTransaction(fixture.ctx, func(transaction *postgres.Transaction) error {
		created, err := transaction.PutEvidenceSpan(fixture.ctx, span)
		if err != nil {
			return err
		}
		if !created {
			return fmt.Errorf("first PutEvidenceSpan() created = false")
		}
		created, err = transaction.PutEvidenceSpan(fixture.ctx, span)
		if err != nil {
			return err
		}
		if created {
			return fmt.Errorf("repeated PutEvidenceSpan() created = true")
		}
		return nil
	}); err != nil {
		t.Fatalf("persist evidence span: %v", err)
	}

	got, err := fixture.database.LoadEvidenceSpan(fixture.ctx, span.ID())
	if err != nil {
		t.Fatalf("LoadEvidenceSpan() error = %v", err)
	}
	if got.ID() != span.ID() ||
		got.Provider() != syntheticProvider ||
		got.ProviderDocumentID() != "source-opaque-a" ||
		got.DocumentDigest() != document.Digest() ||
		got.SectionID() != "section-transcript" ||
		got.StartOffset() != 6 ||
		got.EndOffset() != 11 ||
		got.Text() != "café" ||
		got.Locator() != document.Locator() ||
		got.RecordedAt() != span.RecordedAt() ||
		got.DigestVersion() != span.DigestVersion() ||
		got.Digest() != span.Digest() {
		t.Fatalf("loaded evidence span does not match exact UTF-8 source range")
	}
	if put.Ref.VersionID == "" {
		t.Fatal("document version fixture has blank ID")
	}
}

func TestCurrentVersionPointerRejectsVersionFromDifferentSource(t *testing.T) {
	fixture := newDocumentRepositoryFixture(t)
	first, err := fixture.database.PutDocumentVersion(
		fixture.ctx,
		canonicalDocument(t, "source-opaque-a", "revision-a", documentRecordedAt),
	)
	if err != nil {
		t.Fatalf("first PutDocumentVersion() error = %v", err)
	}
	second, err := fixture.database.PutDocumentVersion(
		fixture.ctx,
		canonicalDocument(t, "source-opaque-b", "revision-a", documentRecordedAt),
	)
	if err != nil {
		t.Fatalf("second PutDocumentVersion() error = %v", err)
	}

	err = fixture.database.InTransaction(fixture.ctx, func(transaction *postgres.Transaction) error {
		return transaction.SetCurrentDocumentVersion(
			fixture.ctx,
			first.Ref.SourceDocumentID,
			second.Ref.VersionID,
		)
	})
	if err == nil {
		t.Fatal("SetCurrentDocumentVersion() error = nil, want cross-source rejection")
	}
	var currentVersion *string
	if err := fixture.admin.QueryRow(
		fixture.ctx,
		`SELECT current_version_id FROM stacks_core.source_documents WHERE id = $1`,
		first.Ref.SourceDocumentID,
	).Scan(&currentVersion); err != nil {
		t.Fatalf("inspect current version after rejection: %v", err)
	}
	if currentVersion != nil {
		t.Fatalf("current version after cross-source rejection = %q, want nil", *currentVersion)
	}
}

func TestApplicationRoleCannotRewriteImmutableEvidence(t *testing.T) {
	fixture := newDocumentRepositoryFixture(t)
	document := canonicalDocument(t, "source-opaque-a", "revision-a", documentRecordedAt)
	put, err := fixture.database.PutDocumentVersion(fixture.ctx, document)
	if err != nil {
		t.Fatalf("PutDocumentVersion() error = %v", err)
	}
	if err := fixture.database.InTransaction(fixture.ctx, func(transaction *postgres.Transaction) error {
		if err := transaction.SetCurrentDocumentVersion(
			fixture.ctx,
			put.Ref.SourceDocumentID,
			put.Ref.VersionID,
		); err != nil {
			return err
		}
		span, err := evidence.NewEvidenceSpan(evidence.EvidenceSpanInput{
			Document: document, SectionID: "section-transcript",
			StartOffset: 0, EndOffset: 5, Quote: "Alpha", RecordedAt: document.RecordedAt(),
		})
		if err != nil {
			return err
		}
		_, err = transaction.PutEvidenceSpan(fixture.ctx, span)
		return err
	}); err != nil {
		t.Fatalf("create immutable privilege fixtures: %v", err)
	}

	application, err := pgx.Connect(fixture.ctx, fixture.applicationURL)
	if err != nil {
		t.Fatalf("connect application role directly: %v", err)
	}
	defer application.Close(context.Background())
	for _, attempt := range []struct {
		statement string
		argument  string
	}{
		{`UPDATE stacks_core.document_versions SET title = 'rewritten' WHERE id = $1`, put.Ref.VersionID},
		{`DELETE FROM stacks_core.document_sections WHERE document_version_id = $1`, put.Ref.VersionID},
		{`UPDATE stacks_core.source_revision_observations SET provider_revision = 'rewritten' WHERE document_version_id = $1`, put.Ref.VersionID},
		{`UPDATE stacks_core.evidence_spans SET quote = 'rewritten' WHERE document_version_id = $1`, put.Ref.VersionID},
		{`UPDATE stacks_core.source_documents SET provider = 'rewritten' WHERE id = $1`, put.Ref.SourceDocumentID},
	} {
		if _, err := application.Exec(fixture.ctx, attempt.statement, attempt.argument); err == nil {
			t.Fatalf("application role immutable rewrite succeeded: %s", attempt.statement)
		}
	}

	var canUpdateCurrent, canUpdateProvider bool
	if err := application.QueryRow(fixture.ctx, `
		SELECT
			has_column_privilege(current_user, 'stacks_core.source_documents', 'current_version_id', 'UPDATE'),
			has_column_privilege(current_user, 'stacks_core.source_documents', 'provider', 'UPDATE')`,
	).Scan(&canUpdateCurrent, &canUpdateProvider); err != nil {
		t.Fatalf("inspect application update privileges: %v", err)
	}
	if !canUpdateCurrent || canUpdateProvider {
		t.Fatalf("application update privileges current/provider = %v/%v, want true/false", canUpdateCurrent, canUpdateProvider)
	}
}

func newDocumentRepositoryFixture(t testing.TB) documentRepositoryFixture {
	t.Helper()
	isolated := postgrestest.NewDatabase(t)
	manifest, err := coremigrations.Manifest()
	if err != nil {
		t.Fatalf("coremigrations.Manifest() error = %v", err)
	}
	applicationConfig, err := pgx.ParseConfig(isolated.ApplicationURL())
	if err != nil {
		t.Fatalf("parse application test database URL: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), documentRepositoryTimeout)
	t.Cleanup(cancel)
	if _, err := (migration.Migrator{
		DatabaseURL:     isolated.AdminURL(),
		ApplicationRole: applicationConfig.User,
		Manifests:       []migration.Manifest{manifest},
	}).Apply(ctx); err != nil {
		t.Fatalf("install canonical document schema: %v", err)
	}
	database, err := postgres.Open(ctx, isolated.ApplicationURL())
	if err != nil {
		t.Fatalf("postgres.Open() error = %v", err)
	}
	t.Cleanup(database.Close)
	admin, err := pgx.Connect(ctx, isolated.AdminURL())
	if err != nil {
		t.Fatalf("connect fixture admin: %v", err)
	}
	t.Cleanup(func() {
		_ = admin.Close(context.Background())
	})
	return documentRepositoryFixture{
		ctx:            ctx,
		database:       database,
		admin:          admin,
		applicationURL: isolated.ApplicationURL(),
	}
}

func canonicalDocument(
	t testing.TB,
	providerDocumentID string,
	_ string,
	recordedAt time.Time,
) evidence.DocumentVersion {
	t.Helper()
	transcript, err := evidence.NewSection(evidence.SectionInput{
		ID:       "section-transcript",
		Title:    "Synthetic transcript",
		Path:     []string{"Synthetic meeting", "Transcript"},
		Order:    0,
		Role:     "transcript",
		Text:     "Alpha café omega",
		ParentID: "section-root",
	})
	if err != nil {
		t.Fatalf("NewSection(transcript) error = %v", err)
	}
	notes, err := evidence.NewSection(evidence.SectionInput{
		ID:       "section-notes",
		Title:    "Synthetic notes",
		Path:     []string{"Synthetic meeting", "Notes"},
		Order:    1,
		Role:     "notes",
		Text:     "Synthetic secondary material",
		ParentID: "section-root",
	})
	if err != nil {
		t.Fatalf("NewSection(notes) error = %v", err)
	}
	sourceTime := time.Date(
		2026,
		time.July,
		24,
		10,
		0,
		0,
		123456789,
		time.FixedZone("synthetic", -4*60*60),
	)
	document, err := evidence.NewDocumentVersion(evidence.DocumentVersionInput{
		Provider:           syntheticProvider,
		ProviderDocumentID: providerDocumentID,
		Title:              "Synthetic longitudinal record",
		Locator:            "synthetic://documents/" + providerDocumentID,
		ProviderVersion:    syntheticProviderVersion,
		ModifiedAt:         documentModifiedAt,
		SourceTime:         &sourceTime,
		RecordedAt:         recordedAt,
		Sections:           []evidence.Section{transcript, notes},
	})
	if err != nil {
		t.Fatalf("NewDocumentVersion() error = %v", err)
	}
	return document
}

func putDocumentWithRevision(
	t testing.TB,
	fixture documentRepositoryFixture,
	document evidence.DocumentVersion,
	providerRevision string,
) postgres.PutDocumentVersionResult {
	t.Helper()
	var result postgres.PutDocumentVersionResult
	if err := fixture.database.InTransaction(
		fixture.ctx,
		func(transaction *postgres.Transaction) error {
			var err error
			result, err = transaction.PutDocumentVersion(fixture.ctx, document)
			if err != nil {
				return err
			}
			revision := sourceRevision(
				t,
				document,
				providerRevision,
				result.Ref.RecordedAt,
			)
			_, err = transaction.PutSourceRevisionObservation(
				fixture.ctx,
				revision,
			)
			return err
		},
	); err != nil {
		t.Fatalf("persist document and source revision: %v", err)
	}
	return result
}

func documentRevisionID(
	t testing.TB,
	document evidence.DocumentVersion,
	firstRecordedAt time.Time,
) string {
	t.Helper()
	return sourceRevision(
		t,
		document,
		"revision-a",
		firstRecordedAt,
	).ID()
}

func sourceRevision(
	t testing.TB,
	document evidence.DocumentVersion,
	providerRevision string,
	firstRecordedAt time.Time,
) evidence.SourceRevisionObservation {
	t.Helper()
	revision, err := evidence.NewSourceRevisionObservation(evidence.SourceRevisionObservationInput{
		Provider:              document.Provider(),
		ProviderDocumentID:    document.ProviderDocumentID(),
		DocumentDigestVersion: document.DigestVersion(),
		DocumentDigest:        document.Digest(),
		ProviderVersion:       document.ProviderVersion(),
		ProviderRevision:      providerRevision,
		FirstRecordedAt:       firstRecordedAt,
	})
	if err != nil {
		t.Fatalf("NewSourceRevisionObservation() error = %v", err)
	}
	return revision
}

func rowXID(
	t testing.TB,
	ctx context.Context,
	connection *pgx.Conn,
	table string,
	id string,
) string {
	t.Helper()
	if !strings.HasPrefix(table, "source_") &&
		table != "document_versions" &&
		table != "evidence_spans" {
		t.Fatalf("unsupported xmin fixture table %q", table)
	}
	var xid string
	query := "SELECT xmin::text FROM " + pgx.Identifier{"stacks_core", table}.Sanitize() + " WHERE id = $1"
	if err := connection.QueryRow(ctx, query, id).Scan(&xid); err != nil {
		t.Fatalf("read %s row xmin: %v", table, err)
	}
	return xid
}
