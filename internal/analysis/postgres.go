package analysis

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	postgres "github.com/JakeFAU/stacks/adapters/postgres"
	"github.com/JakeFAU/stacks/core/identity"
	"github.com/JakeFAU/stacks/core/observation"
)

const transcriptSectionRole = "transcript"

type relationshipSnapshotLoader interface {
	LoadRelationshipSnapshot(
		context.Context,
		identity.EntityID,
		identity.EntityID,
	) (postgres.RelationshipSnapshot, error)
}

// PostgresRepository translates generic canonical relationship observations
// into the temporary manager-confidence query contract.
type PostgresRepository struct {
	Database relationshipSnapshotLoader
}

var _ Repository = PostgresRepository{}

// LoadPairInputs reads the configured manager-subject/employee-object pair.
func (repository PostgresRepository) LoadPairInputs(
	ctx context.Context,
	employeeID string,
	managerID string,
) (PairSnapshot, error) {
	if repository.Database == nil {
		return PairSnapshot{}, fmt.Errorf(
			"load canonical manager inputs: database is required",
		)
	}
	employeeID = strings.TrimSpace(employeeID)
	managerID = strings.TrimSpace(managerID)
	if employeeID == "" || managerID == "" || employeeID == managerID {
		return PairSnapshot{}, fmt.Errorf(
			"load canonical manager inputs: distinct employee and manager IDs are required",
		)
	}
	snapshot, err := repository.Database.LoadRelationshipSnapshot(
		ctx,
		identity.EntityID(managerID),
		identity.EntityID(employeeID),
	)
	if err != nil {
		return PairSnapshot{}, fmt.Errorf(
			"load canonical relationship snapshot: %w",
			err,
		)
	}
	result := PairSnapshot{
		Accepted: snapshot.SubjectAccepted && snapshot.ObjectAccepted,
	}
	if !result.Accepted {
		return result, nil
	}
	result.Signals = make([]Signal, 0, len(snapshot.Observations))
	for _, record := range snapshot.Observations {
		signal, mapped, err := canonicalManagerSignal(record)
		if err != nil {
			return PairSnapshot{}, fmt.Errorf(
				"translate canonical manager observation %q: %w",
				record.Observation.ID(),
				err,
			)
		}
		if mapped {
			result.Signals = append(result.Signals, signal)
		}
	}
	sort.Slice(result.Signals, func(left, right int) bool {
		if result.Signals[left].RecordedAt.Equal(result.Signals[right].RecordedAt) {
			return result.Signals[left].ID < result.Signals[right].ID
		}
		return result.Signals[left].RecordedAt.Before(
			result.Signals[right].RecordedAt,
		)
	})
	return result, nil
}

func canonicalManagerSignal(
	record postgres.ObservationRecord,
) (Signal, bool, error) {
	value := record.Observation
	predicate := value.Statement().Predicate
	namespacePrefix := interactionObservationPredicateNamespace + "/"
	if !strings.HasPrefix(string(predicate), namespacePrefix) {
		return Signal{}, false, nil
	}
	category, direction, err := ParseInteractionObservationPredicate(predicate)
	if err != nil {
		return Signal{}, false, fmt.Errorf(
			"manager-interaction predicate is invalid",
		)
	}
	confidence, present := value.Confidence()
	if !present || confidence.Scale() != observation.ConfidenceUnitInterval {
		return Signal{}, false, fmt.Errorf(
			"manager-interaction confidence must use the unit-interval scale",
		)
	}
	validTime, err := managerSignalValidTime(value.ValidTime())
	if err != nil {
		return Signal{}, false, err
	}
	citations := make([]Citation, len(record.Evidence))
	supportingTranscriptDocuments := make(map[string]struct{})
	transcriptBacked := false
	for index, evidenceRecord := range record.Evidence {
		citationRole, err := managerCitationRole(evidenceRecord.Role)
		if err != nil {
			return Signal{}, false, err
		}
		transcript := evidenceRecord.SectionRole == transcriptSectionRole
		if citationRole == CitationSupporting && transcript {
			transcriptBacked = true
			supportingTranscriptDocuments[evidenceRecord.SourceDocumentID] = struct{}{}
		}
		citations[index] = Citation{
			ID:                 string(evidenceRecord.Span.ID()),
			ProviderDocumentID: evidenceRecord.Span.ProviderDocumentID(),
			ProviderTabID:      evidenceRecord.SectionID,
			StartOffset:        evidenceRecord.Span.StartOffset(),
			EndOffset:          evidenceRecord.Span.EndOffset(),
			Quote:              evidenceRecord.Span.Text(),
			Locator:            evidenceRecord.Span.Locator(),
			Role:               citationRole,
			SectionRole:        evidenceRecord.SectionRole,
			Transcript:         transcript,
		}
	}
	sort.Slice(citations, func(left, right int) bool {
		if citations[left].ID == citations[right].ID {
			return citations[left].Role < citations[right].Role
		}
		return citations[left].ID < citations[right].ID
	})
	meetingID := ""
	if len(supportingTranscriptDocuments) == 1 {
		for sourceDocumentID := range supportingTranscriptDocuments {
			meetingID = sourceDocumentID
		}
	}
	id := string(value.ID())
	return Signal{
		ID:               id,
		MeetingID:        meetingID,
		ObservationID:    id,
		Category:         category,
		Direction:        direction,
		ValidTime:        validTime,
		RecordedAt:       value.RecordedAt(),
		Rationale:        ExplainSignal(category, direction),
		Confidence:       confidence.Value(),
		Validated:        true,
		TranscriptBacked: transcriptBacked,
		Citations:        citations,
	}, true, nil
}

func managerSignalValidTime(
	extent observation.TemporalExtent,
) (*time.Time, error) {
	switch extent.Kind() {
	case observation.TemporalUnknown:
		return nil, nil
	case observation.TemporalInstant:
		value, ok := extent.Instant()
		if !ok {
			return nil, fmt.Errorf(
				"manager-interaction temporal shape is invalid",
			)
		}
		return &value, nil
	default:
		return nil, fmt.Errorf(
			"manager-interaction temporal shape must be unknown or instant",
		)
	}
}

func managerCitationRole(role observation.EvidenceRole) (CitationRole, error) {
	switch role {
	case observation.EvidenceSupporting:
		return CitationSupporting, nil
	case observation.EvidenceContradicting:
		return CitationContradicting, nil
	default:
		return "", fmt.Errorf("manager-interaction evidence role is invalid")
	}
}
