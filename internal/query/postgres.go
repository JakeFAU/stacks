package query

import (
	"context"
	"errors"
	"slices"

	postgres "github.com/JakeFAU/stacks/adapters/postgres"
	"github.com/JakeFAU/stacks/core/temporal"
)

// PostgresRepository translates PostgreSQL adapter snapshots into the
// provider-neutral query reader contract.
type PostgresRepository struct {
	Database interface {
		LoadTemporalQuerySnapshot(
			context.Context,
			postgres.TemporalQuerySelection,
			postgres.TemporalSnapshotObserver,
		) (postgres.TemporalQuerySnapshot, error)
	}
	SnapshotObserver postgres.TemporalSnapshotObserver
}

var _ Reader = PostgresRepository{}

// Read loads one coherent adapter snapshot and maps it without applying
// valid-time, aggregation, or state-selection policy.
func (repository PostgresRepository) Read(
	ctx context.Context,
	selection ReadSelection,
) (ReadSnapshot, error) {
	if repository.Database == nil {
		return ReadSnapshot{}, boundedPostgresReadError{
			operation: "validate database",
			cause:     errors.New("database is required"),
		}
	}
	adapterSelection, err := postgresTemporalQuerySelection(selection)
	if err != nil {
		return ReadSnapshot{}, err
	}
	snapshot, err := repository.Database.LoadTemporalQuerySnapshot(
		ctx,
		adapterSelection,
		repository.SnapshotObserver,
	)
	if err != nil {
		return ReadSnapshot{}, boundedPostgresReadError{
			operation: "load temporal snapshot",
			cause:     err,
		}
	}
	result, err := readSnapshotFromPostgres(snapshot)
	if err != nil {
		return ReadSnapshot{}, err
	}
	return result, nil
}

func postgresTemporalQuerySelection(
	selection ReadSelection,
) (postgres.TemporalQuerySelection, error) {
	match, err := postgresTemporalEntityMatch(selection.EntityMatch)
	if err != nil {
		return postgres.TemporalQuerySelection{}, err
	}
	result := postgres.TemporalQuerySelection{
		EntityIDs:   slices.Clone(selection.EntityIDs),
		EntityMatch: match,
		Predicates:  slices.Clone(selection.Predicates),
		Selections:  slices.Clone(selection.Selections),
	}
	switch selection.KnowledgeScope.Kind() {
	case temporal.KnowledgeCurrent:
	case temporal.KnowledgeAsOf:
		cutoff, ok := selection.KnowledgeScope.AsOf()
		if !ok {
			return postgres.TemporalQuerySelection{}, boundedPostgresReadError{
				operation: "translate knowledge scope",
				cause:     errors.New("knowledge cutoff is invalid"),
			}
		}
		result.KnowledgeAsOf = &cutoff
	default:
		return postgres.TemporalQuerySelection{}, boundedPostgresReadError{
			operation: "translate knowledge scope",
			cause:     errors.New("knowledge scope is invalid"),
		}
	}
	return result, nil
}

func postgresTemporalEntityMatch(
	match EntityMatch,
) (postgres.TemporalEntityMatch, error) {
	switch match {
	case EntityMatchAll:
		return postgres.TemporalEntityMatchAll, nil
	case EntityMatchAny:
		return postgres.TemporalEntityMatchAny, nil
	default:
		return 0, boundedPostgresReadError{
			operation: "translate entity match",
			cause:     errors.New("entity match is invalid"),
		}
	}
}

func readSnapshotFromPostgres(
	snapshot postgres.TemporalQuerySnapshot,
) (ReadSnapshot, error) {
	entities := make([]EntityAuthority, len(snapshot.Entities))
	for index, record := range snapshot.Entities {
		entities[index] = EntityAuthority{
			EntityID: record.EntityID,
			Known:    record.Known,
		}
	}
	observations := make([]ReadObservation, len(snapshot.Observations))
	for index, record := range snapshot.Observations {
		observations[index] = ReadObservation{
			Observation:               record.Observation,
			Subject:                   record.Subject,
			Object:                    record.Object,
			SubjectGroundingMentionID: record.SubjectGroundingMentionID,
			ObjectGroundingMentionID:  record.ObjectGroundingMentionID,
			Evidence:                  citationsFromPostgres(record.Evidence),
		}
	}
	coverage := make([]Coverage, len(snapshot.Coverage))
	for index, record := range snapshot.Coverage {
		reason, err := coverageReasonFromPostgres(record.Reason)
		if err != nil {
			return ReadSnapshot{}, err
		}
		coverage[index] = Coverage{
			Reason:        reason,
			EntityID:      record.EntityID,
			Predicate:     record.Predicate,
			ObservationID: record.ObservationID,
			ValidTime:     record.ValidTime,
		}
	}
	return ReadSnapshot{
		Entities:     preserveNilEntities(snapshot.Entities, entities),
		Observations: preserveNilObservations(snapshot.Observations, observations),
		Coverage:     preserveNilCoverage(snapshot.Coverage, coverage),
	}, nil
}

func citationsFromPostgres(
	records []postgres.TemporalEvidenceRecord,
) []Citation {
	if records == nil {
		return nil
	}
	result := make([]Citation, len(records))
	for index, record := range records {
		result[index] = Citation{
			EvidenceID:        record.EvidenceID,
			Role:              record.Role,
			SourceDocumentID:  record.SourceDocumentID,
			DocumentVersionID: record.DocumentVersionID,
			SectionID:         record.SectionID,
			SectionTitle:      record.SectionTitle,
			SectionPath:       slices.Clone(record.SectionPath),
			SectionOrder:      record.SectionOrder,
			SectionRole:       record.SectionRole,
			StartOffset:       record.StartOffset,
			EndOffset:         record.EndOffset,
			Locator:           record.Locator,
			Text:              record.Text,
		}
	}
	return result
}

func coverageReasonFromPostgres(
	reason postgres.TemporalCoverageReason,
) (CoverageReason, error) {
	switch reason {
	case postgres.TemporalCoverageUnresolvedMention:
		return CoverageUnresolvedMention, nil
	case postgres.TemporalCoverageAuthorityExcluded:
		return CoverageAuthorityExcluded, nil
	case postgres.TemporalCoverageEntityFiltered:
		return CoverageEntityFiltered, nil
	case postgres.TemporalCoveragePredicateFiltered:
		return CoveragePredicateFiltered, nil
	default:
		return "", boundedPostgresReadError{
			operation: "translate temporal coverage",
			cause:     errors.New("coverage reason is invalid"),
		}
	}
}

func preserveNilEntities(
	source []postgres.TemporalEntityRecord,
	result []EntityAuthority,
) []EntityAuthority {
	if source == nil {
		return nil
	}
	return result
}

func preserveNilObservations(
	source []postgres.TemporalObservationRecord,
	result []ReadObservation,
) []ReadObservation {
	if source == nil {
		return nil
	}
	return result
}

func preserveNilCoverage(
	source []postgres.TemporalCoverageRecord,
	result []Coverage,
) []Coverage {
	if source == nil {
		return nil
	}
	return result
}

type boundedPostgresReadError struct {
	operation string
	cause     error
}

func (err boundedPostgresReadError) Error() string {
	return "read PostgreSQL temporal snapshot: " + err.operation + " failed"
}

func (err boundedPostgresReadError) Unwrap() error {
	return err.cause
}
