package storage

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"stacks/internal/ingest"
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

// IngestionRepository owns the one transaction that makes validated evidence,
// identity proposals, observations, signals, and completion state visible.
type IngestionRepository struct {
	pool *pgxpool.Pool
}

var _ ingest.Repository = (*IngestionRepository)(nil)

// NewIngestionRepository creates the durable sync repository.
func NewIngestionRepository(pool *pgxpool.Pool) *IngestionRepository {
	return &IngestionRepository{pool: pool}
}

// PrepareVersion creates a pending immutable version or resumes a prior
// incomplete/failed attempt. A completed version is returned unchanged.
func (repository *IngestionRepository) PrepareVersion(ctx context.Context, version knowledge.DocumentVersion) (ingest.VersionState, error) {
	if repository == nil || repository.pool == nil {
		return ingest.VersionState{}, fmt.Errorf("prepare ingestion version: repository is not configured")
	}
	transaction, err := repository.pool.Begin(ctx)
	if err != nil {
		return ingest.VersionState{}, fmt.Errorf("prepare ingestion version: %w", err)
	}
	defer transaction.Rollback(ctx) //nolint:errcheck // committed transactions are already closed.

	documents := &DocumentRepository{query: transaction}
	stored, created, err := documents.PutDocumentVersion(ctx, version)
	if err != nil {
		return ingest.VersionState{}, err
	}
	state := ingest.VersionState{ID: stored.ID}
	if created {
		state.Status = ingest.VersionStatusPending
	} else {
		var status string
		var failureCode *string
		if err := transaction.QueryRow(ctx, `
			SELECT processing_status, retry_count, failure_code
			FROM stacks.document_versions WHERE id = $1 FOR UPDATE`, stored.ID).Scan(&status, &state.RetryCount, &failureCode); err != nil {
			return ingest.VersionState{}, fmt.Errorf("load ingestion version %q state: %w", stored.ID, err)
		}
		state.Status = ingest.VersionStatus(status)
		if failureCode != nil {
			state.FailureCode = ingest.FailureCode(*failureCode)
		}
		if state.Status != ingest.VersionStatusComplete {
			if err := transaction.QueryRow(ctx, `
				UPDATE stacks.document_versions
				SET processing_status = 'pending', failure_code = NULL, retry_count = retry_count + 1
				WHERE id = $1
				RETURNING retry_count`, stored.ID).Scan(&state.RetryCount); err != nil {
				return ingest.VersionState{}, fmt.Errorf("resume ingestion version %q: %w", stored.ID, err)
			}
			state.Status = ingest.VersionStatusPending
			state.FailureCode = ""
		}
	}
	if err := transaction.Commit(ctx); err != nil {
		return ingest.VersionState{}, fmt.Errorf("commit ingestion version preparation: %w", err)
	}
	return state, nil
}

// RecordFailure stores only finite processing state and a bounded diagnostic
// code. It never stores an error string or private model/source text.
func (repository *IngestionRepository) RecordFailure(ctx context.Context, versionID string, status ingest.VersionStatus, code ingest.FailureCode) error {
	if repository == nil || repository.pool == nil {
		return fmt.Errorf("record ingestion failure: repository is not configured")
	}
	if status != ingest.VersionStatusIncomplete && status != ingest.VersionStatusFailed {
		return fmt.Errorf("record ingestion failure: status is invalid")
	}
	if !validIngestionFailureCode(code) {
		return fmt.Errorf("record ingestion failure: code is invalid")
	}
	result, err := repository.pool.Exec(ctx, `
		UPDATE stacks.document_versions
		SET processing_status = $1, failure_code = $2
		WHERE id = $3 AND processing_status <> 'complete'`, string(status), string(code), versionID)
	if err != nil {
		return fmt.Errorf("record ingestion version %q failure: %w", versionID, err)
	}
	if result.RowsAffected() != 1 {
		return fmt.Errorf("record ingestion version %q failure: version is missing or complete", versionID)
	}
	return nil
}

// CompleteVersion commits the entire validated write-set and marks the version
// complete only after every required durable row has succeeded.
func (repository *IngestionRepository) CompleteVersion(ctx context.Context, completion ingest.Completion) error {
	if err := ingest.ValidateForPersistence(completion); err != nil {
		return fmt.Errorf("complete ingestion version input: %w", err)
	}
	if repository == nil || repository.pool == nil {
		return fmt.Errorf("complete ingestion version: repository is not configured")
	}
	if _, err := uuid.Parse(completion.VersionID); err != nil {
		return fmt.Errorf("complete ingestion version: version ID is invalid")
	}
	transaction, err := repository.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("start ingestion completion: %w", err)
	}
	defer transaction.Rollback(ctx) //nolint:errcheck // committed transactions are already closed.

	var status string
	if err := transaction.QueryRow(ctx, `
		SELECT processing_status FROM stacks.document_versions WHERE id = $1 FOR UPDATE`, completion.VersionID).Scan(&status); err != nil {
		return fmt.Errorf("lock ingestion version %q: %w", completion.VersionID, err)
	}
	if status == string(ingest.VersionStatusComplete) {
		return transaction.Commit(ctx)
	}

	evidenceIDs, err := persistIngestionEvidence(ctx, transaction, completion.Evidence)
	if err != nil {
		return err
	}
	mentionIDs, err := persistIngestionMentions(ctx, transaction, completion.VersionID, completion.Mentions, evidenceIDs)
	if err != nil {
		return err
	}
	if err := persistIngestionGraph(ctx, transaction, completion.Observations, completion.Signals, evidenceIDs, mentionIDs); err != nil {
		return err
	}
	result, err := transaction.Exec(ctx, `
		UPDATE stacks.document_versions
		SET processing_status = 'complete', failure_code = NULL
		WHERE id = $1`, completion.VersionID)
	if err != nil {
		return fmt.Errorf("mark ingestion version %q complete: %w", completion.VersionID, err)
	}
	if result.RowsAffected() != 1 {
		return fmt.Errorf("mark ingestion version %q complete: version does not exist", completion.VersionID)
	}
	if err := transaction.Commit(ctx); err != nil {
		return fmt.Errorf("commit ingestion version %q: %w", completion.VersionID, err)
	}
	return nil
}

func persistIngestionEvidence(ctx context.Context, transaction pgx.Tx, records []ingest.EvidenceRecord) (map[string]string, error) {
	documents := &DocumentRepository{query: transaction}
	identifiers := make(map[string]string, len(records))
	for _, record := range records {
		key := record.Key
		if key == "" || strings.TrimSpace(key) != key {
			return nil, fmt.Errorf("persist ingestion evidence: key is required")
		}
		if _, exists := identifiers[key]; exists {
			return nil, fmt.Errorf("persist ingestion evidence: key is duplicated")
		}
		stored, err := documents.PutEvidenceSpan(ctx, record.Span)
		if err != nil {
			return nil, err
		}
		identifiers[key] = stored.ID
	}
	return identifiers, nil
}

func persistIngestionMentions(ctx context.Context, transaction pgx.Tx, versionID string, records []ingest.MentionRecord, evidenceIDs map[string]string) (map[string]string, error) {
	mentionIDs := make(map[string]string, len(records))
	for _, record := range records {
		evidenceID, exists := evidenceIDs[record.EvidenceKey]
		if !exists || strings.TrimSpace(record.Key) == "" || strings.TrimSpace(record.Surface) == "" {
			return nil, fmt.Errorf("persist ingestion mention: input is invalid")
		}
		var mentionID string
		err := transaction.QueryRow(ctx, `
			INSERT INTO stacks.mentions (evidence_span_id, surface, role, recorded_at)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (evidence_span_id, surface, role) DO UPDATE SET role = EXCLUDED.role
			RETURNING id`, evidenceID, record.Surface, record.Role, time.Now().UTC()).Scan(&mentionID)
		if err != nil {
			return nil, fmt.Errorf("persist ingestion mention: %w", err)
		}
		mentionIDs[record.Key] = mentionID
		var proposalID string
		err = transaction.QueryRow(ctx, `
			INSERT INTO stacks.resolution_proposals (mention_id, derivation, recorded_at)
			VALUES ($1, $2, $3)
			ON CONFLICT (mention_id) DO UPDATE SET mention_id = EXCLUDED.mention_id
			RETURNING id`, mentionID, "extract", time.Now().UTC()).Scan(&proposalID)
		if err != nil {
			return nil, fmt.Errorf("persist ingestion resolution proposal: %w", err)
		}
		for rank, candidate := range record.Resolution.Candidates {
			confidence := candidate.Confidence
			if _, err := transaction.Exec(ctx, `
				INSERT INTO stacks.resolution_candidates (proposal_id, entity_id, rank, confidence, reason)
				VALUES ($1, $2, $3, $4, $5)
				ON CONFLICT (proposal_id, entity_id) DO NOTHING`, proposalID, candidate.EntityID, rank, &confidence, candidate.Reason); err != nil {
				return nil, fmt.Errorf("persist ingestion resolution candidate: %w", err)
			}
		}
		if record.Resolution.AutoResolved {
			decisionID := uuid.NewSHA1(uuid.NameSpaceOID, []byte(versionID+"\x00decision\x00"+record.Key)).String()
			digest := resolutionDecisionDigest(ResolutionDecisionInput{
				ProposalID: proposalID, Outcome: ResolutionOutcomeAccepted, EntityID: record.Resolution.EntityID,
			}, "")
			if _, err := transaction.Exec(ctx, `
				INSERT INTO stacks.resolution_decisions
					(id, proposal_id, outcome, entity_id, digest, recorded_at)
				VALUES ($1, $2, 'accepted', $3, $4, $5)
				ON CONFLICT (id) DO NOTHING`, decisionID, proposalID, record.Resolution.EntityID, digest[:], time.Now().UTC()); err != nil {
				return nil, fmt.Errorf("persist ingestion resolution decision: %w", err)
			}
			if err := updateProposalStatus(ctx, transaction, proposalID, ResolutionOutcomeAccepted); err != nil {
				return nil, err
			}
		}
	}
	return mentionIDs, nil
}

func persistIngestionGraph(ctx context.Context, transaction pgx.Tx, observations []ingest.ObservationRecord, signals []ingest.SignalRecord, evidenceIDs, mentionIDs map[string]string) error {
	for _, record := range observations {
		resolvedEvidence, err := resolveEvidenceKeys(record.EvidenceKeys, evidenceIDs)
		if err != nil {
			return err
		}
		input := ObservationInput{
			ID: record.ID, SubjectEntityID: record.SubjectEntityID, ObjectEntityID: record.ObjectEntityID,
			SubjectMentionID: mentionIDs[record.SubjectMentionKey], ObjectMentionID: mentionIDs[record.ObjectMentionKey],
			Predicate: record.Predicate, ValidStart: record.ValidStart,
			Derivation: "model_extraction", EpistemicStatus: "inferred", Confidence: record.Confidence,
		}
		canonicalInput, canonicalEvidence, err := canonicalizeObservationIdentity(input, resolvedEvidence)
		if err != nil {
			return err
		}
		if err := validateObservationInput(canonicalInput); err != nil {
			return err
		}
		digest, err := ComputeObservationDigest(canonicalInput, canonicalEvidence)
		if err != nil {
			return err
		}
		observation, err := putObservation(ctx, transaction, canonicalInput, digest[:])
		if err != nil {
			return err
		}
		for _, evidenceID := range canonicalEvidence {
			if _, err := transaction.Exec(ctx, `
				INSERT INTO stacks.observation_evidence (observation_id, evidence_span_id)
				VALUES ($1, $2) ON CONFLICT DO NOTHING`, observation.ID, evidenceID); err != nil {
				return fmt.Errorf("persist ingestion observation evidence: %w", err)
			}
		}
	}
	for _, record := range signals {
		evidence := make([]SignalEvidenceInput, 0, len(record.Evidence))
		for _, link := range record.Evidence {
			evidenceID, exists := evidenceIDs[link.EvidenceKey]
			if !exists {
				return fmt.Errorf("persist ingestion signal: evidence reference is unknown")
			}
			evidence = append(evidence, SignalEvidenceInput{EvidenceSpanID: evidenceID, Role: link.Role})
		}
		input := SignalInput{
			ID: record.ID, ObservationID: record.ObservationID, Category: record.Category,
			Direction: record.Direction, ExtractionModelID: record.ExtractionModelID,
			PromptVersion: record.PromptVersion, Rationale: record.Rationale, Confidence: record.Confidence,
		}
		canonicalInput, canonicalEvidence, err := canonicalizeSignalIdentity(input, evidence)
		if err != nil {
			return err
		}
		if err := validateSignalInput(canonicalInput); err != nil {
			return err
		}
		digest, err := ComputeSignalDigest(canonicalInput, canonicalEvidence)
		if err != nil {
			return err
		}
		signal, err := putSignal(ctx, transaction, canonicalInput, digest[:])
		if err != nil {
			return err
		}
		for _, link := range canonicalEvidence {
			if !validSignalEvidenceRole(link.Role) {
				return fmt.Errorf("persist ingestion signal: evidence role is invalid")
			}
			if _, err := transaction.Exec(ctx, `
				INSERT INTO stacks.signal_evidence (signal_id, evidence_span_id, role)
				VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, signal.ID, link.EvidenceSpanID, link.Role); err != nil {
				return fmt.Errorf("persist ingestion signal evidence: %w", err)
			}
		}
	}
	return nil
}

func resolveEvidenceKeys(keys []string, evidenceIDs map[string]string) ([]string, error) {
	resolved := make([]string, len(keys))
	for index, key := range keys {
		identifier, exists := evidenceIDs[key]
		if !exists {
			return nil, fmt.Errorf("persist ingestion observation: evidence reference is unknown")
		}
		resolved[index] = identifier
	}
	return resolved, nil
}

func validIngestionFailureCode(code ingest.FailureCode) bool {
	switch code {
	case ingest.FailureSource, ingest.FailureInvalidSource, ingest.FailureModel, ingest.FailureInvalidOutput, ingest.FailureStorage:
		return true
	default:
		return false
	}
}
