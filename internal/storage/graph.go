package storage

import (
	"context"
	"crypto/sha256"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/JakeFAU/stacks/core/evidence"
	"github.com/JakeFAU/stacks/core/observation"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ObservationInput is a retry-stable observation record. ID is supplied by the
// caller so retries cannot create a second logical observation.
type ObservationInput struct {
	ID               string
	ExtractionRunID  string
	SubjectEntityID  string
	ObjectEntityID   string
	SubjectMentionID string
	ObjectMentionID  string
	Predicate        string
	ValidStart       *time.Time
	ValidEnd         *time.Time
	Derivation       string
	EpistemicStatus  string
	Confidence       *float64
}

// Observation identifies a durable temporal observation.
type Observation struct {
	ID string
}

// SignalInput is a retry-stable interaction signal. Completion requires at
// least one supporting transcript evidence span at transaction commit.
type SignalInput struct {
	ID                string
	ObservationID     string
	Category          string
	Direction         string
	ExtractionModelID string
	PromptVersion     string
	Rationale         string
	Confidence        float64
}

// InteractionSignal identifies a durable interaction signal.
type InteractionSignal struct {
	ID string
}

// SignalEvidenceInput assigns one exact citation its signal role.
type SignalEvidenceInput struct {
	EvidenceSpanID string
	Role           string
}

// GraphRepository persists observations and interaction signals in explicit
// completion transactions.
type GraphRepository struct {
	pool *pgxpool.Pool
}

// NewGraphRepository creates a graph repository backed by pool.
func NewGraphRepository(pool *pgxpool.Pool) *GraphRepository {
	return &GraphRepository{pool: pool}
}

// CompleteObservation atomically persists one canonical observation, its
// private observation-origin evidence, and its optional interaction signal.
func (repository *GraphRepository) CompleteObservation(
	ctx context.Context,
	value observation.Observation,
	observationEvidenceOrigin []evidence.EvidenceID,
	signal *SignalInput,
	signalEvidence []SignalEvidenceInput,
) (observation.Observation, *InteractionSignal, error) {
	canonicalObservationID, err := canonicalUUID(string(value.ID()))
	if err != nil {
		return observation.Observation{}, nil, newLegacyUUIDPreflightError(value.ID())
	}
	boundaryObservationID := observation.ObservationID(canonicalObservationID)
	origin, err := normalizeLegacyOrigin(observationEvidenceOrigin)
	if err != nil {
		return observation.Observation{}, nil, newLegacyUUIDPreflightError(value.ID())
	}
	state, err := canonicalSignalState(boundaryObservationID, signal, signalEvidence)
	if err != nil {
		return observation.Observation{}, nil, err
	}
	derivation := value.Derivation()
	if derivation.LegacyUnversioned {
		return observation.Observation{}, nil, newObservationBoundaryError(ErrObservationNotRepresentable, reasonLegacyDerivationNotRepresentable, canonicalObservationID)
	}
	if derivation.RunID == "" {
		return observation.Observation{}, nil, newObservationBoundaryError(ErrObservationCompatibility, reasonOwningRunRequired, canonicalObservationID)
	}
	compatibility := legacyObservationCompatibility{observationEvidenceOrigin: origin}
	preflight, err := preflightLegacyObservation(value, compatibility, state)
	if err != nil {
		return observation.Observation{}, nil, err
	}

	var stored observation.Observation
	var storedSignal *InteractionSignal
	err = repository.withTransaction(ctx, func(transaction pgx.Tx) error {
		run, err := loadOwningExtractionRun(ctx, transaction, preflight.derivationRunID, boundaryObservationID)
		if err != nil {
			return err
		}
		write, err := encodeLegacyObservation(value, compatibility, run, state)
		if err != nil {
			return err
		}
		stored, storedSignal, err = putLegacyObservation(ctx, transaction, write)
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return observation.Observation{}, nil, err
	}
	return stored, storedSignal, nil
}

func canonicalSignalState(
	observationID observation.ObservationID,
	signal *SignalInput,
	signalEvidence []SignalEvidenceInput,
) (*legacySignalState, error) {
	if signal == nil {
		if len(signalEvidence) != 0 {
			return nil, newObservationBoundaryError(ErrObservationCompatibility, reasonEvidenceOwnershipMismatch, string(observationID))
		}
		return nil, nil
	}
	input, links, err := canonicalizeSignalIdentity(*signal, signalEvidence)
	if err != nil {
		return nil, newLegacyUUIDPreflightError(observationID)
	}
	if err := validateSignalInput(input); err != nil {
		return nil, err
	}
	if len(links) == 0 {
		return nil, fmt.Errorf("complete signal %q: evidence is required", input.ID)
	}
	for _, link := range links {
		if !validSignalEvidenceRole(link.Role) {
			return nil, fmt.Errorf("complete signal %q: evidence input is invalid", input.ID)
		}
	}
	digest, err := ComputeSignalDigest(input, links)
	if err != nil {
		return nil, err
	}
	return &legacySignalState{Input: input, Evidence: links, Digest: digest}, nil
}

func loadOwningExtractionRun(
	ctx context.Context,
	transaction pgx.Tx,
	runID string,
	observationID observation.ObservationID,
) (*owningExtractionRun, error) {
	var run owningExtractionRun
	if err := transaction.QueryRow(ctx, `
		SELECT id::text, model_id, prompt_version, recorded_at
		FROM stacks.extraction_runs
		WHERE id = $1 AND currently_admissible`, runID).Scan(&run.ID, &run.ModelID, &run.PromptVersion, &run.RecordedAt); err != nil {
		if err != pgx.ErrNoRows {
			return nil, fmt.Errorf("load owning extraction run %q: %w", runID, err)
		}
		return nil, newObservationBoundaryError(ErrObservationCompatibility, reasonOwningRunNotAdmissible, string(observationID))
	}
	return &run, nil
}

func (repository *GraphRepository) withTransaction(ctx context.Context, work func(pgx.Tx) error) error {
	transaction, err := repository.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("start graph transaction: %w", err)
	}
	defer transaction.Rollback(ctx) //nolint:errcheck // committed transactions are already closed.
	if err := work(transaction); err != nil {
		return err
	}
	if err := transaction.Commit(ctx); err != nil {
		return fmt.Errorf("commit graph transaction: %w", err)
	}
	return nil
}

func putObservation(ctx context.Context, transaction pgx.Tx, input ObservationInput, digest []byte) (Observation, error) {
	var observation Observation
	err := transaction.QueryRow(ctx, `
		INSERT INTO stacks.observations
			(id, extraction_run_id, subject_entity_id, object_entity_id, subject_mention_id, object_mention_id, predicate, valid_start, valid_end, recorded_at, derivation, epistemic_status, confidence, digest, currently_admissible)
		VALUES ($1::uuid, NULLIF($2, '')::uuid, NULLIF($3, '')::uuid, NULLIF($4, '')::uuid, NULLIF($5, '')::uuid, NULLIF($6, '')::uuid, $7, $8, $9, $10, $11, $12, $13, $14, true)
		ON CONFLICT (id) DO NOTHING
		RETURNING id`,
		input.ID, input.ExtractionRunID, input.SubjectEntityID, input.ObjectEntityID, input.SubjectMentionID, input.ObjectMentionID,
		input.Predicate, input.ValidStart, input.ValidEnd, time.Now().UTC(), input.Derivation, input.EpistemicStatus, input.Confidence, digest).Scan(&observation.ID)
	if err == pgx.ErrNoRows {
		var storedDigest []byte
		err = transaction.QueryRow(ctx, `SELECT id, digest FROM stacks.observations WHERE id = $1`, input.ID).Scan(&observation.ID, &storedDigest)
		if err != nil {
			return Observation{}, fmt.Errorf("load observation %q: %w", input.ID, err)
		}
		if string(storedDigest) != string(digest) {
			return Observation{}, fmt.Errorf("load observation %q: immutable payload conflicts", input.ID)
		}
		return observation, nil
	}
	if err != nil {
		return Observation{}, fmt.Errorf("persist observation %q: %w", input.ID, err)
	}
	return observation, nil
}

func putSignal(ctx context.Context, transaction pgx.Tx, input SignalInput, digest []byte) (InteractionSignal, error) {
	var signal InteractionSignal
	err := transaction.QueryRow(ctx, `
		INSERT INTO stacks.interaction_signals
			(id, observation_id, category, direction, extraction_model_id, prompt_version, rationale, confidence, digest, currently_admissible)
		VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7, $8, $9, true)
		ON CONFLICT (id) DO NOTHING
		RETURNING id`,
		input.ID, input.ObservationID, input.Category, input.Direction, input.ExtractionModelID, input.PromptVersion, input.Rationale, input.Confidence, digest).Scan(&signal.ID)
	if err == pgx.ErrNoRows {
		var storedDigest []byte
		err = transaction.QueryRow(ctx, `SELECT id, digest FROM stacks.interaction_signals WHERE id = $1`, input.ID).Scan(&signal.ID, &storedDigest)
		if err != nil {
			return InteractionSignal{}, fmt.Errorf("load signal %q: %w", input.ID, err)
		}
		if string(storedDigest) != string(digest) {
			return InteractionSignal{}, fmt.Errorf("load signal %q: immutable payload conflicts", input.ID)
		}
		return signal, nil
	}
	if err != nil {
		return InteractionSignal{}, fmt.Errorf("persist signal %q: %w", input.ID, err)
	}
	return signal, nil
}

func validateObservationInput(input ObservationInput) error {
	if strings.TrimSpace(input.ID) == "" || strings.TrimSpace(input.Predicate) == "" || strings.TrimSpace(input.Derivation) == "" {
		return fmt.Errorf("complete observation: ID, predicate, and derivation are required")
	}
	if !validEpistemicStatus(input.EpistemicStatus) {
		return fmt.Errorf("complete observation %q: epistemic status is invalid", input.ID)
	}
	if input.ValidEnd != nil && (input.ValidStart == nil || input.ValidEnd.Before(*input.ValidStart)) {
		return fmt.Errorf("complete observation %q: valid time range is invalid", input.ID)
	}
	if input.Confidence != nil && !isFinite(*input.Confidence) {
		return fmt.Errorf("complete observation %q: confidence is invalid", input.ID)
	}
	return nil
}

func validateSignalInput(input SignalInput) error {
	if strings.TrimSpace(input.ID) == "" || strings.TrimSpace(input.ObservationID) == "" || strings.TrimSpace(input.ExtractionModelID) == "" || strings.TrimSpace(input.PromptVersion) == "" {
		return fmt.Errorf("complete signal: ID, observation ID, model ID, and prompt version are required")
	}
	if !validSignalCategory(input.Category) || !validSignalDirection(input.Direction) || !isFinite(input.Confidence) {
		return fmt.Errorf("complete signal %q: category, direction, or confidence is invalid", input.ID)
	}
	return nil
}

func validEpistemicStatus(status string) bool {
	switch status {
	case "observed", "inferred", "hypothesized", "validated_structurally", "validated_empirically", "rejected":
		return true
	default:
		return false
	}
}

func validSignalCategory(category string) bool {
	switch category {
	case "delegation_autonomy", "scrutiny_correction", "endorsement_trust", "support_advocacy", "future_responsibility":
		return true
	default:
		return false
	}
}

func validSignalDirection(direction string) bool {
	switch direction {
	case "strengthening", "weakening", "mixed", "unclear":
		return true
	default:
		return false
	}
}

func validSignalEvidenceRole(role string) bool {
	return role == "supporting" || role == "contradicting"
}

func isFinite(value float64) bool {
	return !math.IsInf(value, 0) && !math.IsNaN(value)
}

// ComputeObservationDigest derives a semantic observation identity. The stable
// row ID is intentionally excluded; evidence IDs are a canonical set.
func ComputeObservationDigest(input ObservationInput, evidenceSpanIDs []string) ([sha256.Size]byte, error) {
	canonicalInput, canonicalEvidenceSpanIDs, err := canonicalizeObservationIdentity(input, evidenceSpanIDs)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	input = canonicalInput
	fields := []string{input.ExtractionRunID, input.SubjectEntityID, input.ObjectEntityID, input.SubjectMentionID, input.ObjectMentionID, input.Predicate, input.Derivation, input.EpistemicStatus}
	if input.ValidStart != nil {
		fields = append(fields, input.ValidStart.UTC().Format(time.RFC3339Nano))
	} else {
		fields = append(fields, "")
	}
	if input.ValidEnd != nil {
		fields = append(fields, input.ValidEnd.UTC().Format(time.RFC3339Nano))
	} else {
		fields = append(fields, "")
	}
	if input.Confidence != nil {
		fields = append(fields, fmt.Sprintf("%.17g", *input.Confidence))
	} else {
		fields = append(fields, "")
	}
	fields = append(fields, canonicalEvidenceSpanIDs...)
	return sha256.Sum256([]byte(strings.Join(fields, "\x00"))), nil
}

// ComputeSignalDigest derives a semantic signal identity. The stable row ID is
// intentionally excluded; evidence ID/role pairs are a canonical set.
func ComputeSignalDigest(input SignalInput, evidence []SignalEvidenceInput) ([sha256.Size]byte, error) {
	canonicalInput, canonicalEvidence, err := canonicalizeSignalIdentity(input, evidence)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	input = canonicalInput
	fields := []string{input.ObservationID, input.Category, input.Direction, input.ExtractionModelID, input.PromptVersion, input.Rationale, fmt.Sprintf("%.17g", input.Confidence)}
	for _, signalEvidence := range canonicalEvidence {
		fields = append(fields, signalEvidence.EvidenceSpanID, signalEvidence.Role)
	}
	return sha256.Sum256([]byte(strings.Join(fields, "\x00"))), nil
}

func canonicalizeObservationIdentity(input ObservationInput, evidenceSpanIDs []string) (ObservationInput, []string, error) {
	canonicalID, err := canonicalUUID(input.ID)
	if err != nil {
		return ObservationInput{}, nil, fmt.Errorf("complete observation: ID is invalid")
	}
	input.ID = canonicalID
	for field, value := range map[string]*string{
		"extraction run ID":  &input.ExtractionRunID,
		"subject entity ID":  &input.SubjectEntityID,
		"object entity ID":   &input.ObjectEntityID,
		"subject mention ID": &input.SubjectMentionID,
		"object mention ID":  &input.ObjectMentionID,
	} {
		if *value == "" {
			continue
		}
		canonicalValue, err := canonicalUUID(*value)
		if err != nil {
			return ObservationInput{}, nil, fmt.Errorf("complete observation %q: %s is invalid", input.ID, field)
		}
		*value = canonicalValue
	}
	canonicalEvidence, err := canonicalEvidenceIDs(evidenceSpanIDs)
	if err != nil {
		return ObservationInput{}, nil, fmt.Errorf("complete observation %q: %w", input.ID, err)
	}
	return input, canonicalEvidence, nil
}

func canonicalizeSignalIdentity(input SignalInput, evidence []SignalEvidenceInput) (SignalInput, []SignalEvidenceInput, error) {
	canonicalID, err := canonicalUUID(input.ID)
	if err != nil {
		return SignalInput{}, nil, fmt.Errorf("complete signal: ID is invalid")
	}
	canonicalObservationID, err := canonicalUUID(input.ObservationID)
	if err != nil {
		return SignalInput{}, nil, fmt.Errorf("complete signal %q: observation ID is invalid", canonicalID)
	}
	input.ID = canonicalID
	input.ObservationID = canonicalObservationID
	canonicalEvidence := make([]SignalEvidenceInput, 0, len(evidence))
	seen := make(map[string]struct{}, len(evidence))
	for _, signalEvidence := range evidence {
		canonicalEvidenceID, err := canonicalUUID(signalEvidence.EvidenceSpanID)
		if err != nil {
			return SignalInput{}, nil, fmt.Errorf("complete signal %q: evidence span ID is invalid", input.ID)
		}
		key := canonicalEvidenceID + "\x00" + signalEvidence.Role
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		canonicalEvidence = append(canonicalEvidence, SignalEvidenceInput{EvidenceSpanID: canonicalEvidenceID, Role: signalEvidence.Role})
	}
	sort.Slice(canonicalEvidence, func(left, right int) bool {
		if canonicalEvidence[left].EvidenceSpanID == canonicalEvidence[right].EvidenceSpanID {
			return canonicalEvidence[left].Role < canonicalEvidence[right].Role
		}
		return canonicalEvidence[left].EvidenceSpanID < canonicalEvidence[right].EvidenceSpanID
	})
	return input, canonicalEvidence, nil
}

func canonicalEvidenceIDs(evidenceSpanIDs []string) ([]string, error) {
	canonicalEvidence := make([]string, 0, len(evidenceSpanIDs))
	seen := make(map[string]struct{}, len(evidenceSpanIDs))
	for _, evidenceSpanID := range evidenceSpanIDs {
		canonicalID, err := canonicalUUID(evidenceSpanID)
		if err != nil {
			return nil, fmt.Errorf("evidence span ID is invalid")
		}
		if _, exists := seen[canonicalID]; exists {
			continue
		}
		seen[canonicalID] = struct{}{}
		canonicalEvidence = append(canonicalEvidence, canonicalID)
	}
	sort.Strings(canonicalEvidence)
	return canonicalEvidence, nil
}
