package storage

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"hash"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const analysisInputDigestLength = 32

// AnalysisInput is the stable completed-analysis identity. Its digest is
// computed by deterministic analysis policy from the ordered inputs and
// versions before crossing the storage boundary.
type AnalysisInput struct {
	EmployeeEntityID      string
	ManagerEntityID       string
	AnalysisPromptVersion string
	PolicyVersion         string
	Inputs                []AnalysisInputReference
}

// AnalysisInputKind identifies a durable analysis input class.
type AnalysisInputKind string

const (
	AnalysisInputKindDocumentVersion    AnalysisInputKind = "document_version"
	AnalysisInputKindDocumentTab        AnalysisInputKind = "document_tab"
	AnalysisInputKindObservation        AnalysisInputKind = "observation"
	AnalysisInputKindSignal             AnalysisInputKind = "signal"
	AnalysisInputKindResolutionDecision AnalysisInputKind = "resolution_decision"
)

// AnalysisInputReference records one ordered, immutable input to analysis.
type AnalysisInputReference struct {
	Kind   AnalysisInputKind
	ID     string
	Digest []byte
}

// AnalysisRun identifies a durable completed analysis.
type AnalysisRun struct {
	ID string
}

// AnalysisRepository owns transactions that complete an analysis run and its
// input identity together.
type AnalysisRepository struct {
	pool *pgxpool.Pool
}

// NewAnalysisRepository creates an analysis repository backed by pool.
func NewAnalysisRepository(pool *pgxpool.Pool) *AnalysisRepository {
	return &AnalysisRepository{pool: pool}
}

// Complete records a completed analysis exactly once for its stable digest.
// A repeated completion returns the existing ID with created false.
func (repository *AnalysisRepository) Complete(ctx context.Context, input AnalysisInput) (AnalysisRun, bool, error) {
	if err := validateAnalysisInput(input); err != nil {
		return AnalysisRun{}, false, err
	}
	digest, err := ComputeAnalysisDigest(input)
	if err != nil {
		return AnalysisRun{}, false, err
	}

	transaction, err := repository.pool.Begin(ctx)
	if err != nil {
		return AnalysisRun{}, false, fmt.Errorf("start analysis transaction: %w", err)
	}
	defer transaction.Rollback(ctx) //nolint:errcheck // committed transactions are already closed.

	var run AnalysisRun
	err = transaction.QueryRow(ctx, `
		INSERT INTO stacks.analysis_runs
			(employee_entity_id, manager_entity_id, input_digest, analysis_prompt_version, policy_version, state, recorded_at, completed_at)
		VALUES ($1, $2, $3, $4, $5, 'complete', $6, $6)
		ON CONFLICT (input_digest) DO NOTHING
		RETURNING id`,
		input.EmployeeEntityID, input.ManagerEntityID, digest[:], input.AnalysisPromptVersion, input.PolicyVersion, time.Now().UTC()).Scan(&run.ID)
	if err == pgx.ErrNoRows {
		var storedEmployeeID, storedManagerID, storedPromptVersion, storedPolicyVersion string
		err = transaction.QueryRow(ctx, `
			SELECT id, employee_entity_id::text, manager_entity_id::text, analysis_prompt_version, policy_version
			FROM stacks.analysis_runs WHERE input_digest = $1`, digest[:]).Scan(
			&run.ID, &storedEmployeeID, &storedManagerID, &storedPromptVersion, &storedPolicyVersion)
		if err != nil {
			return AnalysisRun{}, false, fmt.Errorf("load analysis for input digest: %w", err)
		}
		if storedEmployeeID != input.EmployeeEntityID || storedManagerID != input.ManagerEntityID ||
			storedPromptVersion != input.AnalysisPromptVersion || storedPolicyVersion != input.PolicyVersion {
			return AnalysisRun{}, false, fmt.Errorf("load analysis for input digest: stored analysis metadata conflicts")
		}
		if err := transaction.Commit(ctx); err != nil {
			return AnalysisRun{}, false, fmt.Errorf("commit repeated analysis transaction: %w", err)
		}
		return run, false, nil
	}
	if err != nil {
		return AnalysisRun{}, false, fmt.Errorf("persist analysis for input digest: %w", err)
	}
	for position, analysisInput := range input.Inputs {
		_, err := transaction.Exec(ctx, `
			INSERT INTO stacks.analysis_inputs (analysis_run_id, input_kind, input_id, input_digest, position)
			VALUES ($1, $2, $3::uuid, $4, $5)`,
			run.ID, string(analysisInput.Kind), analysisInput.ID, analysisInput.Digest, position)
		if err != nil {
			return AnalysisRun{}, false, fmt.Errorf("persist analysis input %d for analysis %q: %w", position, run.ID, err)
		}
	}
	if err := transaction.Commit(ctx); err != nil {
		return AnalysisRun{}, false, fmt.Errorf("commit analysis transaction: %w", err)
	}
	return run, true, nil
}

func validateAnalysisInput(input AnalysisInput) error {
	if strings.TrimSpace(input.EmployeeEntityID) == "" {
		return fmt.Errorf("complete analysis: employee entity ID is required")
	}
	if strings.TrimSpace(input.ManagerEntityID) == "" {
		return fmt.Errorf("complete analysis: manager entity ID is required")
	}
	if strings.TrimSpace(input.AnalysisPromptVersion) == "" {
		return fmt.Errorf("complete analysis: prompt version is required")
	}
	if strings.TrimSpace(input.PolicyVersion) == "" {
		return fmt.Errorf("complete analysis: policy version is required")
	}
	if len(input.Inputs) == 0 {
		return fmt.Errorf("complete analysis: inputs are required")
	}
	seenDigests := make(map[string]struct{}, len(input.Inputs))
	for position, analysisInput := range input.Inputs {
		if !validAnalysisInputKind(analysisInput.Kind) {
			return fmt.Errorf("complete analysis input %d: kind is invalid", position)
		}
		if strings.TrimSpace(analysisInput.ID) == "" {
			return fmt.Errorf("complete analysis input %d: ID is required", position)
		}
		if len(analysisInput.Digest) != analysisInputDigestLength {
			return fmt.Errorf("complete analysis input %d: digest must be %d bytes", position, analysisInputDigestLength)
		}
		digestIdentity := string(analysisInput.Digest)
		if _, exists := seenDigests[digestIdentity]; exists {
			return fmt.Errorf("complete analysis input %d: digest is repeated", position)
		}
		seenDigests[digestIdentity] = struct{}{}
	}
	return nil
}

// ComputeAnalysisDigest derives the immutable completed-analysis identity from
// the configured pair, ordered input identities, and active analysis versions.
func ComputeAnalysisDigest(input AnalysisInput) ([sha256.Size]byte, error) {
	if err := validateAnalysisInput(input); err != nil {
		return [sha256.Size]byte{}, err
	}
	hasher := sha256.New()
	writeAnalysisDigestString(hasher, input.EmployeeEntityID)
	writeAnalysisDigestString(hasher, input.ManagerEntityID)
	writeAnalysisDigestString(hasher, input.AnalysisPromptVersion)
	writeAnalysisDigestString(hasher, input.PolicyVersion)
	writeAnalysisDigestLength(hasher, uint64(len(input.Inputs)))
	for _, analysisInput := range input.Inputs {
		writeAnalysisDigestString(hasher, string(analysisInput.Kind))
		writeAnalysisDigestString(hasher, analysisInput.ID)
		writeAnalysisDigestBytes(hasher, analysisInput.Digest)
	}
	var digest [sha256.Size]byte
	copy(digest[:], hasher.Sum(nil))
	return digest, nil
}

func validAnalysisInputKind(kind AnalysisInputKind) bool {
	switch kind {
	case AnalysisInputKindDocumentVersion, AnalysisInputKindDocumentTab, AnalysisInputKindObservation, AnalysisInputKindSignal, AnalysisInputKindResolutionDecision:
		return true
	default:
		return false
	}
}

func writeAnalysisDigestString(hasher hash.Hash, value string) {
	writeAnalysisDigestBytes(hasher, []byte(value))
}

func writeAnalysisDigestBytes(hasher hash.Hash, value []byte) {
	writeAnalysisDigestLength(hasher, uint64(len(value)))
	_, _ = hasher.Write(value)
}

func writeAnalysisDigestLength(hasher hash.Hash, length uint64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], length)
	_, _ = hasher.Write(encoded[:])
}
