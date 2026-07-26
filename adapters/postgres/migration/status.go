package migration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// State is one bounded migration-scope state.
type State string

const (
	StateAbsent           State = "absent"
	StatePending          State = "pending"
	StateCurrent          State = "current"
	StateChecksumMismatch State = "checksum_mismatch"
	StateSchemaDrift      State = "schema_drift"
)

// ScopeStatus is the bounded status of one known migration scope.
type ScopeStatus struct {
	Scope           Scope
	State           State
	AppliedVersion  int64
	ExpectedVersion int64
	Configured      bool
}

// Inspector performs read-only migration-ledger and schema inspection.
type Inspector struct {
	DatabaseURL string
	Manifests   []Manifest
	Configured  []Scope
}

// Status returns one record for every known manifest in manifest order.
func (inspector Inspector) Status(ctx context.Context) ([]ScopeStatus, error) {
	if err := inspector.validate(ctx, true); err != nil {
		return nil, err
	}
	connection, err := pgx.Connect(ctx, inspector.DatabaseURL)
	if err != nil {
		return nil, wrapContextError(ctx, "connect for PostgreSQL migration inspection", err)
	}
	defer func() {
		_ = connection.Close(context.Background())
	}()
	return inspector.statusWithConnection(ctx, connection)
}

// StatusWithConnection inspects migration state through a caller-owned
// PostgreSQL connection.
func (inspector Inspector) StatusWithConnection(
	ctx context.Context,
	connection *pgx.Conn,
) ([]ScopeStatus, error) {
	if err := inspector.validate(ctx, false); err != nil {
		return nil, err
	}
	if connection == nil {
		return nil, fmt.Errorf("inspect PostgreSQL migrations: connection is required")
	}
	return inspector.statusWithConnection(ctx, connection)
}

func (inspector Inspector) validate(ctx context.Context, requireDatabaseURL bool) error {
	if ctx == nil {
		return fmt.Errorf("inspect PostgreSQL migrations: context is required")
	}
	if requireDatabaseURL && strings.TrimSpace(inspector.DatabaseURL) == "" {
		return fmt.Errorf("inspect PostgreSQL migrations: database URL is required")
	}
	if err := ValidateManifestSet(inspector.Manifests); err != nil {
		return fmt.Errorf("inspect PostgreSQL migrations: %w", err)
	}
	if _, err := configuredScopeSet(inspector.Manifests, inspector.Configured); err != nil {
		return fmt.Errorf("inspect PostgreSQL migrations: %w", err)
	}
	return nil
}

func (inspector Inspector) statusWithConnection(
	ctx context.Context,
	connection *pgx.Conn,
) ([]ScopeStatus, error) {
	configured, err := configuredScopeSet(inspector.Manifests, inspector.Configured)
	if err != nil {
		return nil, fmt.Errorf("inspect PostgreSQL migrations: %w", err)
	}

	statuses := make([]ScopeStatus, 0, len(inspector.Manifests))
	for _, manifest := range inspector.Manifests {
		status, err := inspectScope(ctx, connection, manifest, configured[manifest.Scope])
		if err != nil {
			return nil, wrapContextError(
				ctx,
				fmt.Sprintf("inspect PostgreSQL migration scope %q", manifest.Scope),
				err,
			)
		}
		statuses = append(statuses, status)
	}
	return statuses, nil
}

func inspectScope(
	ctx context.Context,
	connection *pgx.Conn,
	manifest Manifest,
	configured bool,
) (ScopeStatus, error) {
	status := ScopeStatus{
		Scope:           manifest.Scope,
		ExpectedVersion: manifest.Migrations[len(manifest.Migrations)-1].Version,
		Configured:      configured,
	}

	qualifiedLedger := migrationSchemaName + "." + manifest.Ledger
	var ledgerPresent bool
	if err := connection.QueryRow(
		ctx,
		"SELECT to_regclass($1) IS NOT NULL",
		qualifiedLedger,
	).Scan(&ledgerPresent); err != nil {
		return ScopeStatus{}, err
	}
	if !ledgerPresent {
		status.State = StateAbsent
		return status, nil
	}

	applied, checksumMismatch, err := inspectLedger(ctx, connection, manifest)
	if err != nil {
		return ScopeStatus{}, err
	}
	status.AppliedVersion = applied
	if checksumMismatch {
		status.State = StateChecksumMismatch
		return status, nil
	}
	if applied < status.ExpectedVersion {
		status.State = StatePending
		return status, nil
	}

	objects, err := readCatalog(ctx, connection, manifest)
	if err != nil {
		return ScopeStatus{}, err
	}
	status.State = classifyStatus(
		true,
		false,
		applied,
		status.ExpectedVersion,
		Fingerprint(objects),
		manifest.ExpectedFingerprint,
	)
	return status, nil
}

func inspectLedger(
	ctx context.Context,
	connection *pgx.Conn,
	manifest Manifest,
) (int64, bool, error) {
	qualifiedLedger := pgx.Identifier{migrationSchemaName, manifest.Ledger}.Sanitize()
	rows, err := connection.Query(
		ctx,
		"SELECT version, name, checksum FROM "+qualifiedLedger+" ORDER BY version",
	)
	if err != nil {
		return 0, false, err
	}
	defer rows.Close()

	index := 0
	var appliedVersion int64
	checksumMismatch := false
	for rows.Next() {
		var version int64
		var name string
		var checksum []byte
		if err := rows.Scan(&version, &name, &checksum); err != nil {
			return 0, false, err
		}
		appliedVersion = version
		if index >= len(manifest.Migrations) {
			checksumMismatch = true
			continue
		}
		expected := manifest.Migrations[index]
		if version != expected.Version ||
			name != expected.Name ||
			!bytes.Equal(checksum, expected.Checksum[:]) {
			checksumMismatch = true
		}
		index++
	}
	if err := rows.Err(); err != nil {
		return 0, false, err
	}
	return appliedVersion, checksumMismatch, nil
}

func classifyStatus(
	ledgerPresent bool,
	checksumMismatch bool,
	appliedVersion int64,
	expectedVersion int64,
	liveFingerprint [sha256.Size]byte,
	expectedFingerprint [sha256.Size]byte,
) State {
	switch {
	case !ledgerPresent:
		return StateAbsent
	case checksumMismatch:
		return StateChecksumMismatch
	case appliedVersion < expectedVersion:
		return StatePending
	case liveFingerprint != expectedFingerprint:
		return StateSchemaDrift
	default:
		return StateCurrent
	}
}

func configuredScopeSet(manifests []Manifest, scopes []Scope) (map[Scope]bool, error) {
	known := make(map[Scope]struct{}, len(manifests))
	for _, manifest := range manifests {
		known[manifest.Scope] = struct{}{}
	}
	configured := make(map[Scope]bool, len(scopes))
	for _, scope := range scopes {
		if _, ok := known[scope]; !ok {
			return nil, fmt.Errorf("configured migration scope %q is unknown", scope)
		}
		if configured[scope] {
			return nil, fmt.Errorf("configured migration scope %q is duplicated", scope)
		}
		configured[scope] = true
	}
	if !configured[coreScope] {
		return nil, fmt.Errorf("configured migration scope core is required exactly once")
	}
	return configured, nil
}
