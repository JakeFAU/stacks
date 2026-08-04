package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/JakeFAU/stacks/core/evidence"
	"github.com/JakeFAU/stacks/core/timepoint"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	sourceDocumentIDVersion  = "stacks.postgres.source-document.v1"
	documentVersionIDVersion = "stacks.postgres.document-version.v1"
	uniqueViolationCode      = "23505"
)

// DocumentVersionRef identifies one stored immutable content version and its
// owning logical source document.
type DocumentVersionRef struct {
	SourceDocumentID string
	VersionID        string
	RecordedAt       time.Time
}

// DocumentVersionRecord contains canonical content and every append-only
// source revision observed for it.
type DocumentVersionRecord struct {
	Ref       DocumentVersionRef
	Version   evidence.DocumentVersion
	Revisions []evidence.SourceRevisionObservation
}

type documentVersionRecordLoader func(
	context.Context,
	documentReader,
	string,
) (DocumentVersionRecord, error)

// PutDocumentVersionResult reports whether this call created the immutable
// content row. Revision provenance may still be appended when content existed.
type PutDocumentVersionResult struct {
	Ref            DocumentVersionRef
	ContentCreated bool
}

type documentReader interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

type documentSectionBatcher interface {
	SendBatch(context.Context, *pgx.Batch) pgx.BatchResults
}

// PutDocumentVersion stores canonical immutable content. Callers that observe
// provider revision metadata must append a SourceRevisionObservation
// explicitly in the same transaction.
func (database *Database) PutDocumentVersion(
	ctx context.Context,
	version evidence.DocumentVersion,
) (PutDocumentVersionResult, error) {
	if err := contextRequired(ctx, "put document version"); err != nil {
		return PutDocumentVersionResult{}, err
	}
	if database == nil || database.pool == nil {
		return PutDocumentVersionResult{}, fmt.Errorf("put document version: database is closed")
	}

	var result PutDocumentVersionResult
	err := database.InTransaction(ctx, func(transaction *Transaction) error {
		var err error
		result, err = transaction.PutDocumentVersion(ctx, version)
		return err
	})
	if err != nil {
		return PutDocumentVersionResult{}, wrapDocumentError(ctx, "put document version", err)
	}
	return result, nil
}

// PutDocumentVersion stores only the immutable content portion. The owning
// transaction must append the corresponding source revision observation.
func (transaction *Transaction) PutDocumentVersion(
	ctx context.Context,
	version evidence.DocumentVersion,
) (PutDocumentVersionResult, error) {
	if err := contextRequired(ctx, "put document version"); err != nil {
		return PutDocumentVersionResult{}, err
	}
	if transaction == nil || transaction.transaction == nil {
		return PutDocumentVersionResult{}, fmt.Errorf("put document version: transaction is closed")
	}
	if err := validateDocumentVersion(version); err != nil {
		return PutDocumentVersionResult{}, fmt.Errorf("put document version: %w", err)
	}

	sourceID := deriveOpaqueID(
		sourceDocumentIDVersion,
		[]byte(version.Provider()),
		[]byte(version.ProviderDocumentID()),
	)
	contentDigest := version.Digest()
	versionID := deriveOpaqueID(
		documentVersionIDVersion,
		[]byte(sourceID),
		[]byte(version.DigestVersion()),
		contentDigest[:],
	)

	if _, err := transaction.transaction.Exec(ctx, `
		INSERT INTO stacks_core.source_documents (
			id,
			provider,
			provider_document_id,
			created_at
		)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (provider, provider_document_id) DO NOTHING`,
		sourceID,
		version.Provider(),
		version.ProviderDocumentID(),
		version.RecordedAt(),
	); err != nil {
		return PutDocumentVersionResult{}, wrapDocumentError(ctx, "insert source document", conflictError(err))
	}

	var storedSourceID string
	if err := transaction.transaction.QueryRow(ctx, `
		SELECT id
		FROM stacks_core.source_documents
		WHERE provider = $1
		  AND provider_document_id = $2`,
		version.Provider(),
		version.ProviderDocumentID(),
	).Scan(&storedSourceID); err != nil {
		return PutDocumentVersionResult{}, wrapDocumentError(ctx, "load source document identity", err)
	}
	if storedSourceID != sourceID {
		return PutDocumentVersionResult{}, fmt.Errorf(
			"put document version: source document identity: %w",
			ErrConflict,
		)
	}

	var recordedAt time.Time
	insertErr := transaction.transaction.QueryRow(ctx, `
		INSERT INTO stacks_core.document_versions (
			id,
			source_document_id,
			digest_version,
			content_digest,
			title,
			locator,
			provider_version,
			modified_at,
			source_time,
			recorded_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (source_document_id, digest_version, content_digest) DO NOTHING
		RETURNING recorded_at`,
		versionID,
		sourceID,
		version.DigestVersion(),
		contentDigest[:],
		version.Title(),
		version.Locator(),
		version.ProviderVersion(),
		version.ModifiedAt(),
		version.SourceTime(),
		version.RecordedAt(),
	).Scan(&recordedAt)
	switch {
	case insertErr == nil:
		recordedAt, err := canonicalStoredTime(recordedAt)
		if err != nil {
			return PutDocumentVersionResult{}, fmt.Errorf(
				"insert document version recorded time: %w",
				err,
			)
		}
		if err := insertDocumentSections(
			ctx,
			transaction.transaction,
			versionID,
			version.Sections(),
		); err != nil {
			return PutDocumentVersionResult{}, wrapDocumentError(
				ctx,
				"insert document section",
				conflictError(err),
			)
		}
		return PutDocumentVersionResult{
			Ref: DocumentVersionRef{
				SourceDocumentID: sourceID,
				VersionID:        versionID,
				RecordedAt:       recordedAt,
			},
			ContentCreated: true,
		}, nil
	case errors.Is(insertErr, pgx.ErrNoRows):
		stored, err := loadDocumentVersionRecord(ctx, transaction.transaction, versionID)
		if err != nil {
			return PutDocumentVersionResult{}, wrapDocumentError(
				ctx,
				"load existing document version",
				err,
			)
		}
		if stored.Ref.SourceDocumentID != sourceID ||
			!sameStoredDocumentPayload(stored.Version, version) {
			return PutDocumentVersionResult{}, fmt.Errorf(
				"put document version: stable content identity: %w",
				ErrConflict,
			)
		}
		return PutDocumentVersionResult{Ref: stored.Ref}, nil
	default:
		return PutDocumentVersionResult{}, wrapDocumentError(
			ctx,
			"insert document version",
			conflictError(insertErr),
		)
	}
}

func insertDocumentSections(
	ctx context.Context,
	batcher documentSectionBatcher,
	versionID string,
	sections []evidence.Section,
) error {
	const insertDocumentSectionSQL = `
		INSERT INTO stacks_core.document_sections (
			document_version_id,
			section_id,
			title,
			parent_id,
			path,
			section_order,
			role,
			content
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`

	batch := &pgx.Batch{}
	for _, section := range sections {
		batch.Queue(
			insertDocumentSectionSQL,
			versionID,
			section.ID(),
			section.Title(),
			section.ParentID(),
			section.Path(),
			section.Order(),
			section.Role(),
			section.Text(),
		)
	}
	return batcher.SendBatch(ctx, batch).Close()
}

// PutSourceRevisionObservation appends one immutable provider revision for a
// stored content version. It returns false for an exact retry.
func (transaction *Transaction) PutSourceRevisionObservation(
	ctx context.Context,
	revision evidence.SourceRevisionObservation,
) (bool, error) {
	if err := contextRequired(ctx, "put source revision observation"); err != nil {
		return false, err
	}
	if transaction == nil || transaction.transaction == nil {
		return false, fmt.Errorf("put source revision observation: transaction is closed")
	}
	if err := validateSourceRevisionObservation(revision); err != nil {
		return false, fmt.Errorf("put source revision observation: %w", err)
	}

	sourceID := deriveOpaqueID(
		sourceDocumentIDVersion,
		[]byte(revision.Provider()),
		[]byte(revision.ProviderDocumentID()),
	)
	documentDigest := revision.DocumentDigest()
	versionID := deriveOpaqueID(
		documentVersionIDVersion,
		[]byte(sourceID),
		[]byte(revision.DocumentDigestVersion()),
		documentDigest[:],
	)
	var (
		storedProviderVersion string
		storedRecordedAt      time.Time
	)
	if err := transaction.transaction.QueryRow(ctx, `
		SELECT provider_version, recorded_at
		FROM stacks_core.document_versions
		WHERE id = $1
		  AND source_document_id = $2
		  AND digest_version = $3
		  AND content_digest = $4`,
		versionID,
		sourceID,
		revision.DocumentDigestVersion(),
		documentDigest[:],
	).Scan(&storedProviderVersion, &storedRecordedAt); err != nil {
		return false, wrapDocumentError(ctx, "load source revision content version", err)
	}
	storedRecordedAt, err := canonicalStoredTime(storedRecordedAt)
	if err != nil {
		return false, fmt.Errorf(
			"put source revision observation: stored content recorded time: %w",
			err,
		)
	}
	if storedProviderVersion != revision.ProviderVersion() {
		return false, fmt.Errorf(
			"put source revision observation: provider version does not match content: %w",
			ErrConflict,
		)
	}
	if storedRecordedAt != revision.FirstRecordedAt() {
		return false, fmt.Errorf(
			"put source revision observation: first recorded time does not match content: %w",
			ErrConflict,
		)
	}

	var insertedID string
	revisionDigest := revision.Digest()
	insertErr := transaction.transaction.QueryRow(ctx, `
		INSERT INTO stacks_core.source_revision_observations (
			id,
			source_document_id,
			document_version_id,
			digest_version,
			digest,
			provider_version,
			provider_revision,
			first_recorded_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (
			source_document_id,
			provider_version,
			provider_revision,
			document_version_id
		) DO NOTHING
		RETURNING id`,
		revision.ID(),
		sourceID,
		versionID,
		revision.DigestVersion(),
		revisionDigest[:],
		revision.ProviderVersion(),
		revision.ProviderRevision(),
		revision.FirstRecordedAt(),
	).Scan(&insertedID)
	switch {
	case insertErr == nil:
		if insertedID != revision.ID() {
			return false, fmt.Errorf(
				"put source revision observation: inserted identity: %w",
				ErrConflict,
			)
		}
		return true, nil
	case errors.Is(insertErr, pgx.ErrNoRows):
		stored, err := loadSourceRevisionObservation(
			ctx,
			transaction.transaction,
			sourceID,
			versionID,
			revision.ProviderVersion(),
			revision.ProviderRevision(),
		)
		if err != nil {
			return false, wrapDocumentError(ctx, "load existing source revision observation", err)
		}
		if !sameSourceRevisionObservation(stored, revision) {
			return false, fmt.Errorf(
				"put source revision observation: immutable identity: %w",
				ErrConflict,
			)
		}
		return false, nil
	default:
		return false, wrapDocumentError(
			ctx,
			"insert source revision observation",
			conflictError(insertErr),
		)
	}
}

// LoadDocumentVersion loads and validates one canonical immutable content
// version and all of its source revision observations.
func (database *Database) LoadDocumentVersion(
	ctx context.Context,
	versionID string,
) (DocumentVersionRecord, error) {
	if err := contextRequired(ctx, "load document version"); err != nil {
		return DocumentVersionRecord{}, err
	}
	if database == nil || database.pool == nil {
		return DocumentVersionRecord{}, fmt.Errorf("load document version: database is closed")
	}
	versionID = strings.TrimSpace(versionID)
	if versionID == "" {
		return DocumentVersionRecord{}, fmt.Errorf("load document version: version ID is required")
	}
	record, err := loadDocumentVersionRecord(ctx, database.pool, versionID)
	if err != nil {
		return DocumentVersionRecord{}, wrapDocumentError(ctx, "load document version", err)
	}
	return record, nil
}

// PutEvidenceSpan stores one exact immutable source range. It returns false for
// an exact retry.
func (transaction *Transaction) PutEvidenceSpan(
	ctx context.Context,
	span evidence.EvidenceSpan,
) (bool, error) {
	if err := contextRequired(ctx, "put evidence span"); err != nil {
		return false, err
	}
	if transaction == nil || transaction.transaction == nil {
		return false, fmt.Errorf("put evidence span: transaction is closed")
	}
	if err := validateEvidenceSpan(span); err != nil {
		return false, fmt.Errorf("put evidence span: %w", err)
	}

	sourceID := deriveOpaqueID(
		sourceDocumentIDVersion,
		[]byte(span.Provider()),
		[]byte(span.ProviderDocumentID()),
	)
	documentDigest := span.DocumentDigest()
	versionID := deriveOpaqueID(
		documentVersionIDVersion,
		[]byte(sourceID),
		[]byte(evidence.DocumentDigestVersion),
		documentDigest[:],
	)
	digest := span.Digest()
	var insertedID string
	insertErr := transaction.transaction.QueryRow(ctx, `
		INSERT INTO stacks_core.evidence_spans (
			id,
			document_version_id,
			section_id,
			digest_version,
			digest,
			start_offset,
			end_offset,
			quote,
			recorded_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (id) DO NOTHING
		RETURNING id`,
		string(span.ID()),
		versionID,
		span.SectionID(),
		span.DigestVersion(),
		digest[:],
		span.StartOffset(),
		span.EndOffset(),
		span.Text(),
		span.RecordedAt(),
	).Scan(&insertedID)
	switch {
	case insertErr == nil:
		if insertedID != string(span.ID()) {
			return false, fmt.Errorf("put evidence span: inserted identity: %w", ErrConflict)
		}
		return true, nil
	case errors.Is(insertErr, pgx.ErrNoRows):
		stored, err := loadEvidenceSpan(ctx, transaction.transaction, span.ID())
		if err != nil {
			return false, wrapDocumentError(ctx, "load existing evidence span", err)
		}
		if !sameEvidenceSpan(stored, span) {
			return false, fmt.Errorf("put evidence span: immutable identity: %w", ErrConflict)
		}
		return false, nil
	default:
		return false, wrapDocumentError(ctx, "insert evidence span", conflictError(insertErr))
	}
}

// LoadEvidenceSpan loads and verifies one immutable exact source range.
func (database *Database) LoadEvidenceSpan(
	ctx context.Context,
	id evidence.EvidenceID,
) (evidence.EvidenceSpan, error) {
	if err := contextRequired(ctx, "load evidence span"); err != nil {
		return evidence.EvidenceSpan{}, err
	}
	if database == nil || database.pool == nil {
		return evidence.EvidenceSpan{}, fmt.Errorf("load evidence span: database is closed")
	}
	if strings.TrimSpace(string(id)) == "" {
		return evidence.EvidenceSpan{}, fmt.Errorf("load evidence span: evidence ID is required")
	}
	span, err := loadEvidenceSpan(ctx, database.pool, id)
	if err != nil {
		return evidence.EvidenceSpan{}, wrapDocumentError(ctx, "load evidence span", err)
	}
	return span, nil
}

// SetCurrentDocumentVersion moves one source-owned current pointer to one of
// that same source's immutable versions.
func (transaction *Transaction) SetCurrentDocumentVersion(
	ctx context.Context,
	sourceDocumentID string,
	versionID string,
) error {
	if err := contextRequired(ctx, "set current document version"); err != nil {
		return err
	}
	if transaction == nil || transaction.transaction == nil {
		return fmt.Errorf("set current document version: transaction is closed")
	}
	sourceDocumentID, versionID = strings.TrimSpace(sourceDocumentID), strings.TrimSpace(versionID)
	if sourceDocumentID == "" || versionID == "" {
		return fmt.Errorf("set current document version: source and version IDs are required")
	}
	tag, err := transaction.transaction.Exec(ctx, `
		UPDATE stacks_core.source_documents
		SET current_version_id = $2
		WHERE id = $1`,
		sourceDocumentID,
		versionID,
	)
	if err != nil {
		return wrapDocumentError(ctx, "set current document version", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("set current document version: source document: %w", pgx.ErrNoRows)
	}
	return nil
}

func loadDocumentVersionRecord(
	ctx context.Context,
	reader documentReader,
	versionID string,
) (DocumentVersionRecord, error) {
	var (
		sourceID, provider, providerDocumentID         string
		digestVersion, title, locator, providerVersion string
		contentDigest                                  []byte
		modifiedAt, recordedAt                         time.Time
		sourceTime                                     *time.Time
	)
	if err := reader.QueryRow(ctx, `
		SELECT
			source.id,
			source.provider,
			source.provider_document_id,
			version.digest_version,
			version.content_digest,
			version.title,
			version.locator,
			version.provider_version,
			version.modified_at,
			version.source_time,
			version.recorded_at
		FROM stacks_core.document_versions AS version
		JOIN stacks_core.source_documents AS source
		  ON source.id = version.source_document_id
		WHERE version.id = $1`,
		versionID,
	).Scan(
		&sourceID,
		&provider,
		&providerDocumentID,
		&digestVersion,
		&contentDigest,
		&title,
		&locator,
		&providerVersion,
		&modifiedAt,
		&sourceTime,
		&recordedAt,
	); err != nil {
		return DocumentVersionRecord{}, err
	}
	modifiedAt, err := canonicalStoredTime(modifiedAt)
	if err != nil {
		return DocumentVersionRecord{}, fmt.Errorf("stored modified time: %w", err)
	}
	recordedAt, err = canonicalStoredTime(recordedAt)
	if err != nil {
		return DocumentVersionRecord{}, fmt.Errorf("stored recorded time: %w", err)
	}
	if sourceTime != nil {
		canonicalSourceTime, err := canonicalStoredTime(*sourceTime)
		if err != nil {
			return DocumentVersionRecord{}, fmt.Errorf("stored source time: %w", err)
		}
		sourceTime = &canonicalSourceTime
	}

	rows, err := reader.Query(ctx, `
		SELECT section_id, title, parent_id, path, section_order, role, content
		FROM stacks_core.document_sections
		WHERE document_version_id = $1
		ORDER BY section_order`,
		versionID,
	)
	if err != nil {
		return DocumentVersionRecord{}, err
	}
	var sections []evidence.Section
	for rows.Next() {
		var input evidence.SectionInput
		if err := rows.Scan(
			&input.ID,
			&input.Title,
			&input.ParentID,
			&input.Path,
			&input.Order,
			&input.Role,
			&input.Text,
		); err != nil {
			rows.Close()
			return DocumentVersionRecord{}, err
		}
		section, err := evidence.NewSection(input)
		if err != nil {
			rows.Close()
			return DocumentVersionRecord{}, fmt.Errorf("validate stored document section: %w", err)
		}
		sections = append(sections, section)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return DocumentVersionRecord{}, err
	}
	rows.Close()

	version, err := evidence.NewDocumentVersion(evidence.DocumentVersionInput{
		Provider:           provider,
		ProviderDocumentID: providerDocumentID,
		Title:              title,
		Locator:            locator,
		ProviderVersion:    providerVersion,
		ModifiedAt:         modifiedAt,
		SourceTime:         sourceTime,
		RecordedAt:         recordedAt,
		Sections:           sections,
	})
	if err != nil {
		return DocumentVersionRecord{}, fmt.Errorf("validate stored document version: %w", err)
	}
	if version.DigestVersion() != digestVersion ||
		!sameDigestBytes(version.Digest(), contentDigest) ||
		deriveOpaqueID(
			documentVersionIDVersion,
			[]byte(sourceID),
			[]byte(digestVersion),
			contentDigest,
		) != versionID {
		return DocumentVersionRecord{}, fmt.Errorf("stored document version: %w", ErrConflict)
	}

	revisionRows, err := reader.Query(ctx, `
		SELECT
			id,
			digest_version,
			digest,
			provider_version,
			provider_revision,
			first_recorded_at
		FROM stacks_core.source_revision_observations
		WHERE source_document_id = $1
		  AND document_version_id = $2
		ORDER BY provider_version, provider_revision, id`,
		sourceID,
		versionID,
	)
	if err != nil {
		return DocumentVersionRecord{}, err
	}
	var revisions []evidence.SourceRevisionObservation
	for revisionRows.Next() {
		var (
			storedID, storedDigestVersion, revisionProviderVersion, providerRevision string
			storedDigest                                                             []byte
			firstRecordedAt                                                          time.Time
		)
		if err := revisionRows.Scan(
			&storedID,
			&storedDigestVersion,
			&storedDigest,
			&revisionProviderVersion,
			&providerRevision,
			&firstRecordedAt,
		); err != nil {
			revisionRows.Close()
			return DocumentVersionRecord{}, err
		}
		firstRecordedAt, err = canonicalStoredTime(firstRecordedAt)
		if err != nil {
			revisionRows.Close()
			return DocumentVersionRecord{}, fmt.Errorf(
				"stored source revision recorded time: %w",
				err,
			)
		}
		if firstRecordedAt != recordedAt {
			revisionRows.Close()
			return DocumentVersionRecord{}, fmt.Errorf(
				"stored source revision recorded time differs from content: %w",
				ErrConflict,
			)
		}
		revision, err := evidence.NewSourceRevisionObservation(
			evidence.SourceRevisionObservationInput{
				Provider:              provider,
				ProviderDocumentID:    providerDocumentID,
				DocumentDigestVersion: digestVersion,
				DocumentDigest:        version.Digest(),
				ProviderVersion:       revisionProviderVersion,
				ProviderRevision:      providerRevision,
				FirstRecordedAt:       firstRecordedAt,
			},
		)
		if err != nil {
			revisionRows.Close()
			return DocumentVersionRecord{}, fmt.Errorf(
				"validate stored source revision observation: %w",
				err,
			)
		}
		if revision.ID() != storedID ||
			revision.DigestVersion() != storedDigestVersion ||
			!sameDigestBytes(revision.Digest(), storedDigest) {
			revisionRows.Close()
			return DocumentVersionRecord{}, fmt.Errorf(
				"stored source revision observation: %w",
				ErrConflict,
			)
		}
		revisions = append(revisions, revision)
	}
	if err := revisionRows.Err(); err != nil {
		revisionRows.Close()
		return DocumentVersionRecord{}, err
	}
	revisionRows.Close()
	return DocumentVersionRecord{
		Ref: DocumentVersionRef{
			SourceDocumentID: sourceID,
			VersionID:        versionID,
			RecordedAt:       recordedAt,
		},
		Version:   version,
		Revisions: revisions,
	}, nil
}

func loadSourceRevisionObservation(
	ctx context.Context,
	reader documentReader,
	sourceID string,
	versionID string,
	providerVersion string,
	providerRevision string,
) (evidence.SourceRevisionObservation, error) {
	var (
		provider, providerDocumentID, documentDigestVersion                  string
		documentDigest, storedDigest                                         []byte
		storedID, storedDigestVersion, storedProviderVersion, storedRevision string
		firstRecordedAt                                                      time.Time
	)
	if err := reader.QueryRow(ctx, `
		SELECT
			source.provider,
			source.provider_document_id,
			version.digest_version,
			version.content_digest,
			revision.id,
			revision.digest_version,
			revision.digest,
			revision.provider_version,
			revision.provider_revision,
			revision.first_recorded_at
		FROM stacks_core.source_revision_observations AS revision
		JOIN stacks_core.source_documents AS source
		  ON source.id = revision.source_document_id
		JOIN stacks_core.document_versions AS version
		  ON version.id = revision.document_version_id
		WHERE revision.source_document_id = $1
		  AND revision.document_version_id = $2
		  AND revision.provider_version = $3
		  AND revision.provider_revision = $4`,
		sourceID,
		versionID,
		providerVersion,
		providerRevision,
	).Scan(
		&provider,
		&providerDocumentID,
		&documentDigestVersion,
		&documentDigest,
		&storedID,
		&storedDigestVersion,
		&storedDigest,
		&storedProviderVersion,
		&storedRevision,
		&firstRecordedAt,
	); err != nil {
		return evidence.SourceRevisionObservation{}, err
	}
	firstRecordedAt, err := canonicalStoredTime(firstRecordedAt)
	if err != nil {
		return evidence.SourceRevisionObservation{}, err
	}
	parsedDocumentDigest, err := evidenceDigest(documentDigest)
	if err != nil {
		return evidence.SourceRevisionObservation{}, err
	}
	revision, err := evidence.NewSourceRevisionObservation(
		evidence.SourceRevisionObservationInput{
			Provider:              provider,
			ProviderDocumentID:    providerDocumentID,
			DocumentDigestVersion: documentDigestVersion,
			DocumentDigest:        parsedDocumentDigest,
			ProviderVersion:       storedProviderVersion,
			ProviderRevision:      storedRevision,
			FirstRecordedAt:       firstRecordedAt,
		},
	)
	if err != nil {
		return evidence.SourceRevisionObservation{}, err
	}
	if revision.ID() != storedID ||
		revision.DigestVersion() != storedDigestVersion ||
		!sameDigestBytes(revision.Digest(), storedDigest) {
		return evidence.SourceRevisionObservation{}, fmt.Errorf(
			"stored source revision observation: %w",
			ErrConflict,
		)
	}
	return revision, nil
}

func loadEvidenceSpan(
	ctx context.Context,
	reader documentReader,
	id evidence.EvidenceID,
) (evidence.EvidenceSpan, error) {
	return loadEvidenceSpanWithDocumentCache(ctx, reader, id, nil)
}

func loadEvidenceSpanWithDocumentCache(
	ctx context.Context,
	reader documentReader,
	id evidence.EvidenceID,
	documentVersions map[string]DocumentVersionRecord,
) (evidence.EvidenceSpan, error) {
	span, _, err := loadEvidenceSpanAndDocumentWithCache(
		ctx,
		reader,
		id,
		documentVersions,
	)
	return span, err
}

func loadEvidenceSpanAndDocumentWithCache(
	ctx context.Context,
	reader documentReader,
	id evidence.EvidenceID,
	documentVersions map[string]DocumentVersionRecord,
) (evidence.EvidenceSpan, DocumentVersionRecord, error) {
	var (
		versionID, sectionID, storedDigestVersion, quote string
		storedDigest                                     []byte
		startOffset, endOffset                           int
		recordedAt                                       time.Time
	)
	if err := reader.QueryRow(ctx, `
		SELECT
			document_version_id,
			section_id,
			digest_version,
			digest,
			start_offset,
			end_offset,
			quote,
			recorded_at
		FROM stacks_core.evidence_spans
		WHERE id = $1`,
		string(id),
	).Scan(
		&versionID,
		&sectionID,
		&storedDigestVersion,
		&storedDigest,
		&startOffset,
		&endOffset,
		&quote,
		&recordedAt,
	); err != nil {
		return evidence.EvidenceSpan{}, DocumentVersionRecord{}, err
	}
	recordedAt, err := canonicalStoredTime(recordedAt)
	if err != nil {
		return evidence.EvidenceSpan{}, DocumentVersionRecord{}, err
	}
	document, err := loadDocumentVersionRecordCached(
		ctx,
		reader,
		versionID,
		documentVersions,
		loadDocumentVersionRecord,
	)
	if err != nil {
		return evidence.EvidenceSpan{}, DocumentVersionRecord{}, err
	}
	span, err := evidence.NewEvidenceSpan(evidence.EvidenceSpanInput{
		Document:    document.Version,
		SectionID:   sectionID,
		StartOffset: startOffset,
		EndOffset:   endOffset,
		Quote:       quote,
		RecordedAt:  recordedAt,
	})
	if err != nil {
		return evidence.EvidenceSpan{}, DocumentVersionRecord{}, fmt.Errorf(
			"validate stored evidence span: %w",
			err,
		)
	}
	if span.ID() != id ||
		span.DigestVersion() != storedDigestVersion ||
		!sameDigestBytes(span.Digest(), storedDigest) {
		return evidence.EvidenceSpan{}, DocumentVersionRecord{}, fmt.Errorf(
			"stored evidence span: %w",
			ErrConflict,
		)
	}
	return span, document, nil
}

func loadDocumentVersionRecordCached(
	ctx context.Context,
	reader documentReader,
	versionID string,
	documentVersions map[string]DocumentVersionRecord,
	load documentVersionRecordLoader,
) (DocumentVersionRecord, error) {
	if document, exists := documentVersions[versionID]; exists {
		return document, nil
	}
	document, err := load(ctx, reader, versionID)
	if err != nil {
		return DocumentVersionRecord{}, err
	}
	if documentVersions != nil {
		documentVersions[versionID] = document
	}
	return document, nil
}

func validateDocumentVersion(version evidence.DocumentVersion) error {
	if version.Provider() == "" ||
		version.ProviderDocumentID() == "" ||
		version.Title() == "" ||
		version.Locator() == "" ||
		version.ProviderVersion() == "" ||
		version.DigestVersion() == "" ||
		version.Digest() == (evidence.ContentDigest{}) ||
		len(version.Sections()) == 0 {
		return fmt.Errorf("canonical document version is required")
	}
	if !timepoint.IsCanonical(version.ModifiedAt()) ||
		!timepoint.IsCanonical(version.RecordedAt()) {
		return fmt.Errorf("document timestamps must use canonical UTC microsecond precision")
	}
	if sourceTime := version.SourceTime(); sourceTime != nil && !timepoint.IsCanonical(*sourceTime) {
		return fmt.Errorf("document source time must use canonical UTC microsecond precision")
	}
	return nil
}

func validateSourceRevisionObservation(revision evidence.SourceRevisionObservation) error {
	if revision.ID() == "" ||
		revision.DigestVersion() == "" ||
		revision.Digest() == (evidence.ContentDigest{}) ||
		revision.Provider() == "" ||
		revision.ProviderDocumentID() == "" ||
		revision.DocumentDigestVersion() == "" ||
		revision.DocumentDigest() == (evidence.ContentDigest{}) ||
		revision.ProviderVersion() == "" {
		return fmt.Errorf("canonical source revision observation is required")
	}
	if !timepoint.IsCanonical(revision.FirstRecordedAt()) {
		return fmt.Errorf("source revision time must use canonical UTC microsecond precision")
	}
	return nil
}

func validateEvidenceSpan(span evidence.EvidenceSpan) error {
	if span.ID() == "" ||
		span.Provider() == "" ||
		span.ProviderDocumentID() == "" ||
		span.DocumentDigest() == (evidence.ContentDigest{}) ||
		span.SectionID() == "" ||
		span.StartOffset() < 0 ||
		span.EndOffset() <= span.StartOffset() ||
		span.Text() == "" ||
		span.DigestVersion() == "" ||
		span.Digest() == (evidence.ContentDigest{}) {
		return fmt.Errorf("canonical evidence span is required")
	}
	if !timepoint.IsCanonical(span.RecordedAt()) {
		return fmt.Errorf("evidence recorded time must use canonical UTC microsecond precision")
	}
	return nil
}

func sameStoredDocumentPayload(stored, supplied evidence.DocumentVersion) bool {
	if stored.Provider() != supplied.Provider() ||
		stored.ProviderDocumentID() != supplied.ProviderDocumentID() ||
		stored.Title() != supplied.Title() ||
		stored.Locator() != supplied.Locator() ||
		stored.ProviderVersion() != supplied.ProviderVersion() ||
		stored.ModifiedAt() != supplied.ModifiedAt() ||
		stored.DigestVersion() != supplied.DigestVersion() ||
		stored.Digest() != supplied.Digest() ||
		!sameOptionalTime(stored.SourceTime(), supplied.SourceTime()) {
		return false
	}
	storedSections, suppliedSections := stored.Sections(), supplied.Sections()
	if len(storedSections) != len(suppliedSections) {
		return false
	}
	for index := range storedSections {
		if !sameSection(storedSections[index], suppliedSections[index]) {
			return false
		}
	}
	return true
}

func sameSection(left, right evidence.Section) bool {
	if left.ID() != right.ID() ||
		left.Title() != right.Title() ||
		left.ParentID() != right.ParentID() ||
		left.Order() != right.Order() ||
		left.Role() != right.Role() ||
		left.Text() != right.Text() {
		return false
	}
	leftPath, rightPath := left.Path(), right.Path()
	if len(leftPath) != len(rightPath) {
		return false
	}
	for index := range leftPath {
		if leftPath[index] != rightPath[index] {
			return false
		}
	}
	return true
}

func sameOptionalTime(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func sameSourceRevisionObservation(
	left,
	right evidence.SourceRevisionObservation,
) bool {
	return left.ID() == right.ID() &&
		left.DigestVersion() == right.DigestVersion() &&
		left.Digest() == right.Digest() &&
		left.Provider() == right.Provider() &&
		left.ProviderDocumentID() == right.ProviderDocumentID() &&
		left.DocumentDigestVersion() == right.DocumentDigestVersion() &&
		left.DocumentDigest() == right.DocumentDigest() &&
		left.ProviderVersion() == right.ProviderVersion() &&
		left.ProviderRevision() == right.ProviderRevision() &&
		left.FirstRecordedAt() == right.FirstRecordedAt()
}

func sameEvidenceSpan(left, right evidence.EvidenceSpan) bool {
	return left.ID() == right.ID() &&
		left.Provider() == right.Provider() &&
		left.ProviderDocumentID() == right.ProviderDocumentID() &&
		left.DocumentDigest() == right.DocumentDigest() &&
		left.SectionID() == right.SectionID() &&
		left.StartOffset() == right.StartOffset() &&
		left.EndOffset() == right.EndOffset() &&
		left.Text() == right.Text() &&
		left.RecordedAt() == right.RecordedAt() &&
		left.Locator() == right.Locator() &&
		left.DigestVersion() == right.DigestVersion() &&
		left.Digest() == right.Digest()
}

func deriveOpaqueID(version string, parts ...[]byte) string {
	hasher := sha256.New()
	writeIDPart(hasher, []byte(version))
	for _, part := range parts {
		writeIDPart(hasher, part)
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

type idHasher interface {
	Write([]byte) (int, error)
}

func writeIDPart(hasher idHasher, value []byte) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = hasher.Write(length[:])
	_, _ = hasher.Write(value)
}

func sameDigestBytes(digest evidence.ContentDigest, stored []byte) bool {
	if len(stored) != len(digest) {
		return false
	}
	for index := range stored {
		if digest[index] != stored[index] {
			return false
		}
	}
	return true
}

func evidenceDigest(value []byte) (evidence.ContentDigest, error) {
	if len(value) != sha256.Size {
		return evidence.ContentDigest{}, fmt.Errorf("stored content digest is not 32 bytes")
	}
	var digest evidence.ContentDigest
	copy(digest[:], value)
	return digest, nil
}

func canonicalStoredTime(value time.Time) (time.Time, error) {
	if value.IsZero() || value.Nanosecond()%int(timepoint.Precision) != 0 {
		return time.Time{}, fmt.Errorf("timestamp is not UTC microsecond-compatible")
	}
	return value.UTC(), nil
}

func conflictError(err error) error {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && postgresError.Code == uniqueViolationCode {
		return fmt.Errorf("%w: unique database identity", ErrConflict)
	}
	return err
}

func contextRequired(ctx context.Context, operation string) error {
	if ctx == nil {
		return fmt.Errorf("%s: context is required", operation)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%s: %w", operation, err)
	}
	return nil
}

func wrapDocumentError(ctx context.Context, operation string, err error) error {
	if ctx != nil {
		if contextError := ctx.Err(); contextError != nil {
			return fmt.Errorf("%s: %w", operation, contextError)
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}
