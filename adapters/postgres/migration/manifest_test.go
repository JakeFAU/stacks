package migration

import (
	"crypto/sha256"
	"strings"
	"testing"
	"testing/fstest"
)

func TestManifestHashesExactEmbeddedBytes(t *testing.T) {
	t.Parallel()

	const path = "testdata/001_exact.sql"
	sql := "CREATE SCHEMA stacks_core;\r\n-- trailing whitespace is significant \t\n"
	manifest, err := LoadManifest(
		"core",
		"core_version",
		sha256.Sum256([]byte("expected core fingerprint")),
		fstest.MapFS{path: &fstest.MapFile{Data: []byte(sql)}},
		[]File{{Version: 1, Name: "baseline", Path: path}},
		[]string{"stacks_core"},
		[]OwnedObject{
			{Kind: ObjectSchema, Schema: "stacks_migrations", Name: "stacks_migrations"},
			{Kind: ObjectTable, Schema: "stacks_migrations", Name: "core_version"},
		},
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("LoadManifest() error = %v", err)
	}

	want := sha256.Sum256([]byte(sql))
	if got := manifest.Migrations[0].Checksum; got != want {
		t.Fatalf("migration checksum = %x, want exact embedded-byte SHA-256 %x", got, want)
	}
	if got := manifest.Migrations[0].SQL; got != sql {
		t.Fatalf("migration SQL = %q, want exact embedded bytes %q", got, sql)
	}
}

func TestManifestRejectsUnsafeIdentifiers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		ledger        string
		ownedTrees    []string
		ownedObjects  []OwnedObject
		schemaGrants  []SchemaGrant
		tableGrants   []TableGrant
		wantSubstring string
	}{
		{
			name:          "ledger",
			ledger:        `core_version"; DROP SCHEMA stacks_core; --`,
			ownedTrees:    []string{"stacks_core"},
			wantSubstring: "ledger",
		},
		{
			name:          "owned schema tree",
			ledger:        "core_version",
			ownedTrees:    []string{"stacks-core"},
			wantSubstring: "schema",
		},
		{
			name:       "owned table",
			ledger:     "core_version",
			ownedTrees: []string{"stacks_core"},
			ownedObjects: []OwnedObject{{
				Kind: ObjectTable, Schema: "stacks_migrations", Name: "core version",
			}},
			wantSubstring: "object",
		},
		{
			name:       "trigger parent table",
			ledger:     "core_version",
			ownedTrees: []string{"stacks_core"},
			ownedObjects: []OwnedObject{{
				Kind: ObjectTrigger, Schema: "stacks_core", Parent: "source documents", Name: "immutable_source",
			}},
			wantSubstring: "object",
		},
		{
			name:          "schema grant",
			ledger:        "core_version",
			ownedTrees:    []string{"stacks_core"},
			schemaGrants:  []SchemaGrant{{Schema: "stacks core", Privileges: []Privilege{PrivilegeUsage}}},
			wantSubstring: "schema grant",
		},
		{
			name:       "table grant",
			ledger:     "core_version",
			ownedTrees: []string{"stacks_core"},
			tableGrants: []TableGrant{{
				Schema: "stacks_core", Table: `source_documents"`, Privileges: []Privilege{PrivilegeSelect},
			}},
			wantSubstring: "table grant",
		},
		{
			name:       "update column",
			ledger:     "core_version",
			ownedTrees: []string{"stacks_core"},
			tableGrants: []TableGrant{{
				Schema: "stacks_core", Table: "source_documents", Privileges: []Privilege{PrivilegeUpdate},
				UpdateColumns: []string{"current version"},
			}},
			wantSubstring: "update column",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			manifest := validManifest("core", test.ledger)
			manifest.OwnedSchemaTrees = test.ownedTrees
			manifest.OwnedObjects = test.ownedObjects
			manifest.ApplicationSchemaGrants = test.schemaGrants
			manifest.ApplicationTableGrants = test.tableGrants

			err := manifest.Validate()
			if err == nil {
				t.Fatal("Manifest.Validate() error = nil, want unsafe identifier rejection")
			}
			if !strings.Contains(err.Error(), test.wantSubstring) {
				t.Fatalf("Manifest.Validate() error = %q, want substring %q", err, test.wantSubstring)
			}
		})
	}
}

func TestManifestRejectsBlankSQL(t *testing.T) {
	t.Parallel()

	manifest := validManifest("core", "core_version")
	manifest.Migrations[0].SQL = " \n\t"
	if err := manifest.Validate(); err == nil || !strings.Contains(err.Error(), "blank SQL") {
		t.Fatalf("Manifest.Validate() error = %v, want blank SQL rejection", err)
	}
}

func TestFingerprintManifestRejectsZeroExpectedFingerprint(t *testing.T) {
	t.Parallel()

	manifest := validManifest("core", "core_version")
	manifest.ExpectedFingerprint = [sha256.Size]byte{}
	if err := manifest.Validate(); err == nil || !strings.Contains(err.Error(), "fingerprint") {
		t.Fatalf("Manifest.Validate() error = %v, want missing fingerprint rejection", err)
	}
}

func TestSchemaOwnedObjectRejectsFunctionOnlyFields(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		mutate func(*OwnedObject)
	}{
		{
			name: "source declaration",
			mutate: func(object *OwnedObject) {
				object.FunctionParameters = "()"
			},
		},
		{
			name: "identity declaration",
			mutate: func(object *OwnedObject) {
				object.FunctionIdentityArguments = "()"
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			object := OwnedObject{
				Kind: ObjectSchema, Schema: "stacks_example", Name: "stacks_example",
			}
			testCase.mutate(&object)
			if err := validateOwnedObject(object); err == nil {
				t.Fatal("validateOwnedObject(schema) error = nil, want function-field rejection")
			}
		})
	}
}

func TestManifestRejectsExactFunctionWithoutSourceOrIdentityDeclaration(t *testing.T) {
	t.Parallel()

	for _, object := range []OwnedObject{
		{
			Kind:                      ObjectFunction,
			Schema:                    "synthetic_exact",
			Name:                      "normalize",
			FunctionIdentityArguments: "(integer)",
		},
		{
			Kind:               ObjectFunction,
			Schema:             "synthetic_exact",
			Name:               "normalize",
			FunctionParameters: "(value integer)",
		},
	} {
		manifest := validManifest("synthetic", "synthetic_version")
		manifest.OwnedObjects = []OwnedObject{object}
		if err := manifest.Validate(); err == nil {
			t.Fatal("Manifest.Validate() error = nil, want both function declarations required")
		}
	}
}

func TestManifestAcceptsNamedFunctionParameterDeclaration(t *testing.T) {
	t.Parallel()

	manifest := validManifest("synthetic", "synthetic_version")
	setMigrationSQL(
		&manifest.Migrations[0],
		`CREATE FUNCTION synthetic_exact.normalize(value integer)
		 RETURNS integer LANGUAGE sql IMMUTABLE AS 'SELECT value'`,
	)
	manifest.OwnedObjects = []OwnedObject{{
		Kind:                      ObjectFunction,
		Schema:                    "synthetic_exact",
		Name:                      "normalize",
		FunctionParameters:        "(value integer)",
		FunctionIdentityArguments: "(integer)",
	}}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("Manifest.Validate() error = %v", err)
	}
}

func TestManifestAcceptsSimpleFunctionSourceAndIdentityGrammar(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		parameters string
		identity   string
	}{
		{name: "zero", parameters: "()", identity: "()"},
		{name: "unnamed", parameters: "(integer)", identity: "(integer)"},
		{name: "named alias", parameters: "(value int)", identity: "(int)"},
		{name: "unnamed multiword", parameters: "(double precision)", identity: "(double precision)"},
		{name: "named multiword", parameters: "(value timestamp with time zone)", identity: "(timestamp with time zone)"},
		{name: "qualified custom", parameters: "(synthetic_types.custom_type)", identity: "(synthetic_types.custom_type)"},
		{name: "named custom array", parameters: "(value synthetic_types.custom_type[])", identity: "(synthetic_types.custom_type[])"},
		{name: "multiple", parameters: "(left_value integer, right_value text[])", identity: "(integer, text[])"},
		{name: "multidimensional array", parameters: "(value synthetic_types.custom_type[][])", identity: "(synthetic_types.custom_type[][])"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			manifest := validManifest("synthetic", "synthetic_version")
			manifest.OwnedObjects = []OwnedObject{{
				Kind:                      ObjectFunction,
				Schema:                    "synthetic_exact",
				Name:                      "normalize",
				FunctionParameters:        test.parameters,
				FunctionIdentityArguments: test.identity,
			}}
			if err := manifest.Validate(); err != nil {
				t.Fatalf("Manifest.Validate() error = %v", err)
			}
		})
	}
}

func TestManifestRejectsMismatchedFunctionSourceAndIdentity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		parameters string
		identity   string
	}{
		{name: "extra source prefix", parameters: "(foo bar integer)", identity: "(integer)"},
		{name: "parameter count", parameters: "(left_value integer, right_value text)", identity: "(integer)"},
		{name: "type spelling", parameters: "(value int)", identity: "(integer)"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			manifest := validManifest("synthetic", "synthetic_version")
			manifest.OwnedObjects = []OwnedObject{{
				Kind:                      ObjectFunction,
				Schema:                    "synthetic_exact",
				Name:                      "normalize",
				FunctionParameters:        test.parameters,
				FunctionIdentityArguments: test.identity,
			}}
			if err := manifest.Validate(); err == nil {
				t.Fatal("Manifest.Validate() error = nil, want source/identity mismatch rejection")
			}
		})
	}
}

func TestManifestRejectsReservedFunctionParameterNames(t *testing.T) {
	t.Parallel()

	for _, name := range []string{
		"default",
		"in",
		"out",
		"inout",
		"variadic",
		"table",
	} {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			manifest := validManifest("synthetic", "synthetic_version")
			manifest.OwnedObjects = []OwnedObject{{
				Kind:                      ObjectFunction,
				Schema:                    "synthetic_exact",
				Name:                      "normalize",
				FunctionParameters:        "(" + name + " integer)",
				FunctionIdentityArguments: "(integer)",
			}}
			if err := manifest.Validate(); err == nil {
				t.Fatal("Manifest.Validate() error = nil, want reserved parameter name rejection")
			}
		})
	}
}

func TestManifestRejectsDuplicateFunctionSourceDeclaration(t *testing.T) {
	t.Parallel()

	manifest := validManifest("synthetic", "synthetic_version")
	function := OwnedObject{
		Kind:                      ObjectFunction,
		Schema:                    "synthetic_exact",
		Name:                      "normalize",
		FunctionParameters:        "(value int)",
		FunctionIdentityArguments: "(int)",
	}
	manifest.OwnedObjects = []OwnedObject{function, function}
	if err := manifest.Validate(); err == nil {
		t.Fatal("Manifest.Validate() error = nil, want duplicate source declaration rejection")
	}
}

func TestManifestRejectsUnsupportedFunctionIdentityGrammar(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		parameters string
		identity   string
	}{
		{name: "default keyword", parameters: "(value integer default 1)", identity: "(integer default 1)"},
		{name: "equals default", parameters: "(value integer = 1)", identity: "(integer = 1)"},
		{name: "explicit in", parameters: "(in value integer)", identity: "(in integer)"},
		{name: "out", parameters: "(out value integer)", identity: "(out integer)"},
		{name: "inout", parameters: "(inout value integer)", identity: "(inout integer)"},
		{name: "variadic", parameters: "(variadic value integer[])", identity: "(variadic integer[])"},
		{name: "table", parameters: "(table value integer)", identity: "(table integer)"},
		{name: "type modifier", parameters: "(value numeric(10))", identity: "(numeric(10))"},
		{name: "nested comma", parameters: "(value numeric(10,2))", identity: "(numeric(10,2))"},
		{name: "quoted parameter", parameters: `("value" integer)`, identity: "(integer)"},
		{name: "quoted type", parameters: `(value "integer")`, identity: `("integer")`},
		{name: "quoted qualified type", parameters: `(value synthetic_types."custom_type")`, identity: `(synthetic_types."custom_type")`},
		{name: "punctuation", parameters: "(value integer;drop)", identity: "(integer;drop)"},
		{name: "leading empty segment", parameters: "(, value integer)", identity: "(, integer)"},
		{name: "middle empty segment", parameters: "(value integer,, other text)", identity: "(integer,, text)"},
		{name: "trailing empty segment", parameters: "(value integer, )", identity: "(integer, )"},
		{name: "overqualified type", parameters: "(value first.second.third)", identity: "(first.second.third)"},
		{name: "leading whitespace", parameters: "( value integer)", identity: "( integer)"},
		{name: "repeated whitespace", parameters: "(value  integer)", identity: "( integer)"},
		{name: "missing comma space", parameters: "(value integer,other text)", identity: "(integer,text)"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			manifest := validManifest("synthetic", "synthetic_version")
			manifest.OwnedObjects = []OwnedObject{{
				Kind:                      ObjectFunction,
				Schema:                    "synthetic_exact",
				Name:                      "normalize",
				FunctionParameters:        test.parameters,
				FunctionIdentityArguments: test.identity,
			}}
			err := manifest.Validate()
			if err == nil {
				t.Fatal("Manifest.Validate() error = nil, want unsupported parameter rejection")
			}
			if strings.Contains(err.Error(), test.parameters) ||
				strings.Contains(err.Error(), test.identity) {
				t.Fatalf("Manifest.Validate() error exposed parameter declaration: %q", err)
			}
		})
	}
}

func TestManifestRejectsInvalidMigrationOrderingAndIdentity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		migrations    []Migration
		wantSubstring string
	}{
		{
			name: "duplicate version",
			migrations: []Migration{
				testMigration(1, "baseline", "SELECT 1"),
				testMigration(1, "next", "SELECT 2"),
			},
			wantSubstring: "version",
		},
		{
			name: "unordered version",
			migrations: []Migration{
				testMigration(2, "next", "SELECT 2"),
				testMigration(1, "baseline", "SELECT 1"),
			},
			wantSubstring: "ordered",
		},
		{
			name: "duplicate name",
			migrations: []Migration{
				testMigration(1, "baseline", "SELECT 1"),
				testMigration(2, "baseline", "SELECT 2"),
			},
			wantSubstring: "name",
		},
		{
			name:          "non-positive version",
			migrations:    []Migration{testMigration(0, "baseline", "SELECT 1")},
			wantSubstring: "positive",
		},
		{
			name:          "unsafe name",
			migrations:    []Migration{testMigration(1, "bad name", "SELECT 1")},
			wantSubstring: "name",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			manifest := validManifest("core", "core_version")
			manifest.Migrations = test.migrations

			err := manifest.Validate()
			if err == nil {
				t.Fatal("Manifest.Validate() error = nil, want migration rejection")
			}
			if !strings.Contains(err.Error(), test.wantSubstring) {
				t.Fatalf("Manifest.Validate() error = %q, want substring %q", err, test.wantSubstring)
			}
		})
	}
}

func TestGrantRejectsUnsupportedOrContradictoryPrivileges(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		schemaGrants  []SchemaGrant
		tableGrants   []TableGrant
		wantSubstring string
	}{
		{
			name:          "unsupported schema privilege",
			schemaGrants:  []SchemaGrant{{Schema: "stacks_core", Privileges: []Privilege{"CREATE"}}},
			wantSubstring: "schema privilege",
		},
		{
			name: "unsupported table privilege",
			tableGrants: []TableGrant{{
				Schema: "stacks_core", Table: "source_documents", Privileges: []Privilege{"DELETE"},
			}},
			wantSubstring: "table privilege",
		},
		{
			name: "update columns without update privilege",
			tableGrants: []TableGrant{{
				Schema: "stacks_core", Table: "source_documents", Privileges: []Privilege{PrivilegeSelect},
				UpdateColumns: []string{"current_version_id"},
			}},
			wantSubstring: "UPDATE",
		},
		{
			name: "unscoped update grant",
			tableGrants: []TableGrant{{
				Schema: "stacks_core", Table: "source_documents", Privileges: []Privilege{PrivilegeUpdate},
			}},
			wantSubstring: "update columns",
		},
		{
			name: "duplicate privilege",
			tableGrants: []TableGrant{{
				Schema: "stacks_core", Table: "source_documents",
				Privileges: []Privilege{PrivilegeSelect, PrivilegeSelect},
			}},
			wantSubstring: "duplicate",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			manifest := validManifest("core", "core_version")
			manifest.ApplicationSchemaGrants = test.schemaGrants
			manifest.ApplicationTableGrants = test.tableGrants

			err := manifest.Validate()
			if err == nil {
				t.Fatal("Manifest.Validate() error = nil, want grant rejection")
			}
			if !strings.Contains(err.Error(), test.wantSubstring) {
				t.Fatalf("Manifest.Validate() error = %q, want substring %q", err, test.wantSubstring)
			}
		})
	}
}

func TestGrantRejectsLedgerWritePrivileges(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		table      string
		privileges []Privilege
		columns    []string
	}{
		{
			name:       "insert",
			privileges: []Privilege{PrivilegeSelect, PrivilegeInsert},
		},
		{
			name:       "update",
			privileges: []Privilege{PrivilegeSelect, PrivilegeUpdate},
			columns:    []string{"name"},
		},
		{
			name:       "update columns without update",
			privileges: []Privilege{PrivilegeSelect},
			columns:    []string{"name"},
		},
		{
			name:       "other ledger",
			table:      "directory_version",
			privileges: []Privilege{PrivilegeSelect},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			manifest := coreManifestForSet()
			table := test.table
			if table == "" {
				table = "core_version"
			}
			manifest.ApplicationTableGrants = []TableGrant{{
				Schema:        "stacks_migrations",
				Table:         table,
				Privileges:    test.privileges,
				UpdateColumns: test.columns,
			}}

			err := manifest.Validate()
			if err == nil {
				t.Fatal("Manifest.Validate() error = nil, want ledger write-grant rejection")
			}
			if !strings.Contains(err.Error(), "ledger") ||
				!strings.Contains(err.Error(), "SELECT") {
				t.Fatalf("Manifest.Validate() error = %q, want ledger SELECT-only context", err)
			}
		})
	}
}

func TestGrantRequiresLedgerSelectInspection(t *testing.T) {
	t.Parallel()

	manifest := coreManifestForSet()
	manifest.ApplicationTableGrants = nil
	err := ValidateManifestSet([]Manifest{manifest})
	if err == nil {
		t.Fatal("ValidateManifestSet() error = nil, want missing ledger SELECT rejection")
	}
	if !strings.Contains(err.Error(), "ledger") || !strings.Contains(err.Error(), "SELECT") {
		t.Fatalf("ValidateManifestSet() error = %q, want required ledger SELECT context", err)
	}
}

func TestOwnershipRejectsDuplicateClaimsAcrossScopes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		mutate        func(*Manifest)
		wantSubstring string
	}{
		{
			name: "schema tree",
			mutate: func(directory *Manifest) {
				directory.OwnedSchemaTrees = []string{"stacks_core"}
			},
			wantSubstring: "schema tree",
		},
		{
			name: "exact object",
			mutate: func(directory *Manifest) {
				directory.OwnedObjects = append(directory.OwnedObjects, OwnedObject{
					Kind: ObjectTable, Schema: "stacks_migrations", Name: "core_version",
				})
			},
			wantSubstring: "object",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			core := validManifest("core", "core_version")
			core.OwnedSchemaTrees = []string{"stacks_core"}
			core.OwnedObjects = []OwnedObject{
				{Kind: ObjectSchema, Schema: "stacks_migrations", Name: "stacks_migrations"},
				{Kind: ObjectTable, Schema: "stacks_migrations", Name: "core_version"},
			}
			directory := validManifest("directory", "directory_version")
			directory.OwnedSchemaTrees = []string{"stacks_directory"}
			directory.OwnedObjects = []OwnedObject{{
				Kind: ObjectTable, Schema: "stacks_migrations", Name: "directory_version",
			}}
			test.mutate(&directory)

			err := ValidateManifestSet([]Manifest{core, directory})
			if err == nil {
				t.Fatal("ValidateManifestSet() error = nil, want duplicate ownership rejection")
			}
			if !strings.Contains(err.Error(), test.wantSubstring) {
				t.Fatalf("ValidateManifestSet() error = %q, want substring %q", err, test.wantSubstring)
			}
		})
	}
}

func TestOwnershipRejectsCreatedObjectsWithoutOwners(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		sql           string
		ownedTrees    []string
		ownedObjects  []OwnedObject
		wantSubstring string
	}{
		{
			name:          "schema",
			sql:           "CREATE SCHEMA stacks_unowned",
			wantSubstring: "schema stacks_unowned",
		},
		{
			name:          "table",
			sql:           "CREATE TABLE stacks_unowned.items (id bigint)",
			wantSubstring: "table stacks_unowned.items",
		},
		{
			name:          "function",
			sql:           "CREATE FUNCTION stacks_unowned.touch() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RETURN NEW; END $$",
			wantSubstring: "function stacks_unowned.touch",
		},
		{
			name:          "trigger",
			sql:           "CREATE TRIGGER touch BEFORE UPDATE ON stacks_unowned.items FOR EACH ROW EXECUTE FUNCTION stacks_unowned.touch()",
			wantSubstring: "trigger stacks_unowned.items.touch",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			manifest := validManifest("core", "core_version")
			setMigrationSQL(&manifest.Migrations[0], test.sql)
			manifest.OwnedSchemaTrees = test.ownedTrees
			manifest.OwnedObjects = test.ownedObjects

			err := manifest.Validate()
			if err == nil {
				t.Fatal("Manifest.Validate() error = nil, want unowned created object rejection")
			}
			if !strings.Contains(err.Error(), test.wantSubstring) {
				t.Fatalf("Manifest.Validate() error = %q, want substring %q", err, test.wantSubstring)
			}
		})
	}
}

func TestOwnershipAcceptsCompleteCoreAndDirectoryScopesAtIndependentVersionOne(t *testing.T) {
	t.Parallel()

	core := validManifest("core", "core_version")
	setMigrationSQL(&core.Migrations[0], `
		CREATE SCHEMA stacks_core;
		CREATE TABLE stacks_core.documents (id text PRIMARY KEY);
		CREATE SCHEMA stacks_migrations;
		CREATE TABLE stacks_migrations.core_version (version bigint PRIMARY KEY);
	`)
	core.OwnedSchemaTrees = []string{"stacks_core"}
	core.OwnedObjects = []OwnedObject{
		{Kind: ObjectSchema, Schema: "stacks_migrations", Name: "stacks_migrations"},
		{Kind: ObjectTable, Schema: "stacks_migrations", Name: "core_version"},
	}
	core.ApplicationTableGrants = []TableGrant{{
		Schema: "stacks_migrations", Table: "core_version",
		Privileges: []Privilege{PrivilegeSelect},
	}}

	directory := validManifest("directory", "directory_version")
	setMigrationSQL(&directory.Migrations[0], `
		CREATE SCHEMA stacks_directory;
		CREATE TABLE stacks_directory.profiles (id text PRIMARY KEY);
		CREATE TABLE stacks_migrations.directory_version (version bigint PRIMARY KEY);
	`)
	directory.OwnedSchemaTrees = []string{"stacks_directory"}
	directory.OwnedObjects = []OwnedObject{{
		Kind: ObjectTable, Schema: "stacks_migrations", Name: "directory_version",
	}}
	directory.ApplicationTableGrants = []TableGrant{{
		Schema: "stacks_migrations", Table: "directory_version",
		Privileges: []Privilege{PrivilegeSelect},
	}}

	if err := ValidateManifestSet([]Manifest{core, directory}); err != nil {
		t.Fatalf("ValidateManifestSet() error = %v, want independent version 1 ledgers", err)
	}
}

func TestOwnershipRejectsMissingLedgerOrSharedSchemaOwner(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		coreObjects   []OwnedObject
		wantSubstring string
	}{
		{
			name: "shared migration schema",
			coreObjects: []OwnedObject{{
				Kind: ObjectTable, Schema: "stacks_migrations", Name: "core_version",
			}},
			wantSubstring: "stacks_migrations",
		},
		{
			name: "scope ledger",
			coreObjects: []OwnedObject{{
				Kind: ObjectSchema, Schema: "stacks_migrations", Name: "stacks_migrations",
			}},
			wantSubstring: "core_version",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			core := validManifest("core", "core_version")
			core.OwnedSchemaTrees = []string{"stacks_core"}
			core.OwnedObjects = test.coreObjects

			err := ValidateManifestSet([]Manifest{core})
			if err == nil {
				t.Fatal("ValidateManifestSet() error = nil, want migration infrastructure ownership rejection")
			}
			if !strings.Contains(err.Error(), test.wantSubstring) {
				t.Fatalf("ValidateManifestSet() error = %q, want substring %q", err, test.wantSubstring)
			}
		})
	}
}

func TestManifestSetRequiresCoreFirstOwnedScopes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		manifests     []Manifest
		wantSubstring string
	}{
		{
			name:          "nonempty",
			wantSubstring: "core",
		},
		{
			name:          "directory only",
			manifests:     []Manifest{directoryManifestForSet()},
			wantSubstring: "core",
		},
		{
			name: "reverse order",
			manifests: []Manifest{
				directoryManifestForSet(),
				coreManifestForSet(),
			},
			wantSubstring: "first",
		},
		{
			name: "wrong core ledger",
			manifests: []Manifest{func() Manifest {
				manifest := coreManifestForSet()
				manifest.Ledger = "wrong_version"
				manifest.OwnedObjects[1].Name = "wrong_version"
				manifest.ApplicationTableGrants[0].Table = "wrong_version"
				return manifest
			}()},
			wantSubstring: "core_version",
		},
		{
			name: "wrong directory ledger",
			manifests: []Manifest{
				coreManifestForSet(),
				func() Manifest {
					manifest := directoryManifestForSet()
					manifest.Ledger = "wrong_version"
					manifest.OwnedObjects[0].Name = "wrong_version"
					manifest.ApplicationTableGrants[0].Table = "wrong_version"
					return manifest
				}(),
			},
			wantSubstring: "directory_version",
		},
		{
			name: "directory owns shared schema",
			manifests: []Manifest{
				func() Manifest {
					manifest := coreManifestForSet()
					manifest.OwnedObjects = manifest.OwnedObjects[1:]
					return manifest
				}(),
				func() Manifest {
					manifest := directoryManifestForSet()
					manifest.OwnedObjects = append(manifest.OwnedObjects, OwnedObject{
						Kind: ObjectSchema, Schema: "stacks_migrations", Name: "stacks_migrations",
					})
					return manifest
				}(),
			},
			wantSubstring: "core",
		},
		{
			name: "unexpected directory schema tree",
			manifests: []Manifest{
				coreManifestForSet(),
				func() Manifest {
					manifest := directoryManifestForSet()
					manifest.OwnedSchemaTrees = append(
						manifest.OwnedSchemaTrees,
						"stacks_directory_extra",
					)
					return manifest
				}(),
			},
			wantSubstring: "directory",
		},
		{
			name: "unexpected directory exact object",
			manifests: []Manifest{
				coreManifestForSet(),
				func() Manifest {
					manifest := directoryManifestForSet()
					manifest.OwnedObjects = append(manifest.OwnedObjects, OwnedObject{
						Kind: ObjectTable, Schema: "stacks_other", Name: "unexpected",
					})
					return manifest
				}(),
			},
			wantSubstring: "directory",
		},
		{
			name: "unexpected scope",
			manifests: []Manifest{
				coreManifestForSet(),
				func() Manifest {
					manifest := directoryManifestForSet()
					manifest.Scope = "plugin"
					return manifest
				}(),
			},
			wantSubstring: "scope",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateManifestSet(test.manifests)
			if err == nil {
				t.Fatal("ValidateManifestSet() error = nil, want core-first scope rejection")
			}
			if !strings.Contains(err.Error(), test.wantSubstring) {
				t.Fatalf("ValidateManifestSet() error = %q, want substring %q", err, test.wantSubstring)
			}
		})
	}
}

func TestManifestSetRejectsNoncanonicalCoreOwnership(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*Manifest)
	}{
		{
			name: "missing core schema tree",
			mutate: func(core *Manifest) {
				core.OwnedSchemaTrees = nil
			},
		},
		{
			name: "wrong core schema tree",
			mutate: func(core *Manifest) {
				core.OwnedSchemaTrees = []string{"stacks_wrong"}
			},
		},
		{
			name: "extra core schema tree",
			mutate: func(core *Manifest) {
				core.OwnedSchemaTrees = append(core.OwnedSchemaTrees, "stacks_extra")
			},
		},
		{
			name: "extra core exact table",
			mutate: func(core *Manifest) {
				core.OwnedObjects = append(core.OwnedObjects, OwnedObject{
					Kind: ObjectTable, Schema: "stacks_migrations", Name: "unexpected_version",
				})
			},
		},
		{
			name: "extra core exact function",
			mutate: func(core *Manifest) {
				core.OwnedObjects = append(core.OwnedObjects, OwnedObject{
					Kind: ObjectFunction, Schema: "stacks_other", Name: "unexpected_function",
				})
			},
		},
		{
			name: "extra core exact trigger",
			mutate: func(core *Manifest) {
				core.OwnedObjects = append(core.OwnedObjects, OwnedObject{
					Kind:   ObjectTrigger,
					Schema: "stacks_other",
					Parent: "records",
					Name:   "unexpected_trigger",
				})
			},
		},
		{
			name: "extra core exact schema",
			mutate: func(core *Manifest) {
				core.OwnedObjects = append(core.OwnedObjects, OwnedObject{
					Kind: ObjectSchema, Schema: "stacks_other", Name: "stacks_other",
				})
			},
		},
		{
			name: "unexpected directory ledger object",
			mutate: func(core *Manifest) {
				core.OwnedObjects = append(core.OwnedObjects, OwnedObject{
					Kind: ObjectTable, Schema: "stacks_migrations", Name: "directory_version",
				})
			},
		},
		{
			name: "redundant exact object inside core tree",
			mutate: func(core *Manifest) {
				core.OwnedObjects = append(core.OwnedObjects, OwnedObject{
					Kind: ObjectTable, Schema: "stacks_core", Name: "records",
				})
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			core := coreManifestForSet()
			test.mutate(&core)

			err := ValidateManifestSet([]Manifest{core})
			if err == nil {
				t.Fatal("ValidateManifestSet() error = nil, want canonical core ownership rejection")
			}
			if !strings.Contains(err.Error(), "core") {
				t.Fatalf("ValidateManifestSet() error = %q, want core ownership context", err)
			}
		})
	}
}

func TestManifestSetAcceptsCanonicalCoreOwnership(t *testing.T) {
	t.Parallel()

	core := coreManifestForSet()
	directory := directoryManifestForSet()
	for _, manifests := range [][]Manifest{
		{core},
		{core, directory},
	} {
		if err := ValidateManifestSet(manifests); err != nil {
			t.Fatalf("ValidateManifestSet(%d scopes) error = %v", len(manifests), err)
		}
	}
}

func validManifest(scope Scope, ledger string) Manifest {
	return Manifest{
		Scope:               scope,
		Ledger:              ledger,
		ExpectedFingerprint: sha256.Sum256([]byte("expected:" + string(scope) + ":" + ledger)),
		Migrations: []Migration{{
			Version:  1,
			Name:     "baseline",
			SQL:      "SELECT 1",
			Checksum: sha256.Sum256([]byte("SELECT 1")),
		}},
	}
}

func setMigrationSQL(migration *Migration, sql string) {
	migration.SQL = sql
	migration.Checksum = sha256.Sum256([]byte(sql))
}

func testMigration(version int64, name, sql string) Migration {
	migration := Migration{Version: version, Name: name}
	setMigrationSQL(&migration, sql)
	return migration
}

func coreManifestForSet() Manifest {
	manifest := validManifest("core", "core_version")
	manifest.OwnedSchemaTrees = []string{"stacks_core"}
	manifest.OwnedObjects = []OwnedObject{
		{Kind: ObjectSchema, Schema: "stacks_migrations", Name: "stacks_migrations"},
		{Kind: ObjectTable, Schema: "stacks_migrations", Name: "core_version"},
	}
	manifest.ApplicationTableGrants = []TableGrant{{
		Schema: "stacks_migrations", Table: "core_version",
		Privileges: []Privilege{PrivilegeSelect},
	}}
	return manifest
}

func directoryManifestForSet() Manifest {
	manifest := validManifest("directory", "directory_version")
	manifest.OwnedSchemaTrees = []string{"stacks_directory"}
	manifest.OwnedObjects = []OwnedObject{{
		Kind: ObjectTable, Schema: "stacks_migrations", Name: "directory_version",
	}}
	manifest.ApplicationTableGrants = []TableGrant{{
		Schema: "stacks_migrations", Table: "directory_version",
		Privileges: []Privilege{PrivilegeSelect},
	}}
	return manifest
}
