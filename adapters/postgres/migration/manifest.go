// Package migration applies independently versioned PostgreSQL migration
// manifests with explicit object ownership.
package migration

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"regexp"
	"strings"
)

// Scope identifies one independently versioned database concern.
type Scope string

// File identifies one embedded migration file.
type File struct {
	Version int64
	Name    string
	Path    string
}

// Migration is the immutable content and identity of one migration version.
type Migration struct {
	Version  int64
	Name     string
	SQL      string
	Checksum [sha256.Size]byte
}

// Privilege is an application-role database privilege granted by a scope.
type Privilege string

const (
	PrivilegeUsage  Privilege = "USAGE"
	PrivilegeSelect Privilege = "SELECT"
	PrivilegeInsert Privilege = "INSERT"
	PrivilegeUpdate Privilege = "UPDATE"
)

// SchemaGrant describes application-role privileges on a schema.
type SchemaGrant struct {
	Schema     string
	Privileges []Privilege
}

// TableGrant describes application-role privileges on a table. UPDATE grants
// must identify their writable columns explicitly.
type TableGrant struct {
	Schema        string
	Table         string
	Privileges    []Privilege
	UpdateColumns []string
}

// ObjectKind identifies a PostgreSQL object represented in a scope manifest.
type ObjectKind string

const (
	ObjectSchema   ObjectKind = "schema"
	ObjectTable    ObjectKind = "table"
	ObjectFunction ObjectKind = "function"
	ObjectTrigger  ObjectKind = "trigger"
)

// OwnedObject identifies one exact object owned by a migration scope. Parent
// is set only for triggers and contains the owning table name.
type OwnedObject struct {
	Kind   ObjectKind
	Schema string
	Parent string
	Name   string
}

// Manifest describes one independently versioned migration scope.
type Manifest struct {
	Scope                   Scope
	Ledger                  string
	Migrations              []Migration
	OwnedSchemaTrees        []string
	OwnedObjects            []OwnedObject
	ApplicationSchemaGrants []SchemaGrant
	ApplicationTableGrants  []TableGrant
}

var (
	identifierPattern = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)
	createPatterns    = []struct {
		kind    ObjectKind
		pattern *regexp.Regexp
	}{
		{
			kind:    ObjectSchema,
			pattern: regexp.MustCompile(`(?i)\bCREATE\s+SCHEMA\s+(?:IF\s+NOT\s+EXISTS\s+)?([a-z_][a-z0-9_]*)`),
		},
		{
			kind:    ObjectTable,
			pattern: regexp.MustCompile(`(?i)\bCREATE\s+(?:UNLOGGED\s+)?TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?([a-z_][a-z0-9_]*)\.([a-z_][a-z0-9_]*)`),
		},
		{
			kind:    ObjectFunction,
			pattern: regexp.MustCompile(`(?i)\bCREATE\s+(?:OR\s+REPLACE\s+)?FUNCTION\s+([a-z_][a-z0-9_]*)\.([a-z_][a-z0-9_]*)\s*\(`),
		},
		{
			kind:    ObjectTrigger,
			pattern: regexp.MustCompile(`(?i)\bCREATE\s+TRIGGER\s+([a-z_][a-z0-9_]*)\b[\s\S]*?\bON\s+([a-z_][a-z0-9_]*)\.([a-z_][a-z0-9_]*)`),
		},
	}
)

// LoadManifest reads exact migration bytes from files and constructs a
// validated immutable manifest value.
func LoadManifest(
	scope Scope,
	ledger string,
	files fs.FS,
	entries []File,
	ownedSchemaTrees []string,
	ownedObjects []OwnedObject,
	schemaGrants []SchemaGrant,
	tableGrants []TableGrant,
) (Manifest, error) {
	if files == nil {
		return Manifest{}, fmt.Errorf("load migration manifest %q: file system is required", scope)
	}

	migrations := make([]Migration, 0, len(entries))
	for _, entry := range entries {
		if !fs.ValidPath(entry.Path) {
			return Manifest{}, fmt.Errorf(
				"load migration manifest %q version %d: invalid embedded path",
				scope,
				entry.Version,
			)
		}
		contents, err := fs.ReadFile(files, entry.Path)
		if err != nil {
			return Manifest{}, fmt.Errorf(
				"load migration manifest %q version %d: read embedded SQL: %w",
				scope,
				entry.Version,
				err,
			)
		}
		migrations = append(migrations, Migration{
			Version:  entry.Version,
			Name:     entry.Name,
			SQL:      string(contents),
			Checksum: sha256.Sum256(contents),
		})
	}

	manifest := Manifest{
		Scope:                   scope,
		Ledger:                  ledger,
		Migrations:              append([]Migration(nil), migrations...),
		OwnedSchemaTrees:        append([]string(nil), ownedSchemaTrees...),
		OwnedObjects:            append([]OwnedObject(nil), ownedObjects...),
		ApplicationSchemaGrants: cloneSchemaGrants(schemaGrants),
		ApplicationTableGrants:  cloneTableGrants(tableGrants),
	}
	if err := manifest.Validate(); err != nil {
		return Manifest{}, fmt.Errorf("load migration manifest %q: %w", scope, err)
	}
	return manifest, nil
}

// Validate checks one manifest's identifiers, migration sequence, grants, and
// declared ownership.
func (manifest Manifest) Validate() error {
	if err := validateIdentifier("scope", string(manifest.Scope)); err != nil {
		return err
	}
	if err := validateIdentifier("ledger", manifest.Ledger); err != nil {
		return err
	}
	if len(manifest.Migrations) == 0 {
		return fmt.Errorf("manifest %q has no migrations", manifest.Scope)
	}

	versions := make(map[int64]struct{}, len(manifest.Migrations))
	names := make(map[string]struct{}, len(manifest.Migrations))
	var previousVersion int64
	for index, migration := range manifest.Migrations {
		if migration.Version <= 0 {
			return fmt.Errorf("manifest %q migration version must be positive", manifest.Scope)
		}
		if index > 0 && migration.Version <= previousVersion {
			if _, duplicate := versions[migration.Version]; duplicate {
				return fmt.Errorf(
					"manifest %q has duplicate migration version %d",
					manifest.Scope,
					migration.Version,
				)
			}
			return fmt.Errorf("manifest %q migration versions are not ordered", manifest.Scope)
		}
		if _, duplicate := versions[migration.Version]; duplicate {
			return fmt.Errorf(
				"manifest %q has duplicate migration version %d",
				manifest.Scope,
				migration.Version,
			)
		}
		if err := validateIdentifier("migration name", migration.Name); err != nil {
			return fmt.Errorf("manifest %q version %d: %w", manifest.Scope, migration.Version, err)
		}
		if _, duplicate := names[migration.Name]; duplicate {
			return fmt.Errorf(
				"manifest %q has duplicate migration name %q",
				manifest.Scope,
				migration.Name,
			)
		}
		if strings.TrimSpace(migration.SQL) == "" {
			return fmt.Errorf(
				"manifest %q migration version %d has blank SQL",
				manifest.Scope,
				migration.Version,
			)
		}
		if checksum := sha256.Sum256([]byte(migration.SQL)); checksum != migration.Checksum {
			return fmt.Errorf(
				"manifest %q migration version %d checksum does not match SQL",
				manifest.Scope,
				migration.Version,
			)
		}
		versions[migration.Version] = struct{}{}
		names[migration.Name] = struct{}{}
		previousVersion = migration.Version
	}

	ownership, err := validateOwnership(manifest)
	if err != nil {
		return err
	}
	if err := validateGrants(manifest, ownership); err != nil {
		return err
	}
	for _, migration := range manifest.Migrations {
		for _, object := range createdObjects(migration.SQL) {
			if !ownership.owns(object) {
				return fmt.Errorf(
					"manifest %q creates unowned %s",
					manifest.Scope,
					object.description(),
				)
			}
		}
	}
	return nil
}

// ValidateManifestSet validates each scope and rejects overlapping ownership,
// ledger, and scope claims.
func ValidateManifestSet(manifests []Manifest) error {
	scopes := make(map[Scope]struct{}, len(manifests))
	ledgers := make(map[string]Scope, len(manifests))
	treeOwners := make(map[string]Scope)
	objectOwners := make(map[string]Scope)

	for _, manifest := range manifests {
		if err := manifest.Validate(); err != nil {
			return err
		}
		if _, duplicate := scopes[manifest.Scope]; duplicate {
			return fmt.Errorf("duplicate migration scope %q", manifest.Scope)
		}
		scopes[manifest.Scope] = struct{}{}
		if owner, duplicate := ledgers[manifest.Ledger]; duplicate {
			return fmt.Errorf(
				"migration scopes %q and %q claim ledger %q",
				owner,
				manifest.Scope,
				manifest.Ledger,
			)
		}
		ledgers[manifest.Ledger] = manifest.Scope

		for _, tree := range manifest.OwnedSchemaTrees {
			if owner, duplicate := treeOwners[tree]; duplicate {
				return fmt.Errorf(
					"migration scopes %q and %q duplicate schema tree %q ownership",
					owner,
					manifest.Scope,
					tree,
				)
			}
			for key, owner := range objectOwners {
				if strings.HasPrefix(key, tree+"\x00") {
					return fmt.Errorf(
						"migration scopes %q and %q overlap schema tree %q and exact object ownership",
						owner,
						manifest.Scope,
						tree,
					)
				}
			}
			treeOwners[tree] = manifest.Scope
		}
		for _, object := range manifest.OwnedObjects {
			key := object.key()
			if owner, duplicate := objectOwners[key]; duplicate {
				return fmt.Errorf(
					"migration scopes %q and %q duplicate object %s ownership",
					owner,
					manifest.Scope,
					object.description(),
				)
			}
			if owner, overlap := treeOwners[object.Schema]; overlap {
				return fmt.Errorf(
					"migration scopes %q and %q overlap schema tree %q and exact object ownership",
					owner,
					manifest.Scope,
					object.Schema,
				)
			}
			objectOwners[key] = manifest.Scope
		}
	}
	migrationSchema := OwnedObject{
		Kind: ObjectSchema, Schema: "stacks_migrations", Name: "stacks_migrations",
	}
	if _, owned := objectOwners[migrationSchema.key()]; !owned {
		return fmt.Errorf("migration manifest set has no exact stacks_migrations schema owner")
	}
	for _, manifest := range manifests {
		ledger := OwnedObject{
			Kind: ObjectTable, Schema: "stacks_migrations", Name: manifest.Ledger,
		}
		owner, owned := objectOwners[ledger.key()]
		if !owned || owner != manifest.Scope {
			return fmt.Errorf(
				"migration scope %q does not exactly own ledger %q",
				manifest.Scope,
				manifest.Ledger,
			)
		}
	}
	return nil
}

type ownershipSet struct {
	trees   map[string]struct{}
	objects map[string]struct{}
}

func validateOwnership(manifest Manifest) (ownershipSet, error) {
	ownership := ownershipSet{
		trees:   make(map[string]struct{}, len(manifest.OwnedSchemaTrees)),
		objects: make(map[string]struct{}, len(manifest.OwnedObjects)),
	}
	for _, schema := range manifest.OwnedSchemaTrees {
		if err := validateIdentifier("owned schema tree", schema); err != nil {
			return ownershipSet{}, err
		}
		if _, duplicate := ownership.trees[schema]; duplicate {
			return ownershipSet{}, fmt.Errorf(
				"manifest %q has duplicate owned schema tree %q",
				manifest.Scope,
				schema,
			)
		}
		ownership.trees[schema] = struct{}{}
	}
	for _, object := range manifest.OwnedObjects {
		if err := validateOwnedObject(object); err != nil {
			return ownershipSet{}, fmt.Errorf("manifest %q object: %w", manifest.Scope, err)
		}
		if _, treeOwned := ownership.trees[object.Schema]; treeOwned {
			return ownershipSet{}, fmt.Errorf(
				"manifest %q redundantly owns exact object %s inside schema tree %q",
				manifest.Scope,
				object.description(),
				object.Schema,
			)
		}
		key := object.key()
		if _, duplicate := ownership.objects[key]; duplicate {
			return ownershipSet{}, fmt.Errorf(
				"manifest %q has duplicate owned object %s",
				manifest.Scope,
				object.description(),
			)
		}
		ownership.objects[key] = struct{}{}
	}
	return ownership, nil
}

func validateOwnedObject(object OwnedObject) error {
	switch object.Kind {
	case ObjectSchema, ObjectTable, ObjectFunction, ObjectTrigger:
	default:
		return fmt.Errorf("unsupported kind %q", object.Kind)
	}
	if err := validateIdentifier("schema", object.Schema); err != nil {
		return err
	}
	if err := validateIdentifier("name", object.Name); err != nil {
		return err
	}
	if object.Kind == ObjectSchema {
		if object.Parent != "" {
			return fmt.Errorf("schema object parent must be blank")
		}
		if object.Name != object.Schema {
			return fmt.Errorf("schema object name must equal schema")
		}
		return nil
	}
	if object.Kind == ObjectTrigger {
		if err := validateIdentifier("parent", object.Parent); err != nil {
			return err
		}
		return nil
	}
	if object.Parent != "" {
		return fmt.Errorf("%s object parent must be blank", object.Kind)
	}
	return nil
}

func validateGrants(manifest Manifest, ownership ownershipSet) error {
	schemaTargets := make(map[string]struct{}, len(manifest.ApplicationSchemaGrants))
	for _, grant := range manifest.ApplicationSchemaGrants {
		if err := validateIdentifier("schema grant", grant.Schema); err != nil {
			return err
		}
		if _, duplicate := schemaTargets[grant.Schema]; duplicate {
			return fmt.Errorf("manifest %q has duplicate schema grant %q", manifest.Scope, grant.Schema)
		}
		schemaTargets[grant.Schema] = struct{}{}
		if len(grant.Privileges) == 0 {
			return fmt.Errorf("manifest %q schema grant %q has no privileges", manifest.Scope, grant.Schema)
		}
		seen := make(map[Privilege]struct{}, len(grant.Privileges))
		for _, privilege := range grant.Privileges {
			if privilege != PrivilegeUsage {
				return fmt.Errorf(
					"manifest %q schema privilege %q is unsupported",
					manifest.Scope,
					privilege,
				)
			}
			if _, duplicate := seen[privilege]; duplicate {
				return fmt.Errorf(
					"manifest %q schema grant %q has duplicate privilege %q",
					manifest.Scope,
					grant.Schema,
					privilege,
				)
			}
			seen[privilege] = struct{}{}
		}
		if !ownership.owns(OwnedObject{
			Kind: ObjectSchema, Schema: grant.Schema, Name: grant.Schema,
		}) {
			return fmt.Errorf(
				"manifest %q schema grant %q targets an unowned schema",
				manifest.Scope,
				grant.Schema,
			)
		}
	}

	tableTargets := make(map[string]struct{}, len(manifest.ApplicationTableGrants))
	for _, grant := range manifest.ApplicationTableGrants {
		if err := validateIdentifier("table grant schema", grant.Schema); err != nil {
			return err
		}
		if err := validateIdentifier("table grant table", grant.Table); err != nil {
			return err
		}
		target := grant.Schema + "." + grant.Table
		if _, duplicate := tableTargets[target]; duplicate {
			return fmt.Errorf("manifest %q has duplicate table grant %q", manifest.Scope, target)
		}
		tableTargets[target] = struct{}{}
		if len(grant.Privileges) == 0 {
			return fmt.Errorf("manifest %q table grant %q has no privileges", manifest.Scope, target)
		}

		seen := make(map[Privilege]struct{}, len(grant.Privileges))
		hasUpdate := false
		for _, privilege := range grant.Privileges {
			switch privilege {
			case PrivilegeSelect, PrivilegeInsert:
			case PrivilegeUpdate:
				hasUpdate = true
			default:
				return fmt.Errorf(
					"manifest %q table privilege %q is unsupported",
					manifest.Scope,
					privilege,
				)
			}
			if _, duplicate := seen[privilege]; duplicate {
				return fmt.Errorf(
					"manifest %q table grant %q has duplicate privilege %q",
					manifest.Scope,
					target,
					privilege,
				)
			}
			seen[privilege] = struct{}{}
		}
		if hasUpdate && len(grant.UpdateColumns) == 0 {
			return fmt.Errorf(
				"manifest %q table grant %q requires explicit update columns",
				manifest.Scope,
				target,
			)
		}
		if !hasUpdate && len(grant.UpdateColumns) > 0 {
			return fmt.Errorf(
				"manifest %q table grant %q has update columns without UPDATE privilege",
				manifest.Scope,
				target,
			)
		}
		columns := make(map[string]struct{}, len(grant.UpdateColumns))
		for _, column := range grant.UpdateColumns {
			if err := validateIdentifier("update column", column); err != nil {
				return err
			}
			if _, duplicate := columns[column]; duplicate {
				return fmt.Errorf(
					"manifest %q table grant %q has duplicate update column %q",
					manifest.Scope,
					target,
					column,
				)
			}
			columns[column] = struct{}{}
		}
		if !ownership.owns(OwnedObject{
			Kind: ObjectTable, Schema: grant.Schema, Name: grant.Table,
		}) {
			return fmt.Errorf(
				"manifest %q table grant %q targets an unowned table",
				manifest.Scope,
				target,
			)
		}
	}
	return nil
}

func validateIdentifier(label, identifier string) error {
	if !identifierPattern.MatchString(identifier) {
		return fmt.Errorf("%s identifier %q is unsafe", label, identifier)
	}
	return nil
}

func (ownership ownershipSet) owns(object OwnedObject) bool {
	if _, treeOwned := ownership.trees[object.Schema]; treeOwned {
		return true
	}
	_, exactOwned := ownership.objects[object.key()]
	return exactOwned
}

func (object OwnedObject) key() string {
	return object.Schema + "\x00" + string(object.Kind) + "\x00" + object.Parent + "\x00" + object.Name
}

func (object OwnedObject) description() string {
	switch object.Kind {
	case ObjectSchema:
		return "schema " + object.Schema
	case ObjectTrigger:
		return "trigger " + object.Schema + "." + object.Parent + "." + object.Name
	default:
		return string(object.Kind) + " " + object.Schema + "." + object.Name
	}
}

func createdObjects(sql string) []OwnedObject {
	sql = stripSQLLiteralsAndComments(sql)
	var objects []OwnedObject
	for _, createPattern := range createPatterns {
		for _, match := range createPattern.pattern.FindAllStringSubmatch(sql, -1) {
			switch createPattern.kind {
			case ObjectSchema:
				schema := strings.ToLower(match[1])
				objects = append(objects, OwnedObject{
					Kind: ObjectSchema, Schema: schema, Name: schema,
				})
			case ObjectTable, ObjectFunction:
				objects = append(objects, OwnedObject{
					Kind: createPattern.kind, Schema: strings.ToLower(match[1]), Name: strings.ToLower(match[2]),
				})
			case ObjectTrigger:
				objects = append(objects, OwnedObject{
					Kind:   ObjectTrigger,
					Schema: strings.ToLower(match[2]),
					Parent: strings.ToLower(match[3]),
					Name:   strings.ToLower(match[1]),
				})
			}
		}
	}
	return objects
}

func stripSQLLiteralsAndComments(sql string) string {
	output := []byte(sql)
	for index := 0; index < len(output); {
		switch {
		case output[index] == '\'':
			index = blankSingleQuoted(output, index)
		case index+1 < len(output) && output[index] == '-' && output[index+1] == '-':
			index = blankLineComment(output, index)
		case index+1 < len(output) && output[index] == '/' && output[index+1] == '*':
			index = blankBlockComment(output, index)
		case output[index] == '$':
			if end, ok := blankDollarQuoted(output, index); ok {
				index = end
			} else {
				index++
			}
		default:
			index++
		}
	}
	return string(output)
}

func blankSingleQuoted(sql []byte, start int) int {
	index := start
	sql[index] = ' '
	index++
	for index < len(sql) {
		if sql[index] != '\'' {
			sql[index] = ' '
			index++
			continue
		}
		sql[index] = ' '
		index++
		if index < len(sql) && sql[index] == '\'' {
			sql[index] = ' '
			index++
			continue
		}
		break
	}
	return index
}

func blankLineComment(sql []byte, start int) int {
	index := start
	for index < len(sql) && sql[index] != '\n' {
		sql[index] = ' '
		index++
	}
	return index
}

func blankBlockComment(sql []byte, start int) int {
	index := start
	for index < len(sql) {
		if index+1 < len(sql) && sql[index] == '*' && sql[index+1] == '/' {
			sql[index], sql[index+1] = ' ', ' '
			return index + 2
		}
		sql[index] = ' '
		index++
	}
	return index
}

func blankDollarQuoted(sql []byte, start int) (int, bool) {
	endTag := start + 1
	for endTag < len(sql) && (sql[endTag] == '_' ||
		sql[endTag] >= 'a' && sql[endTag] <= 'z' ||
		sql[endTag] >= 'A' && sql[endTag] <= 'Z' ||
		sql[endTag] >= '0' && sql[endTag] <= '9') {
		endTag++
	}
	if endTag >= len(sql) || sql[endTag] != '$' {
		return start, false
	}
	delimiter := append([]byte(nil), sql[start:endTag+1]...)
	for index := start; index <= endTag; index++ {
		sql[index] = ' '
	}
	bodyStart := endTag + 1
	bodyEnd := strings.Index(string(sql[bodyStart:]), string(delimiter))
	if bodyEnd < 0 {
		for index := bodyStart; index < len(sql); index++ {
			sql[index] = ' '
		}
		return len(sql), true
	}
	bodyEnd += bodyStart
	for index := bodyStart; index < bodyEnd+len(delimiter); index++ {
		sql[index] = ' '
	}
	return bodyEnd + len(delimiter), true
}

func cloneSchemaGrants(grants []SchemaGrant) []SchemaGrant {
	cloned := make([]SchemaGrant, len(grants))
	for index, grant := range grants {
		cloned[index] = SchemaGrant{
			Schema:     grant.Schema,
			Privileges: append([]Privilege(nil), grant.Privileges...),
		}
	}
	return cloned
}

func cloneTableGrants(grants []TableGrant) []TableGrant {
	cloned := make([]TableGrant, len(grants))
	for index, grant := range grants {
		cloned[index] = TableGrant{
			Schema:        grant.Schema,
			Table:         grant.Table,
			Privileges:    append([]Privilege(nil), grant.Privileges...),
			UpdateColumns: append([]string(nil), grant.UpdateColumns...),
		}
	}
	return cloned
}
