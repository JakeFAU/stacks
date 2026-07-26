package ingest

import (
	"context"
	"time"

	"github.com/JakeFAU/stacks/core/admission"
	"github.com/JakeFAU/stacks/core/evidence"
	"github.com/JakeFAU/stacks/core/identity"
	"github.com/JakeFAU/stacks/core/observation"

	"stacks/internal/entity"
	"stacks/internal/modelpolicy"
)

func newInitialAdmission(
	runID string,
	targetKind admission.TargetKind,
	targetID string,
	recordedAt time.Time,
) (admission.Decision, error) {
	return admission.NewDecision(admission.DecisionInput{
		ID: stableCompletionID(
			runID,
			"admission",
			string(targetKind)+"\x00"+targetID,
		),
		TargetKind: targetKind, TargetID: targetID, Outcome: admission.Admitted,
		ReasonCode: admissionReasonValidatedExtraction,
		Authority:  admission.AuthorityPolicy,
		RecordedAt: recordedAt,
	})
}

// DraftTerm refers to model-local mention keys until the completion
// transaction resolves them to canonical identifiers.
type DraftTerm struct {
	Kind                observation.TermKind
	Text                string
	MentionKey          string
	EntityID            string
	GroundingMentionKey string
}

// DraftEvidenceLink refers to model-local evidence keys until completion.
type DraftEvidenceLink struct {
	EvidenceKey string
	Role        observation.EvidenceRole
}

// CanonicalObservationDraft is a complete canonical observation payload whose
// local evidence and mention references are resolved inside one transaction.
type CanonicalObservationDraft struct {
	ID         observation.ObservationID
	Subject    DraftTerm
	Predicate  observation.Predicate
	Object     DraftTerm
	ValidTime  observation.TemporalExtent
	RecordedAt time.Time
	Evidence   []DraftEvidenceLink
	Derivation observation.Derivation
	Status     observation.EpistemicStatus
	Confidence *observation.Confidence
}

// Completion is the complete atomic canonical write set for one extraction.
type Completion struct {
	VersionID          string
	RunID              string
	AttemptID          string
	LeaseOwner         string
	CompletedAt        time.Time
	Evidence           []EvidenceRecord
	Mentions           []MentionRecord
	Proposals          []identity.ResolutionProposal
	Candidates         []identity.ResolutionCandidate
	Decisions          []identity.ResolutionDecision
	AliasAssertions    []identity.AliasAssertion
	Observations       []CanonicalObservationDraft
	AdmissionDecisions []admission.Decision
}

// VersionState identifies a prepared content version and extraction attempt.
type VersionState struct {
	VersionID          string
	RunID              string
	AttemptID          string
	LeaseOwner         string
	DocumentRecordedAt time.Time
	LeaseExpiresAt     time.Time
	Status             VersionStatus
	RetryCount         int
	FailureCode        FailureCode
}

// Failure is the bounded terminal state for one owned extraction attempt.
type Failure struct {
	RunID      string
	AttemptID  string
	LeaseOwner string
	Status     VersionStatus
	Code       FailureCode
	FailedAt   time.Time
}

// SourceRevisionMetadata preserves provider revision provenance outside the
// stable content-version value.
type SourceRevisionMetadata struct {
	ProviderVersion  string
	ProviderRevision string
}

// Repository owns canonical processing state and the atomic completion
// boundary.
type Repository interface {
	PrepareVersion(
		context.Context,
		evidence.DocumentVersion,
		SourceRevisionMetadata,
		DerivationIdentity,
		modelpolicy.DataMode,
		time.Duration,
	) (VersionState, error)
	CompleteVersion(context.Context, Completion) error
	RecordFailure(context.Context, Failure) error
	EntitySnapshots(context.Context) ([]entity.EntitySnapshot, error)
}
