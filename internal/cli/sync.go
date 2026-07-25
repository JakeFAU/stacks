package cli

import (
	"context"
	"fmt"
	"io"

	"stacks/internal/ingest"
)

// Syncer performs one idempotent collection sync.
type Syncer interface {
	Sync(context.Context) (ingest.Summary, error)
}

// SyncCommand renders privacy-safe per-document outcomes and aggregate counts.
type SyncCommand struct {
	Service Syncer
	Output  io.Writer
}

// Run executes `sync` without positional arguments.
func (command SyncCommand) Run(ctx context.Context, args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("sync command usage: sync")
	}
	if command.Service == nil {
		return fmt.Errorf("sync command: service is not configured")
	}
	summary, syncErr := command.Service.Sync(ctx)
	output := command.Output
	if output == nil {
		output = io.Discard
	}
	for _, result := range summary.Results {
		if !validSyncOutcome(result.Outcome) {
			return fmt.Errorf("sync command: outcome is invalid")
		}
		if _, err := fmt.Fprintf(output, "document_id=%s version_id=%s outcome=%s retry_count=%d", result.DocumentID, result.VersionID, result.Outcome, result.RetryCount); err != nil {
			return fmt.Errorf("write sync outcome: %w", err)
		}
		if _, err := fmt.Fprintln(output); err != nil {
			return fmt.Errorf("write sync outcome: %w", err)
		}
	}
	if _, err := fmt.Fprintf(
		output,
		"summary unchanged=%d completed=%d incomplete=%d failed=%d"+
			" directory_attempted=%d directory_reused=%d directory_matched=%d"+
			" directory_review=%d directory_no_match=%d"+
			" directory_ambiguous=%d directory_unavailable=%d\n",
		summary.Unchanged,
		summary.Completed,
		summary.Incomplete,
		summary.Failed,
		summary.Directory.Attempted,
		summary.Directory.Reused,
		summary.Directory.Matched,
		summary.Directory.Review,
		summary.Directory.NoMatch,
		summary.Directory.Ambiguous,
		summary.Directory.Unavailable,
	); err != nil {
		return fmt.Errorf("write sync summary: %w", err)
	}
	return syncErr
}

func validSyncOutcome(outcome ingest.Outcome) bool {
	switch outcome {
	case ingest.OutcomeUnchanged, ingest.OutcomeCompleted, ingest.OutcomeIncomplete, ingest.OutcomeFailed:
		return true
	default:
		return false
	}
}
