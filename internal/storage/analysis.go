package storage

import (
	"context"
	"fmt"
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
	InputDigest           []byte
	AnalysisPromptVersion string
	PolicyVersion         string
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
		input.EmployeeEntityID, input.ManagerEntityID, input.InputDigest, input.AnalysisPromptVersion, input.PolicyVersion, time.Now().UTC()).Scan(&run.ID)
	if err == pgx.ErrNoRows {
		err = transaction.QueryRow(ctx, `SELECT id FROM stacks.analysis_runs WHERE input_digest = $1`, input.InputDigest).Scan(&run.ID)
		if err != nil {
			return AnalysisRun{}, false, fmt.Errorf("load analysis for input digest: %w", err)
		}
		if err := transaction.Commit(ctx); err != nil {
			return AnalysisRun{}, false, fmt.Errorf("commit repeated analysis transaction: %w", err)
		}
		return run, false, nil
	}
	if err != nil {
		return AnalysisRun{}, false, fmt.Errorf("persist analysis for input digest: %w", err)
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
	if len(input.InputDigest) != analysisInputDigestLength {
		return fmt.Errorf("complete analysis: input digest must be %d bytes", analysisInputDigestLength)
	}
	if strings.TrimSpace(input.AnalysisPromptVersion) == "" {
		return fmt.Errorf("complete analysis: prompt version is required")
	}
	if strings.TrimSpace(input.PolicyVersion) == "" {
		return fmt.Errorf("complete analysis: policy version is required")
	}
	return nil
}
