package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	knowledge "github.com/JakeFAU/stacks/core/evidence"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"stacks/internal/ingest"
)

var errCompletedWriteSetMismatch = errors.New("completed ingestion write-set mismatch")

func compareCompletedWriteSet(
	ctx context.Context,
	transaction pgx.Tx,
	completion ingest.Completion,
	run owningExtractionRun,
) error {
	evidenceIDs, err := loadCompletedEvidenceMap(ctx, transaction, completion)
	if err != nil {
		return completedWriteSetComparisonError(completion.DerivationID, err)
	}
	mentionIDs, err := loadCompletedMentionMap(
		ctx,
		transaction,
		completion.DerivationID,
		completion.Mentions,
		evidenceIDs,
	)
	if err != nil {
		return completedWriteSetComparisonError(completion.DerivationID, err)
	}
	if err := compareCompletedObservations(
		ctx,
		transaction,
		run,
		completion,
		evidenceIDs,
		mentionIDs,
	); err != nil {
		return completedWriteSetComparisonError(completion.DerivationID, err)
	}
	if err := compareCompletedRunState(ctx, transaction, completion, run); err != nil {
		return completedWriteSetComparisonError(completion.DerivationID, err)
	}
	return nil
}

func completedWriteSetComparisonError(runID string, err error) error {
	if errors.Is(err, errCompletedWriteSetMismatch) ||
		errors.Is(err, ErrObservationConflict) ||
		errors.Is(err, ErrObservationCompatibility) {
		return newCompletionBoundaryError(
			ErrObservationConflict,
			reasonCompletionWriteSetMismatch,
			runID,
		)
	}
	return err
}

func loadCompletedEvidenceMap(
	ctx context.Context,
	transaction pgx.Tx,
	completion ingest.Completion,
) (map[string]string, error) {
	version, err := loadCompletedCanonicalDocumentVersion(ctx, transaction, completion.VersionID)
	if err != nil {
		return nil, err
	}
	canonicalDocumentDigest := version.Digest()

	identifiers := make(map[string]string, len(completion.Evidence))
	seenEvidenceIDs := make(map[string]struct{}, len(completion.Evidence))
	for _, record := range completion.Evidence {
		span := record.Span
		var evidenceID, quote string
		if err := transaction.QueryRow(ctx, `
			SELECT evidence.id::text, evidence.quote
			FROM stacks.evidence_spans AS evidence
			JOIN stacks.document_tabs AS tab ON tab.id = evidence.document_tab_id
			JOIN stacks.document_versions AS version ON version.id = tab.document_version_id
			JOIN stacks.source_documents AS document ON document.id = version.source_document_id
			WHERE version.id = $1
			  AND document.provider = $2
			  AND document.provider_document_id = $3
			  AND tab.provider_tab_id = $4
			  AND evidence.start_offset = $5
			  AND evidence.end_offset = $6`,
			completion.VersionID,
			span.Provider(),
			span.ProviderDocumentID(),
			span.SectionID(),
			span.StartOffset(),
			span.EndOffset(),
		).Scan(&evidenceID, &quote); err != nil {
			if err == pgx.ErrNoRows {
				return nil, errCompletedWriteSetMismatch
			}
			return nil, fmt.Errorf("compare completed evidence: %w", err)
		}
		documentDigest := span.DocumentDigest()
		if quote != span.Text() ||
			documentDigest != canonicalDocumentDigest {
			return nil, errCompletedWriteSetMismatch
		}
		if _, exists := identifiers[record.Key]; exists {
			return nil, errCompletedWriteSetMismatch
		}
		if _, exists := seenEvidenceIDs[evidenceID]; exists {
			return nil, errCompletedWriteSetMismatch
		}
		identifiers[record.Key] = evidenceID
		seenEvidenceIDs[evidenceID] = struct{}{}
	}
	return identifiers, nil
}

func loadCompletedCanonicalDocumentVersion(
	ctx context.Context,
	transaction pgx.Tx,
	versionID string,
) (knowledge.DocumentVersion, error) {
	var input knowledge.DocumentVersionInput
	var modifiedAt *time.Time
	var storedDigest, stableDigest []byte
	if err := transaction.QueryRow(ctx, `
		SELECT document.provider,
		       document.provider_document_id,
		       version.title,
		       version.locator,
		       version.provider_version,
		       version.provider_revision,
		       version.provider_modified_at,
		       version.source_meeting_time,
		       version.recorded_at,
		       version.digest,
		       version.content_digest_v2
		FROM stacks.document_versions AS version
		JOIN stacks.source_documents AS document ON document.id = version.source_document_id
		WHERE version.id = $1`,
		versionID,
	).Scan(
		&input.Provider,
		&input.ProviderDocumentID,
		&input.Title,
		&input.Locator,
		&input.ProviderVersion,
		&input.ProviderRevision,
		&modifiedAt,
		&input.SourceTime,
		&input.RecordedAt,
		&storedDigest,
		&stableDigest,
	); err != nil {
		if err == pgx.ErrNoRows {
			return knowledge.DocumentVersion{}, errCompletedWriteSetMismatch
		}
		return knowledge.DocumentVersion{}, fmt.Errorf("compare completed document version: %w", err)
	}
	if modifiedAt == nil {
		return knowledge.DocumentVersion{}, errCompletedWriteSetMismatch
	}
	input.ModifiedAt = *modifiedAt

	rows, err := transaction.Query(ctx, `
		SELECT provider_tab_id,
		       title,
		       parent_provider_tab_id,
		       title_path,
		       display_order,
		       role,
		       content,
		       content_digest
		FROM stacks.document_tabs
		WHERE document_version_id = $1
		ORDER BY display_order, id`,
		versionID,
	)
	if err != nil {
		return knowledge.DocumentVersion{}, fmt.Errorf("compare completed document tabs: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var sectionInput knowledge.SectionInput
		var storedContentDigest []byte
		if err := rows.Scan(
			&sectionInput.ID,
			&sectionInput.Title,
			&sectionInput.ParentID,
			&sectionInput.Path,
			&sectionInput.Order,
			&sectionInput.Role,
			&sectionInput.Text,
			&storedContentDigest,
		); err != nil {
			return knowledge.DocumentVersion{}, fmt.Errorf("compare completed document tab: %w", err)
		}
		computedContentDigest := sha256.Sum256([]byte(sectionInput.Text))
		if !bytes.Equal(storedContentDigest, computedContentDigest[:]) {
			return knowledge.DocumentVersion{}, errCompletedWriteSetMismatch
		}
		section, err := knowledge.NewSection(sectionInput)
		if err != nil {
			return knowledge.DocumentVersion{}, errCompletedWriteSetMismatch
		}
		input.Sections = append(input.Sections, section)
	}
	if err := rows.Err(); err != nil {
		return knowledge.DocumentVersion{}, fmt.Errorf("iterate completed document tabs: %w", err)
	}

	version, err := knowledge.NewDocumentVersion(input)
	if err != nil {
		return knowledge.DocumentVersion{}, errCompletedWriteSetMismatch
	}
	canonicalDigest := version.Digest()
	legacyDigest := version.LegacyRevisionInclusiveDigest()
	if (!bytes.Equal(storedDigest, canonicalDigest[:]) &&
		!bytes.Equal(storedDigest, legacyDigest[:])) ||
		(stableDigest != nil && !bytes.Equal(stableDigest, canonicalDigest[:])) {
		return knowledge.DocumentVersion{}, errCompletedWriteSetMismatch
	}
	return version, nil
}

func loadCompletedMentionMap(
	ctx context.Context,
	transaction pgx.Tx,
	derivationID string,
	records []ingest.MentionRecord,
	evidenceIDs map[string]string,
) (map[string]string, error) {
	var mentionCount, proposalCount int
	if err := transaction.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM stacks.mentions WHERE extraction_run_id = $1),
			(SELECT count(*)
			 FROM stacks.resolution_proposals AS proposal
			 JOIN stacks.mentions AS mention ON mention.id = proposal.mention_id
			 WHERE mention.extraction_run_id = $1)`,
		derivationID,
	).Scan(&mentionCount, &proposalCount); err != nil {
		return nil, fmt.Errorf("compare completed mention count: %w", err)
	}
	if mentionCount != len(records) || proposalCount != len(records) {
		return nil, errCompletedWriteSetMismatch
	}

	mentionIDs := make(map[string]string, len(records))
	for _, record := range records {
		evidenceID, exists := evidenceIDs[record.EvidenceKey]
		if !exists {
			return nil, errCompletedWriteSetMismatch
		}
		var mentionID, normalizedName, proposedEmail, proposedEmailEvidenceID string
		var proposalID, proposalDerivation string
		if err := transaction.QueryRow(ctx, `
			SELECT mention.id::text, mention.normalized_name, mention.proposed_email,
			       COALESCE(mention.proposed_email_evidence_span_id::text, ''),
			       proposal.id::text, proposal.derivation
			FROM stacks.mentions AS mention
			JOIN stacks.resolution_proposals AS proposal ON proposal.mention_id = mention.id
			WHERE mention.extraction_run_id = $1
			  AND mention.evidence_span_id = $2
			  AND mention.surface = $3
			  AND mention.role = $4`,
			derivationID,
			evidenceID,
			record.Surface,
			record.Role,
		).Scan(
			&mentionID,
			&normalizedName,
			&proposedEmail,
			&proposedEmailEvidenceID,
			&proposalID,
			&proposalDerivation,
		); err != nil {
			if err == pgx.ErrNoRows {
				return nil, errCompletedWriteSetMismatch
			}
			return nil, fmt.Errorf("compare completed mention: %w", err)
		}
		expectedProposedEmailEvidenceID := ""
		if record.ProposedEmailEvidenceKey != "" {
			var exists bool
			expectedProposedEmailEvidenceID, exists = evidenceIDs[record.ProposedEmailEvidenceKey]
			if !exists {
				return nil, errCompletedWriteSetMismatch
			}
		}
		if normalizedName != record.NormalizedName ||
			proposedEmail != record.ProposedEmail ||
			proposedEmailEvidenceID != expectedProposedEmailEvidenceID ||
			proposalDerivation != "extract" {
			return nil, errCompletedWriteSetMismatch
		}
		if err := compareCompletedResolution(
			ctx,
			transaction,
			derivationID,
			record,
			proposalID,
		); err != nil {
			return nil, err
		}
		if _, exists := mentionIDs[record.Key]; exists {
			return nil, errCompletedWriteSetMismatch
		}
		mentionIDs[record.Key] = mentionID
	}
	return mentionIDs, nil
}

func compareCompletedResolution(
	ctx context.Context,
	transaction pgx.Tx,
	derivationID string,
	record ingest.MentionRecord,
	proposalID string,
) error {
	var completionCandidateCount int
	if err := transaction.QueryRow(ctx, `
		SELECT count(*)
		FROM stacks.resolution_candidates
		WHERE proposal_id = $1
		  AND directory_profile_snapshot_id IS NULL`,
		proposalID,
	).Scan(&completionCandidateCount); err != nil {
		return fmt.Errorf("compare completed resolution candidate count: %w", err)
	}
	if completionCandidateCount != len(record.Resolution.Candidates) {
		return errCompletedWriteSetMismatch
	}
	for rank, candidate := range record.Resolution.Candidates {
		var storedRank int
		var storedConfidence *float64
		var storedReason string
		var directoryProfileID *string
		if err := transaction.QueryRow(ctx, `
			SELECT rank, confidence, reason, directory_profile_snapshot_id::text
			FROM stacks.resolution_candidates
			WHERE proposal_id = $1 AND entity_id = $2`,
			proposalID,
			candidate.EntityID,
		).Scan(&storedRank, &storedConfidence, &storedReason, &directoryProfileID); err != nil {
			if err == pgx.ErrNoRows {
				return errCompletedWriteSetMismatch
			}
			return fmt.Errorf("compare completed resolution candidate: %w", err)
		}
		if storedRank != rank ||
			storedConfidence == nil ||
			*storedConfidence != candidate.Confidence ||
			storedReason != candidate.Reason ||
			directoryProfileID != nil {
			return errCompletedWriteSetMismatch
		}
	}

	decisionID := uuid.NewSHA1(
		uuid.NameSpaceOID,
		[]byte(derivationID+"\x00decision\x00"+record.Key),
	).String()
	if !record.Resolution.AutoResolved {
		if record.Resolution.EntityID != "" {
			return errCompletedWriteSetMismatch
		}
		var automaticDecisionCount int
		if err := transaction.QueryRow(ctx, `
			SELECT count(*)
			FROM stacks.resolution_decisions
			WHERE id = $1`,
			decisionID,
		).Scan(&automaticDecisionCount); err != nil {
			return fmt.Errorf("compare absent completed automatic decision: %w", err)
		}
		if automaticDecisionCount != 0 {
			return errCompletedWriteSetMismatch
		}
		return nil
	}
	expectedDigest := resolutionDecisionDigest(ResolutionDecisionInput{
		ProposalID: proposalID,
		Outcome:    ResolutionOutcomeAccepted,
		EntityID:   record.Resolution.EntityID,
	}, "")
	var storedProposalID, outcome, entityID string
	var storedDigest []byte
	if err := transaction.QueryRow(ctx, `
		SELECT proposal_id::text, outcome, entity_id::text, digest
		FROM stacks.resolution_decisions
		WHERE id = $1`,
		decisionID,
	).Scan(&storedProposalID, &outcome, &entityID, &storedDigest); err != nil {
		if err == pgx.ErrNoRows {
			return errCompletedWriteSetMismatch
		}
		return fmt.Errorf("compare completed automatic decision: %w", err)
	}
	if storedProposalID != proposalID ||
		outcome != string(ResolutionOutcomeAccepted) ||
		entityID != record.Resolution.EntityID ||
		!bytes.Equal(storedDigest, expectedDigest[:]) {
		return errCompletedWriteSetMismatch
	}
	if record.NormalizedName == "" {
		return nil
	}
	var aliasCount int
	if err := transaction.QueryRow(ctx, `
		SELECT count(*)
		FROM stacks.entity_alias_assertions
		WHERE decision_id = $1
		  AND entity_id = $2
		  AND normalized_value = $3
		  AND alias_type = 'name'`,
		decisionID,
		record.Resolution.EntityID,
		record.NormalizedName,
	).Scan(&aliasCount); err != nil {
		return fmt.Errorf("compare completed automatic alias: %w", err)
	}
	if aliasCount != 1 {
		return errCompletedWriteSetMismatch
	}
	return nil
}

func compareCompletedObservations(
	ctx context.Context,
	transaction pgx.Tx,
	run owningExtractionRun,
	completion ingest.Completion,
	evidenceIDs map[string]string,
	mentionIDs map[string]string,
) error {
	staged, err := stageCanonicalIngestionWrites(
		run,
		completion.Observations,
		completion.Signals,
		evidenceIDs,
		mentionIDs,
	)
	if err != nil {
		return errCompletedWriteSetMismatch
	}
	var observationCount, signalCount int
	if err := transaction.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM stacks.observations WHERE extraction_run_id = $1),
			(SELECT count(*)
			 FROM stacks.interaction_signals AS signal
			 JOIN stacks.observations AS observation ON observation.id = signal.observation_id
			 WHERE observation.extraction_run_id = $1)`,
		run.ID,
	).Scan(&observationCount, &signalCount); err != nil {
		return fmt.Errorf("compare completed observation count: %w", err)
	}
	if observationCount != len(staged) || signalCount != len(completion.Signals) {
		return errCompletedWriteSetMismatch
	}
	for _, expected := range staged {
		stored, err := loadLegacyObservation(ctx, transaction, expected.Row.ID)
		if err != nil {
			if errors.Is(err, ErrObservationConflict) ||
				errors.Is(err, ErrObservationCompatibility) ||
				errors.Is(err, pgx.ErrNoRows) {
				return errCompletedWriteSetMismatch
			}
			return fmt.Errorf("compare completed observation: %w", err)
		}
		if !equalLegacyObservationRetry(expected, stored) {
			return errCompletedWriteSetMismatch
		}
	}
	return nil
}

func compareCompletedRunState(
	ctx context.Context,
	transaction pgx.Tx,
	completion ingest.Completion,
	run owningExtractionRun,
) error {
	var versionID, status, dataMode, modelID, promptVersion string
	var recordedAtEqual, currentlyAdmissible bool
	var completedByOwner, currentVersionID *string
	var completedAtPresent bool
	if err := transaction.QueryRow(ctx, `
		SELECT extraction_run.document_version_id::text,
		       extraction_run.processing_status,
		       extraction_run.data_mode,
		       extraction_run.model_id,
		       extraction_run.prompt_version,
		       extraction_run.recorded_at = $2,
		       extraction_run.currently_admissible,
		       extraction_run.completed_by_owner::text,
		       extraction_run.completed_at IS NOT NULL,
		       source.current_document_version_id::text
		FROM stacks.extraction_runs AS extraction_run
		JOIN stacks.document_versions AS version ON version.id = extraction_run.document_version_id
		JOIN stacks.source_documents AS source ON source.id = version.source_document_id
		WHERE extraction_run.id = $1`,
		run.ID,
		run.RecordedAt,
	).Scan(
		&versionID,
		&status,
		&dataMode,
		&modelID,
		&promptVersion,
		&recordedAtEqual,
		&currentlyAdmissible,
		&completedByOwner,
		&completedAtPresent,
		&currentVersionID,
	); err != nil {
		if err == pgx.ErrNoRows {
			return errCompletedWriteSetMismatch
		}
		return fmt.Errorf("compare completed run state: %w", err)
	}
	if versionID != completion.VersionID ||
		status != string(ingest.VersionStatusComplete) ||
		dataMode != string(completion.DataMode) ||
		modelID != run.ModelID ||
		promptVersion != run.PromptVersion ||
		!recordedAtEqual ||
		!currentlyAdmissible ||
		completedByOwner == nil ||
		*completedByOwner != completion.LeaseOwner ||
		!completedAtPresent ||
		currentVersionID == nil ||
		*currentVersionID != completion.VersionID {
		return errCompletedWriteSetMismatch
	}
	return nil
}
