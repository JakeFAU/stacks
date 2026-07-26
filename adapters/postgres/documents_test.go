package postgres_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	postgres "github.com/JakeFAU/stacks/adapters/postgres"
	"github.com/JakeFAU/stacks/core/evidence"
)

func TestDocumentVersionRoundTripsCanonicalSourceAndSectionState(t *testing.T) {
	fixture := newDocumentRepositoryFixture(t)
	document := canonicalDocument(t, "source-opaque-a", "revision-a", documentRecordedAt)

	put, err := fixture.database.PutDocumentVersion(fixture.ctx, document)
	if err != nil {
		t.Fatalf("PutDocumentVersion() error = %v", err)
	}
	if !put.ContentCreated {
		t.Fatal("PutDocumentVersion() ContentCreated = false, want true")
	}
	if put.Ref.SourceDocumentID == "" || put.Ref.VersionID == "" {
		t.Fatalf("PutDocumentVersion() refs = %#v, want opaque nonblank text IDs", put.Ref)
	}
	if put.Ref.RecordedAt != documentRecordedAt {
		t.Fatalf("PutDocumentVersion() recorded at = %v, want %v", put.Ref.RecordedAt, documentRecordedAt)
	}

	got, err := fixture.database.LoadDocumentVersion(fixture.ctx, put.Ref.VersionID)
	if err != nil {
		t.Fatalf("LoadDocumentVersion() error = %v", err)
	}
	assertDocumentRecord(t, got, put.Ref, document, []string{"revision-a"})
}

func TestDocumentVersionRevisionChurnReusesStableContentVersion(t *testing.T) {
	fixture := newDocumentRepositoryFixture(t)
	first := canonicalDocument(t, "source-opaque-a", "revision-a", documentRecordedAt)
	second := canonicalDocument(t, "source-opaque-a", "revision-b", documentRecordedAt.Add(time.Hour))

	firstPut, err := fixture.database.PutDocumentVersion(fixture.ctx, first)
	if err != nil {
		t.Fatalf("first PutDocumentVersion() error = %v", err)
	}
	secondPut, err := fixture.database.PutDocumentVersion(fixture.ctx, second)
	if err != nil {
		t.Fatalf("second PutDocumentVersion() error = %v", err)
	}
	if !firstPut.ContentCreated || secondPut.ContentCreated {
		t.Fatalf("content created results = (%v, %v), want (true, false)", firstPut.ContentCreated, secondPut.ContentCreated)
	}
	if firstPut.Ref != secondPut.Ref {
		t.Fatalf("revision churn refs = (%#v, %#v), want exact stable ref", firstPut.Ref, secondPut.Ref)
	}

	var versions int
	if err := fixture.admin.QueryRow(
		fixture.ctx,
		`SELECT count(*) FROM stacks_core.document_versions WHERE source_document_id = $1`,
		firstPut.Ref.SourceDocumentID,
	).Scan(&versions); err != nil {
		t.Fatalf("count stable content versions: %v", err)
	}
	if versions != 1 {
		t.Fatalf("stable content version count = %d, want 1", versions)
	}
}

func TestProviderRevisionChurnAppendsProvenanceWithoutRewritingContent(t *testing.T) {
	fixture := newDocumentRepositoryFixture(t)
	first := canonicalDocument(t, "source-opaque-a", "revision-a", documentRecordedAt)
	firstPut, err := fixture.database.PutDocumentVersion(fixture.ctx, first)
	if err != nil {
		t.Fatalf("first PutDocumentVersion() error = %v", err)
	}
	contentXID := rowXID(t, fixture.ctx, fixture.admin, "document_versions", firstPut.Ref.VersionID)

	second := canonicalDocument(t, "source-opaque-a", "revision-b", documentRecordedAt.Add(time.Hour))
	if _, err := fixture.database.PutDocumentVersion(fixture.ctx, second); err != nil {
		t.Fatalf("second PutDocumentVersion() error = %v", err)
	}
	if got := rowXID(t, fixture.ctx, fixture.admin, "document_versions", firstPut.Ref.VersionID); got != contentXID {
		t.Fatalf("provider revision churn rewrote content xmin from %s to %s", contentXID, got)
	}

	record, err := fixture.database.LoadDocumentVersion(fixture.ctx, firstPut.Ref.VersionID)
	if err != nil {
		t.Fatalf("LoadDocumentVersion() error = %v", err)
	}
	if len(record.Revisions) != 2 {
		t.Fatalf("revision observation count = %d, want 2", len(record.Revisions))
	}
	gotRevisions := []string{record.Revisions[0].ProviderRevision(), record.Revisions[1].ProviderRevision()}
	if !reflect.DeepEqual(gotRevisions, []string{"revision-a", "revision-b"}) {
		t.Fatalf("provider revisions = %v, want append order [revision-a revision-b]", gotRevisions)
	}
	for _, revision := range record.Revisions {
		if revision.FirstRecordedAt() != firstPut.Ref.RecordedAt {
			t.Errorf("revision first recorded at = %v, want stable content time %v", revision.FirstRecordedAt(), firstPut.Ref.RecordedAt)
		}
	}
}

func TestDocumentVersionExactRetryIsReadOnly(t *testing.T) {
	fixture := newDocumentRepositoryFixture(t)
	document := canonicalDocument(t, "source-opaque-a", "revision-a", documentRecordedAt)
	first, err := fixture.database.PutDocumentVersion(fixture.ctx, document)
	if err != nil {
		t.Fatalf("first PutDocumentVersion() error = %v", err)
	}
	before := []string{
		rowXID(t, fixture.ctx, fixture.admin, "source_documents", first.Ref.SourceDocumentID),
		rowXID(t, fixture.ctx, fixture.admin, "document_versions", first.Ref.VersionID),
		rowXID(t, fixture.ctx, fixture.admin, "source_revision_observations", documentRevisionID(t, document, first.Ref.RecordedAt)),
	}

	second, err := fixture.database.PutDocumentVersion(fixture.ctx, document)
	if err != nil {
		t.Fatalf("repeated PutDocumentVersion() error = %v", err)
	}
	if second.ContentCreated || second.Ref != first.Ref {
		t.Fatalf("repeated PutDocumentVersion() = %#v, want original ref and no content creation", second)
	}
	after := []string{
		rowXID(t, fixture.ctx, fixture.admin, "source_documents", first.Ref.SourceDocumentID),
		rowXID(t, fixture.ctx, fixture.admin, "document_versions", first.Ref.VersionID),
		rowXID(t, fixture.ctx, fixture.admin, "source_revision_observations", documentRevisionID(t, document, first.Ref.RecordedAt)),
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("exact retry changed row identities from %v to %v", before, after)
	}
}

func TestDocumentVersionStableIdentityConflictIsBounded(t *testing.T) {
	fixture := newDocumentRepositoryFixture(t)
	document := canonicalDocument(t, "source-opaque-a", "revision-a", documentRecordedAt)
	put, err := fixture.database.PutDocumentVersion(fixture.ctx, document)
	if err != nil {
		t.Fatalf("PutDocumentVersion() error = %v", err)
	}
	if _, err := fixture.admin.Exec(
		fixture.ctx,
		`UPDATE stacks_core.document_versions SET title = 'different synthetic payload' WHERE id = $1`,
		put.Ref.VersionID,
	); err != nil {
		t.Fatalf("corrupt stable identity fixture: %v", err)
	}

	conflictContext, cancel := context.WithTimeout(fixture.ctx, 2*time.Second)
	defer cancel()
	started := time.Now()
	_, err = fixture.database.PutDocumentVersion(conflictContext, document)
	if !errors.Is(err, postgres.ErrConflict) {
		t.Fatalf("PutDocumentVersion() error = %v, want ErrConflict", err)
	}
	if elapsed := time.Since(started); elapsed >= 2*time.Second {
		t.Fatalf("stable identity conflict took %v, want bounded before context deadline", elapsed)
	}
}

func TestDocumentRepositoryPreservesCancellation(t *testing.T) {
	fixture := newDocumentRepositoryFixture(t)
	document := canonicalDocument(t, "source-opaque-a", "revision-a", documentRecordedAt)
	put, err := fixture.database.PutDocumentVersion(fixture.ctx, document)
	if err != nil {
		t.Fatalf("PutDocumentVersion() error = %v", err)
	}

	canceled, cancel := context.WithCancel(fixture.ctx)
	cancel()
	if _, err := fixture.database.LoadDocumentVersion(canceled, put.Ref.VersionID); !errors.Is(err, context.Canceled) {
		t.Fatalf("LoadDocumentVersion() error = %v, want context.Canceled", err)
	}
	if _, err := fixture.database.PutDocumentVersion(canceled, document); !errors.Is(err, context.Canceled) {
		t.Fatalf("PutDocumentVersion() error = %v, want context.Canceled", err)
	}
}

func assertDocumentRecord(
	t testing.TB,
	got postgres.DocumentVersionRecord,
	wantRef postgres.DocumentVersionRef,
	want evidence.DocumentVersion,
	wantRevisions []string,
) {
	t.Helper()
	if got.Ref != wantRef {
		t.Fatalf("loaded ref = %#v, want %#v", got.Ref, wantRef)
	}
	if got.Version.Provider() != want.Provider() ||
		got.Version.ProviderDocumentID() != want.ProviderDocumentID() ||
		got.Version.Title() != want.Title() ||
		got.Version.Locator() != want.Locator() ||
		got.Version.ProviderVersion() != want.ProviderVersion() ||
		got.Version.ProviderRevision() != "" ||
		got.Version.ModifiedAt() != want.ModifiedAt() ||
		got.Version.RecordedAt() != wantRef.RecordedAt ||
		got.Version.DigestVersion() != want.DigestVersion() ||
		got.Version.Digest() != want.Digest() {
		t.Fatalf("loaded canonical document metadata does not match stored content")
	}
	if !equalOptionalTime(got.Version.SourceTime(), want.SourceTime()) {
		t.Fatalf("loaded source time = %v, want %v", got.Version.SourceTime(), want.SourceTime())
	}
	gotSections, wantSections := got.Version.Sections(), want.Sections()
	if len(gotSections) != len(wantSections) {
		t.Fatalf("loaded section count = %d, want %d", len(gotSections), len(wantSections))
	}
	for index := range wantSections {
		if !equalSection(gotSections[index], wantSections[index]) {
			t.Errorf("loaded section %d does not match canonical input", index)
		}
	}
	gotRevisions := make([]string, len(got.Revisions))
	for index, revision := range got.Revisions {
		gotRevisions[index] = revision.ProviderRevision()
		if revision.Provider() != want.Provider() ||
			revision.ProviderDocumentID() != want.ProviderDocumentID() ||
			revision.DocumentDigestVersion() != want.DigestVersion() ||
			revision.DocumentDigest() != want.Digest() ||
			revision.ProviderVersion() != want.ProviderVersion() ||
			revision.FirstRecordedAt() != wantRef.RecordedAt {
			t.Errorf("loaded revision %d does not match canonical source provenance", index)
		}
	}
	if !reflect.DeepEqual(gotRevisions, wantRevisions) {
		t.Errorf("loaded provider revisions = %v, want %v", gotRevisions, wantRevisions)
	}
}

func equalOptionalTime(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func equalSection(left, right evidence.Section) bool {
	return left.ID() == right.ID() &&
		left.Title() == right.Title() &&
		left.ParentID() == right.ParentID() &&
		reflect.DeepEqual(left.Path(), right.Path()) &&
		left.Order() == right.Order() &&
		left.Role() == right.Role() &&
		left.Text() == right.Text()
}
