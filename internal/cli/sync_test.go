package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"stacks/internal/directory"
	"stacks/internal/ingest"
)

func TestSyncCommandPrintsOnlyBoundedOutcomesOperationalIDsAndCounts(t *testing.T) {
	service := &fixedSyncer{summary: ingest.Summary{
		Results: []ingest.Result{
			{DocumentID: "document-1", VersionID: "version-1", Outcome: ingest.OutcomeUnchanged},
			{DocumentID: "document-2", VersionID: "version-2", Outcome: ingest.OutcomeCompleted, RetryCount: 1},
			{DocumentID: "document-3", VersionID: "version-3", Outcome: ingest.OutcomeIncomplete, RetryCount: 2, FailureCode: ingest.FailureStorage},
			{DocumentID: "document-4", VersionID: "version-4", Outcome: ingest.OutcomeFailed, FailureCode: ingest.FailureInvalidOutput},
		},
		Unchanged: 1, Completed: 1, Incomplete: 1, Failed: 1,
		Directory: directory.Summary{
			Attempted: 1, Reused: 2, Matched: 3, Review: 4,
			NoMatch: 5, Ambiguous: 6, Unavailable: 7,
		},
	}}
	var output bytes.Buffer
	command := SyncCommand{Service: service, Output: &output}

	if err := command.Run(context.Background(), Invocation{Command: CommandSync}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	want := "document_id=document-1 version_id=version-1 outcome=unchanged retry_count=0\n" +
		"document_id=document-2 version_id=version-2 outcome=completed retry_count=1\n" +
		"document_id=document-3 version_id=version-3 outcome=incomplete retry_count=2\n" +
		"document_id=document-4 version_id=version-4 outcome=failed retry_count=0\n" +
		"summary unchanged=1 completed=1 incomplete=1 failed=1" +
		" directory_attempted=1 directory_reused=2 directory_matched=3" +
		" directory_review=4 directory_no_match=5" +
		" directory_ambiguous=6 directory_unavailable=7\n"
	if output.String() != want {
		t.Fatalf("output = %q, want %q", output.String(), want)
	}
	for _, privateValue := range []string{"Synthetic Meeting", "Synthetic Person", "private excerpt"} {
		if strings.Contains(output.String(), privateValue) {
			t.Fatalf("output contains private value %q", privateValue)
		}
	}
}

func TestSyncCommandRendersCompletedDocumentOutcomesBeforeReturningAggregateError(t *testing.T) {
	service := &fixedSyncer{
		summary: ingest.Summary{
			Results: []ingest.Result{
				{DocumentID: "document-1", VersionID: "version-1", Outcome: ingest.OutcomeIncomplete},
				{DocumentID: "document-2", VersionID: "version-2", Outcome: ingest.OutcomeCompleted},
			},
			Incomplete: 1,
			Completed:  1,
		},
		err: ingest.ErrFailurePersistence,
	}
	var output bytes.Buffer
	err := (SyncCommand{Service: service, Output: &output}).Run(context.Background(), Invocation{Command: CommandSync})
	if !errors.Is(err, ingest.ErrFailurePersistence) {
		t.Fatalf("Run() error = %v, want aggregate persistence error", err)
	}
	if !strings.Contains(output.String(), "document_id=document-2 version_id=version-2 outcome=completed") ||
		!strings.Contains(output.String(), "summary unchanged=0 completed=1 incomplete=1 failed=0") {
		t.Fatalf("partial-success output = %q, want later completed outcome and summary", output.String())
	}
}

func TestSyncCommandRendersCompletedDocumentOutcomesBeforeReturningCancellation(t *testing.T) {
	service := &fixedSyncer{
		summary: ingest.Summary{
			Results:   []ingest.Result{{DocumentID: "document-1", VersionID: "version-1", Outcome: ingest.OutcomeCompleted}},
			Completed: 1,
		},
		err: context.Canceled,
	}
	var output bytes.Buffer
	err := (SyncCommand{Service: service, Output: &output}).Run(context.Background(), Invocation{Command: CommandSync})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context cancellation", err)
	}
	if !strings.Contains(output.String(), "document_id=document-1 version_id=version-1 outcome=completed") ||
		!strings.Contains(output.String(), "summary unchanged=0 completed=1 incomplete=0 failed=0") {
		t.Fatalf("partial-success output = %q, want completed outcome and summary", output.String())
	}
}

type fixedSyncer struct {
	summary ingest.Summary
	err     error
}

func (service *fixedSyncer) Sync(context.Context) (ingest.Summary, error) {
	return service.summary, service.err
}
