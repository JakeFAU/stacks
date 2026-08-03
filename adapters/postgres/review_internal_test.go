package postgres

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/JakeFAU/stacks/core/evidence"
	"github.com/jackc/pgx/v5"
)

func TestLoadReviewerEvidenceUsesOneQuery(t *testing.T) {
	reader := &reviewerEvidenceReader{
		ids:    []string{"evidence-2", "evidence-1", "evidence-3"},
		quotes: []string{"second", "first", "third"},
	}
	wantIDs := []evidence.EvidenceID{"evidence-2", "evidence-1", "evidence-3"}

	got, err := loadReviewerEvidenceRecords(context.Background(), reader, wantIDs)
	if err != nil {
		t.Fatalf("loadReviewerEvidenceRecords() error = %v", err)
	}
	want := []ReviewerEvidence{
		{ID: "evidence-2", Quote: "second"},
		{ID: "evidence-1", Quote: "first"},
		{ID: "evidence-3", Quote: "third"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("evidence = %#v, want %#v", got, want)
	}
	if reader.queryCount != 1 {
		t.Fatalf("query count = %d, want 1", reader.queryCount)
	}
	if !reflect.DeepEqual(reader.arguments, []any{[]string{"evidence-2", "evidence-1", "evidence-3"}}) {
		t.Fatalf("query arguments = %#v, want evidence IDs", reader.arguments)
	}
}

func TestLoadReviewerEvidenceRejectsMissingEvidence(t *testing.T) {
	reader := &reviewerEvidenceReader{
		ids:    []string{"evidence-1"},
		quotes: []string{"first"},
	}

	_, err := loadReviewerEvidenceRecords(
		context.Background(),
		reader,
		[]evidence.EvidenceID{"evidence-1", "evidence-missing"},
	)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("loadReviewerEvidenceRecords() error = %v, want ErrNotFound", err)
	}
	if reader.queryCount != 1 {
		t.Fatalf("query count = %d, want 1", reader.queryCount)
	}
}

type reviewerEvidenceReader struct {
	queryCount int
	arguments  []any
	ids        []string
	quotes     []string
}

func (*reviewerEvidenceReader) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, fmt.Errorf("unexpected Query call")
}

func (reader *reviewerEvidenceReader) QueryRow(
	_ context.Context,
	_ string,
	arguments ...any,
) pgx.Row {
	reader.queryCount++
	reader.arguments = append([]any(nil), arguments...)
	return reviewerEvidenceRow{ids: reader.ids, quotes: reader.quotes}
}

type reviewerEvidenceRow struct {
	ids    []string
	quotes []string
}

func (row reviewerEvidenceRow) Scan(destinations ...any) error {
	if len(destinations) != 2 {
		return fmt.Errorf("scan destination count = %d, want 2", len(destinations))
	}
	ids, ok := destinations[0].(*[]string)
	if !ok {
		return fmt.Errorf("ID destination is %T, want *[]string", destinations[0])
	}
	quotes, ok := destinations[1].(*[]string)
	if !ok {
		return fmt.Errorf("quote destination is %T, want *[]string", destinations[1])
	}
	*ids = append([]string(nil), row.ids...)
	*quotes = append([]string(nil), row.quotes...)
	return nil
}
