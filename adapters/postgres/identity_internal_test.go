package postgres

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/JakeFAU/stacks/core/evidence"
	"github.com/JakeFAU/stacks/core/identity"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestInsertResolutionProposalEvidenceUsesOneCommand(t *testing.T) {
	writer := &resolutionProposalEvidenceWriter{}
	evidenceIDs := []evidence.EvidenceID{"evidence-1", "evidence-2", "evidence-3"}

	err := insertResolutionProposalEvidence(
		context.Background(),
		writer,
		identity.ProposalID("proposal-1"),
		evidenceIDs,
	)
	if err != nil {
		t.Fatalf("insertResolutionProposalEvidence() error = %v", err)
	}
	if writer.commandCount != 1 {
		t.Fatalf("command count = %d, want 1", writer.commandCount)
	}
	wantArguments := []any{
		identity.ProposalID("proposal-1"),
		[]string{"evidence-1", "evidence-2", "evidence-3"},
		[]int32{0, 1, 2},
	}
	if !reflect.DeepEqual(writer.arguments, wantArguments) {
		t.Fatalf("arguments = %#v, want %#v", writer.arguments, wantArguments)
	}
}

type resolutionProposalEvidenceWriter struct {
	commandCount int
	arguments    []any
}

func (writer *resolutionProposalEvidenceWriter) Exec(
	_ context.Context,
	_ string,
	arguments ...any,
) (pgconn.CommandTag, error) {
	writer.commandCount++
	if writer.commandCount > 1 {
		return pgconn.CommandTag{}, fmt.Errorf("unexpected extra command")
	}
	writer.arguments = append([]any(nil), arguments...)
	return pgconn.CommandTag{}, nil
}
