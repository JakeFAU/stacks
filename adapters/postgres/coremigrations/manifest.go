// Package coremigrations owns the embedded canonical core PostgreSQL schema.
package coremigrations

import (
	"embed"

	"github.com/JakeFAU/stacks/adapters/postgres/migration"
)

const (
	coreSchema       = "stacks_core"
	migrationSchema  = "stacks_migrations"
	coreLedger       = "core_version"
	documentsVersion = 1
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Manifest returns the validated canonical core migration manifest.
func Manifest() (migration.Manifest, error) {
	return migration.LoadManifest(
		migration.Scope("core"),
		coreLedger,
		migrationFiles,
		[]migration.File{{
			Version: documentsVersion,
			Name:    "documents_evidence",
			Path:    "migrations/00001_documents_evidence.sql",
		}},
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
