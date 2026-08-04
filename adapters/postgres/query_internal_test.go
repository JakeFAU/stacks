package postgres

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/JakeFAU/stacks/core/evidence"
	"github.com/JakeFAU/stacks/core/identity"
	"github.com/JakeFAU/stacks/core/observation"
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

func TestLoadDocumentVersionRecordCachedLoadsVersionOnce(t *testing.T) {
	want := DocumentVersionRecord{
		Ref: DocumentVersionRef{
			SourceDocumentID: "source-1",
			VersionID:        "version-1",
		},
	}
	loadCount := 0
	load := func(
		_ context.Context,
		_ documentReader,
		versionID string,
	) (DocumentVersionRecord, error) {
		if versionID != want.Ref.VersionID {
			return DocumentVersionRecord{}, fmt.Errorf(
				"version ID = %q, want %q",
				versionID,
				want.Ref.VersionID,
			)
		}
		loadCount++
		return want, nil
	}
	cache := make(map[string]DocumentVersionRecord)

	first, err := loadDocumentVersionRecordCached(
		context.Background(),
		nil,
		want.Ref.VersionID,
		cache,
		load,
	)
	if err != nil {
		t.Fatalf("first loadDocumentVersionRecordCached() error = %v", err)
	}
	second, err := loadDocumentVersionRecordCached(
		context.Background(),
		nil,
		want.Ref.VersionID,
		cache,
		load,
	)
	if err != nil {
		t.Fatalf("second loadDocumentVersionRecordCached() error = %v", err)
	}
	if first.Ref != want.Ref || second.Ref != want.Ref {
		t.Fatalf("records = (%#v, %#v), want %#v", first.Ref, second.Ref, want.Ref)
	}
	if loadCount != 1 {
		t.Fatalf("document version load count = %d, want 1", loadCount)
	}
}

func TestLoadObservationEvidenceRecordReusesCanonicalProvenance(t *testing.T) {
	recordedAt := time.Date(2026, time.August, 4, 12, 0, 0, 0, time.UTC)
	section, err := evidence.NewSection(evidence.SectionInput{
		ID:    "section-1",
		Title: "Synthetic section",
		Path:  []string{"Synthetic document", "Synthetic section"},
		Order: 1,
		Role:  "body",
		Text:  "Synthetic citation text.",
	})
	if err != nil {
		t.Fatalf("evidence.NewSection() error = %v", err)
	}
	document, err := evidence.NewDocumentVersion(evidence.DocumentVersionInput{
		Provider:           "synthetic",
		ProviderDocumentID: "document-1",
		Title:              "Synthetic document",
		Locator:            "synthetic://document-1",
		ProviderVersion:    "version-1",
		ModifiedAt:         recordedAt,
		RecordedAt:         recordedAt,
		Sections:           []evidence.Section{section},
	})
	if err != nil {
		t.Fatalf("evidence.NewDocumentVersion() error = %v", err)
	}
	span, err := evidence.NewEvidenceSpan(evidence.EvidenceSpanInput{
		Document:    document,
		SectionID:   section.ID(),
		StartOffset: 0,
		EndOffset:   len(section.Text()),
		Quote:       section.Text(),
		RecordedAt:  recordedAt,
	})
	if err != nil {
		t.Fatalf("evidence.NewEvidenceSpan() error = %v", err)
	}

	sourceID := deriveOpaqueID(
		sourceDocumentIDVersion,
		[]byte(document.Provider()),
		[]byte(document.ProviderDocumentID()),
	)
	documentDigest := document.Digest()
	versionID := deriveOpaqueID(
		documentVersionIDVersion,
		[]byte(sourceID),
		[]byte(document.DigestVersion()),
		documentDigest[:],
	)
	spanDigest := span.Digest()
	reader := &observationEvidenceReader{
		rowValues: [][]any{
			{
				versionID,
				section.ID(),
				span.DigestVersion(),
				spanDigest[:],
				span.StartOffset(),
				span.EndOffset(),
				span.Text(),
				span.RecordedAt(),
			},
			{
				sourceID,
				document.Provider(),
				document.ProviderDocumentID(),
				document.DigestVersion(),
				documentDigest[:],
				document.Title(),
				document.Locator(),
				document.ProviderVersion(),
				document.ModifiedAt(),
				nil,
				document.RecordedAt(),
			},
		},
		queryValues: [][][]any{
			{{
				section.ID(),
				section.Title(),
				section.ParentID(),
				section.Path(),
				section.Order(),
				section.Role(),
				section.Text(),
			}},
			nil,
		},
	}

	record, err := loadObservationEvidenceRecord(
		context.Background(),
		reader,
		observation.EvidenceLink{
			EvidenceID: span.ID(),
			Role:       observation.EvidenceSupporting,
		},
		make(map[string]DocumentVersionRecord),
	)
	if err != nil {
		t.Fatalf("loadObservationEvidenceRecord() error = %v", err)
	}
	if reader.queryCount != 4 {
		t.Fatalf("query count = %d, want 4", reader.queryCount)
	}
	if record.Span != span ||
		record.Role != observation.EvidenceSupporting ||
		record.SourceDocumentID != sourceID ||
		record.DocumentVersionID != versionID ||
		record.SectionID != section.ID() ||
		record.SectionTitle != section.Title() ||
		record.SectionOrder != section.Order() ||
		record.SectionRole != section.Role() {
		t.Fatalf("record = %#v, want canonical span and provenance", record)
	}
	if fmt.Sprint(record.SectionPath) != fmt.Sprint(section.Path()) {
		t.Fatalf("section path = %#v, want %#v", record.SectionPath, section.Path())
	}
}

type observationEvidenceReader struct {
	rowValues   [][]any
	queryValues [][][]any
	queryCount  int
	rowIndex    int
	queryIndex  int
}

func (reader *observationEvidenceReader) Query(
	context.Context,
	string,
	...any,
) (pgx.Rows, error) {
	reader.queryCount++
	if reader.queryIndex >= len(reader.queryValues) {
		return nil, fmt.Errorf("unexpected Query call %d", reader.queryCount)
	}
	values := reader.queryValues[reader.queryIndex]
	reader.queryIndex++
	return &temporalQueryFakeRows{values: values}, nil
}

func (reader *observationEvidenceReader) QueryRow(
	context.Context,
	string,
	...any,
) pgx.Row {
	reader.queryCount++
	if reader.rowIndex >= len(reader.rowValues) {
		return observationEvidenceRow{err: fmt.Errorf(
			"unexpected QueryRow call %d",
			reader.queryCount,
		)}
	}
	values := reader.rowValues[reader.rowIndex]
	reader.rowIndex++
	return observationEvidenceRow{values: values}
}

type observationEvidenceRow struct {
	values []any
	err    error
}

func (row observationEvidenceRow) Scan(destinations ...any) error {
	if row.err != nil {
		return row.err
	}
	if len(destinations) != len(row.values) {
		return fmt.Errorf(
			"scan destination count = %d, want %d",
			len(destinations),
			len(row.values),
		)
	}
	for index, destination := range destinations {
		if err := assignTemporalQueryValue(destination, row.values[index]); err != nil {
			return fmt.Errorf("scan column %d: %w", index, err)
		}
	}
	return nil
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
