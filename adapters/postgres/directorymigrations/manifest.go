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
	0xd2, 0xbf, 0xfd, 0x64, 0x2f, 0x31, 0x4d, 0xcd,
	0x0c, 0xb9, 0x2c, 0xcd, 0x83, 0xd4, 0xfa, 0xb9,
	0x03, 0x17, 0x3b, 0x8b, 0x9f, 0x88, 0xdf, 0x8f,
	0x32, 0x82, 0x81, 0xf1, 0x6e, 0x42, 0x07, 0x6b,
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
