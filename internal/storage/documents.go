package storage

import (
	"context"
	"crypto/sha256"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"stacks/internal/knowledge"
	"stacks/internal/source"
)

// StoredDocumentVersion identifies a durable immutable document version.
type StoredDocumentVersion struct {
	ID string
}

// StoredEvidenceSpan identifies a durable exact source citation.
type StoredEvidenceSpan struct {
	ID string
}

type documentQueryer interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

// DocumentRepository persists immutable source documents, versions, tabs, and
// exact evidence spans. Use InTransaction for document-completion work that
// must become durable atomically.
type DocumentRepository struct {
	pool  *pgxpool.Pool
	query documentQueryer
}

// NewDocumentRepository creates a document repository backed by pool.
func NewDocumentRepository(pool *pgxpool.Pool) *DocumentRepository {
	return &DocumentRepository{pool: pool, query: pool}
}

// InTransaction executes work in one document-completion transaction.
func (repository *DocumentRepository) InTransaction(ctx context.Context, work func(*DocumentRepository) error) error {
	if repository.pool == nil {
		return fmt.Errorf("start document transaction: repository is already transaction-scoped")
	}

	transaction, err := repository.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("start document transaction: %w", err)
	}
	defer transaction.Rollback(ctx) //nolint:errcheck // committed transactions are already closed.

	if err := work(&DocumentRepository{query: transaction}); err != nil {
		return err
	}
	if err := transaction.Commit(ctx); err != nil {
		return fmt.Errorf("commit document transaction: %w", err)
	}
	return nil
}

// PutDocumentVersion stores a version and its ordered tabs. Repeating the same
// provider document and digest returns its existing stable ID with created false.
func (repository *DocumentRepository) PutDocumentVersion(ctx context.Context, version knowledge.DocumentVersion) (StoredDocumentVersion, bool, error) {
	if repository.pool != nil {
		var stored StoredDocumentVersion
		var created bool
		err := repository.InTransaction(ctx, func(transaction *DocumentRepository) error {
			var err error
			stored, created, err = transaction.putDocumentVersion(ctx, version)
			return err
		})
		if err != nil {
			return StoredDocumentVersion{}, false, err
		}
		return stored, created, nil
	}
	return repository.putDocumentVersion(ctx, version)
}

func (repository *DocumentRepository) putDocumentVersion(ctx context.Context, version knowledge.DocumentVersion) (StoredDocumentVersion, bool, error) {
	var sourceDocumentID string
	err := repository.query.QueryRow(ctx, `
		INSERT INTO stacks.source_documents (provider, provider_document_id, recorded_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (provider, provider_document_id) DO UPDATE
		SET provider = EXCLUDED.provider
		RETURNING id`, version.Provider(), version.ProviderDocumentID(), version.RecordedAt()).Scan(&sourceDocumentID)
	if err != nil {
		return StoredDocumentVersion{}, false, fmt.Errorf("persist source document %q: %w", version.ProviderDocumentID(), err)
	}

	var stored StoredDocumentVersion
	digest := version.Digest()
	err = repository.query.QueryRow(ctx, `
		INSERT INTO stacks.document_versions (source_document_id, digest, recorded_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (source_document_id, digest) DO NOTHING
		RETURNING id`, sourceDocumentID, digest[:], version.RecordedAt()).Scan(&stored.ID)
	if err == pgx.ErrNoRows {
		err = repository.query.QueryRow(ctx, `
			SELECT id FROM stacks.document_versions
			WHERE source_document_id = $1 AND digest = $2`, sourceDocumentID, digest[:]).Scan(&stored.ID)
		if err != nil {
			return StoredDocumentVersion{}, false, fmt.Errorf("load document version %q: %w", version.Digest().String(), err)
		}
		return stored, false, nil
	}
	if err != nil {
		return StoredDocumentVersion{}, false, fmt.Errorf("persist document version %q: %w", version.Digest().String(), err)
	}

	for _, tab := range version.Tabs() {
		if err := repository.putTab(ctx, stored.ID, tab); err != nil {
			return StoredDocumentVersion{}, false, fmt.Errorf("persist document version %q: %w", stored.ID, err)
		}
	}
	return stored, true, nil
}

func (repository *DocumentRepository) putTab(ctx context.Context, documentVersionID string, tab source.Tab) error {
	contentDigest := sha256.Sum256([]byte(tab.Text))
	_, err := repository.query.Exec(ctx, `
		INSERT INTO stacks.document_tabs
			(document_version_id, provider_tab_id, title, parent_provider_tab_id, title_path, display_order, role, content, content_digest)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		documentVersionID, tab.ID, tab.Title, tab.ParentID, tab.Path, tab.Order, string(tab.Role), tab.Text, contentDigest[:])
	if err != nil {
		return fmt.Errorf("persist tab %q: %w", tab.ID, err)
	}
	return nil
}

// PutEvidenceSpan persists an exact citation after its domain validation has
// already established offsets and matching source text.
func (repository *DocumentRepository) PutEvidenceSpan(ctx context.Context, span knowledge.EvidenceSpan) (StoredEvidenceSpan, error) {
	var stored StoredEvidenceSpan
	digest := span.DocumentDigest()
	err := repository.query.QueryRow(ctx, `
		INSERT INTO stacks.evidence_spans (document_tab_id, start_offset, end_offset, quote)
		SELECT tab.id, $1, $2, $3
		FROM stacks.document_tabs AS tab
		JOIN stacks.document_versions AS version ON version.id = tab.document_version_id
		JOIN stacks.source_documents AS document ON document.id = version.source_document_id
		WHERE document.provider = $4
			AND document.provider_document_id = $5
			AND version.digest = $6
			AND tab.provider_tab_id = $7
		ON CONFLICT (document_tab_id, start_offset, end_offset) DO NOTHING
		RETURNING id`,
		span.StartOffset(), span.EndOffset(), span.Text(), span.Provider(), span.ProviderDocumentID(), digest[:], span.TabID()).Scan(&stored.ID)
	if err == pgx.ErrNoRows {
		var storedQuote string
		err = repository.query.QueryRow(ctx, `
			SELECT span.id, span.quote
			FROM stacks.evidence_spans AS span
			JOIN stacks.document_tabs AS tab ON tab.id = span.document_tab_id
			JOIN stacks.document_versions AS version ON version.id = tab.document_version_id
			JOIN stacks.source_documents AS document ON document.id = version.source_document_id
			WHERE document.provider = $1
				AND document.provider_document_id = $2
				AND version.digest = $3
				AND tab.provider_tab_id = $4
				AND span.start_offset = $5
				AND span.end_offset = $6`,
			span.Provider(), span.ProviderDocumentID(), digest[:], span.TabID(), span.StartOffset(), span.EndOffset()).Scan(&stored.ID, &storedQuote)
		if err != nil {
			return StoredEvidenceSpan{}, fmt.Errorf("load evidence span for document %q tab %q: %w", span.ProviderDocumentID(), span.TabID(), err)
		}
		if storedQuote != span.Text() {
			return StoredEvidenceSpan{}, fmt.Errorf("persist evidence span for document %q tab %q: immutable quote conflicts", span.ProviderDocumentID(), span.TabID())
		}
		return stored, nil
	}
	if err != nil {
		return StoredEvidenceSpan{}, fmt.Errorf("persist evidence span for document %q tab %q: %w", span.ProviderDocumentID(), span.TabID(), err)
	}
	return stored, nil
}
