// Package coremigrations owns the embedded canonical core PostgreSQL schema.
package coremigrations

import (
	"crypto/sha256"
	"embed"

	"github.com/JakeFAU/stacks/adapters/postgres/migration"
)

const (
	coreSchema        = "stacks_core"
	migrationSchema   = "stacks_migrations"
	coreLedger        = "core_version"
	documentsVersion  = 1
	identityVersion   = 2
	extractionVersion = 3
)

var expectedFingerprint = [sha256.Size]byte{
	0x08, 0xe2, 0x62, 0xb3, 0x72, 0x4b, 0x3c, 0x36,
	0x67, 0xbe, 0x98, 0xff, 0x0c, 0x4b, 0x61, 0x91,
	0x89, 0x81, 0xf3, 0xd1, 0xf4, 0x4a, 0x30, 0xf5,
	0x73, 0xc5, 0x0a, 0xff, 0x19, 0xfd, 0x31, 0x1d,
}

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Manifest returns the validated canonical core migration manifest.
func Manifest() (migration.Manifest, error) {
	return migration.LoadManifest(
		migration.Scope("core"),
		coreLedger,
		expectedFingerprint,
		migrationFiles,
		[]migration.File{
			{
				Version: documentsVersion,
				Name:    "documents_evidence",
				Path:    "migrations/00001_documents_evidence.sql",
			},
			{
				Version: identityVersion,
				Name:    "identity_admission",
				Path:    "migrations/00002_identity_admission.sql",
			},
			{
				Version: extractionVersion,
				Name:    "extraction_observations",
				Path:    "migrations/00003_extraction_observations.sql",
			},
		},
		[]string{coreSchema},
		[]migration.OwnedObject{
			{
				Kind:   migration.ObjectSchema,
				Schema: migrationSchema,
				Name:   migrationSchema,
			},
			{
				Kind:   migration.ObjectTable,
				Schema: migrationSchema,
				Name:   coreLedger,
			},
		},
		[]migration.SchemaGrant{
			{
				Schema:     migrationSchema,
				Privileges: []migration.Privilege{migration.PrivilegeUsage},
			},
			{
				Schema:     coreSchema,
				Privileges: []migration.Privilege{migration.PrivilegeUsage},
			},
		},
		[]migration.TableGrant{
			{
				Schema:        coreSchema,
				Table:         "source_documents",
				Privileges:    []migration.Privilege{migration.PrivilegeSelect, migration.PrivilegeInsert, migration.PrivilegeUpdate},
				UpdateColumns: []string{"current_version_id"},
			},
			immutableTableGrant("document_versions"),
			immutableTableGrant("source_revision_observations"),
			immutableTableGrant("document_sections"),
			immutableTableGrant("evidence_spans"),
			immutableTableGrant("entities"),
			immutableTableGrant("mentions"),
			immutableTableGrant("resolution_proposals"),
			immutableTableGrant("resolution_proposal_evidence"),
			immutableTableGrant("resolution_candidates"),
			immutableTableGrant("resolution_decisions"),
			immutableTableGrant("entity_alias_assertions"),
			immutableTableGrant("admission_targets"),
			immutableTableGrant("admission_decisions"),
			{
				Schema:     coreSchema,
				Table:      "extraction_runs",
				Privileges: []migration.Privilege{migration.PrivilegeSelect, migration.PrivilegeInsert, migration.PrivilegeUpdate},
				UpdateColumns: []string{
					"state",
					"completed_at",
					"write_set_digest_version",
					"write_set_digest",
				},
			},
			{
				Schema:     coreSchema,
				Table:      "extraction_attempts",
				Privileges: []migration.Privilege{migration.PrivilegeSelect, migration.PrivilegeInsert, migration.PrivilegeUpdate},
				UpdateColumns: []string{
					"lease_expires_at",
					"state",
					"terminal_at",
					"failure_code",
				},
			},
			immutableTableGrant("observations"),
			immutableTableGrant("observation_evidence"),
			{
				Schema:     migrationSchema,
				Table:      coreLedger,
				Privileges: []migration.Privilege{migration.PrivilegeSelect},
			},
		},
	)
}

func immutableTableGrant(table string) migration.TableGrant {
	return migration.TableGrant{
		Schema:     coreSchema,
		Table:      table,
		Privileges: []migration.Privilege{migration.PrivilegeSelect, migration.PrivilegeInsert},
	}
}
