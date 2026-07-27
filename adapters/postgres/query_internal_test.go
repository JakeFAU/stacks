package postgres

import (
	"context"
	"fmt"
	"testing"

	"github.com/JakeFAU/stacks/core/identity"
	"github.com/jackc/pgx/v5"
)

func TestLoadCurrentAcceptedIdentityAuthoritiesUsesOneQuery(t *testing.T) {
	reader := &authorityReader{
		subjectAccepted: true,
		objectAccepted:  false,
	}

	subjectAccepted, objectAccepted, err := loadCurrentAcceptedIdentityAuthorities(
		context.Background(),
		reader,
		identity.EntityID("entity-subject"),
		identity.EntityID("entity-object"),
	)
	if err != nil {
		t.Fatalf("loadCurrentAcceptedIdentityAuthorities() error = %v", err)
	}
	if !subjectAccepted || objectAccepted {
		t.Fatalf(
			"authority = (%v, %v), want (true, false)",
			subjectAccepted,
			objectAccepted,
		)
	}
	if reader.queryCount != 1 {
		t.Fatalf("query count = %d, want 1", reader.queryCount)
	}
	if len(reader.arguments) != 2 ||
		reader.arguments[0] != identity.EntityID("entity-subject") ||
		reader.arguments[1] != identity.EntityID("entity-object") {
		t.Fatalf("query arguments = %#v, want ordered subject and object IDs", reader.arguments)
	}
}

type authorityReader struct {
	queryCount      int
	arguments       []any
	subjectAccepted bool
	objectAccepted  bool
}

func (*authorityReader) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, fmt.Errorf("unexpected Query call")
}

func (reader *authorityReader) QueryRow(
	_ context.Context,
	_ string,
	arguments ...any,
) pgx.Row {
	reader.queryCount++
	reader.arguments = append([]any(nil), arguments...)
	return authorityRow{
		subjectAccepted: reader.subjectAccepted,
		objectAccepted:  reader.objectAccepted,
	}
}

type authorityRow struct {
	subjectAccepted bool
	objectAccepted  bool
}

func (row authorityRow) Scan(destinations ...any) error {
	if len(destinations) != 2 {
		return fmt.Errorf("scan destination count = %d, want 2", len(destinations))
	}
	subjectAccepted, ok := destinations[0].(*bool)
	if !ok {
		return fmt.Errorf("subject destination is %T, want *bool", destinations[0])
	}
	objectAccepted, ok := destinations[1].(*bool)
	if !ok {
		return fmt.Errorf("object destination is %T, want *bool", destinations[1])
	}
	*subjectAccepted = row.subjectAccepted
	*objectAccepted = row.objectAccepted
	return nil
}
