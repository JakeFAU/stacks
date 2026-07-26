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
	0x2e, 0xed, 0x97, 0xe7, 0xb6, 0x8c, 0x02, 0x51,
	0xce, 0x46, 0xc6, 0x57, 0xc0, 0x32, 0x61, 0x39,
	0x8c, 0x78, 0xf0, 0x28, 0x0a, 0xc2, 0x73, 0x08,
	0xc8, 0xab, 0x78, 0xd3, 0x7c, 0xff, 0x20, 0x92,
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
