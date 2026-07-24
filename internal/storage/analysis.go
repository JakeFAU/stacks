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
	"stacks/internal/modelpolicy"
)

const (
	analysisInputDigestLength = 32
	legacyAnalysisDigestScope = "stacks.legacy-analysis-completion.v1"
	meetingDigestScope        = "stacks.source-document-meeting.v1"
)

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
	AnalysisInputKindSourceDocument     AnalysisInputKind = "source_document"
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
	pool                 *pgxpool.Pool
	beginSnapshot        func(context.Context, pgx.TxOptions) (analysisSnapshot, error)
	beginCompletedLookup func(context.Context) (completedAnalysisLookup, error)
}

type effectivePairDecision struct {
	EntityID string
	Input    analysisdomain.InputReference
}

type analysisSnapshot interface {
	LoadPairIdentity(context.Context, string, string) (analysisdomain.PairSnapshot, error)
	LoadPairSignals(context.Context, string, string, analysisdomain.PairSnapshot) (analysisdomain.PairSnapshot, error)
	Commit(context.Context) error
	Rollback(context.Context) error
}

type postgresAnalysisSnapshot struct {
	transaction pgx.Tx
}

type completedAnalysisLookup interface {
	ValidateEffectivePairDecisions(context.Context, analysisdomain.AnalysisIdentity) error
	FindCompleted(context.Context, analysisdomain.AnalysisIdentity) (analysisdomain.Report, bool, error)
	Commit(context.Context) error
	Rollback(context.Context) error
}

type postgresCompletedAnalysisLookup struct {
	transaction pgx.Tx
}

var _ analysisdomain.Repository = (*AnalysisRepository)(nil)

// NewAnalysisRepository creates an analysis repository backed by pool.
func NewAnalysisRepository(pool *pgxpool.Pool) *AnalysisRepository {
	repository := &AnalysisRepository{pool: pool}
	if pool != nil {
		repository.beginSnapshot = func(ctx context.Context, options pgx.TxOptions) (analysisSnapshot, error) {
			transaction, err := pool.BeginTx(ctx, options)
			if err != nil {
				return nil, err
			}
			return &postgresAnalysisSnapshot{transaction: transaction}, nil
		}
		repository.beginCompletedLookup = func(ctx context.Context) (completedAnalysisLookup, error) {
			transaction, err := pool.Begin(ctx)
			if err != nil {
				return nil, err
			}
			return &postgresCompletedAnalysisLookup{transaction: transaction}, nil
		}
	}
	return repository
}

// LoadPairInputs resolves observation mention links through only the current
// effective accepted decisions. Pending guesses never become eligible, and a
// correction changes eligibility without rewriting the immutable observation.
func (repository *AnalysisRepository) LoadPairInputs(ctx context.Context, employeeID, managerID string) (analysisdomain.PairSnapshot, error) {
	if repository == nil || repository.beginSnapshot == nil {
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
	transaction, err := repository.beginSnapshot(ctx, pgx.TxOptions{
		IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return analysisdomain.PairSnapshot{}, boundedAnalysisError("start pair analysis snapshot", err)
	}
	defer transaction.Rollback(ctx) //nolint:errcheck // committed transactions are already closed.
	snapshot, err := transaction.LoadPairIdentity(ctx, employeeID, managerID)
	if err != nil {
		return analysisdomain.PairSnapshot{}, boundedAnalysisError("load pair identity snapshot", err)
	}
	if snapshot.Accepted {
		snapshot, err = transaction.LoadPairSignals(ctx, employeeID, managerID, snapshot)
		if err != nil {
			return analysisdomain.PairSnapshot{}, boundedAnalysisError("load pair analysis signals", err)
		}
	}
	if err := transaction.Commit(ctx); err != nil {
		return analysisdomain.PairSnapshot{}, boundedAnalysisError("commit pair analysis snapshot", err)
	}
	return snapshot, nil
}

func (snapshot *postgresAnalysisSnapshot) LoadPairIdentity(ctx context.Context, employeeID, managerID string) (analysisdomain.PairSnapshot, error) {
	decisionRows, err := snapshot.transaction.Query(ctx, `
		SELECT decision.entity_id::text, decision.id::text, decision.digest
		FROM stacks.resolution_decisions AS decision
		JOIN stacks.resolution_proposals AS proposal ON proposal.id = decision.proposal_id
		JOIN stacks.mentions AS mention ON mention.id = proposal.mention_id
		LEFT JOIN stacks.extraction_runs AS extraction_run ON extraction_run.id = mention.extraction_run_id
		LEFT JOIN stacks.document_versions AS extraction_version ON extraction_version.id = extraction_run.document_version_id
		LEFT JOIN stacks.source_documents AS source_document ON source_document.id = extraction_version.source_document_id
		JOIN stacks.entities AS entity ON entity.id = decision.entity_id
		WHERE decision.superseded_by_id IS NULL
		  AND decision.outcome IN ('accepted', 'created')
		  AND decision.currently_admissible
		  AND mention.currently_admissible
		  AND (mention.extraction_run_id IS NULL OR (
		      extraction_run.currently_admissible
		      AND source_document.current_document_version_id = extraction_run.document_version_id
		  ))
		  AND entity.kind = 'person'
		  AND decision.entity_id = ANY($2::uuid[])
		ORDER BY CASE WHEN decision.entity_id = $1::uuid THEN 0 ELSE 1 END,
		         decision.id`, employeeID, []string{employeeID, managerID})
	if err != nil {
		return analysisdomain.PairSnapshot{}, fmt.Errorf("query configured pair identity decisions: %w", err)
	}
	var decisions []effectivePairDecision
	for decisionRows.Next() {
		var entityID, decisionID string
		var decisionDigest []byte
		if err := decisionRows.Scan(&entityID, &decisionID, &decisionDigest); err != nil {
			decisionRows.Close()
			return analysisdomain.PairSnapshot{}, fmt.Errorf("scan configured pair identity decision: %w", err)
		}
		decisions = append(decisions, effectivePairDecision{
			EntityID: entityID,
			Input: analysisdomain.InputReference{
				Kind: analysisdomain.InputResolutionDecision, ID: decisionID, Digest: digestArray(decisionDigest),
			},
		})
	}
	if err := decisionRows.Err(); err != nil {
		decisionRows.Close()
		return analysisdomain.PairSnapshot{}, fmt.Errorf("iterate configured pair identity decisions: %w", err)
	}
	decisionRows.Close()
	pair, err := pairIdentitySnapshot(employeeID, managerID, decisions)
	if err != nil {
		return analysisdomain.PairSnapshot{}, err
	}
	return pair, nil
}

func (snapshot *postgresAnalysisSnapshot) LoadPairSignals(ctx context.Context, employeeID, managerID string, pair analysisdomain.PairSnapshot) (analysisdomain.PairSnapshot, error) {
	rows, err := snapshot.transaction.Query(ctx, `
		WITH effective_decisions AS (
			SELECT proposal.mention_id, decision.id, decision.entity_id, decision.digest
			FROM stacks.resolution_proposals AS proposal
			JOIN stacks.mentions AS mention ON mention.id = proposal.mention_id
			LEFT JOIN stacks.extraction_runs AS extraction_run ON extraction_run.id = mention.extraction_run_id
			LEFT JOIN stacks.document_versions AS extraction_version ON extraction_version.id = extraction_run.document_version_id
			LEFT JOIN stacks.source_documents AS source_document ON source_document.id = extraction_version.source_document_id
			JOIN stacks.resolution_decisions AS decision ON decision.proposal_id = proposal.id
			WHERE decision.superseded_by_id IS NULL
			  AND decision.outcome IN ('accepted', 'created')
			  AND decision.currently_admissible
			  AND mention.currently_admissible
			  AND (mention.extraction_run_id IS NULL OR (
			      extraction_run.currently_admissible
			      AND source_document.current_document_version_id = extraction_run.document_version_id
			  ))
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
			       (
					SELECT supporting_version.source_document_id
					FROM stacks.signal_evidence AS supporting
					JOIN stacks.evidence_spans AS supporting_span ON supporting_span.id = supporting.evidence_span_id
					JOIN stacks.document_tabs AS supporting_tab ON supporting_tab.id = supporting_span.document_tab_id
					JOIN stacks.document_versions AS supporting_version ON supporting_version.id = supporting_tab.document_version_id
					WHERE supporting.signal_id = signal.id
					  AND supporting.role = 'supporting'
					  AND supporting_tab.role = 'transcript'
					ORDER BY supporting_version.source_document_id
					LIMIT 1
			       ) AS meeting_id,
			       subject_decision.id AS subject_decision_id,
			       subject_decision.digest AS subject_decision_digest,
			       object_decision.id AS object_decision_id,
			       object_decision.digest AS object_decision_digest
			FROM stacks.interaction_signals AS signal
			JOIN stacks.observations AS observation ON observation.id = signal.observation_id
			LEFT JOIN stacks.extraction_runs AS extraction_run ON extraction_run.id = observation.extraction_run_id
			LEFT JOIN stacks.document_versions AS extraction_version ON extraction_version.id = extraction_run.document_version_id
			LEFT JOIN stacks.source_documents AS source_document ON source_document.id = extraction_version.source_document_id
			JOIN effective_decisions AS subject_decision ON subject_decision.mention_id = observation.subject_mention_id
			JOIN effective_decisions AS object_decision ON object_decision.mention_id = observation.object_mention_id
			WHERE subject_decision.entity_id = $2::uuid
			  AND object_decision.entity_id = $1::uuid
			  AND signal.currently_admissible
			  AND observation.currently_admissible
			  AND (observation.extraction_run_id IS NULL OR (
			      extraction_run.currently_admissible
			      AND source_document.current_document_version_id = extraction_run.document_version_id
			  ))
			  AND EXISTS (
					SELECT 1
					FROM stacks.signal_evidence AS supporting
					JOIN stacks.evidence_spans AS supporting_span ON supporting_span.id = supporting.evidence_span_id
					JOIN stacks.document_tabs AS supporting_tab ON supporting_tab.id = supporting_span.document_tab_id
					WHERE supporting.signal_id = signal.id
					  AND supporting.role = 'supporting'
					  AND supporting_tab.role = 'transcript'
			  )
			  AND 1 = (
					SELECT count(DISTINCT supporting_version.source_document_id)
					FROM stacks.signal_evidence AS supporting
					JOIN stacks.evidence_spans AS supporting_span ON supporting_span.id = supporting.evidence_span_id
					JOIN stacks.document_tabs AS supporting_tab ON supporting_tab.id = supporting_span.document_tab_id
					JOIN stacks.document_versions AS supporting_version ON supporting_version.id = supporting_tab.document_version_id
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
		       eligible.meeting_id::text,
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
		       tab.role,
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

	inputSeen := make(map[string]struct{})
	for _, input := range pair.Inputs {
		inputSeen[string(input.Kind)+"\x00"+input.ID] = struct{}{}
	}
	var current *analysisdomain.Signal
	for rows.Next() {
		var (
			signalID, category, direction, storedRationale, observationID string
			subjectDecisionID, objectDecisionID                           string
			documentVersionID, documentTabID                              string
			meetingID, provider, providerDocumentID, providerTabID        string
			tabRole                                                       string
			evidenceID, quote, evidenceRole                               string
			signalDigest, observationDigest                               []byte
			subjectDecisionDigest, objectDecisionDigest                   []byte
			documentDigest, tabDigest                                     []byte
			validTime                                                     *time.Time
			recordedAt                                                    time.Time
			confidence                                                    float64
			startOffset, endOffset                                        int
		)
		if err := rows.Scan(
			&signalID, &signalDigest, &category, &direction, &storedRationale, &confidence,
			&observationID, &observationDigest, &validTime, &recordedAt,
			&meetingID,
			&subjectDecisionID, &subjectDecisionDigest, &objectDecisionID, &objectDecisionDigest,
			&documentVersionID, &documentDigest, &documentTabID, &tabDigest,
			&provider, &providerDocumentID, &providerTabID, &tabRole, &evidenceID,
			&startOffset, &endOffset, &quote, &evidenceRole,
		); err != nil {
			return analysisdomain.PairSnapshot{}, fmt.Errorf("scan pair analysis signal: %w", err)
		}
		if current == nil || current.ID != signalID {
			meetingInput, err := meetingInputReference(meetingID)
			if err != nil {
				return analysisdomain.PairSnapshot{}, err
			}
			signalInputRefs, err := analysisInputReferences(
				analysisdomain.InputReference{Kind: analysisdomain.InputResolutionDecision, ID: subjectDecisionID, Digest: digestArray(subjectDecisionDigest)},
				analysisdomain.InputReference{Kind: analysisdomain.InputResolutionDecision, ID: objectDecisionID, Digest: digestArray(objectDecisionDigest)},
				meetingInput,
				analysisdomain.InputReference{Kind: analysisdomain.InputObservation, ID: observationID, Digest: digestArray(observationDigest)},
				analysisdomain.InputReference{Kind: analysisdomain.InputSignal, ID: signalID, Digest: digestArray(signalDigest)},
			)
			if err != nil {
				return analysisdomain.PairSnapshot{}, err
			}
			pair.Signals = append(pair.Signals, analysisdomain.Signal{
				ID: signalID, MeetingID: meetingInput.ID, ObservationID: observationID,
				Category: analysisdomain.Category(category), Direction: analysisdomain.Direction(direction),
				ValidTime: validTime, RecordedAt: recordedAt.UTC(),
				Rationale:  analysisdomain.ExplainSignal(analysisdomain.Category(category), analysisdomain.Direction(direction)),
				Confidence: confidence, Validated: true, TranscriptBacked: true,
				Inputs: signalInputRefs,
			})
			current = &pair.Signals[len(pair.Signals)-1]
			for _, input := range signalInputRefs {
				appendAnalysisInput(&pair.Inputs, inputSeen, input)
			}
		}
		documentInput := analysisdomain.InputReference{Kind: analysisdomain.InputDocumentVersion, ID: documentVersionID, Digest: digestArray(documentDigest)}
		tabInput := analysisdomain.InputReference{Kind: analysisdomain.InputDocumentTab, ID: documentTabID, Digest: digestArray(tabDigest)}
		current.Inputs = appendUniqueInput(current.Inputs, documentInput, tabInput)
		appendAnalysisInput(&pair.Inputs, inputSeen, documentInput)
		appendAnalysisInput(&pair.Inputs, inputSeen, tabInput)
		current.Citations = append(current.Citations, analysisdomain.Citation{
			ID: evidenceID, ProviderDocumentID: providerDocumentID, ProviderTabID: providerTabID,
			StartOffset: startOffset, EndOffset: endOffset, Quote: quote,
			Role: analysisdomain.CitationRole(evidenceRole), Transcript: tabRole == "transcript",
			Locator: driveTabLocator(provider, providerDocumentID, providerTabID),
		})
	}
	if err := rows.Err(); err != nil {
		return analysisdomain.PairSnapshot{}, fmt.Errorf("iterate pair analysis signals: %w", err)
	}
	return pair, nil
}

func (snapshot *postgresAnalysisSnapshot) Commit(ctx context.Context) error {
	return snapshot.transaction.Commit(ctx)
}

func (snapshot *postgresAnalysisSnapshot) Rollback(ctx context.Context) error {
	return snapshot.transaction.Rollback(ctx)
}

func (lookup *postgresCompletedAnalysisLookup) ValidateEffectivePairDecisions(ctx context.Context, identity analysisdomain.AnalysisIdentity) error {
	return validateEffectivePairDecisions(ctx, lookup.transaction, identity)
}

func (lookup *postgresCompletedAnalysisLookup) FindCompleted(ctx context.Context, identity analysisdomain.AnalysisIdentity) (analysisdomain.Report, bool, error) {
	return findCompletedAnalysis(ctx, lookup.transaction, identity)
}

func (lookup *postgresCompletedAnalysisLookup) Commit(ctx context.Context) error {
	return lookup.transaction.Commit(ctx)
}

func (lookup *postgresCompletedAnalysisLookup) Rollback(ctx context.Context) error {
	return lookup.transaction.Rollback(ctx)
}

type boundedAnalysisOperationError struct {
	operation string
	cause     error
}

func (err boundedAnalysisOperationError) Error() string { return err.operation }
func (err boundedAnalysisOperationError) Unwrap() error { return err.cause }

func boundedAnalysisError(operation string, cause error) error {
	return boundedAnalysisOperationError{operation: operation, cause: cause}
}

func pairIdentitySnapshot(employeeID, managerID string, decisions []effectivePairDecision) (analysisdomain.PairSnapshot, error) {
	snapshot := analysisdomain.PairSnapshot{}
	seenInputs := make(map[string]struct{}, len(decisions))
	var employeeAccepted, managerAccepted bool
	for index, decision := range decisions {
		if decision.EntityID != employeeID && decision.EntityID != managerID {
			return analysisdomain.PairSnapshot{}, fmt.Errorf("load pair analysis inputs: identity decision %d belongs to an unexpected entity", index)
		}
		if decision.Input.Kind != analysisdomain.InputResolutionDecision || decision.Input.Digest == ([sha256.Size]byte{}) {
			return analysisdomain.PairSnapshot{}, fmt.Errorf("load pair analysis inputs: identity decision %d is invalid", index)
		}
		employeeAccepted = employeeAccepted || decision.EntityID == employeeID
		managerAccepted = managerAccepted || decision.EntityID == managerID
		appendAnalysisInput(&snapshot.Inputs, seenInputs, decision.Input)
	}
	if !employeeAccepted || !managerAccepted {
		return analysisdomain.PairSnapshot{Accepted: false}, nil
	}
	snapshot.Accepted = true
	return snapshot, nil
}

// FindCompleted loads a completed report only while its accepted pair decisions
// remain current. Historical rows remain durable but are not returned for a
// stale snapshot identity.
func (repository *AnalysisRepository) FindCompleted(ctx context.Context, identity analysisdomain.AnalysisIdentity) (analysisdomain.Report, bool, error) {
	if repository == nil || repository.beginCompletedLookup == nil {
		return analysisdomain.Report{}, false, fmt.Errorf("find completed analysis: repository is not configured")
	}
	wantDigest, err := analysisdomain.ComputeInputDigest(identity)
	if err != nil || wantDigest != identity.InputDigest {
		return analysisdomain.Report{}, false, fmt.Errorf("find completed analysis: input digest is invalid")
	}
	transaction, err := repository.beginCompletedLookup(ctx)
	if err != nil {
		return analysisdomain.Report{}, false, boundedAnalysisError("start completed analysis lookup", err)
	}
	defer transaction.Rollback(ctx) //nolint:errcheck // committed transactions are already closed.
	if err := transaction.ValidateEffectivePairDecisions(ctx, identity); err != nil {
		return analysisdomain.Report{}, false, err
	}
	report, found, err := transaction.FindCompleted(ctx, identity)
	if err != nil {
		return analysisdomain.Report{}, false, boundedAnalysisError("load completed analysis", err)
	}
	if err := transaction.Commit(ctx); err != nil {
		return analysisdomain.Report{}, false, boundedAnalysisError("commit completed analysis lookup", err)
	}
	return report, found, nil
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
	if err := validateCompletedReport(completion.Report, completion.Identity); err != nil {
		return analysisdomain.Report{}, err
	}
	provenance, err := analysisCompletionProvenance(completion)
	if err != nil {
		return analysisdomain.Report{}, err
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
	if err := validateEffectivePairDecisions(ctx, transaction, completion.Identity); err != nil {
		return analysisdomain.Report{}, err
	}
	for position, input := range completion.Identity.Inputs {
		if input.Kind == analysisdomain.InputResolutionDecision {
			continue
		}
		if err := validatePersistedDomainAnalysisInput(ctx, transaction, input); err != nil {
			return analysisdomain.Report{}, fmt.Errorf("validate analysis input %d: %w", position, err)
		}
	}
	result, err := transaction.Exec(ctx, `
		INSERT INTO stacks.analysis_runs
			(id, employee_entity_id, manager_entity_id, input_digest, analysis_prompt_version, policy_version,
			 state, recorded_at, completed_at, hypothesis, report_state, model_provider, data_mode,
			 bedrock_region, model_id, max_output_tokens, report_json,
			 currently_admissible)
		VALUES ($1, $2, $3, $4, $5, $6, 'complete', $7, $7, $8, $9, $10, $11, $12, $13, $14, $15, true)
		ON CONFLICT (input_digest) DO NOTHING`,
		completion.Report.ID, completion.Identity.EmployeeEntityID, completion.Identity.ManagerEntityID,
		wantDigest[:], completion.Identity.PromptVersion, completion.Identity.PolicyVersion,
		completion.Report.RecordedAt, completion.Report.Rationale, string(completion.Report.Status),
		provenance.provider, provenance.dataMode, provenance.region, provenance.modelID, provenance.maxTokens, reportJSON)
	if err != nil {
		return analysisdomain.Report{}, fmt.Errorf("persist pair analysis: %w", err)
	}
	if result.RowsAffected() == 0 {
		report, found, err := findCompletedAnalysis(ctx, transaction, completion.Identity)
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
			(employee_entity_id, manager_entity_id, input_digest, analysis_prompt_version, policy_version, state, recorded_at, completed_at, currently_admissible)
		VALUES ($1, $2, $3, $4, $5, 'complete', $6, $6, true)
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
	writeAnalysisDigestString(hasher, legacyAnalysisDigestScope)
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
	if input.Kind == AnalysisInputKindSourceDocument {
		var storedID string
		err := transaction.QueryRow(ctx, `SELECT id::text FROM stacks.source_documents WHERE id = $1`, input.ID).Scan(&storedID)
		if err == pgx.ErrNoRows {
			return fmt.Errorf("%s %q does not exist", input.Kind, input.ID)
		}
		if err != nil {
			return fmt.Errorf("load %s %q: %w", input.Kind, input.ID, err)
		}
		stored, err := meetingInputReference(storedID)
		if err != nil || string(stored.Digest[:]) != string(input.Digest) {
			return fmt.Errorf("%s %q digest does not match", input.Kind, input.ID)
		}
		return nil
	}
	query := ""
	switch input.Kind {
	case AnalysisInputKindDocumentVersion:
		query = `SELECT digest FROM stacks.document_versions WHERE id = $1`
	case AnalysisInputKindDocumentTab:
		query = `SELECT content_digest FROM stacks.document_tabs WHERE id = $1`
	case AnalysisInputKindObservation:
		query = `SELECT digest FROM stacks.observations WHERE id = $1 AND currently_admissible`
	case AnalysisInputKindSignal:
		query = `SELECT digest FROM stacks.interaction_signals WHERE id = $1 AND currently_admissible`
	case AnalysisInputKindResolutionDecision:
		query = `SELECT digest FROM stacks.resolution_decisions WHERE id = $1 AND currently_admissible`
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
	case AnalysisInputKindDocumentVersion, AnalysisInputKindSourceDocument, AnalysisInputKindDocumentTab, AnalysisInputKindObservation, AnalysisInputKindSignal, AnalysisInputKindResolutionDecision:
		return true
	default:
		return false
	}
}

func meetingInputReference(sourceDocumentID string) (analysisdomain.InputReference, error) {
	canonicalID, err := canonicalUUID(sourceDocumentID)
	if err != nil {
		return analysisdomain.InputReference{}, fmt.Errorf("load pair analysis inputs: source document ID is invalid")
	}
	hasher := sha256.New()
	writeAnalysisDigestString(hasher, meetingDigestScope)
	writeAnalysisDigestString(hasher, canonicalID)
	var digest [sha256.Size]byte
	copy(digest[:], hasher.Sum(nil))
	return analysisdomain.InputReference{Kind: analysisdomain.InputSourceDocument, ID: canonicalID, Digest: digest}, nil
}

type completedAnalysisQueryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func validateEffectivePairDecisions(ctx context.Context, queryer completedAnalysisQueryer, identity analysisdomain.AnalysisIdentity) error {
	employeeID, err := canonicalUUID(identity.EmployeeEntityID)
	if err != nil {
		return fmt.Errorf("validate current resolution decisions: configured pair is invalid")
	}
	managerID, err := canonicalUUID(identity.ManagerEntityID)
	if err != nil || employeeID == managerID {
		return fmt.Errorf("validate current resolution decisions: configured pair is invalid")
	}
	var employeeAccepted, managerAccepted bool
	for _, input := range identity.Inputs {
		if input.Kind != analysisdomain.InputResolutionDecision {
			continue
		}
		var storedDigest []byte
		var entityID string
		err := queryer.QueryRow(ctx, `
			SELECT decision.digest, decision.entity_id::text
			FROM stacks.resolution_decisions AS decision
			JOIN stacks.resolution_proposals AS proposal ON proposal.id = decision.proposal_id
			JOIN stacks.mentions AS mention ON mention.id = proposal.mention_id
			LEFT JOIN stacks.extraction_runs AS extraction_run ON extraction_run.id = mention.extraction_run_id
			LEFT JOIN stacks.document_versions AS extraction_version ON extraction_version.id = extraction_run.document_version_id
			LEFT JOIN stacks.source_documents AS source_document ON source_document.id = extraction_version.source_document_id
			WHERE decision.id = $1
			  AND decision.superseded_by_id IS NULL
			  AND decision.outcome IN ('accepted', 'created')
			  AND decision.currently_admissible
			  AND mention.currently_admissible
			  AND (mention.extraction_run_id IS NULL OR (
			      extraction_run.currently_admissible
			      AND source_document.current_document_version_id = extraction_run.document_version_id
			  ))
			FOR SHARE OF decision`, input.ID).Scan(&storedDigest, &entityID)
		if err == pgx.ErrNoRows {
			return fmt.Errorf("validate current resolution decisions: %w", analysisdomain.ErrStaleAnalysisInput)
		}
		if err != nil {
			return boundedAnalysisError("validate current resolution decisions", err)
		}
		if string(storedDigest) != string(input.Digest[:]) || (entityID != employeeID && entityID != managerID) {
			return fmt.Errorf("validate current resolution decisions: %w", analysisdomain.ErrStaleAnalysisInput)
		}
		employeeAccepted = employeeAccepted || entityID == employeeID
		managerAccepted = managerAccepted || entityID == managerID
	}
	if !employeeAccepted || !managerAccepted {
		return fmt.Errorf("validate current resolution decisions: %w", analysisdomain.ErrStaleAnalysisInput)
	}
	return nil
}

func findCompletedAnalysis(ctx context.Context, queryer completedAnalysisQueryer, identity analysisdomain.AnalysisIdentity) (analysisdomain.Report, bool, error) {
	var reportID string
	var reportJSON []byte
	var provider, dataMode, region, modelID *string
	var maxTokens *int
	err := queryer.QueryRow(ctx, `
		SELECT id::text, report_json, model_provider, data_mode, bedrock_region, model_id, max_output_tokens
		FROM stacks.analysis_runs
		WHERE input_digest = $1 AND state = 'complete' AND report_json IS NOT NULL
		  AND currently_admissible`, identity.InputDigest[:]).Scan(
		&reportID, &reportJSON, &provider, &dataMode, &region, &modelID, &maxTokens,
	)
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
	if report.ID != reportID || report.InputDigest != identity.InputDigest {
		return analysisdomain.Report{}, false, fmt.Errorf("load completed pair analysis: stored report identity conflicts")
	}
	if !storedAnalysisProvenanceMatches(identity, analysisModelProvenance{
		provider: provider, dataMode: dataMode, region: region, modelID: modelID, maxTokens: maxTokens,
	}) {
		return analysisdomain.Report{}, false, fmt.Errorf("load completed pair analysis: stored model provenance conflicts")
	}
	if err := validateCompletedReport(report, identity); err != nil {
		return analysisdomain.Report{}, false, fmt.Errorf("load completed pair analysis: stored report metadata is invalid")
	}
	return report, true, nil
}

func validateCompletedReport(report analysisdomain.Report, identity analysisdomain.AnalysisIdentity) error {
	if report.RecordedAt.IsZero() || strings.TrimSpace(report.ModelID) == "" || strings.TrimSpace(report.ModelID) != report.ModelID ||
		report.MaxTokens <= 0 || strings.TrimSpace(report.PromptVersion) == "" || strings.TrimSpace(report.PolicyVersion) == "" {
		return fmt.Errorf("complete pair analysis: report metadata is invalid")
	}
	if report.PromptVersion != identity.PromptVersion || report.PolicyVersion != identity.PolicyVersion ||
		report.Region != identity.Region || report.ModelID != identity.ModelID || report.MaxTokens != identity.MaxTokens {
		return fmt.Errorf("complete pair analysis: report configuration conflicts with input identity")
	}
	switch report.Status {
	case analysisdomain.StatusInsufficientEvidence, analysisdomain.StatusNoMaterialChange,
		analysisdomain.StatusMixedOrConflicting, analysisdomain.StatusPossibleDecline:
	default:
		return fmt.Errorf("complete pair analysis: report status is invalid")
	}
	return nil
}

type analysisModelProvenance struct {
	provider  *string
	dataMode  *string
	region    *string
	modelID   *string
	maxTokens *int
}

func analysisCompletionProvenance(completion analysisdomain.Completion) (analysisModelProvenance, error) {
	if completion.DataMode == "" {
		return analysisModelProvenance{}, nil
	}
	if err := (modelpolicy.Invocation{
		Provider: completion.Identity.Provider,
		DataMode: completion.DataMode,
		Region:   completion.Identity.Region,
	}).Validate(); err != nil {
		return analysisModelProvenance{}, fmt.Errorf("complete pair analysis: model policy is invalid")
	}
	provider := string(completion.Identity.Provider)
	dataMode := string(completion.DataMode)
	modelID := completion.Identity.ModelID
	maxTokens := completion.Identity.MaxTokens
	return analysisModelProvenance{
		provider: &provider, dataMode: &dataMode,
		region:  nullableModelRegion(completion.Identity.Provider, completion.Identity.Region),
		modelID: &modelID, maxTokens: &maxTokens,
	}, nil
}

func storedAnalysisProvenanceMatches(identity analysisdomain.AnalysisIdentity, stored analysisModelProvenance) bool {
	if stored.provider == nil && stored.dataMode == nil && stored.region == nil && stored.modelID == nil && stored.maxTokens == nil {
		return true
	}
	if stored.provider == nil || stored.dataMode == nil || stored.modelID == nil || stored.maxTokens == nil {
		return false
	}
	provider := modelpolicy.Provider(*stored.provider)
	dataMode := modelpolicy.DataMode(*stored.dataMode)
	if !provider.Valid() || (dataMode != modelpolicy.DataModePersonal && dataMode != modelpolicy.DataModeRestricted && dataMode != modelpolicy.DataModeLegacy) {
		return false
	}
	return provider == identity.Provider &&
		modelRegionMatches(provider, identity.Region, stored.region) &&
		*stored.modelID == identity.ModelID && *stored.maxTokens == identity.MaxTokens
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
