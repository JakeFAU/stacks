package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

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
	}}
	var output bytes.Buffer
	command := SyncCommand{Service: service, Output: &output}

	if err := command.Run(context.Background(), nil); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	want := "document_id=document-1 version_id=version-1 outcome=unchanged retry_count=0\n" +
		"document_id=document-2 version_id=version-2 outcome=completed retry_count=1\n" +
		"document_id=document-3 version_id=version-3 outcome=incomplete retry_count=2\n" +
		"document_id=document-4 version_id=version-4 outcome=failed retry_count=0\n" +
		"summary unchanged=1 completed=1 incomplete=1 failed=1\n"
	if output.String() != want {
		t.Fatalf("output = %q, want %q", output.String(), want)
	}
	for _, privateValue := range []string{"Synthetic Meeting", "Synthetic Person", "private excerpt"} {
		if strings.Contains(output.String(), privateValue) {
			t.Fatalf("output contains private value %q", privateValue)
		}
	}
}

func TestSyncCommandRejectsArguments(t *testing.T) {
	err := (SyncCommand{Service: &fixedSyncer{}}).Run(context.Background(), []string{"unexpected"})
	if err == nil || !strings.Contains(err.Error(), "sync command usage") {
		t.Fatalf("Run() error = %v, want usage error", err)
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
	err := (SyncCommand{Service: service, Output: &output}).Run(context.Background(), nil)
	if !errors.Is(err, ingest.ErrFailurePersistence) {
		t.Fatalf("Run() error = %v, want aggregate persistence error", err)
	}
	if !strings.Contains(output.String(), "document_id=document-2 version_id=version-2 outcome=completed") ||
		!strings.Contains(output.String(), "summary unchanged=0 completed=1 incomplete=1 failed=0") {
		t.Fatalf("partial-success output = %q, want later completed outcome and summary", output.String())
	}
}

type fixedSyncer struct {
	summary ingest.Summary
	err     error
}

func (service *fixedSyncer) Sync(context.Context) (ingest.Summary, error) {
	return service.summary, service.err
}
