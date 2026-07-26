// Package directorymigrations owns the optional PostgreSQL directory scope.
package directorymigrations

import (
	"crypto/sha256"
	"embed"

	"github.com/JakeFAU/stacks/adapters/postgres/migration"
)

const (
	directorySchema  = "stacks_directory"
	migrationSchema  = "stacks_migrations"
	directoryLedger  = "directory_version"
	directoryVersion = 1
)

var expectedFingerprint = [sha256.Size]byte{
	0x18, 0x8a, 0xc3, 0xf2, 0xd1, 0x7a, 0xca, 0x77,
	0xe9, 0xd8, 0xd5, 0x6f, 0x0c, 0xd8, 0x84, 0x4b,
	0x7e, 0x38, 0xc6, 0x9e, 0x7e, 0x66, 0x18, 0xc3,
	0xd6, 0x59, 0x59, 0x67, 0xdf, 0x68, 0xf2, 0x23,
}

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Manifest returns the validated optional directory migration manifest.
func Manifest() (migration.Manifest, error) {
	return migration.LoadManifest(
		migration.Scope("directory"),
		directoryLedger,
		expectedFingerprint,
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
