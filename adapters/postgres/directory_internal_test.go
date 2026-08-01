package postgres

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestInsertDirectoryProfileEmailsUsesOneCommand(t *testing.T) {
	writer := &directoryProfileEmailWriter{}
	emails := []DirectoryEmail{
		{Value: "primary@example.test", Primary: true},
		{Value: "alias@example.test"},
		{Value: "other@example.test"},
	}

	err := insertDirectoryProfileEmails(
		context.Background(),
		writer,
		"snapshot:synthetic",
		emails,
	)
	if err != nil {
		t.Fatalf("insertDirectoryProfileEmails() error = %v", err)
	}
	if writer.commandCount != 1 {
		t.Fatalf("command count = %d, want 1", writer.commandCount)
	}
	wantArguments := []any{
		"snapshot:synthetic",
		[]string{"primary@example.test", "alias@example.test", "other@example.test"},
		[]bool{true, false, false},
		[]int32{0, 1, 2},
	}
	if !reflect.DeepEqual(writer.arguments, wantArguments) {
		t.Fatalf("arguments = %#v, want %#v", writer.arguments, wantArguments)
	}
}

type directoryProfileEmailWriter struct {
	commandCount int
	arguments    []any
}

func (writer *directoryProfileEmailWriter) Exec(
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
