package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/JakeFAU/stacks/core/admission"
	"github.com/JakeFAU/stacks/core/evidence"
	"github.com/JakeFAU/stacks/core/identity"
	"github.com/JakeFAU/stacks/core/observation"
	"github.com/jackc/pgx/v5"
)

// ObservationEvidenceRecord carries one explicit evidence role together with
// the exact span and provider-neutral source/version/section provenance.
type ObservationEvidenceRecord struct {
	Span              evidence.EvidenceSpan
	Role              observation.EvidenceRole
	SourceDocumentID  string
	DocumentVersionID string
	SectionID         string
	SectionTitle      string
	SectionPath       []string
	SectionOrder      int
	SectionRole       string
}

// ObservationRecord is a generic admitted relationship projection. The
// canonical observation remains unchanged while mention terms are resolved
// through current effective identity authority.
type ObservationRecord struct {
	Observation        observation.Observation
	Evidence           []ObservationEvidenceRecord
	SubjectEntityID    identity.EntityID
	ObjectEntityID     identity.EntityID
	EffectiveAdmission admission.Decision
}

// RelationshipSnapshot is one coherent read of current identity authority and
// admitted canonical observations for an ordered entity pair.
type RelationshipSnapshot struct {
	SubjectAccepted bool
	ObjectAccepted  bool
	Observations    []ObservationRecord
}

// LoadRelationshipSnapshot reads current pair authority and relationship
// observations in one read-only repeatable-read transaction.
func (database *Database) LoadRelationshipSnapshot(
	ctx context.Context,
	subjectEntityID identity.EntityID,
	objectEntityID identity.EntityID,
) (RelationshipSnapshot, error) {
	if err := contextRequired(ctx, "load relationship snapshot"); err != nil {
		return RelationshipSnapshot{}, err
	}
	if database == nil || database.pool == nil {
		return RelationshipSnapshot{}, fmt.Errorf(
			"load relationship snapshot: database is closed",
		)
	}
	subjectEntityID = identity.EntityID(strings.TrimSpace(string(subjectEntityID)))
	objectEntityID = identity.EntityID(strings.TrimSpace(string(objectEntityID)))
	if subjectEntityID == "" ||
		objectEntityID == "" ||
		subjectEntityID == objectEntityID {
		return RelationshipSnapshot{}, fmt.Errorf(
			"load relationship snapshot: distinct subject and object entity IDs are required",
		)
	}
	transaction, err := database.pool.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return RelationshipSnapshot{}, wrapObservationError(
			ctx,
			"start relationship snapshot",
			err,
		)
	}
	defer transaction.Rollback(ctx) //nolint:errcheck // committed transactions are already closed.

	snapshot := RelationshipSnapshot{}
	snapshot.SubjectAccepted, snapshot.ObjectAccepted, err = loadCurrentAcceptedIdentityAuthorities(
		ctx,
		transaction,
		subjectEntityID,
		objectEntityID,
	)
	if err != nil {
		return RelationshipSnapshot{}, wrapIdentityError(
			ctx,
			"load relationship identity authority",
			err,
		)
	}
	if snapshot.SubjectAccepted && snapshot.ObjectAccepted {
		snapshot.Observations, err = listAdmittedRelationshipObservations(
			ctx,
			transaction,
			subjectEntityID,
			objectEntityID,
		)
		if err != nil {
			return RelationshipSnapshot{}, err
		}
	}
	if err := transaction.Commit(ctx); err != nil {
		return RelationshipSnapshot{}, wrapObservationError(
			ctx,
			"commit relationship snapshot",
			err,
		)
	}
	return snapshot, nil
}

// ListAdmittedRelationshipObservations returns deterministic current
// relationship projections for the ordered subject/object entity pair.
func (database *Database) ListAdmittedRelationshipObservations(
	ctx context.Context,
	subjectEntityID identity.EntityID,
	objectEntityID identity.EntityID,
) ([]ObservationRecord, error) {
	if err := contextRequired(ctx, "list admitted relationship observations"); err != nil {
		return nil, err
	}
	if database == nil || database.pool == nil {
		return nil, fmt.Errorf(
			"list admitted relationship observations: database is closed",
		)
	}
	if strings.TrimSpace(string(subjectEntityID)) == "" ||
		strings.TrimSpace(string(objectEntityID)) == "" {
		return nil, fmt.Errorf(
			"list admitted relationship observations: subject and object entity IDs are required",
		)
	}
	return listAdmittedRelationshipObservations(
		ctx,
		database.pool,
		subjectEntityID,
		objectEntityID,
	)
}

func listAdmittedRelationshipObservations(
	ctx context.Context,
	reader documentReader,
	subjectEntityID identity.EntityID,
	objectEntityID identity.EntityID,
) ([]ObservationRecord, error) {
	rows, err := reader.Query(ctx, `
		SELECT value.id
		FROM stacks_core.observations AS value
		JOIN stacks_core.admission_decisions AS decision
		  ON decision.target_kind = 'observation'
		 AND decision.target_id = value.id
		 AND decision.outcome = 'admitted'
		WHERE NOT EXISTS (
			SELECT 1
			FROM stacks_core.admission_decisions AS successor
			WHERE successor.supersedes_id = decision.id
		)
		ORDER BY value.recorded_at, value.id`)
	if err != nil {
		return nil, wrapObservationError(
			ctx,
			"list admitted observation IDs",
			err,
		)
	}
	defer rows.Close()
	var observationIDs []observation.ObservationID
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, wrapObservationError(
				ctx,
				"scan admitted observation ID",
				err,
			)
		}
		observationIDs = append(observationIDs, observation.ObservationID(id))
	}
	if err := rows.Err(); err != nil {
		return nil, wrapObservationError(
			ctx,
			"iterate admitted observation IDs",
			err,
		)
	}
	rows.Close()

	records := make([]ObservationRecord, 0, len(observationIDs))
	// Document versions are immutable, so one validated record can serve every
	// evidence span that cites it during this relationship read.
	documentVersions := make(map[string]DocumentVersionRecord)
	for _, id := range observationIDs {
		value, err := loadObservationValue(ctx, reader, id)
		if err != nil {
			return nil, wrapObservationError(ctx, "load admitted observation", err)
		}
		resolvedSubject, subjectResolved, err := resolveObservationTerm(
			ctx,
			reader,
			value.Statement().Subject,
		)
		if err != nil {
			return nil, err
		}
		resolvedObject, objectResolved, err := resolveObservationTerm(
			ctx,
			reader,
			value.Statement().Object,
		)
		if err != nil {
			return nil, err
		}
		if !subjectResolved ||
			!objectResolved ||
			resolvedSubject != subjectEntityID ||
			resolvedObject != objectEntityID {
			continue
		}
		effectiveAdmission, err := loadEffectiveAdmissionDecisionValue(
			ctx,
			reader,
			admission.TargetObservation,
			string(id),
		)
		if err != nil {
			return nil, wrapAdmissionReadError(
				ctx,
				"load observation admission",
				err,
			)
		}
		if effectiveAdmission.Outcome() != admission.Admitted {
			continue
		}
		evidenceRecords, err := loadObservationEvidenceRecords(
			ctx,
			reader,
			value,
			documentVersions,
		)
		if err != nil {
			return nil, wrapObservationError(
				ctx,
				"load observation evidence provenance",
				err,
			)
		}
		records = append(records, ObservationRecord{
			Observation:        value,
			Evidence:           evidenceRecords,
			SubjectEntityID:    resolvedSubject,
			ObjectEntityID:     resolvedObject,
			EffectiveAdmission: effectiveAdmission,
		})
	}
	return records, nil
}

func resolveObservationTerm(
	ctx context.Context,
	reader documentReader,
	term observation.Term,
) (identity.EntityID, bool, error) {
	if entityID, _, ok := term.Entity(); ok {
		return identity.EntityID(entityID), true, nil
	}
	mentionID, ok := term.MentionID()
	if !ok {
		return "", false, nil
	}
	rows, err := reader.Query(ctx, `
		SELECT DISTINCT decision.entity_id
		FROM stacks_core.resolution_proposals AS proposal
		JOIN stacks_core.resolution_decisions AS decision
		  ON decision.proposal_id = proposal.id
		 AND decision.outcome = 'accepted'
		WHERE proposal.mention_id = $1
		  AND NOT EXISTS (
			SELECT 1
			FROM stacks_core.resolution_decisions AS successor
			WHERE successor.supersedes_id = decision.id
		  )
		ORDER BY decision.entity_id`,
		mentionID,
	)
	if err != nil {
		return "", false, wrapIdentityError(
			ctx,
			"resolve observation mention",
			err,
		)
	}
	defer rows.Close()
	var entityIDs []identity.EntityID
	for rows.Next() {
		var entityID string
		if err := rows.Scan(&entityID); err != nil {
			return "", false, wrapIdentityError(
				ctx,
				"scan observation mention authority",
				err,
			)
		}
		entityIDs = append(entityIDs, identity.EntityID(entityID))
	}
	if err := rows.Err(); err != nil {
		return "", false, wrapIdentityError(
			ctx,
			"iterate observation mention authority",
			err,
		)
	}
	if len(entityIDs) != 1 {
		return "", false, nil
	}
	return entityIDs[0], true, nil
}

func loadCurrentAcceptedIdentityAuthorities(
	ctx context.Context,
	reader documentReader,
	subjectEntityID identity.EntityID,
	objectEntityID identity.EntityID,
) (bool, bool, error) {
	var subjectAccepted, objectAccepted bool
	if err := reader.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM stacks_core.entities AS entity
			JOIN stacks_core.resolution_decisions AS decision
			  ON decision.entity_id = entity.id
			 AND decision.outcome = 'accepted'
			WHERE entity.id = $1
			  AND entity.kind = 'person'
			  AND NOT EXISTS (
				SELECT 1
				FROM stacks_core.resolution_decisions AS successor
				WHERE successor.supersedes_id = decision.id
			  )
		),
		EXISTS (
			SELECT 1
			FROM stacks_core.entities AS entity
			JOIN stacks_core.resolution_decisions AS decision
			  ON decision.entity_id = entity.id
			 AND decision.outcome = 'accepted'
			WHERE entity.id = $2
			  AND entity.kind = 'person'
			  AND NOT EXISTS (
				SELECT 1
				FROM stacks_core.resolution_decisions AS successor
				WHERE successor.supersedes_id = decision.id
			  )
		)`,
		subjectEntityID,
		objectEntityID,
	).Scan(&subjectAccepted, &objectAccepted); err != nil {
		return false, false, err
	}
	return subjectAccepted, objectAccepted, nil
}

func loadObservationEvidenceRecords(
	ctx context.Context,
	reader documentReader,
	value observation.Observation,
	documentVersions map[string]DocumentVersionRecord,
) ([]ObservationEvidenceRecord, error) {
	links := value.EvidenceLinks()
	records := make([]ObservationEvidenceRecord, 0, len(links))
	for _, link := range links {
		record, err := loadObservationEvidenceRecord(
			ctx,
			reader,
			link,
			documentVersions,
		)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}

func loadObservationEvidenceRecord(
	ctx context.Context,
	reader documentReader,
	link observation.EvidenceLink,
	documentVersions map[string]DocumentVersionRecord,
) (ObservationEvidenceRecord, error) {
	span, document, err := loadEvidenceSpanAndDocumentWithCache(
		ctx,
		reader,
		link.EvidenceID,
		documentVersions,
	)
	if err != nil {
		return ObservationEvidenceRecord{}, err
	}
	for _, section := range document.Version.Sections() {
		if section.ID() != span.SectionID() {
			continue
		}
		return ObservationEvidenceRecord{
			Span:              span,
			Role:              link.Role,
			SourceDocumentID:  document.Ref.SourceDocumentID,
			DocumentVersionID: document.Ref.VersionID,
			SectionID:         section.ID(),
			SectionTitle:      section.Title(),
			SectionPath:       section.Path(),
			SectionOrder:      section.Order(),
			SectionRole:       section.Role(),
		}, nil
	}
	return ObservationEvidenceRecord{}, fmt.Errorf(
		"stored evidence section: %w",
		ErrConflict,
	)
}
