package coremigrations

import (
	"context"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/JakeFAU/stacks/adapters/postgres/migration"
	"github.com/JakeFAU/stacks/adapters/postgres/postgrestest"
	"github.com/jackc/pgx/v5"
)

const schemaTestTimeout = 10 * time.Second

func TestCoreManifestStartsWithDocumentsAndEvidence(t *testing.T) {
	t.Parallel()

	manifest, err := Manifest()
	if err != nil {
		t.Fatalf("Manifest() error = %v", err)
	}
	if manifest.Scope != "core" || manifest.Ledger != "core_version" {
		t.Fatalf("manifest identity = (%q, %q), want (core, core_version)", manifest.Scope, manifest.Ledger)
	}
	if len(manifest.Migrations) != 3 {
		t.Fatalf("migration count = %d, want 3", len(manifest.Migrations))
	}
	first := manifest.Migrations[0]
	if first.Version != 1 || first.Name != "documents_evidence" {
		t.Fatalf("first migration = (%d, %q), want (1, documents_evidence)", first.Version, first.Name)
	}
	second := manifest.Migrations[1]
	if second.Version != 2 || second.Name != "identity_admission" {
		t.Fatalf("second migration = (%d, %q), want (2, identity_admission)", second.Version, second.Name)
	}
	third := manifest.Migrations[2]
	if third.Version != 3 || third.Name != "extraction_observations" {
		t.Fatalf("third migration = (%d, %q), want (3, extraction_observations)", third.Version, third.Name)
	}

	wantTables := []string{
		"admission_decisions",
		"admission_targets",
		"document_sections",
		"document_versions",
		"entities",
		"entity_alias_assertions",
		"evidence_spans",
		"extraction_attempts",
		"extraction_runs",
		"mentions",
		"observation_evidence",
		"observations",
		"resolution_candidates",
		"resolution_decisions",
		"resolution_proposal_evidence",
		"resolution_proposals",
		"source_documents",
		"source_revision_observations",
	}
	var gotTables []string
	for _, grant := range manifest.ApplicationTableGrants {
		if grant.Schema == "stacks_core" {
			gotTables = append(gotTables, grant.Table)
		}
	}
	sort.Strings(gotTables)
	if !reflect.DeepEqual(gotTables, wantTables) {
		t.Fatalf("core application grant tables = %v, want %v", gotTables, wantTables)
	}
}

func TestIdentityAdmissionSchemaUsesOpaqueTextIDsAndTimestampSix(t *testing.T) {
	database := installCoreDocuments(t)
	ctx, cancel := context.WithTimeout(context.Background(), schemaTestTimeout)
	defer cancel()
	connection := openSchemaTestConnection(t, ctx, database.AdminURL())
	defer connection.Close(context.Background())

	wantTables := []string{
		"admission_decisions",
		"admission_targets",
		"entities",
		"entity_alias_assertions",
		"mentions",
		"resolution_candidates",
		"resolution_decisions",
		"resolution_proposal_evidence",
		"resolution_proposals",
	}
	rows, err := connection.Query(ctx, `
		SELECT table_name
		FROM information_schema.tables
		WHERE table_schema = 'stacks_core'
		  AND table_name = ANY($1)
		ORDER BY table_name`,
		wantTables,
	)
	if err != nil {
		t.Fatalf("inspect identity/admission tables: %v", err)
	}
	defer rows.Close()
	var gotTables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			t.Fatalf("scan identity/admission table: %v", err)
		}
		gotTables = append(gotTables, table)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate identity/admission tables: %v", err)
	}
	if !reflect.DeepEqual(gotTables, wantTables) {
		t.Fatalf("identity/admission tables = %v, want %v", gotTables, wantTables)
	}

	rows, err = connection.Query(ctx, `
		SELECT table_name, column_name, data_type, datetime_precision
		FROM information_schema.columns
		WHERE table_schema = 'stacks_core'
		  AND table_name = ANY($1)
		  AND (
			column_name = 'id'
			OR column_name LIKE '%\_id' ESCAPE '\'
			OR data_type = 'timestamp with time zone'
		  )
		ORDER BY table_name, ordinal_position`,
		wantTables,
	)
	if err != nil {
		t.Fatalf("inspect identity/admission identifier and timestamp columns: %v", err)
	}
	defer rows.Close()
	var identifiers, timestamps int
	for rows.Next() {
		var table, column, dataType string
		var precision *int32
		if err := rows.Scan(&table, &column, &dataType, &precision); err != nil {
			t.Fatalf("scan identity/admission identifier or timestamp column: %v", err)
		}
		if dataType == "timestamp with time zone" {
			timestamps++
			if precision == nil || *precision != 6 {
				t.Errorf("%s.%s timestamp precision = %v, want 6", table, column, precision)
			}
			continue
		}
		identifiers++
		if dataType != "text" {
			t.Errorf("%s.%s identifier type = %q, want text", table, column, dataType)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate identity/admission identifier and timestamp columns: %v", err)
	}
	if identifiers == 0 || timestamps == 0 {
		t.Fatalf("inspected identity/admission identifiers/timestamps = %d/%d, want both nonzero", identifiers, timestamps)
	}
}

func TestIdentityProposalEvidenceHasOrderedForeignKeys(t *testing.T) {
	database := installCoreDocuments(t)
	ctx, cancel := context.WithTimeout(context.Background(), schemaTestTimeout)
	defer cancel()
	connection := openSchemaTestConnection(t, ctx, database.AdminURL())
	defer connection.Close(context.Background())

	assertCatalogConstraint(t, ctx, connection, "resolution_proposal_evidence", "resolution_proposal_evidence_proposal_id_fkey")
	assertCatalogConstraint(t, ctx, connection, "resolution_proposal_evidence", "resolution_proposal_evidence_evidence_id_fkey")
	assertCatalogConstraint(t, ctx, connection, "resolution_proposal_evidence", "resolution_proposal_evidence_proposal_id_evidence_order_key")
}

func TestIdentityDecisionSchemaIsImmutableAndLinear(t *testing.T) {
	database := installCoreDocuments(t)
	ctx, cancel := context.WithTimeout(context.Background(), schemaTestTimeout)
	defer cancel()
	admin := openSchemaTestConnection(t, ctx, database.AdminURL())
	defer admin.Close(context.Background())
	seedIdentityAuthoritySchema(t, ctx, admin)

	if _, err := admin.Exec(ctx, `
		INSERT INTO stacks_core.resolution_decisions (
			id, proposal_id, outcome, entity_id, authority, reason_code,
			recorded_at, supersedes_id, digest_version, digest
		)
		VALUES (
			'decision-root', 'proposal:opaque/1', 'accepted', 'entity:opaque/1',
			'reviewer', 'reviewer_confirmed', '2026-07-25T16:00:00.123456Z',
			NULL, 'stacks.identity-resolution-decision.v1.canonical',
			decode(repeat('11', 32), 'hex')
		)`,
	); err != nil {
		t.Fatalf("insert initial identity decision: %v", err)
	}
	if _, err := admin.Exec(ctx, `
		INSERT INTO stacks_core.resolution_decisions (
			id, proposal_id, outcome, entity_id, authority, reason_code,
			recorded_at, supersedes_id, digest_version, digest
		)
		VALUES (
			'decision-second-root', 'proposal:opaque/1', 'rejected', NULL,
			'reviewer', 'second_root', '2026-07-25T16:01:00.123456Z',
			NULL, 'stacks.identity-resolution-decision.v1.canonical',
			decode(repeat('22', 32), 'hex')
		)`,
	); err == nil {
		t.Fatal("second initial identity decision insert succeeded")
	}
	if _, err := admin.Exec(ctx, `
		INSERT INTO stacks_core.resolution_decisions (
			id, proposal_id, outcome, entity_id, authority, reason_code,
			recorded_at, supersedes_id, digest_version, digest
		)
		VALUES (
			'decision-successor', 'proposal:opaque/1', 'rejected', NULL,
			'reviewer', 'reviewer_corrected', '2026-07-25T16:02:00.123456Z',
			'decision-root', 'stacks.identity-resolution-decision.v1.canonical',
			decode(repeat('33', 32), 'hex')
		)`,
	); err != nil {
		t.Fatalf("insert identity decision successor: %v", err)
	}
	if _, err := admin.Exec(ctx, `
		INSERT INTO stacks_core.resolution_decisions (
			id, proposal_id, outcome, entity_id, authority, reason_code,
			recorded_at, supersedes_id, digest_version, digest
		)
		VALUES (
			'decision-branch', 'proposal:opaque/1', 'rejected', NULL,
			'reviewer', 'branch_attempt', '2026-07-25T16:03:00.123456Z',
			'decision-root', 'stacks.identity-resolution-decision.v1.canonical',
			decode(repeat('44', 32), 'hex')
		)`,
	); err == nil {
		t.Fatal("branching identity decision successor insert succeeded")
	}

	application := openSchemaTestConnection(t, ctx, database.ApplicationURL())
	defer application.Close(context.Background())
	if _, err := application.Exec(
		ctx,
		`UPDATE stacks_core.resolution_decisions SET reason_code = 'rewritten' WHERE id = 'decision-root'`,
	); err == nil {
		t.Fatal("application role rewrote immutable identity decision")
	}
	if _, err := application.Exec(
		ctx,
		`DELETE FROM stacks_core.resolution_decisions WHERE id = 'decision-root'`,
	); err == nil {
		t.Fatal("application role deleted immutable identity decision")
	}
}

func TestAdmissionDecisionSchemaIsImmutableAndLinear(t *testing.T) {
	database := installCoreDocuments(t)
	ctx, cancel := context.WithTimeout(context.Background(), schemaTestTimeout)
	defer cancel()
	admin := openSchemaTestConnection(t, ctx, database.AdminURL())
	defer admin.Close(context.Background())

	if _, err := admin.Exec(ctx, `
		INSERT INTO stacks_core.admission_targets (target_kind, target_id, recorded_at)
		VALUES ('mention', 'mention:opaque/1', '2026-07-25T16:00:00.123456Z');
		INSERT INTO stacks_core.admission_decisions (
			id, target_kind, target_id, outcome, reason_code, authority,
			recorded_at, supersedes_id, digest_version, digest
		)
		VALUES (
			'admission-root', 'mention', 'mention:opaque/1', 'quarantined',
			'needs_review', 'policy', '2026-07-25T16:00:00.123456Z', NULL,
			'stacks.admission-decision.v1.canonical',
			decode(repeat('55', 32), 'hex')
		)`,
	); err != nil {
		t.Fatalf("insert initial admission decision: %v", err)
	}
	if _, err := admin.Exec(ctx, `
		INSERT INTO stacks_core.admission_decisions (
			id, target_kind, target_id, outcome, reason_code, authority,
			recorded_at, supersedes_id, digest_version, digest
		)
		VALUES (
			'admission-second-root', 'mention', 'mention:opaque/1', 'admitted',
			'second_root', 'reviewer', '2026-07-25T16:01:00.123456Z', NULL,
			'stacks.admission-decision.v1.canonical',
			decode(repeat('66', 32), 'hex')
		)`,
	); err == nil {
		t.Fatal("second initial admission decision insert succeeded")
	}
	if _, err := admin.Exec(ctx, `
		INSERT INTO stacks_core.admission_decisions (
			id, target_kind, target_id, outcome, reason_code, authority,
			recorded_at, supersedes_id, digest_version, digest
		)
		VALUES (
			'admission-successor', 'mention', 'mention:opaque/1', 'admitted',
			'reviewer_admitted', 'reviewer', '2026-07-25T16:02:00.123456Z',
			'admission-root', 'stacks.admission-decision.v1.canonical',
			decode(repeat('77', 32), 'hex')
		)`,
	); err != nil {
		t.Fatalf("insert admission decision successor: %v", err)
	}
	if _, err := admin.Exec(ctx, `
		INSERT INTO stacks_core.admission_decisions (
			id, target_kind, target_id, outcome, reason_code, authority,
			recorded_at, supersedes_id, digest_version, digest
		)
		VALUES (
			'admission-branch', 'mention', 'mention:opaque/1', 'retired',
			'branch_attempt', 'reviewer', '2026-07-25T16:03:00.123456Z',
			'admission-root', 'stacks.admission-decision.v1.canonical',
			decode(repeat('88', 32), 'hex')
		)`,
	); err == nil {
		t.Fatal("branching admission decision successor insert succeeded")
	}

	application := openSchemaTestConnection(t, ctx, database.ApplicationURL())
	defer application.Close(context.Background())
	if _, err := application.Exec(
		ctx,
		`UPDATE stacks_core.admission_decisions SET reason_code = 'rewritten' WHERE id = 'admission-root'`,
	); err == nil {
		t.Fatal("application role rewrote immutable admission decision")
	}
	if _, err := application.Exec(
		ctx,
		`DELETE FROM stacks_core.admission_decisions WHERE id = 'admission-root'`,
	); err == nil {
		t.Fatal("application role deleted immutable admission decision")
	}
}

func TestCoreIdentityHasGenericCandidateProvenanceAndNoDirectoryDependency(t *testing.T) {
	database := installCoreDocuments(t)
	ctx, cancel := context.WithTimeout(context.Background(), schemaTestTimeout)
	defer cancel()
	connection := openSchemaTestConnection(t, ctx, database.AdminURL())
	defer connection.Close(context.Background())

	var sourceKindType, sourceReferenceType string
	if err := connection.QueryRow(ctx, `
		SELECT
			max(data_type) FILTER (WHERE column_name = 'source_kind'),
			max(data_type) FILTER (WHERE column_name = 'source_reference')
		FROM information_schema.columns
		WHERE table_schema = 'stacks_core'
		  AND table_name = 'resolution_candidates'`,
	).Scan(&sourceKindType, &sourceReferenceType); err != nil {
		t.Fatalf("inspect generic candidate provenance: %v", err)
	}
	if sourceKindType != "text" || sourceReferenceType != "text" {
		t.Fatalf("candidate source types = (%q, %q), want text/text", sourceKindType, sourceReferenceType)
	}

	var directoryReferences int
	if err := connection.QueryRow(ctx, `
		SELECT count(*)
		FROM pg_catalog.pg_constraint AS catalog_constraint
		JOIN pg_catalog.pg_class AS source_table
		  ON source_table.oid = catalog_constraint.conrelid
		JOIN pg_catalog.pg_namespace AS source_schema
		  ON source_schema.oid = source_table.relnamespace
		JOIN pg_catalog.pg_class AS target_table
		  ON target_table.oid = catalog_constraint.confrelid
		JOIN pg_catalog.pg_namespace AS target_schema
		  ON target_schema.oid = target_table.relnamespace
		WHERE catalog_constraint.contype = 'f'
		  AND source_schema.nspname = 'stacks_core'
		  AND target_schema.nspname = 'stacks_directory'`,
	).Scan(&directoryReferences); err != nil {
		t.Fatalf("inspect core-to-directory foreign keys: %v", err)
	}
	if directoryReferences != 0 {
		t.Fatalf("core-to-directory foreign key count = %d, want 0", directoryReferences)
	}
}

func seedIdentityAuthoritySchema(
	t testing.TB,
	ctx context.Context,
	connection *pgx.Conn,
) {
	t.Helper()
	if _, err := connection.Exec(ctx, `
		INSERT INTO stacks_core.source_documents (
			id, provider, provider_document_id, created_at
		)
		VALUES (
			'source:opaque/1', 'synthetic', 'provider-document:opaque/1',
			'2026-07-25T15:00:00.123456Z'
		);
		INSERT INTO stacks_core.document_versions (
			id, source_document_id, digest_version, content_digest, title,
			locator, provider_version, modified_at, source_time, recorded_at
		)
		VALUES (
			'version:opaque/1', 'source:opaque/1', 'synthetic.digest.v1',
			decode(repeat('aa', 32), 'hex'), 'Synthetic record',
			'synthetic://record/1', 'provider-version:opaque/1',
			'2026-07-25T15:00:00.123456Z', NULL,
			'2026-07-25T15:00:00.123456Z'
		);
		INSERT INTO stacks_core.document_sections (
			document_version_id, section_id, title, parent_id, path,
			section_order, role, content
		)
		VALUES (
			'version:opaque/1', 'section:opaque/1', 'Synthetic section', '',
			ARRAY['Synthetic section'], 0, 'transcript', 'Synthetic Person'
		);
		INSERT INTO stacks_core.evidence_spans (
			id, document_version_id, section_id, digest_version, digest,
			start_offset, end_offset, quote, recorded_at
		)
		VALUES (
			'evidence:opaque/1', 'version:opaque/1', 'section:opaque/1',
			'synthetic.evidence.v1', decode(repeat('bb', 32), 'hex'),
			0, 9, 'Synthetic', '2026-07-25T15:00:00.123456Z'
		);
		INSERT INTO stacks_core.entities (
			id, kind, display_name, recorded_at
		)
		VALUES (
			'entity:opaque/1', 'person', 'Synthetic Person',
			'2026-07-25T15:10:00.123456Z'
		);
		INSERT INTO stacks_core.mentions (
			id, evidence_id, derivation_run_id, surface, normalized_name,
			proposed_email, proposed_email_evidence_id, role, recorded_at
		)
		VALUES (
			'mention:opaque/1', 'evidence:opaque/1', 'run:opaque/1',
			'Synthetic Person', 'synthetic person', '', NULL, 'speaker',
			'2026-07-25T15:20:00.123456Z'
		);
		INSERT INTO stacks_core.resolution_proposals (
			id, mention_id, reason_code, recorded_at
		)
		VALUES (
			'proposal:opaque/1', 'mention:opaque/1', 'identity_review',
			'2026-07-25T15:30:00.123456Z'
		);
		INSERT INTO stacks_core.resolution_proposal_evidence (
			proposal_id, evidence_id, evidence_order
		)
		VALUES ('proposal:opaque/1', 'evidence:opaque/1', 0)`,
	); err != nil {
		t.Fatalf("seed identity authority schema: %v", err)
	}
}

func TestDocumentsMigrationContainsNoVerticalOrProviderObjects(t *testing.T) {
	database := installCoreDocuments(t)
	ctx, cancel := context.WithTimeout(context.Background(), schemaTestTimeout)
	defer cancel()
	connection := openSchemaTestConnection(t, ctx, database.AdminURL())
	defer connection.Close(context.Background())

	rows, err := connection.Query(ctx, `
		SELECT namespace.nspname, class.relname
		FROM pg_catalog.pg_class AS class
		JOIN pg_catalog.pg_namespace AS namespace
		  ON namespace.oid = class.relnamespace
		WHERE namespace.nspname = 'stacks_core'
		  AND class.relkind IN ('r', 'p')
		ORDER BY class.relname`)
	if err != nil {
		t.Fatalf("inspect installed core tables: %v", err)
	}
	defer rows.Close()
	var gotTables []string
	for rows.Next() {
		var schema, table string
		if err := rows.Scan(&schema, &table); err != nil {
			t.Fatalf("scan installed core table: %v", err)
		}
		if schema != "stacks_core" {
			t.Fatalf("installed core table schema = %q, want stacks_core", schema)
		}
		gotTables = append(gotTables, table)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate installed core tables: %v", err)
	}
	wantTables := []string{
		"admission_decisions",
		"admission_targets",
		"document_sections",
		"document_versions",
		"entities",
		"entity_alias_assertions",
		"evidence_spans",
		"extraction_attempts",
		"extraction_runs",
		"mentions",
		"observation_evidence",
		"observations",
		"resolution_candidates",
		"resolution_decisions",
		"resolution_proposal_evidence",
		"resolution_proposals",
		"source_documents",
		"source_revision_observations",
	}
	if !reflect.DeepEqual(gotTables, wantTables) {
		t.Fatalf("installed core tables = %v, want only %v", gotTables, wantTables)
	}

	var forbiddenObjects int
	if err := connection.QueryRow(ctx, `
		SELECT count(*)
		FROM (
			SELECT namespace.nspname || '.' || class.relname AS object_name
			FROM pg_catalog.pg_class AS class
			JOIN pg_catalog.pg_namespace AS namespace
			  ON namespace.oid = class.relnamespace
			WHERE namespace.nspname LIKE 'stacks_%'
			UNION ALL
			SELECT namespace.nspname || '.' || procedure.proname
			FROM pg_catalog.pg_proc AS procedure
			JOIN pg_catalog.pg_namespace AS namespace
			  ON namespace.oid = procedure.pronamespace
			WHERE namespace.nspname LIKE 'stacks_%'
		) AS installed
		WHERE installed.object_name ~ '(manager|confidence|directory|drive|google|bedrock|anthropic|openai|vector|embedding|model)'`,
	).Scan(&forbiddenObjects); err != nil {
		t.Fatalf("inspect forbidden installed objects: %v", err)
	}
	if forbiddenObjects != 0 {
		t.Fatalf("forbidden vertical/provider object count = %d, want 0", forbiddenObjects)
	}
}

func TestCleanCoreDocumentsInstallUsesTextIDsAndTimestampSix(t *testing.T) {
	database := installCoreDocuments(t)
	ctx, cancel := context.WithTimeout(context.Background(), schemaTestTimeout)
	defer cancel()
	connection := openSchemaTestConnection(t, ctx, database.AdminURL())
	defer connection.Close(context.Background())

	rows, err := connection.Query(ctx, `
		SELECT table_name, column_name, data_type, datetime_precision
		FROM information_schema.columns
		WHERE table_schema = 'stacks_core'
		  AND (
			column_name = 'id'
			OR column_name LIKE '%\_id' ESCAPE '\'
			OR data_type = 'timestamp with time zone'
		  )
		ORDER BY table_name, ordinal_position`)
	if err != nil {
		t.Fatalf("inspect core identifier and timestamp columns: %v", err)
	}
	defer rows.Close()
	var identifiers, timestamps int
	for rows.Next() {
		var table, column, dataType string
		var precision *int32
		if err := rows.Scan(&table, &column, &dataType, &precision); err != nil {
			t.Fatalf("scan core identifier or timestamp column: %v", err)
		}
		if dataType == "timestamp with time zone" {
			timestamps++
			if precision == nil || *precision != 6 {
				t.Errorf("%s.%s timestamp precision = %v, want 6", table, column, precision)
			}
			continue
		}
		identifiers++
		if dataType != "text" {
			t.Errorf("%s.%s identifier type = %q, want text", table, column, dataType)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate core identifier and timestamp columns: %v", err)
	}
	if identifiers == 0 || timestamps == 0 {
		t.Fatalf("inspected identifier/timestamp columns = %d/%d, want both nonzero", identifiers, timestamps)
	}

	assertCatalogConstraint(t, ctx, connection, "document_versions", "document_versions_content_digest_check")
	assertCatalogConstraint(t, ctx, connection, "evidence_spans", "evidence_spans_digest_check")
	assertCatalogConstraint(t, ctx, connection, "source_documents", "source_documents_current_version_fkey")
	assertCatalogConstraint(t, ctx, connection, "document_sections", "document_sections_pkey")
	assertCatalogConstraint(t, ctx, connection, "document_sections", "document_sections_document_version_id_section_order_key")
	assertCatalogConstraint(t, ctx, connection, "evidence_spans", "evidence_spans_offsets_check")

	application := openSchemaTestConnection(t, ctx, database.ApplicationURL())
	defer application.Close(context.Background())
	var appliedVersion int64
	if err := application.QueryRow(
		ctx,
		`SELECT max(version) FROM stacks_migrations.core_version`,
	).Scan(&appliedVersion); err != nil {
		t.Fatalf("application role read core migration ledger: %v", err)
	}
	if appliedVersion != 3 {
		t.Fatalf("application-visible core ledger version = %d, want 3", appliedVersion)
	}
}

func installCoreDocuments(t testing.TB) postgrestest.Database {
	t.Helper()
	database := postgrestest.NewDatabase(t)
	manifest, err := Manifest()
	if err != nil {
		t.Fatalf("Manifest() error = %v", err)
	}
	applicationConfig, err := pgx.ParseConfig(database.ApplicationURL())
	if err != nil {
		t.Fatalf("parse application test database URL: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), schemaTestTimeout)
	defer cancel()
	if _, err := (migration.Migrator{
		DatabaseURL:     database.AdminURL(),
		ApplicationRole: applicationConfig.User,
		Manifests:       []migration.Manifest{manifest},
	}).Apply(ctx); err != nil {
		t.Fatalf("install core documents migration: %v", err)
	}
	return database
}

func openSchemaTestConnection(t testing.TB, ctx context.Context, databaseURL string) *pgx.Conn {
	t.Helper()
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect to schema test database: %v", err)
	}
	return connection
}

func assertCatalogConstraint(
	t testing.TB,
	ctx context.Context,
	connection *pgx.Conn,
	table string,
	constraint string,
) {
	t.Helper()
	var found bool
	if err := connection.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM pg_catalog.pg_constraint AS catalog_constraint
			JOIN pg_catalog.pg_class AS class
			  ON class.oid = catalog_constraint.conrelid
			JOIN pg_catalog.pg_namespace AS namespace
			  ON namespace.oid = class.relnamespace
			WHERE namespace.nspname = 'stacks_core'
			  AND class.relname = $1
			  AND catalog_constraint.conname = $2
		)`,
		table,
		constraint,
	).Scan(&found); err != nil {
		t.Fatalf("inspect constraint %s.%s: %v", table, constraint, err)
	}
	if !found {
		t.Errorf("constraint %s.%s is absent", table, constraint)
	}
}
