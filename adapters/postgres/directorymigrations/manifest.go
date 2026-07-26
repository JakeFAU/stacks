// Package directorymigrations owns the optional PostgreSQL directory scope.
package directorymigrations

import (
	"embed"

	"github.com/JakeFAU/stacks/adapters/postgres/migration"
)

const (
	directorySchema  = "stacks_directory"
	migrationSchema  = "stacks_migrations"
	directoryLedger  = "directory_version"
	directoryVersion = 1
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Manifest returns the validated optional directory migration manifest.
func Manifest() (migration.Manifest, error) {
	return migration.LoadManifest(
		migration.Scope("directory"),
		directoryLedger,
		migrationFiles,
		[]migration.File{{
			Version: directoryVersion,
			Name:    "directory",
			Path:    "migrations/00001_directory.sql",
		}},
		[]string{directorySchema},
		[]migration.OwnedObject{{
			Kind:   migration.ObjectTable,
			Schema: migrationSchema,
			Name:   directoryLedger,
		}},
		[]migration.SchemaGrant{{
			Schema:     directorySchema,
			Privileges: []migration.Privilege{migration.PrivilegeUsage},
		}},
		[]migration.TableGrant{
			directoryTableGrant("snapshots"),
			directoryTableGrant("profiles"),
			directoryTableGrant("profile_emails"),
			directoryTableGrant("lookup_attempts"),
			directoryTableGrant("entity_links"),
			{
				Schema:     migrationSchema,
				Table:      directoryLedger,
				Privileges: []migration.Privilege{migration.PrivilegeSelect},
			},
		},
	)
}

func directoryTableGrant(table string) migration.TableGrant {
	return migration.TableGrant{
		Schema:     directorySchema,
		Table:      table,
		Privileges: []migration.Privilege{migration.PrivilegeSelect, migration.PrivilegeInsert},
	}
}
