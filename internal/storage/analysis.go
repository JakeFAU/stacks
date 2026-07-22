package storage

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	analysisdomain "stacks/internal/analysis"
)

const analysisInputDigestLength = 32

// AnalysisInput is the stable completed-analysis identity. Its digest is
// computed by deterministic analysis policy from the ordered inputs and
// versions before crossing the storage boundary.
type AnalysisInput struct {
	EmployeeEntityID      string
	ManagerEntityID       string
	AnalysisPromptVersion string
	PolicyVersion         string
	Inputs                []AnalysisInputReference
}

// AnalysisInputKind identifies a durable analysis input class.
type AnalysisInputKind string

const (
	AnalysisInputKindDocumentVersion    AnalysisInputKind = "document_version"
	AnalysisInputKindDocumentTab        AnalysisInputKind = "document_tab"
	AnalysisInputKindObservation        AnalysisInputKind = "observation"
	AnalysisInputKindSignal             AnalysisInputKind = "signal"
	AnalysisInputKindResolutionDecision AnalysisInputKind = "resolution_decision"
)

// AnalysisInputReference records one ordered, immutable input to analysis.
type AnalysisInputReference struct {
	Kind   AnalysisInputKind
	ID     string
	Digest []byte
}

// AnalysisRun identifies a durable completed analysis.
type AnalysisRun struct {
	ID string
}

// AnalysisRepository owns transactions that complete an analysis run and its
// input identity together.
type AnalysisRepository struct {
	pool *pgxpool.Pool
}

var _ analysisdomain.Repository = (*AnalysisRepository)(nil)

// NewAnalysisRepository creates an analysis repository backed by pool.
func NewAnalysisRepository(pool *pgxpool.Pool) *AnalysisRepository {
	return &AnalysisRepository{pool: pool}
}

// LoadPairInputs resolves observation mention links through only the current
// effective accepted decisions. Pending guesses never become eligible, and a
// correction changes eligibility without rewriting the immutable observation.
func (repository *AnalysisRepository) LoadPairInputs(ctx context.Context, employeeID, managerID string) (analysisdomain.PairSnapshot, error) {
	if repository == nil || repository.pool == nil {
		return analysisdomain.PairSnapshot{}, fmt.Errorf("load pair analysis inputs: repository is not configured")
	}
	employeeID, err := canonicalUUID(employeeID)
	if err != nil {
		return analysisdomain.PairSnapshot{}, fmt.Errorf("load pair analysis inputs: employee entity ID is invalid")
	}
	managerID, err = canonicalUUID(managerID)
	if err != nil || employeeID == managerID {
		return analysisdomain.PairSnapshot{}, fmt.Errorf("load pair analysis inputs: manager entity ID is invalid")
	}
	var entityCount int
	if err := repository.pool.QueryRow(ctx, `
		SELECT count(*) FROM stacks.entities
		WHERE id = ANY($1::uuid[]) AND kind = 'person'`, []string{employeeID, managerID}).Scan(&entityCount); err != nil {
		return analysisdomain.PairSnapshot{}, fmt.Errorf("load configured pair entities: %w", err)
	}
	if entityCount != 2 {
		return analysisdomain.PairSnapshot{Accepted: false}, nil
	}

	rows, err := repository.pool.Query(ctx, `
		WITH effective_decisions AS (
			SELECT proposal.mention_id, decision.id, decision.entity_id, decision.digest
			FROM stacks.resolution_proposals AS proposal
			JOIN stacks.resolution_decisions AS decision ON decision.proposal_id = proposal.id
			WHERE decision.superseded_by_id IS NULL
			  AND decision.outcome IN ('accepted', 'created')
		), eligible_signals AS (
			SELECT signal.id AS signal_id,
			       signal.digest AS signal_digest,
			       signal.category,
			       signal.direction,
			       signal.rationale,
			       signal.confidence,
			       observation.id AS observation_id,
			       observation.digest AS observation_digest,
			       observation.valid_start,
			       observation.recorded_at,
			       subject_decision.id AS subject_decision_id,
			       subject_decision.digest AS subject_decision_digest,
			       object_decision.id AS object_decision_id,
			       object_decision.digest AS object_decision_digest
			FROM stacks.interaction_signals AS signal
			JOIN stacks.observations AS observation ON observation.id = signal.observation_id
			JOIN effective_decisions AS subject_decision ON subject_decision.mention_id = observation.subject_mention_id
			JOIN effective_decisions AS object_decision ON object_decision.mention_id = observation.object_mention_id
			WHERE subject_decision.entity_id = $2::uuid
			  AND object_decision.entity_id = $1::uuid
			  AND EXISTS (
				SELECT 1
				FROM stacks.signal_evidence AS supporting
				JOIN stacks.evidence_spans AS supporting_span ON supporting_span.id = supporting.evidence_span_id
				JOIN stacks.document_tabs AS supporting_tab ON supporting_tab.id = supporting_span.document_tab_id
				WHERE supporting.signal_id = signal.id
				  AND supporting.role = 'supporting'
				  AND supporting_tab.role = 'transcript'
			  )
		)
		SELECT eligible.signal_id::text,
		       eligible.signal_digest,
		       eligible.category,
		       eligible.direction,
		       eligible.rationale,
		       eligible.confidence,
		       eligible.observation_id::text,
		       eligible.observation_digest,
		       eligible.valid_start,
		       eligible.recorded_at,
		       eligible.subject_decision_id::text,
		       eligible.subject_decision_digest,
		       eligible.object_decision_id::text,
		       eligible.object_decision_digest,
		       version.id::text,
		       version.digest,
		       tab.id::text,
		       tab.content_digest,
		       document.provider,
		       document.provider_document_id,
		       tab.provider_tab_id,
		       evidence.id::text,
		       evidence.start_offset,
		       evidence.end_offset,
		       evidence.quote,
		       signal_evidence.role
		FROM eligible_signals AS eligible
		JOIN stacks.signal_evidence AS signal_evidence ON signal_evidence.signal_id = eligible.signal_id
		JOIN stacks.evidence_spans AS evidence ON evidence.id = signal_evidence.evidence_span_id
		JOIN stacks.document_tabs AS tab ON tab.id = evidence.document_tab_id
		JOIN stacks.document_versions AS version ON version.id = tab.document_version_id
		JOIN stacks.source_documents AS document ON document.id = version.source_document_id
		ORDER BY eligible.valid_start ASC NULLS LAST,
		         eligible.signal_id,
		         signal_evidence.role,
		         evidence.id`, employeeID, managerID)
	if err != nil {
		return analysisdomain.PairSnapshot{}, fmt.Errorf("query pair analysis signals: %w", err)
	}
	defer rows.Close()

	snapshot := analysisdomain.PairSnapshot{Accepted: true}
	inputSeen := make(map[string]struct{})
	var current *analysisdomain.Signal
	for rows.Next() {
		var (
			signalID, category, direction, rationale, observationID string
			subjectDecisionID, objectDecisionID                     string
			documentVersionID, documentTabID                        string
			provider, providerDocumentID, providerTabID             string
			evidenceID, quote, evidenceRole                         string
			signalDigest, observationDigest                         []byte
			subjectDecisionDigest, objectDecisionDigest             []byte
			documentDigest, tabDigest                               []byte
			validTime                                               *time.Time
			recordedAt                                              time.Time
			confidence                                              float64
			startOffset, endOffset                                  int
		)
		if err := rows.Scan(
			&signalID, &signalDigest, &category, &direction, &rationale, &confidence,
			&observationID, &observationDigest, &validTime, &recordedAt,
			&subjectDecisionID, &subjectDecisionDigest, &objectDecisionID, &objectDecisionDigest,
			&documentVersionID, &documentDigest, &documentTabID, &tabDigest,
			&provider, &providerDocumentID, &providerTabID, &evidenceID,
			&startOffset, &endOffset, &quote, &evidenceRole,
		); err != nil {
			return analysisdomain.PairSnapshot{}, fmt.Errorf("scan pair analysis signal: %w", err)
		}
		if current == nil || current.ID != signalID {
			signalInputRefs, err := analysisInputReferences(
				analysisdomain.InputReference{Kind: analysisdomain.InputResolutionDecision, ID: subjectDecisionID, Digest: digestArray(subjectDecisionDigest)},
				analysisdomain.InputReference{Kind: analysisdomain.InputResolutionDecision, ID: objectDecisionID, Digest: digestArray(objectDecisionDigest)},
				analysisdomain.InputReference{Kind: analysisdomain.InputObservation, ID: observationID, Digest: digestArray(observationDigest)},
				analysisdomain.InputReference{Kind: analysisdomain.InputSignal, ID: signalID, Digest: digestArray(signalDigest)},
			)
			if err != nil {
				return analysisdomain.PairSnapshot{}, err
			}
			snapshot.Signals = append(snapshot.Signals, analysisdomain.Signal{
				ID: signalID, ObservationID: observationID,
				Category: analysisdomain.Category(category), Direction: analysisdomain.Direction(direction),
				ValidTime: validTime, RecordedAt: recordedAt.UTC(), Rationale: rationale,
				Confidence: confidence, Validated: true, TranscriptBacked: true,
				Inputs: signalInputRefs,
			})
			current = &snapshot.Signals[len(snapshot.Signals)-1]
			for _, input := range signalInputRefs {
				appendAnalysisInput(&snapshot.Inputs, inputSeen, input)
			}
		}
		documentInput := analysisdomain.InputReference{Kind: analysisdomain.InputDocumentVersion, ID: documentVersionID, Digest: digestArray(documentDigest)}
		tabInput := analysisdomain.InputReference{Kind: analysisdomain.InputDocumentTab, ID: documentTabID, Digest: digestArray(tabDigest)}
		current.Inputs = appendUniqueInput(current.Inputs, documentInput, tabInput)
		appendAnalysisInput(&snapshot.Inputs, inputSeen, documentInput)
		appendAnalysisInput(&snapshot.Inputs, inputSeen, tabInput)
		current.Citations = append(current.Citations, analysisdomain.Citation{
			ID: evidenceID, ProviderDocumentID: providerDocumentID, ProviderTabID: providerTabID,
			StartOffset: startOffset, EndOffset: endOffset, Quote: quote,
			Role: analysisdomain.CitationRole(evidenceRole), Locator: driveTabLocator(provider, providerDocumentID, providerTabID),
		})
	}
	if err := rows.Err(); err != nil {
		return analysisdomain.PairSnapshot{}, fmt.Errorf("iterate pair analysis signals: %w", err)
	}
	return snapshot, nil
}

// FindCompleted loads a completed report without invoking the model again.
func (repository *AnalysisRepository) FindCompleted(ctx context.Context, digest [sha256.Size]byte) (analysisdomain.Report, bool, error) {
	if repository == nil || repository.pool == nil {
		return analysisdomain.Report{}, false, fmt.Errorf("find completed analysis: repository is not configured")
	}
	return findCompletedAnalysis(ctx, repository.pool, digest)
}

// CompleteAnalysis persists one bounded report and its ordered provenance.
// Prompts and raw model output are deliberately not accepted by this method.
func (repository *AnalysisRepository) CompleteAnalysis(ctx context.Context, completion analysisdomain.Completion) (analysisdomain.Report, error) {
	if repository == nil || repository.pool == nil {
		return analysisdomain.Report{}, fmt.Errorf("complete pair analysis: repository is not configured")
	}
	wantDigest, err := analysisdomain.ComputeInputDigest(completion.Identity)
	if err != nil || wantDigest != completion.Identity.InputDigest {
		return analysisdomain.Report{}, fmt.Errorf("complete pair analysis: input digest is invalid")
	}
	if err := validateCompletedReport(completion.Report); err != nil {
		return analysisdomain.Report{}, err
	}
	if completion.Report.PromptVersion != completion.Identity.PromptVersion || completion.Report.PolicyVersion != completion.Identity.PolicyVersion {
		return analysisdomain.Report{}, fmt.Errorf("complete pair analysis: report versions conflict with input identity")
	}
	completion.Report.InputDigest = wantDigest
	completion.Report.ID = uuid.NewSHA1(uuid.NameSpaceOID, wantDigest[:]).String()
	reportJSON, err := json.Marshal(completion.Report)
	if err != nil {
		return analysisdomain.Report{}, fmt.Errorf("complete pair analysis: encode report")
	}
	transaction, err := repository.pool.Begin(ctx)
	if err != nil {
		return analysisdomain.Report{}, fmt.Errorf("start pair analysis transaction: %w", err)
	}
	defer transaction.Rollback(ctx) //nolint:errcheck // committed transactions are already closed.
	for position, input := range completion.Identity.Inputs {
		if err := validatePersistedDomainAnalysisInput(ctx, transaction, input); err != nil {
			return analysisdomain.Report{}, fmt.Errorf("validate analysis input %d: %w", position, err)
		}
	}
	result, err := transaction.Exec(ctx, `
		INSERT INTO stacks.analysis_runs
			(id, employee_entity_id, manager_entity_id, input_digest, analysis_prompt_version, policy_version,
			 state, recorded_at, completed_at, hypothesis, report_state, bedrock_region, model_id, max_output_tokens, report_json)
		VALUES ($1, $2, $3, $4, $5, $6, 'complete', $7, $7, $8, $9, $10, $11, $12, $13)
		ON CONFLICT (input_digest) DO NOTHING`,
		completion.Report.ID, completion.Identity.EmployeeEntityID, completion.Identity.ManagerEntityID,
		wantDigest[:], completion.Identity.PromptVersion, completion.Identity.PolicyVersion,
		completion.Report.RecordedAt, completion.Report.Rationale, string(completion.Report.Status),
		completion.Report.Region, completion.Report.ModelID, completion.Report.MaxTokens, reportJSON)
	if err != nil {
		return analysisdomain.Report{}, fmt.Errorf("persist pair analysis: %w", err)
	}
	if result.RowsAffected() == 0 {
		report, found, err := findCompletedAnalysis(ctx, transaction, wantDigest)
		if err != nil {
			return analysisdomain.Report{}, err
		}
		if !found {
			return analysisdomain.Report{}, fmt.Errorf("load repeated pair analysis: completed row is missing")
		}
		if err := transaction.Commit(ctx); err != nil {
			return analysisdomain.Report{}, fmt.Errorf("commit repeated pair analysis: %w", err)
		}
		return report, nil
	}
	for position, input := range completion.Identity.Inputs {
		if _, err := transaction.Exec(ctx, `
			INSERT INTO stacks.analysis_inputs (analysis_run_id, input_kind, input_id, input_digest, position)
			VALUES ($1, $2, $3::uuid, $4, $5)`, completion.Report.ID, string(input.Kind), input.ID, input.Digest[:], position); err != nil {
			return analysisdomain.Report{}, fmt.Errorf("persist pair analysis input %d: %w", position, err)
		}
	}
	if err := transaction.Commit(ctx); err != nil {
		return analysisdomain.Report{}, fmt.Errorf("commit pair analysis: %w", err)
	}
	return completion.Report, nil
}

// Complete records a completed analysis exactly once for its stable digest.
// A repeated completion returns the existing ID with created false.
func (repository *AnalysisRepository) Complete(ctx context.Context, input AnalysisInput) (AnalysisRun, bool, error) {
	canonicalInput, err := canonicalizeAnalysisInput(input)
	if err != nil {
		return AnalysisRun{}, false, err
	}
	input = canonicalInput
	if err := validateAnalysisInput(input); err != nil {
		return AnalysisRun{}, false, err
	}
	digest, err := ComputeAnalysisDigest(input)
	if err != nil {
		return AnalysisRun{}, false, err
	}

	transaction, err := repository.pool.Begin(ctx)
	if err != nil {
		return AnalysisRun{}, false, fmt.Errorf("start analysis transaction: %w", err)
	}
	defer transaction.Rollback(ctx) //nolint:errcheck // committed transactions are already closed.
	for position, analysisInput := range input.Inputs {
		if err := validatePersistedAnalysisInput(ctx, transaction, analysisInput); err != nil {
			return AnalysisRun{}, false, fmt.Errorf("validate analysis input %d: %w", position, err)
		}
	}

	var run AnalysisRun
	err = transaction.QueryRow(ctx, `
		INSERT INTO stacks.analysis_runs
			(employee_entity_id, manager_entity_id, input_digest, analysis_prompt_version, policy_version, state, recorded_at, completed_at)
		VALUES ($1, $2, $3, $4, $5, 'complete', $6, $6)
		ON CONFLICT (input_digest) DO NOTHING
		RETURNING id`,
		input.EmployeeEntityID, input.ManagerEntityID, digest[:], input.AnalysisPromptVersion, input.PolicyVersion, time.Now().UTC()).Scan(&run.ID)
	if err == pgx.ErrNoRows {
		var storedEmployeeID, storedManagerID, storedPromptVersion, storedPolicyVersion string
		err = transaction.QueryRow(ctx, `
			SELECT id, employee_entity_id::text, manager_entity_id::text, analysis_prompt_version, policy_version
			FROM stacks.analysis_runs WHERE input_digest = $1`, digest[:]).Scan(
			&run.ID, &storedEmployeeID, &storedManagerID, &storedPromptVersion, &storedPolicyVersion)
		if err != nil {
			return AnalysisRun{}, false, fmt.Errorf("load analysis for input digest: %w", err)
		}
		if storedEmployeeID != input.EmployeeEntityID || storedManagerID != input.ManagerEntityID ||
			storedPromptVersion != input.AnalysisPromptVersion || storedPolicyVersion != input.PolicyVersion {
			return AnalysisRun{}, false, fmt.Errorf("load analysis for input digest: stored analysis metadata conflicts")
		}
		if err := transaction.Commit(ctx); err != nil {
			return AnalysisRun{}, false, fmt.Errorf("commit repeated analysis transaction: %w", err)
		}
		return run, false, nil
	}
	if err != nil {
		return AnalysisRun{}, false, fmt.Errorf("persist analysis for input digest: %w", err)
	}
	for position, analysisInput := range input.Inputs {
		_, err := transaction.Exec(ctx, `
			INSERT INTO stacks.analysis_inputs (analysis_run_id, input_kind, input_id, input_digest, position)
			VALUES ($1, $2, $3::uuid, $4, $5)`,
			run.ID, string(analysisInput.Kind), analysisInput.ID, analysisInput.Digest, position)
		if err != nil {
			return AnalysisRun{}, false, fmt.Errorf("persist analysis input %d for analysis %q: %w", position, run.ID, err)
		}
	}
	if err := transaction.Commit(ctx); err != nil {
		return AnalysisRun{}, false, fmt.Errorf("commit analysis transaction: %w", err)
	}
	return run, true, nil
}

func validateAnalysisInput(input AnalysisInput) error {
	if strings.TrimSpace(input.EmployeeEntityID) == "" {
		return fmt.Errorf("complete analysis: employee entity ID is required")
	}
	if strings.TrimSpace(input.ManagerEntityID) == "" {
		return fmt.Errorf("complete analysis: manager entity ID is required")
	}
	if strings.TrimSpace(input.AnalysisPromptVersion) == "" {
		return fmt.Errorf("complete analysis: prompt version is required")
	}
	if strings.TrimSpace(input.PolicyVersion) == "" {
		return fmt.Errorf("complete analysis: policy version is required")
	}
	if len(input.Inputs) == 0 {
		return fmt.Errorf("complete analysis: inputs are required")
	}
	seenDigests := make(map[string]struct{}, len(input.Inputs))
	for position, analysisInput := range input.Inputs {
		if !validAnalysisInputKind(analysisInput.Kind) {
			return fmt.Errorf("complete analysis input %d: kind is invalid", position)
		}
		if strings.TrimSpace(analysisInput.ID) == "" {
			return fmt.Errorf("complete analysis input %d: ID is required", position)
		}
		if len(analysisInput.Digest) != analysisInputDigestLength {
			return fmt.Errorf("complete analysis input %d: digest must be %d bytes", position, analysisInputDigestLength)
		}
		digestIdentity := string(analysisInput.Digest)
		if _, exists := seenDigests[digestIdentity]; exists {
			return fmt.Errorf("complete analysis input %d: digest is repeated", position)
		}
		seenDigests[digestIdentity] = struct{}{}
	}
	return nil
}

// ComputeAnalysisDigest derives the immutable completed-analysis identity from
// the configured pair, ordered input identities, and active analysis versions.
func ComputeAnalysisDigest(input AnalysisInput) ([sha256.Size]byte, error) {
	canonicalInput, err := canonicalizeAnalysisInput(input)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	input = canonicalInput
	if err := validateAnalysisInput(input); err != nil {
		return [sha256.Size]byte{}, err
	}
	hasher := sha256.New()
	writeAnalysisDigestString(hasher, input.EmployeeEntityID)
	writeAnalysisDigestString(hasher, input.ManagerEntityID)
	writeAnalysisDigestString(hasher, input.AnalysisPromptVersion)
	writeAnalysisDigestString(hasher, input.PolicyVersion)
	writeAnalysisDigestLength(hasher, uint64(len(input.Inputs)))
	for _, analysisInput := range input.Inputs {
		writeAnalysisDigestString(hasher, string(analysisInput.Kind))
		writeAnalysisDigestString(hasher, analysisInput.ID)
		writeAnalysisDigestBytes(hasher, analysisInput.Digest)
	}
	var digest [sha256.Size]byte
	copy(digest[:], hasher.Sum(nil))
	return digest, nil
}

func canonicalizeAnalysisInput(input AnalysisInput) (AnalysisInput, error) {
	employeeID, err := canonicalUUID(input.EmployeeEntityID)
	if err != nil {
		return AnalysisInput{}, fmt.Errorf("complete analysis: employee entity ID is invalid")
	}
	managerID, err := canonicalUUID(input.ManagerEntityID)
	if err != nil {
		return AnalysisInput{}, fmt.Errorf("complete analysis: manager entity ID is invalid")
	}
	input.EmployeeEntityID = employeeID
	input.ManagerEntityID = managerID
	input.Inputs = append([]AnalysisInputReference(nil), input.Inputs...)
	for index := range input.Inputs {
		canonicalID, err := canonicalUUID(input.Inputs[index].ID)
		if err != nil {
			return AnalysisInput{}, fmt.Errorf("complete analysis input %d: ID is invalid", index)
		}
		input.Inputs[index].ID = canonicalID
	}
	return input, nil
}

func canonicalUUID(value string) (string, error) {
	parsed, err := uuid.Parse(strings.TrimSpace(value))
	if err != nil {
		return "", err
	}
	return parsed.String(), nil
}

func validatePersistedAnalysisInput(ctx context.Context, transaction pgx.Tx, input AnalysisInputReference) error {
	query := ""
	switch input.Kind {
	case AnalysisInputKindDocumentVersion:
		query = `SELECT digest FROM stacks.document_versions WHERE id = $1`
	case AnalysisInputKindDocumentTab:
		query = `SELECT content_digest FROM stacks.document_tabs WHERE id = $1`
	case AnalysisInputKindObservation:
		query = `SELECT digest FROM stacks.observations WHERE id = $1`
	case AnalysisInputKindSignal:
		query = `SELECT digest FROM stacks.interaction_signals WHERE id = $1`
	case AnalysisInputKindResolutionDecision:
		query = `SELECT digest FROM stacks.resolution_decisions WHERE id = $1`
	default:
		return fmt.Errorf("kind is invalid")
	}
	var storedDigest []byte
	err := transaction.QueryRow(ctx, query, input.ID).Scan(&storedDigest)
	if err == pgx.ErrNoRows {
		return fmt.Errorf("%s %q does not exist", input.Kind, input.ID)
	}
	if err != nil {
		return fmt.Errorf("load %s %q: %w", input.Kind, input.ID, err)
	}
	if string(storedDigest) != string(input.Digest) {
		return fmt.Errorf("%s %q digest does not match", input.Kind, input.ID)
	}
	return nil
}

func validAnalysisInputKind(kind AnalysisInputKind) bool {
	switch kind {
	case AnalysisInputKindDocumentVersion, AnalysisInputKindDocumentTab, AnalysisInputKindObservation, AnalysisInputKindSignal, AnalysisInputKindResolutionDecision:
		return true
	default:
		return false
	}
}

type completedAnalysisQueryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func findCompletedAnalysis(ctx context.Context, queryer completedAnalysisQueryer, digest [sha256.Size]byte) (analysisdomain.Report, bool, error) {
	var reportID string
	var reportJSON []byte
	err := queryer.QueryRow(ctx, `
		SELECT id::text, report_json
		FROM stacks.analysis_runs
		WHERE input_digest = $1 AND state = 'complete' AND report_json IS NOT NULL`, digest[:]).Scan(&reportID, &reportJSON)
	if err == pgx.ErrNoRows {
		return analysisdomain.Report{}, false, nil
	}
	if err != nil {
		return analysisdomain.Report{}, false, fmt.Errorf("load completed pair analysis: %w", err)
	}
	var report analysisdomain.Report
	if err := json.Unmarshal(reportJSON, &report); err != nil {
		return analysisdomain.Report{}, false, fmt.Errorf("load completed pair analysis: stored report is invalid")
	}
	if report.ID != reportID || report.InputDigest != digest {
		return analysisdomain.Report{}, false, fmt.Errorf("load completed pair analysis: stored report identity conflicts")
	}
	if err := validateCompletedReport(report); err != nil {
		return analysisdomain.Report{}, false, fmt.Errorf("load completed pair analysis: stored report metadata is invalid")
	}
	return report, true, nil
}

func validateCompletedReport(report analysisdomain.Report) error {
	if report.RecordedAt.IsZero() || strings.TrimSpace(report.Region) == "" || strings.TrimSpace(report.ModelID) == "" ||
		report.MaxTokens <= 0 || strings.TrimSpace(report.PromptVersion) == "" || strings.TrimSpace(report.PolicyVersion) == "" {
		return fmt.Errorf("complete pair analysis: report metadata is invalid")
	}
	switch report.Status {
	case analysisdomain.StatusInsufficientEvidence, analysisdomain.StatusNoMaterialChange,
		analysisdomain.StatusMixedOrConflicting, analysisdomain.StatusPossibleDecline:
	default:
		return fmt.Errorf("complete pair analysis: report status is invalid")
	}
	return nil
}

func validatePersistedDomainAnalysisInput(ctx context.Context, transaction pgx.Tx, input analysisdomain.InputReference) error {
	return validatePersistedAnalysisInput(ctx, transaction, AnalysisInputReference{
		Kind: AnalysisInputKind(input.Kind), ID: input.ID, Digest: input.Digest[:],
	})
}

func digestArray(value []byte) [sha256.Size]byte {
	var digest [sha256.Size]byte
	copy(digest[:], value)
	return digest
}

func analysisInputReferences(inputs ...analysisdomain.InputReference) ([]analysisdomain.InputReference, error) {
	for _, input := range inputs {
		if input.Digest == ([sha256.Size]byte{}) {
			return nil, fmt.Errorf("load pair analysis inputs: persisted digest is invalid")
		}
	}
	return append([]analysisdomain.InputReference(nil), inputs...), nil
}

func appendAnalysisInput(inputs *[]analysisdomain.InputReference, seen map[string]struct{}, input analysisdomain.InputReference) {
	key := string(input.Kind) + "\x00" + input.ID
	if _, exists := seen[key]; exists {
		return
	}
	seen[key] = struct{}{}
	*inputs = append(*inputs, input)
}

func appendUniqueInput(inputs []analysisdomain.InputReference, candidates ...analysisdomain.InputReference) []analysisdomain.InputReference {
	seen := make(map[string]struct{}, len(inputs)+len(candidates))
	for _, input := range inputs {
		seen[string(input.Kind)+"\x00"+input.ID] = struct{}{}
	}
	for _, candidate := range candidates {
		key := string(candidate.Kind) + "\x00" + candidate.ID
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		inputs = append(inputs, candidate)
	}
	return inputs
}

func driveTabLocator(provider, documentID, tabID string) string {
	if provider != "drive" {
		return ""
	}
	return "https://docs.google.com/document/d/" + url.PathEscape(documentID) + "/edit?tab=" + url.QueryEscape(tabID)
}

func writeAnalysisDigestString(hasher hash.Hash, value string) {
	writeAnalysisDigestBytes(hasher, []byte(value))
}

func writeAnalysisDigestBytes(hasher hash.Hash, value []byte) {
	writeAnalysisDigestLength(hasher, uint64(len(value)))
	_, _ = hasher.Write(value)
}

func writeAnalysisDigestLength(hasher hash.Hash, length uint64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], length)
	_, _ = hasher.Write(encoded[:])
}
