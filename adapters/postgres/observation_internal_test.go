package postgres

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/JakeFAU/stacks/core/evidence"
	"github.com/JakeFAU/stacks/core/observation"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestInsertObservationEvidenceUsesOneCommand(t *testing.T) {
	writer := &observationEvidenceWriter{}
	links := []observation.EvidenceLink{
		{
			EvidenceID: evidence.EvidenceID("evidence-1"),
			Role:       observation.EvidenceSupporting,
		},
		{
			EvidenceID: evidence.EvidenceID("evidence-2"),
			Role:       observation.EvidenceContradicting,
		},
		{
			EvidenceID: evidence.EvidenceID("evidence-3"),
			Role:       observation.EvidenceSupporting,
		},
	}

	err := insertObservationEvidence(
		context.Background(),
		writer,
		observation.ObservationID("observation-1"),
		links,
	)
	if err != nil {
		t.Fatalf("insertObservationEvidence() error = %v", err)
	}
	if writer.commandCount != 1 {
		t.Fatalf("command count = %d, want 1", writer.commandCount)
	}
	wantArguments := []any{
		observation.ObservationID("observation-1"),
		[]string{"evidence-1", "evidence-2", "evidence-3"},
		[]string{"supporting", "contradicting", "supporting"},
	}
	if !reflect.DeepEqual(writer.arguments, wantArguments) {
		t.Fatalf("arguments = %#v, want %#v", writer.arguments, wantArguments)
	}
}

type observationEvidenceWriter struct {
	commandCount int
	arguments    []any
}

func (writer *observationEvidenceWriter) Exec(
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
