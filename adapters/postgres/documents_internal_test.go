package postgres

import (
	"context"
	"reflect"
	"testing"

	"github.com/JakeFAU/stacks/core/evidence"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestInsertDocumentSectionsUsesOneBatch(t *testing.T) {
	sections := []evidence.Section{
		canonicalDocumentSection(t, "transcript", "Transcript", "", []string{"Meeting", "Transcript"}, 0, "transcript", "Alpha speaks."),
		canonicalDocumentSection(t, "notes", "Notes", "root", []string{"Meeting", "Notes"}, 1, "notes", "A follow-up."),
	}
	batcher := &documentSectionBatcherRecorder{}

	if err := insertDocumentSections(
		context.Background(),
		batcher,
		"version:synthetic",
		sections,
	); err != nil {
		t.Fatalf("insertDocumentSections() error = %v", err)
	}

	if batcher.calls != 1 {
		t.Fatalf("SendBatch() calls = %d, want 1", batcher.calls)
	}
	if batcher.results.closeCalls != 1 {
		t.Fatalf("BatchResults.Close() calls = %d, want 1", batcher.results.closeCalls)
	}
	if len(batcher.queries) != 2 {
		t.Fatalf("queued section commands = %d, want 2", len(batcher.queries))
	}
	for index, query := range batcher.queries {
		if query.SQL == "" {
			t.Fatalf("queued section command %d SQL is blank", index)
		}
	}
	wantArguments := [][]any{
		{"version:synthetic", "transcript", "Transcript", "", []string{"Meeting", "Transcript"}, 0, "transcript", "Alpha speaks."},
		{"version:synthetic", "notes", "Notes", "root", []string{"Meeting", "Notes"}, 1, "notes", "A follow-up."},
	}
	for index, query := range batcher.queries {
		if !reflect.DeepEqual(query.Arguments, wantArguments[index]) {
			t.Fatalf(
				"queued section command %d arguments = %#v, want %#v",
				index,
				query.Arguments,
				wantArguments[index],
			)
		}
	}
}

type documentSectionBatcherRecorder struct {
	calls   int
	queries []*pgx.QueuedQuery
	results documentSectionBatchResultsRecorder
}

func (recorder *documentSectionBatcherRecorder) SendBatch(
	_ context.Context,
	batch *pgx.Batch,
) pgx.BatchResults {
	recorder.calls++
	recorder.queries = make([]*pgx.QueuedQuery, len(batch.QueuedQueries))
	for index, query := range batch.QueuedQueries {
		recorder.queries[index] = &pgx.QueuedQuery{
			SQL:       query.SQL,
			Arguments: append([]any(nil), query.Arguments...),
		}
	}
	return &recorder.results
}

type documentSectionBatchResultsRecorder struct {
	closeCalls int
}

func (*documentSectionBatchResultsRecorder) Exec() (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

func (*documentSectionBatchResultsRecorder) Query() (pgx.Rows, error) {
	return nil, nil
}

func (*documentSectionBatchResultsRecorder) QueryRow() pgx.Row {
	return nil
}

func (recorder *documentSectionBatchResultsRecorder) Close() error {
	recorder.closeCalls++
	return nil
}

func canonicalDocumentSection(
	t *testing.T,
	id string,
	title string,
	parentID string,
	path []string,
	order int,
	role string,
	content string,
) evidence.Section {
	t.Helper()
	section, err := evidence.NewSection(evidence.SectionInput{
		ID:       id,
		Title:    title,
		ParentID: parentID,
		Path:     path,
		Order:    order,
		Role:     role,
		Text:     content,
	})
	if err != nil {
		t.Fatalf("evidence.NewSection() error = %v", err)
	}
	return section
}
