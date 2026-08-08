package postgres

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/JakeFAU/stacks/core/evidence"
	"github.com/JakeFAU/stacks/core/identity"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestLoadEntitySnapshotsUsesOneQuery(t *testing.T) {
	recordedAt := time.Date(2026, time.August, 8, 12, 0, 0, 0, time.UTC)
	aliasRecordedAt := recordedAt.Add(time.Minute)
	reader := &entitySnapshotsReader{rows: [][]any{
		{
			"entity-1", "person", "First Person", recordedAt,
			(*string)(nil), (*string)(nil), (*string)(nil), (*string)(nil), (*time.Time)(nil),
		},
		{
			"entity-2", "person", "Second Person", recordedAt,
			stringPointer("alias-1"), stringPointer("decision-1"), stringPointer("name"),
			stringPointer("Second Person"), &aliasRecordedAt,
		},
		{
			"entity-2", "person", "Second Person", recordedAt,
			stringPointer("alias-2"), stringPointer("decision-1"), stringPointer("email"),
			stringPointer("second@example.com"), &aliasRecordedAt,
		},
	}}

	got, err := loadEntitySnapshots(context.Background(), reader)
	if err != nil {
		t.Fatalf("loadEntitySnapshots() error = %v", err)
	}
	want := []identity.EntitySnapshot{
		{
			ID:          "entity-1",
			Kind:        identity.KindPerson,
			DisplayName: "First Person",
			RecordedAt:  recordedAt,
			Aliases:     []identity.Alias{},
		},
		{
			ID:          "entity-2",
			Kind:        identity.KindPerson,
			DisplayName: "Second Person",
			RecordedAt:  recordedAt,
			Aliases: []identity.Alias{
				{Type: identity.AliasTypeName, Value: "Second Person"},
				{Type: identity.AliasTypeEmail, Value: "second@example.com"},
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshots = %#v, want %#v", got, want)
	}
	if reader.queryCount != 1 {
		t.Fatalf("query count = %d, want 1", reader.queryCount)
	}
}

func TestLoadEntitySnapshotsRejectsBlankEntityID(t *testing.T) {
	reader := &entitySnapshotsReader{rows: [][]any{{
		"", "person", "Invalid Person", time.Date(2026, time.August, 8, 12, 0, 0, 0, time.UTC),
		(*string)(nil), (*string)(nil), (*string)(nil), (*string)(nil), (*time.Time)(nil),
	}}}

	if _, err := loadEntitySnapshots(context.Background(), reader); err == nil {
		t.Fatal("loadEntitySnapshots() error = nil, want invalid stored entity error")
	}
}

type entitySnapshotsReader struct {
	rows       [][]any
	queryCount int
}

func (reader *entitySnapshotsReader) Query(
	context.Context,
	string,
	...any,
) (pgx.Rows, error) {
	reader.queryCount++
	if reader.queryCount > 1 {
		return nil, fmt.Errorf("unexpected extra query")
	}
	return &temporalQueryFakeRows{values: reader.rows}, nil
}

func (*entitySnapshotsReader) QueryRow(context.Context, string, ...any) pgx.Row {
	return observationEvidenceRow{err: fmt.Errorf("unexpected QueryRow call")}
}

func stringPointer(value string) *string { return &value }

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
