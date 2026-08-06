package postgres

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/JakeFAU/stacks/core/evidence"
	"github.com/JakeFAU/stacks/core/identity"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestInsertEntityAliasAssertionsUsesOneCommand(t *testing.T) {
	recordedAt := time.Date(2026, time.August, 6, 12, 0, 0, 0, time.UTC)
	decisionID := identity.DecisionID("decision-1")
	entityID := identity.EntityID("entity-1")
	aliases := make([]identity.AliasAssertion, 0, 3)
	for index, alias := range []identity.Alias{
		{Type: identity.AliasTypeName, Value: "reviewer person"},
		{Type: identity.AliasTypeName, Value: "source person"},
		{Type: identity.AliasTypeEmail, Value: "reviewer@example.test"},
	} {
		assertion, err := identity.NewAliasAssertion(identity.AliasAssertionInput{
			ID:         identity.AliasAssertionID(fmt.Sprintf("alias-%d", index+1)),
			DecisionID: decisionID,
			EntityID:   entityID,
			Alias:      alias,
			RecordedAt: recordedAt,
		})
		if err != nil {
			t.Fatalf("identity.NewAliasAssertion() error = %v", err)
		}
		aliases = append(aliases, assertion)
	}

	writer := &entityAliasAssertionWriter{}
	if err := insertEntityAliasAssertions(context.Background(), writer, aliases); err != nil {
		t.Fatalf("insertEntityAliasAssertions() error = %v", err)
	}
	if writer.commandCount != 1 {
		t.Fatalf("command count = %d, want 1", writer.commandCount)
	}
	wantArguments := []any{
		[]string{"alias-1", "alias-2", "alias-3"},
		[]string{"decision-1", "decision-1", "decision-1"},
		[]string{"entity-1", "entity-1", "entity-1"},
		[]string{"name", "name", "email"},
		[]string{"reviewer person", "source person", "reviewer@example.test"},
		[]time.Time{recordedAt, recordedAt, recordedAt},
	}
	if !reflect.DeepEqual(writer.arguments, wantArguments) {
		t.Fatalf("arguments = %#v, want %#v", writer.arguments, wantArguments)
	}
}

func TestInsertEntityAliasAssertionsEmptyInputUsesNoCommand(t *testing.T) {
	writer := &entityAliasAssertionWriter{}
	if err := insertEntityAliasAssertions(context.Background(), writer, nil); err != nil {
		t.Fatalf("insertEntityAliasAssertions() error = %v", err)
	}
	if writer.commandCount != 0 {
		t.Fatalf("command count = %d, want 0", writer.commandCount)
	}
}

type entityAliasAssertionWriter struct {
	commandCount int
	arguments    []any
}

func (writer *entityAliasAssertionWriter) Exec(
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
