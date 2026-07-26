package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/JakeFAU/stacks/core/admission"
	"github.com/JakeFAU/stacks/core/evidence"
	"github.com/JakeFAU/stacks/core/identity"
	"github.com/JakeFAU/stacks/core/observation"
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

	rows, err := database.pool.Query(ctx, `
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
	for _, id := range observationIDs {
		value, err := loadObservationValue(ctx, database.pool, id)
		if err != nil {
			return nil, wrapObservationError(ctx, "load admitted observation", err)
		}
		resolvedSubject, subjectResolved, err := database.resolveObservationTerm(
			ctx,
			value.Statement().Subject,
		)
		if err != nil {
			return nil, err
		}
		resolvedObject, objectResolved, err := database.resolveObservationTerm(
			ctx,
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
			database.pool,
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
			database.pool,
			value,
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

func (database *Database) resolveObservationTerm(
	ctx context.Context,
	term observation.Term,
) (identity.EntityID, bool, error) {
	if entityID, _, ok := term.Entity(); ok {
		return identity.EntityID(entityID), true, nil
	}
	mentionID, ok := term.MentionID()
	if !ok {
		return "", false, nil
	}
	rows, err := database.pool.Query(ctx, `
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

func loadObservationEvidenceRecords(
	ctx context.Context,
	reader documentReader,
	value observation.Observation,
) ([]ObservationEvidenceRecord, error) {
	links := value.EvidenceLinks()
	records := make([]ObservationEvidenceRecord, 0, len(links))
	for _, link := range links {
		span, err := loadEvidenceSpan(ctx, reader, link.EvidenceID)
		if err != nil {
			return nil, err
		}
		var record ObservationEvidenceRecord
		record.Span = span
		record.Role = link.Role
		if err := reader.QueryRow(ctx, `
			SELECT
				source.id,
				evidence.document_version_id,
				section.section_id,
				section.title,
				section.path,
				section.section_order,
				section.role
			FROM stacks_core.evidence_spans AS evidence
			JOIN stacks_core.document_versions AS version
			  ON version.id = evidence.document_version_id
			JOIN stacks_core.source_documents AS source
			  ON source.id = version.source_document_id
			JOIN stacks_core.document_sections AS section
			  ON section.document_version_id = evidence.document_version_id
			 AND section.section_id = evidence.section_id
			WHERE evidence.id = $1`,
			link.EvidenceID,
		).Scan(
			&record.SourceDocumentID,
			&record.DocumentVersionID,
			&record.SectionID,
			&record.SectionTitle,
			&record.SectionPath,
			&record.SectionOrder,
			&record.SectionRole,
		); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}
