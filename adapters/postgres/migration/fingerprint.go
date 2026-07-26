package migration

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

// CatalogObject is one stable semantic PostgreSQL catalog record.
type CatalogObject struct {
	Kind       string
	Schema     string
	Parent     string
	Name       string
	Definition string
}

// Fingerprint returns a deterministic SHA-256 over all fields of every
// catalog object. Catalog order is ignored and adjacent fields cannot collide.
func Fingerprint(objects []CatalogObject) [sha256.Size]byte {
	ordered := append([]CatalogObject(nil), objects...)
	sort.Slice(ordered, func(left, right int) bool {
		a := ordered[left]
		b := ordered[right]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Schema != b.Schema {
			return a.Schema < b.Schema
		}
		if a.Parent != b.Parent {
			return a.Parent < b.Parent
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Definition < b.Definition
	})

	digest := sha256.New()
	var length [8]byte
	for _, object := range ordered {
		for _, field := range []string{
			object.Kind,
			object.Schema,
			object.Parent,
			object.Name,
			object.Definition,
		} {
			binary.BigEndian.PutUint64(length[:], uint64(len(field)))
			_, _ = digest.Write(length[:])
			_, _ = digest.Write([]byte(field))
		}
	}
	var result [sha256.Size]byte
	copy(result[:], digest.Sum(nil))
	return result
}

// InspectFingerprint reads one manifest's owned objects from PostgreSQL and
// returns their semantic fingerprint without changing database state.
func InspectFingerprint(
	ctx context.Context,
	databaseURL string,
	manifest Manifest,
) ([sha256.Size]byte, error) {
	if ctx == nil {
		return [sha256.Size]byte{}, fmt.Errorf("inspect PostgreSQL fingerprint: context is required")
	}
	if strings.TrimSpace(databaseURL) == "" {
		return [sha256.Size]byte{}, fmt.Errorf("inspect PostgreSQL fingerprint: database URL is required")
	}
	if err := manifest.Validate(); err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("inspect PostgreSQL fingerprint: %w", err)
	}
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		return [sha256.Size]byte{}, wrapContextError(
			ctx,
			"connect for PostgreSQL fingerprint inspection",
			err,
		)
	}
	defer func() {
		_ = connection.Close(context.Background())
	}()
	objects, err := readCatalog(ctx, connection, manifest)
	if err != nil {
		return [sha256.Size]byte{}, wrapContextError(
			ctx,
			fmt.Sprintf("read PostgreSQL fingerprint for scope %q", manifest.Scope),
			err,
		)
	}
	return Fingerprint(objects), nil
}

func readCatalog(
	ctx context.Context,
	connection *pgx.Conn,
	manifest Manifest,
) ([]CatalogObject, error) {
	transaction, err := connection.BeginTx(
		ctx,
		pgx.TxOptions{AccessMode: pgx.ReadOnly},
	)
	if err != nil {
		return nil, fmt.Errorf("begin stable PostgreSQL catalog inspection: %w", err)
	}
	defer func() {
		_ = transaction.Rollback(context.Background())
	}()
	if _, err := transaction.Exec(
		ctx,
		"SET LOCAL search_path = pg_catalog",
	); err != nil {
		return nil, fmt.Errorf("set stable PostgreSQL catalog search path: %w", err)
	}
	return readCatalogInTransaction(ctx, transaction, manifest)
}

type catalogQuerier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func readCatalogInTransaction(
	ctx context.Context,
	connection catalogQuerier,
	manifest Manifest,
) ([]CatalogObject, error) {
	treeSchemas := append([]string(nil), manifest.OwnedSchemaTrees...)
	exactSchemas := exactObjectNames(manifest.OwnedObjects, ObjectSchema)
	exactTables := exactObjects(manifest.OwnedObjects, ObjectTable)
	exactFunctions := exactObjects(manifest.OwnedObjects, ObjectFunction)
	exactTriggers := exactObjects(manifest.OwnedObjects, ObjectTrigger)

	objects := make(map[string]CatalogObject)
	add := func(object CatalogObject) {
		objects[catalogObjectKey(object)] = object
	}
	if err := queryCatalogObjects(
		ctx,
		connection,
		`SELECT 'schema', namespace.nspname, '', namespace.nspname, ''
		   FROM pg_catalog.pg_namespace AS namespace
		  WHERE namespace.nspname = ANY($1::text[])
		     OR namespace.nspname = ANY($2::text[])`,
		[]any{treeSchemas, exactSchemas},
		add,
	); err != nil {
		return nil, fmt.Errorf("read owned PostgreSQL schemas: %w", err)
	}

	tableSchemas, tableNames := splitExactObjects(exactTables)
	if err := queryCatalogObjects(
		ctx,
		connection,
		`SELECT 'table',
		        namespace.nspname,
		        '',
		        relation.relname,
		        concat(
		            'kind=', relation.relkind,
		            '|persistence=', relation.relpersistence,
		            '|partition=', relation.relispartition
		        )
		   FROM pg_catalog.pg_class AS relation
		   JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = relation.relnamespace
		  WHERE relation.relkind IN ('r', 'p')
		    AND (
		        namespace.nspname = ANY($1::text[])
		        OR (namespace.nspname, relation.relname) IN (
		            SELECT * FROM unnest($2::text[], $3::text[])
		        )
		    )`,
		[]any{treeSchemas, tableSchemas, tableNames},
		add,
	); err != nil {
		return nil, fmt.Errorf("read owned PostgreSQL tables: %w", err)
	}

	if err := queryCatalogObjects(
		ctx,
		connection,
		`SELECT 'column',
		        namespace.nspname,
		        relation.relname,
		        attribute.attname,
		        concat(
		            'type=', pg_catalog.format_type(attribute.atttypid, attribute.atttypmod),
		            '|nullable=', NOT attribute.attnotnull,
		            '|default=', coalesce(pg_catalog.pg_get_expr(default_value.adbin, default_value.adrelid, false), ''),
		            '|identity=', attribute.attidentity,
		            '|generated=', attribute.attgenerated,
		            '|collation=', coalesce(collation_record.collname, '')
		        )
		   FROM pg_catalog.pg_attribute AS attribute
		   JOIN pg_catalog.pg_class AS relation ON relation.oid = attribute.attrelid
		   JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = relation.relnamespace
		   LEFT JOIN pg_catalog.pg_attrdef AS default_value
		          ON default_value.adrelid = attribute.attrelid
		         AND default_value.adnum = attribute.attnum
		   LEFT JOIN pg_catalog.pg_collation AS collation_record
		          ON collation_record.oid = attribute.attcollation
		  WHERE relation.relkind IN ('r', 'p')
		    AND attribute.attnum > 0
		    AND NOT attribute.attisdropped
		    AND (
		        namespace.nspname = ANY($1::text[])
		        OR (namespace.nspname, relation.relname) IN (
		            SELECT * FROM unnest($2::text[], $3::text[])
		        )
		    )`,
		[]any{treeSchemas, tableSchemas, tableNames},
		add,
	); err != nil {
		return nil, fmt.Errorf("read owned PostgreSQL columns: %w", err)
	}

	if err := queryCatalogObjects(
		ctx,
		connection,
		`SELECT 'constraint',
		        namespace.nspname,
		        relation.relname,
		        constraint_record.conname,
		        concat(
		            'type=', constraint_record.contype,
		            '|definition=', pg_catalog.pg_get_constraintdef(constraint_record.oid, false)
		        )
		   FROM pg_catalog.pg_constraint AS constraint_record
		   JOIN pg_catalog.pg_class AS relation ON relation.oid = constraint_record.conrelid
		   JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = relation.relnamespace
		  WHERE (
		        namespace.nspname = ANY($1::text[])
		        OR (namespace.nspname, relation.relname) IN (
		            SELECT * FROM unnest($2::text[], $3::text[])
		        )
		    )`,
		[]any{treeSchemas, tableSchemas, tableNames},
		add,
	); err != nil {
		return nil, fmt.Errorf("read owned PostgreSQL constraints: %w", err)
	}

	if err := queryCatalogObjects(
		ctx,
		connection,
		`SELECT 'index',
		        namespace.nspname,
		        relation.relname,
		        index_relation.relname,
		        concat(
		            'valid=', index_record.indisvalid,
		            '|ready=', index_record.indisready,
		            '|definition=', pg_catalog.pg_get_indexdef(index_record.indexrelid, 0, false)
		        )
		   FROM pg_catalog.pg_index AS index_record
		   JOIN pg_catalog.pg_class AS relation ON relation.oid = index_record.indrelid
		   JOIN pg_catalog.pg_class AS index_relation ON index_relation.oid = index_record.indexrelid
		   JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = relation.relnamespace
		  WHERE (
		        namespace.nspname = ANY($1::text[])
		        OR (namespace.nspname, relation.relname) IN (
		            SELECT * FROM unnest($2::text[], $3::text[])
		        )
		    )`,
		[]any{treeSchemas, tableSchemas, tableNames},
		add,
	); err != nil {
		return nil, fmt.Errorf("read owned PostgreSQL indexes: %w", err)
	}

	exactFunctionOIDs, err := resolveExactFunctionOIDs(ctx, connection, exactFunctions)
	if err != nil {
		return nil, err
	}
	if err := queryCatalogObjects(
		ctx,
		connection,
		`SELECT 'function',
		        namespace.nspname,
		        '',
		        procedure.proname,
		        concat(
		            'identity=', pg_catalog.pg_get_function_identity_arguments(procedure.oid),
		            '|result=', pg_catalog.pg_get_function_result(procedure.oid),
		            '|kind=', procedure.prokind,
		            '|volatility=', procedure.provolatile,
		            '|strict=', procedure.proisstrict,
		            '|leakproof=', procedure.proleakproof,
		            '|parallel=', procedure.proparallel,
		            '|definition=', pg_catalog.pg_get_functiondef(procedure.oid)
		        )
		   FROM pg_catalog.pg_proc AS procedure
		   JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = procedure.pronamespace
		  WHERE namespace.nspname = ANY($1::text[])
		     OR procedure.oid = ANY($2::oid[])`,
		[]any{treeSchemas, exactFunctionOIDs},
		add,
	); err != nil {
		return nil, fmt.Errorf("read owned PostgreSQL functions: %w", err)
	}

	triggerSchemas, triggerNames := splitExactObjects(exactTriggers)
	triggerParents := make([]string, len(exactTriggers))
	for index, object := range exactTriggers {
		triggerParents[index] = object.Parent
	}
	if err := queryCatalogObjects(
		ctx,
		connection,
		`SELECT 'trigger',
		        namespace.nspname,
		        relation.relname,
		        trigger_record.tgname,
		        concat(
		            'enabled=', trigger_record.tgenabled,
		            '|definition=', pg_catalog.pg_get_triggerdef(trigger_record.oid, false)
		        )
		   FROM pg_catalog.pg_trigger AS trigger_record
		   JOIN pg_catalog.pg_class AS relation ON relation.oid = trigger_record.tgrelid
		   JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = relation.relnamespace
		  WHERE NOT trigger_record.tgisinternal
		    AND (
		        namespace.nspname = ANY($1::text[])
		        OR (namespace.nspname, relation.relname, trigger_record.tgname) IN (
		            SELECT * FROM unnest($2::text[], $3::text[], $4::text[])
		        )
		    )`,
		[]any{treeSchemas, triggerSchemas, triggerParents, triggerNames},
		add,
	); err != nil {
		return nil, fmt.Errorf("read owned PostgreSQL triggers: %w", err)
	}

	result := make([]CatalogObject, 0, len(objects))
	for _, object := range objects {
		result = append(result, object)
	}
	return result, nil
}

func resolveExactFunctionOIDs(
	ctx context.Context,
	connection catalogQuerier,
	functions []OwnedObject,
) ([]uint32, error) {
	oids := make([]uint32, 0, len(functions))
	seen := make(map[uint32]struct{}, len(functions))
	for _, function := range functions {
		resolved, err := resolveExactFunctionOID(ctx, connection, function)
		if err != nil {
			return nil, err
		}
		if _, duplicate := seen[resolved]; duplicate {
			return nil, fmt.Errorf("resolve exact owned PostgreSQL function")
		}
		seen[resolved] = struct{}{}
		oids = append(oids, resolved)
	}
	return oids, nil
}

type exactFunctionCandidate struct {
	oid           uint32
	argumentTypes []uint32
}

func resolveExactFunctionOID(
	ctx context.Context,
	connection catalogQuerier,
	function OwnedObject,
) (uint32, error) {
	candidates, err := exactFunctionCandidates(ctx, connection, function)
	if err != nil {
		return 0, err
	}
	identityTypes, err := functionIdentityTypes(
		ctx,
		connection,
		function.FunctionIdentityArguments,
	)
	if err != nil {
		return 0, err
	}
	matches := make([]uint32, 0, 1)
	for _, candidate := range candidates {
		if matchesFunctionParameterTypes(candidate.argumentTypes, identityTypes) {
			matches = append(matches, candidate.oid)
		}
	}
	if len(matches) != 1 {
		return 0, fmt.Errorf("resolve exact owned PostgreSQL function")
	}
	return matches[0], nil
}

func exactFunctionCandidates(
	ctx context.Context,
	connection catalogQuerier,
	function OwnedObject,
) ([]exactFunctionCandidate, error) {
	rows, err := connection.Query(
		ctx,
		`SELECT procedure.oid, procedure.proargtypes::text
		   FROM pg_catalog.pg_proc AS procedure
		   JOIN pg_catalog.pg_namespace AS namespace
		     ON namespace.oid = procedure.pronamespace
		  WHERE namespace.nspname = $1
		    AND procedure.proname = $2`,
		function.Schema,
		function.Name,
	)
	if err != nil {
		return nil, exactFunctionResolutionError(ctx)
	}
	defer rows.Close()

	var candidates []exactFunctionCandidate
	for rows.Next() {
		var candidate exactFunctionCandidate
		var argumentTypes string
		if err := rows.Scan(&candidate.oid, &argumentTypes); err != nil {
			return nil, exactFunctionResolutionError(ctx)
		}
		candidate.argumentTypes, err = parseFunctionArgumentTypes(argumentTypes)
		if err != nil {
			return nil, exactFunctionResolutionError(ctx)
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, exactFunctionResolutionError(ctx)
	}
	return candidates, nil
}

func functionIdentityTypes(
	ctx context.Context,
	connection catalogQuerier,
	identityArguments string,
) ([]map[uint32]struct{}, error) {
	identityTypes, valid := splitFunctionArguments(identityArguments)
	if !valid {
		return nil, exactFunctionResolutionError(ctx)
	}
	if len(identityTypes) == 0 {
		return nil, nil
	}

	result := make([]map[uint32]struct{}, 0, len(identityTypes))
	for _, identityType := range identityTypes {
		var oid uint32
		var namespace string
		err := connection.QueryRow(
			ctx,
			`SELECT type_record.oid, namespace.nspname
			   FROM pg_catalog.pg_type AS type_record
			   JOIN pg_catalog.pg_namespace AS namespace
			     ON namespace.oid = type_record.typnamespace
			  WHERE type_record.oid = pg_catalog.to_regtype($1)`,
			identityType,
		).Scan(&oid, &namespace)
		if err != nil {
			return nil, exactFunctionResolutionError(ctx)
		}
		qualifier, qualified := functionTypeQualifier(identityType)
		if (namespace != "pg_catalog" && !qualified) ||
			(qualified && namespace != qualifier) {
			return nil, exactFunctionResolutionError(ctx)
		}
		result = append(result, map[uint32]struct{}{oid: struct{}{}})
	}
	return result, nil
}

func functionTypeQualifier(value string) (string, bool) {
	if strings.Contains(value, " ") {
		return "", false
	}
	for strings.HasSuffix(value, "[]") {
		value = strings.TrimSuffix(value, "[]")
	}
	parts := strings.Split(value, ".")
	if len(parts) != 2 {
		return "", false
	}
	return parts[0], true
}

func parseFunctionArgumentTypes(value string) ([]uint32, error) {
	fields := strings.Fields(value)
	result := make([]uint32, 0, len(fields))
	for _, field := range fields {
		oid, err := strconv.ParseUint(field, 10, 32)
		if err != nil {
			return nil, err
		}
		result = append(result, uint32(oid))
	}
	return result, nil
}

func matchesFunctionParameterTypes(
	actual []uint32,
	allowed []map[uint32]struct{},
) bool {
	if len(actual) != len(allowed) {
		return false
	}
	for index, oid := range actual {
		if _, ok := allowed[index][oid]; !ok {
			return false
		}
	}
	return true
}

func exactFunctionResolutionError(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return fmt.Errorf("resolve exact owned PostgreSQL function")
}

func queryCatalogObjects(
	ctx context.Context,
	connection catalogQuerier,
	query string,
	arguments []any,
	add func(CatalogObject),
) error {
	rows, err := connection.Query(ctx, query, arguments...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var object CatalogObject
		if err := rows.Scan(
			&object.Kind,
			&object.Schema,
			&object.Parent,
			&object.Name,
			&object.Definition,
		); err != nil {
			return err
		}
		add(object)
	}
	return rows.Err()
}

func exactObjectNames(objects []OwnedObject, kind ObjectKind) []string {
	names := make([]string, 0, len(objects))
	for _, object := range objects {
		if object.Kind == kind {
			names = append(names, object.Schema)
		}
	}
	return names
}

func exactObjects(objects []OwnedObject, kind ObjectKind) []OwnedObject {
	result := make([]OwnedObject, 0, len(objects))
	for _, object := range objects {
		if object.Kind == kind {
			result = append(result, object)
		}
	}
	return result
}

func splitExactObjects(objects []OwnedObject) ([]string, []string) {
	schemas := make([]string, len(objects))
	names := make([]string, len(objects))
	for index, object := range objects {
		schemas[index] = object.Schema
		names[index] = object.Name
	}
	return schemas, names
}

func catalogObjectKey(object CatalogObject) string {
	return strings.Join(
		[]string{object.Kind, object.Schema, object.Parent, object.Name, object.Definition},
		"\x00",
	)
}
