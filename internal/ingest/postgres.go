package ingest

import (
	"context"
	"crypto/sha256"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	postgres "github.com/JakeFAU/stacks/adapters/postgres"
	"github.com/JakeFAU/stacks/core/evidence"
	"github.com/JakeFAU/stacks/core/identity"
	"github.com/JakeFAU/stacks/core/observation"
	"github.com/JakeFAU/stacks/core/timepoint"
	"github.com/google/uuid"

	"stacks/internal/entity"
	"stacks/internal/modelpolicy"
)

const (
	extractionMethod                   = "model_extraction"
	canonicalWriteSetDigestVersion     = "stacks.ingest-write-set.v1.canonical"
	maximumSourceRevisionMetadataRunes = 1024
	resolutionProposalReason           = extractionMethod
	resolutionCandidateSource          = "accepted_alias_resolver"
	admissionReasonValidatedExtraction = "validated_extraction"
)

type completionStage string

const (
	completionStageEvidence             completionStage = "evidence"
	completionStageIdentityInputs       completionStage = "identity_inputs"
	completionStageIdentityAuthority    completionStage = "identity_authority"
	completionStageObservations         completionStage = "observations"
	completionStageAdmission            completionStage = "admission"
	completionStageCurrentVersion       completionStage = "current_version"
	completionStageExtractionCompletion completionStage = "extraction_completion"
)

// PostgresRepository maps the consumer-owned ingestion contract onto the
// provider-neutral PostgreSQL adapter.
type PostgresRepository struct {
	database   *postgres.Database
	now        func() time.Time
	newID      func() string
	afterStage func(completionStage) error
}

// NewPostgresRepository constructs canonical ingestion persistence over one
// caller-owned database.
func NewPostgresRepository(database *postgres.Database) *PostgresRepository {
	return &PostgresRepository{
		database: database,
		now: func() time.Time {
			return timepoint.Normalize(time.Now())
		},
		newID: uuid.NewString,
	}
}

// PrepareVersion stores content and source-revision provenance atomically,
// then claims or resumes the exact extraction derivation.
func (repository *PostgresRepository) PrepareVersion(
	ctx context.Context,
	version evidence.DocumentVersion,
	metadata SourceRevisionMetadata,
	derivation DerivationIdentity,
	dataMode modelpolicy.DataMode,
	leaseDuration time.Duration,
) (VersionState, error) {
	if err := repository.validate(); err != nil {
		return VersionState{}, err
	}
	if metadata.ProviderVersion != version.ProviderVersion() ||
		strings.TrimSpace(metadata.ProviderVersion) == "" ||
		utf8.RuneCountInString(metadata.ProviderVersion) > maximumSourceRevisionMetadataRunes ||
		utf8.RuneCountInString(metadata.ProviderRevision) > maximumSourceRevisionMetadataRunes {
		return VersionState{}, fmt.Errorf("prepare canonical version: source revision metadata is invalid")
	}
	if err := (modelpolicy.Invocation{
		Provider: derivation.Provider, DataMode: dataMode, Region: derivation.Region,
	}).Validate(); err != nil {
		return VersionState{}, fmt.Errorf("prepare canonical version: model policy is invalid")
	}
	wantDigest, err := ComputeDerivationDigest(version, derivation)
	if err != nil || wantDigest != derivation.Digest {
		return VersionState{}, fmt.Errorf("prepare canonical version: derivation identity is invalid")
	}
	if leaseDuration <= 0 {
		return VersionState{}, fmt.Errorf("prepare canonical version: lease duration is invalid")
	}
	claimedAt := timepoint.Normalize(repository.now())
	if claimedAt.IsZero() {
		return VersionState{}, fmt.Errorf("prepare canonical version: claim time is invalid")
	}
	runID := stableDerivationID(derivation.Digest, "run", "")
	attemptID, owner := repository.newID(), repository.newID()
	if strings.TrimSpace(attemptID) == "" || strings.TrimSpace(owner) == "" {
		return VersionState{}, fmt.Errorf("prepare canonical version: attempt identity is invalid")
	}

	var result VersionState
	err = repository.database.InTransaction(ctx, func(transaction *postgres.Transaction) error {
		put, err := transaction.PutDocumentVersion(ctx, version)
		if err != nil {
			return err
		}
		revision, err := evidence.NewSourceRevisionObservation(evidence.SourceRevisionObservationInput{
			Provider:              version.Provider(),
			ProviderDocumentID:    version.ProviderDocumentID(),
			DocumentDigestVersion: version.DigestVersion(),
			DocumentDigest:        version.Digest(),
			ProviderVersion:       metadata.ProviderVersion,
			ProviderRevision:      metadata.ProviderRevision,
			FirstRecordedAt:       put.Ref.RecordedAt,
		})
		if err != nil {
			return fmt.Errorf("construct canonical source revision: %w", err)
		}
		if _, err := transaction.PutSourceRevisionObservation(ctx, revision); err != nil {
			return err
		}
		state, err := transaction.PrepareExtraction(ctx, postgres.ExtractionRunInput{
			ID: runID, DocumentVersionID: put.Ref.VersionID,
			DerivationDigestVersion: derivationDigestVersion(derivation),
			DerivationDigest:        evidence.ContentDigest(derivation.Digest),
			Method:                  extractionMethod,
			Version:                 derivation.PromptVersion,
			Provider:                string(derivation.Provider),
			DataMode:                string(dataMode),
			Model:                   derivation.ModelID,
			PromptVersion:           derivation.PromptVersion,
			SchemaDigest:            evidence.ContentDigest(derivation.SchemaDigest),
			MaxOutputTokens:         derivation.MaxTokens,
			RecordedAt:              put.Ref.RecordedAt,
		}, postgres.LeaseRequest{
			AttemptID: attemptID, Owner: owner, ClaimedAt: claimedAt, LeaseDuration: leaseDuration,
		})
		if err != nil {
			return err
		}
		result = VersionState{
			VersionID: put.Ref.VersionID, RunID: state.RunID, AttemptID: state.AttemptID,
			DocumentRecordedAt: put.Ref.RecordedAt,
			LeaseExpiresAt:     state.LeaseExpiresAt,
			RetryCount:         max(0, state.AttemptNumber-1),
		}
		switch state.Status {
		case postgres.ExtractionClaimed:
			result.Status = VersionStatusPending
			result.LeaseOwner = owner
		case postgres.ExtractionBusy:
			result.Status = VersionStatusBusy
			result.FailureCode = FailureBusy
		case postgres.ExtractionCompleted:
			result.Status = VersionStatusComplete
		default:
			return fmt.Errorf("prepare canonical version: extraction state is invalid")
		}
		return nil
	})
	if err != nil {
		return VersionState{}, fmt.Errorf("prepare canonical version: %w", err)
	}
	return result, nil
}

// CompleteVersion commits the entire resolved canonical write set, advances
// the source pointer, and completes the attempt last.
func (repository *PostgresRepository) CompleteVersion(
	ctx context.Context,
	completion Completion,
) error {
	if err := repository.validate(); err != nil {
		return err
	}
	if err := ValidateForPersistence(completion); err != nil {
		return fmt.Errorf("complete canonical version input: %w", err)
	}
	document, err := repository.database.LoadDocumentVersion(ctx, completion.VersionID)
	if err != nil {
		return fmt.Errorf("complete canonical version: load owning document: %w", err)
	}
	resolved, err := resolveCanonicalWriteSet(completion)
	if err != nil {
		return fmt.Errorf("complete canonical version: %w", err)
	}
	writeSetDigest := digestCanonicalWriteSet(completion, resolved)
	completionInput := postgres.ExtractionCompletionInput{
		RunID: completion.RunID, AttemptID: completion.AttemptID,
		Owner: completion.LeaseOwner, CompletedAt: completion.CompletedAt,
		WriteSetDigestVersion: canonicalWriteSetDigestVersion,
		WriteSetDigest:        writeSetDigest,
	}

	err = repository.database.InTransaction(ctx, func(transaction *postgres.Transaction) error {
		status, err := transaction.CheckExtractionCompletion(
			ctx,
			postgres.ExtractionCompletionCheckInput{
				DocumentVersionID: completion.VersionID,
				Completion:        completionInput,
			},
		)
		if err != nil {
			return err
		}
		if status == postgres.ExtractionCompleted {
			// A completed canonical write set is an exact no-op. Optional
			// directory evidence and later reviewer authority are additive and
			// must not be replayed, reset, or interpreted by ingestion.
			return nil
		}
		for _, record := range completion.Evidence {
			if _, err := transaction.PutEvidenceSpan(ctx, record.Span); err != nil {
				return err
			}
		}
		if err := repository.stage(completionStageEvidence); err != nil {
			return err
		}
		for _, mention := range resolved.mentions {
			if _, err := transaction.PutMention(ctx, mention); err != nil {
				return err
			}
		}
		for _, proposal := range completion.Proposals {
			if _, err := transaction.PutResolutionProposal(ctx, proposal); err != nil {
				return err
			}
		}
		for _, candidate := range completion.Candidates {
			if _, err := transaction.PutResolutionCandidate(ctx, candidate); err != nil {
				return err
			}
		}
		if err := repository.stage(completionStageIdentityInputs); err != nil {
			return err
		}
		aliases := aliasesByDecision(completion.AliasAssertions)
		for _, decision := range completion.Decisions {
			if err := transaction.AppendResolutionDecision(
				ctx,
				decision,
				aliases[decision.ID()],
			); err != nil {
				return err
			}
		}
		if err := repository.stage(completionStageIdentityAuthority); err != nil {
			return err
		}
		for _, value := range resolved.observations {
			if _, err := transaction.PutObservation(ctx, value); err != nil {
				return err
			}
		}
		if err := repository.stage(completionStageObservations); err != nil {
			return err
		}
		for _, decision := range completion.AdmissionDecisions {
			if err := transaction.AppendAdmissionDecision(ctx, decision); err != nil {
				return err
			}
		}
		if err := repository.stage(completionStageAdmission); err != nil {
			return err
		}
		if err := transaction.SetCurrentDocumentVersion(
			ctx,
			document.Ref.SourceDocumentID,
			completion.VersionID,
		); err != nil {
			return err
		}
		if err := repository.stage(completionStageCurrentVersion); err != nil {
			return err
		}
		if err := transaction.CompleteExtraction(ctx, completionInput); err != nil {
			return err
		}
		return repository.stage(completionStageExtractionCompletion)
	})
	if err != nil {
		return fmt.Errorf("complete canonical version: %w", err)
	}
	return nil
}

// RecordFailure records one bounded terminal attempt result.
func (repository *PostgresRepository) RecordFailure(
	ctx context.Context,
	failure Failure,
) error {
	if err := repository.validate(); err != nil {
		return err
	}
	if failure.Status != VersionStatusIncomplete &&
		failure.Status != VersionStatusFailed {
		return fmt.Errorf("record canonical failure: status is invalid")
	}
	if !validFailureCode(failure.Code) {
		return fmt.Errorf("record canonical failure: code is invalid")
	}
	err := repository.database.InTransaction(ctx, func(transaction *postgres.Transaction) error {
		return transaction.RecordExtractionFailure(ctx, postgres.ExtractionFailureInput{
			RunID: failure.RunID, AttemptID: failure.AttemptID, Owner: failure.LeaseOwner,
			FailedAt: failure.FailedAt, FailureCode: string(failure.Code),
		})
	})
	if err != nil {
		return fmt.Errorf("record canonical failure: %w", err)
	}
	return nil
}

// EntitySnapshots returns only current effective accepted identity authority.
func (repository *PostgresRepository) EntitySnapshots(
	ctx context.Context,
) ([]entity.EntitySnapshot, error) {
	if err := repository.validate(); err != nil {
		return nil, err
	}
	return repository.database.EntitySnapshots(ctx)
}

func (repository *PostgresRepository) validate() error {
	if repository == nil || repository.database == nil ||
		repository.now == nil || repository.newID == nil {
		return fmt.Errorf("canonical PostgreSQL repository is not configured")
	}
	return nil
}

func (repository *PostgresRepository) stage(stage completionStage) error {
	if repository.afterStage == nil {
		return nil
	}
	return repository.afterStage(stage)
}

type resolvedWriteSet struct {
	mentions     []identity.MentionRecord
	observations []observation.Observation
}

func resolveCanonicalWriteSet(completion Completion) (resolvedWriteSet, error) {
	evidenceIDs := make(map[string]evidence.EvidenceID, len(completion.Evidence))
	for _, record := range completion.Evidence {
		evidenceIDs[record.Key] = record.Span.ID()
	}
	mentionIDs := make(map[string]identity.MentionID, len(completion.Mentions))
	result := resolvedWriteSet{
		mentions: make([]identity.MentionRecord, 0, len(completion.Mentions)),
	}
	for _, record := range completion.Mentions {
		mentionID := identity.MentionID(stableCompletionID(completion.RunID, "mention", record.Key))
		mentionIDs[record.Key] = mentionID
		value, err := identity.NewMention(identity.MentionInput{
			ID: mentionID, EvidenceID: evidenceIDs[record.EvidenceKey],
			DerivationRunID: completion.RunID, Surface: record.Surface,
			NormalizedName: record.NormalizedName, ProposedEmail: record.ProposedEmail,
			ProposedEmailEvidenceID: evidenceIDs[record.ProposedEmailEvidenceKey],
			Role:                    record.Role, RecordedAt: completion.CompletedAt,
		})
		if err != nil {
			return resolvedWriteSet{}, fmt.Errorf("construct canonical mention: %w", err)
		}
		result.mentions = append(result.mentions, value)
	}
	for _, draft := range completion.Observations {
		subject, err := resolveDraftTerm(draft.Subject, mentionIDs)
		if err != nil {
			return resolvedWriteSet{}, err
		}
		object, err := resolveDraftTerm(draft.Object, mentionIDs)
		if err != nil {
			return resolvedWriteSet{}, err
		}
		links := make([]observation.EvidenceLink, len(draft.Evidence))
		for index, link := range draft.Evidence {
			links[index] = observation.EvidenceLink{
				EvidenceID: evidenceIDs[link.EvidenceKey],
				Role:       link.Role,
			}
		}
		value, err := observation.NewObservation(observation.ObservationInput{
			ID: draft.ID,
			Statement: observation.Statement{
				Subject: subject, Predicate: draft.Predicate, Object: object,
			},
			ValidTime: draft.ValidTime, RecordedAt: draft.RecordedAt,
			Evidence: links, Derivation: draft.Derivation, Status: draft.Status,
			Confidence: draft.Confidence,
		})
		if err != nil {
			return resolvedWriteSet{}, fmt.Errorf("construct canonical observation: %w", err)
		}
		result.observations = append(result.observations, value)
	}
	return result, nil
}

func resolveDraftTerm(
	draft DraftTerm,
	mentions map[string]identity.MentionID,
) (observation.Term, error) {
	switch draft.Kind {
	case observation.TermAbsent:
		return observation.AbsentTerm(), nil
	case observation.TermText:
		return observation.NewTextTerm(draft.Text)
	case observation.TermMention:
		return observation.NewMentionTerm(string(mentions[draft.MentionKey]))
	case observation.TermEntity:
		return observation.NewEntityTerm(
			draft.EntityID,
			string(mentions[draft.GroundingMentionKey]),
		)
	default:
		return observation.Term{}, fmt.Errorf("canonical observation term kind is invalid")
	}
}

func aliasesByDecision(
	assertions []identity.AliasAssertion,
) map[identity.DecisionID][]identity.AliasAssertion {
	result := make(map[identity.DecisionID][]identity.AliasAssertion)
	for _, assertion := range assertions {
		result[assertion.DecisionID()] = append(
			result[assertion.DecisionID()],
			assertion,
		)
	}
	return result
}

func digestCanonicalWriteSet(
	completion Completion,
	resolved resolvedWriteSet,
) evidence.ContentDigest {
	hasher := sha256.New()
	writeDerivationString(hasher, canonicalWriteSetDigestVersion)
	writeDerivationString(hasher, completion.VersionID)
	writeDerivationString(hasher, completion.RunID)
	entries := make(
		[]string,
		0,
		len(completion.Evidence)+
			len(resolved.mentions)+
			len(completion.Proposals)+
			len(completion.Candidates)+
			len(completion.Decisions)+
			len(completion.AliasAssertions)+
			len(resolved.observations)+
			len(completion.AdmissionDecisions),
	)
	for _, record := range completion.Evidence {
		digest := record.Span.Digest()
		entries = append(entries, canonicalWriteSetEntry(
			"evidence",
			string(record.Span.ID()),
			record.Span.DigestVersion(),
			fmt.Sprintf("%x", digest[:]),
		))
	}
	for _, mention := range resolved.mentions {
		entries = append(entries, canonicalWriteSetEntry(
			"mention",
			string(mention.ID()),
			string(mention.EvidenceID()),
			mention.DerivationRunID(),
			mention.Surface(),
			mention.NormalizedName(),
			mention.ProposedEmail(),
			string(mention.ProposedEmailEvidenceID()),
			mention.Role(),
			mention.RecordedAt().Format(time.RFC3339Nano),
		))
	}
	for _, proposal := range completion.Proposals {
		fields := []string{
			"proposal",
			string(proposal.ID()),
			string(proposal.MentionID()),
			proposal.ReasonCode(),
			proposal.RecordedAt().Format(time.RFC3339Nano),
		}
		for _, evidenceID := range proposal.EvidenceIDs() {
			fields = append(fields, string(evidenceID))
		}
		entries = append(entries, canonicalWriteSetEntry(fields...))
	}
	for _, candidate := range completion.Candidates {
		source := candidate.Source()
		entries = append(entries, canonicalWriteSetEntry(
			"candidate",
			string(candidate.ID()),
			string(candidate.ProposalID()),
			string(candidate.EntityID()),
			fmt.Sprintf("%d", candidate.Rank()),
			fmt.Sprintf("%016x", math.Float64bits(candidate.Confidence())),
			candidate.ReasonCode(),
			source.Kind,
			source.Reference,
			candidate.RecordedAt().Format(time.RFC3339Nano),
		))
	}
	for _, decision := range completion.Decisions {
		digest := decision.Digest()
		entries = append(entries, canonicalWriteSetEntry(
			"identity_decision",
			string(decision.ID()),
			decision.DigestVersion(),
			fmt.Sprintf("%x", digest[:]),
		))
	}
	for _, assertion := range completion.AliasAssertions {
		alias := assertion.Alias()
		entries = append(entries, canonicalWriteSetEntry(
			"alias_assertion",
			string(assertion.ID()),
			string(assertion.DecisionID()),
			string(assertion.EntityID()),
			string(alias.Type),
			alias.Value,
			assertion.RecordedAt().Format(time.RFC3339Nano),
		))
	}
	for _, value := range resolved.observations {
		digest := value.Digest()
		entries = append(entries, canonicalWriteSetEntry(
			"observation",
			string(value.ID()),
			value.DigestVersion(),
			fmt.Sprintf("%x", digest[:]),
		))
	}
	for _, decision := range completion.AdmissionDecisions {
		digest := decision.Digest()
		entries = append(entries, canonicalWriteSetEntry(
			"admission_decision",
			decision.ID(),
			decision.DigestVersion(),
			fmt.Sprintf("%x", digest[:]),
		))
	}
	sort.Strings(entries)
	writeDerivationLength(hasher, uint64(len(entries)))
	for _, entry := range entries {
		writeDerivationString(hasher, entry)
	}
	var digest evidence.ContentDigest
	copy(digest[:], hasher.Sum(nil))
	return digest
}

func canonicalWriteSetEntry(fields ...string) string {
	hasher := sha256.New()
	for _, field := range fields {
		writeDerivationString(hasher, field)
	}
	return fmt.Sprintf("%x", hasher.Sum(nil))
}

func stableCompletionID(runID, kind, localKey string) string {
	return uuid.NewSHA1(
		uuid.NameSpaceOID,
		[]byte(strings.Join([]string{runID, kind, localKey}, "\x00")),
	).String()
}

func derivationDigestVersion(derivation DerivationIdentity) string {
	if derivation.Provider == modelpolicy.ProviderBedrock {
		return extractionDerivationDigestVersion
	}
	return providerDerivationDigestVersion
}

func validFailureCode(code FailureCode) bool {
	switch code {
	case FailureSource,
		FailureInvalidSource,
		FailureModel,
		FailureInvalidOutput,
		FailureStorage:
		return true
	default:
		return false
	}
}

var _ Repository = (*PostgresRepository)(nil)
