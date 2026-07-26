package app

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	postgres "github.com/JakeFAU/stacks/adapters/postgres"
	"github.com/JakeFAU/stacks/core/identity"
	"github.com/JakeFAU/stacks/core/timepoint"

	"stacks/internal/cli"
	"stacks/internal/directory"
	"stacks/internal/entity"
)

const (
	reviewerAcceptedReason  = "explicit_reviewer_acceptance"
	reviewerRejectedReason  = "explicit_reviewer_rejection"
	reviewerCorrectedReason = "explicit_reviewer_correction"
	reviewerCreatedReason   = "explicit_reviewer_creation"
)

// IDGenerator supplies caller-owned opaque identities.
type IDGenerator interface {
	NewID() string
}

// Clock supplies caller-owned canonical recorded time.
type Clock interface {
	Now() time.Time
}

// ClockFunc adapts a function into a Clock.
type ClockFunc func() time.Time

// Now implements Clock.
func (clock ClockFunc) Now() time.Time { return clock() }

type canonicalReviewer interface {
	ListEntities(context.Context) ([]postgres.ReviewerEntityRecord, error)
	LoadEntity(context.Context, identity.EntityID) (postgres.ReviewerEntityRecord, error)
	ListProposals(context.Context) ([]postgres.ReviewerProposalRecord, error)
	LoadProposal(context.Context, identity.ProposalID) (postgres.ReviewerProposalRecord, error)
	LoadDecision(context.Context, identity.DecisionID) (identity.ResolutionDecision, error)
	AppendDecision(context.Context, postgres.ReviewerDecisionCommand) (identity.ResolutionDecision, error)
	CreatePerson(context.Context, postgres.ReviewerCreatePersonCommand) (identity.ResolutionDecision, error)
	AcceptDirectoryCandidate(
		context.Context,
		postgres.ReviewerDirectoryDecisionCommand,
	) (identity.ResolutionDecision, error)
}

// ReviewRepository maps the CLI-owned review port onto canonical reviewer
// records and transactions.
type ReviewRepository struct {
	store canonicalReviewer
	ids   IDGenerator
	clock Clock
}

// NewReviewRepository constructs canonical review mapping with explicit
// identity and time capabilities.
func NewReviewRepository(
	store canonicalReviewer,
	ids IDGenerator,
	clock Clock,
) *ReviewRepository {
	return &ReviewRepository{store: store, ids: ids, clock: clock}
}

func (repository *ReviewRepository) ListEntities(
	ctx context.Context,
) ([]cli.EntityView, error) {
	if err := repository.validate(); err != nil {
		return nil, fmt.Errorf("list canonical entities: %w", err)
	}
	records, err := repository.store.ListEntities(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]cli.EntityView, len(records))
	for index, record := range records {
		result[index] = entityView(record)
	}
	return result, nil
}

func (repository *ReviewRepository) ShowEntity(
	ctx context.Context,
	entityID string,
) (cli.EntityView, error) {
	if err := repository.validate(); err != nil {
		return cli.EntityView{}, fmt.Errorf("show canonical entity: %w", err)
	}
	record, err := repository.store.LoadEntity(ctx, identity.EntityID(entityID))
	if err != nil {
		return cli.EntityView{}, err
	}
	return entityView(record), nil
}

func entityView(record postgres.ReviewerEntityRecord) cli.EntityView {
	aliases := make([]string, len(record.Aliases))
	for index, alias := range record.Aliases {
		aliases[index] = alias.Value
	}
	evidence := make([]cli.ReviewEvidence, len(record.Evidence))
	for index, citation := range record.Evidence {
		evidence[index] = cli.ReviewEvidence{ID: citation.ID, Quote: citation.Quote}
	}
	return cli.EntityView{
		ID:           string(record.Entity.ID()),
		DisplayName:  record.Entity.DisplayName(),
		RecordedAt:   record.Entity.RecordedAt(),
		Aliases:      aliases,
		MentionCount: record.MentionCount,
		Evidence:     evidence,
	}
}

func (repository *ReviewRepository) ListReviewProposals(
	ctx context.Context,
) ([]cli.ReviewProposal, error) {
	if err := repository.validate(); err != nil {
		return nil, fmt.Errorf("list canonical proposals: %w", err)
	}
	records, err := repository.store.ListProposals(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]cli.ReviewProposal, len(records))
	for index, record := range records {
		result[index] = proposalView(record)
	}
	return result, nil
}

func (repository *ReviewRepository) ShowReviewProposal(
	ctx context.Context,
	proposalID string,
) (cli.ReviewProposal, error) {
	if err := repository.validate(); err != nil {
		return cli.ReviewProposal{}, fmt.Errorf("show canonical proposal: %w", err)
	}
	record, err := repository.store.LoadProposal(ctx, identity.ProposalID(proposalID))
	if err != nil {
		return cli.ReviewProposal{}, err
	}
	return proposalView(record), nil
}

func proposalView(record postgres.ReviewerProposalRecord) cli.ReviewProposal {
	result := cli.ReviewProposal{
		ID:         string(record.Proposal.ID()),
		Evidence:   make([]cli.ReviewEvidence, len(record.Evidence)),
		Candidates: make([]cli.ReviewCandidate, len(record.Candidates)),
	}
	for index, evidence := range record.Evidence {
		result.Evidence[index] = cli.ReviewEvidence{ID: evidence.ID, Quote: evidence.Quote}
	}
	if record.EffectiveDecision != nil {
		effective := decisionView(*record.EffectiveDecision)
		result.EffectiveDecision = &effective
	}
	for index, record := range record.Candidates {
		confidence := record.Candidate.Confidence()
		source := record.Candidate.Source()
		result.Candidates[index] = cli.ReviewCandidate{
			EntityID:        string(record.Candidate.EntityID()),
			DisplayName:     record.Entity.DisplayName(),
			SourceKind:      source.Kind,
			SourceReference: source.Reference,
			Confidence:      &confidence,
			Reason:          record.Candidate.ReasonCode(),
		}
		if record.Directory != nil {
			result.Candidates[index].DirectoryProfileID = record.Directory.SnapshotID
			result.Candidates[index].DisplayName = record.Directory.DisplayName
			result.Candidates[index].MaskedEmail = record.Directory.MaskedEmail
			result.Candidates[index].DirectorySource = record.Directory.Source
		}
	}
	return result
}

func (repository *ReviewRepository) AcceptReviewProposal(
	ctx context.Context,
	proposalID, entityID string,
) (cli.ReviewDecision, error) {
	proposal, err := repository.loadProposal(ctx, proposalID)
	if err != nil {
		return cli.ReviewDecision{}, err
	}
	recordedAt, err := repository.recordedAt()
	if err != nil {
		return cli.ReviewDecision{}, err
	}
	decision, err := repository.newDecision(
		proposal.Proposal.ID(),
		identity.DecisionAccepted,
		identity.EntityID(entityID),
		"",
		reviewerAcceptedReason,
		recordedAt,
	)
	if err != nil {
		return cli.ReviewDecision{}, err
	}
	aliases, err := repository.aliases(
		decision,
		recordedAt,
		identity.Alias{Type: identity.AliasTypeName, Value: proposal.Mention.NormalizedName()},
	)
	if err != nil {
		return cli.ReviewDecision{}, err
	}
	persisted, err := repository.store.AppendDecision(ctx, postgres.ReviewerDecisionCommand{
		Decision: decision,
		Aliases:  aliases,
	})
	if err != nil {
		return cli.ReviewDecision{}, err
	}
	return decisionView(persisted), nil
}

func (repository *ReviewRepository) RejectReviewProposal(
	ctx context.Context,
	proposalID string,
) (cli.ReviewDecision, error) {
	proposal, err := repository.loadProposal(ctx, proposalID)
	if err != nil {
		return cli.ReviewDecision{}, err
	}
	recordedAt, err := repository.recordedAt()
	if err != nil {
		return cli.ReviewDecision{}, err
	}
	decision, err := repository.newDecision(
		proposal.Proposal.ID(),
		identity.DecisionRejected,
		"",
		"",
		reviewerRejectedReason,
		recordedAt,
	)
	if err != nil {
		return cli.ReviewDecision{}, err
	}
	persisted, err := repository.store.AppendDecision(ctx, postgres.ReviewerDecisionCommand{Decision: decision})
	if err != nil {
		return cli.ReviewDecision{}, err
	}
	return decisionView(persisted), nil
}

func (repository *ReviewRepository) CreateReviewPerson(
	ctx context.Context,
	proposalID string,
	input cli.CreatePersonInput,
	verification *directory.ReviewerVerification,
) (cli.ReviewDecision, error) {
	proposal, err := repository.loadProposal(ctx, proposalID)
	if err != nil {
		return cli.ReviewDecision{}, err
	}
	recordedAt, err := repository.recordedAt()
	if err != nil {
		return cli.ReviewDecision{}, err
	}
	entityID, err := repository.newID("entity")
	if err != nil {
		return cli.ReviewDecision{}, err
	}
	person, err := identity.NewEntity(identity.EntityInput{
		ID:          identity.EntityID(entityID),
		Kind:        identity.KindPerson,
		DisplayName: input.Name,
		RecordedAt:  recordedAt,
	})
	if err != nil {
		return cli.ReviewDecision{}, fmt.Errorf("construct reviewer entity: %w", err)
	}
	decision, err := repository.newDecision(
		proposal.Proposal.ID(),
		identity.DecisionAccepted,
		person.ID(),
		"",
		reviewerCreatedReason,
		recordedAt,
	)
	if err != nil {
		return cli.ReviewDecision{}, err
	}
	aliasValues := []identity.Alias{
		{Type: identity.AliasTypeName, Value: proposal.Mention.NormalizedName()},
		{Type: identity.AliasTypeName, Value: identity.NormalizeName(input.Name)},
	}
	if input.Email != "" {
		aliasValues = append(aliasValues, identity.Alias{
			Type: identity.AliasTypeEmail, Value: identity.NormalizeEmail(input.Email),
		})
	}
	aliases, err := repository.aliases(decision, recordedAt, aliasValues...)
	if err != nil {
		return cli.ReviewDecision{}, err
	}
	command := postgres.ReviewerCreatePersonCommand{
		Entity:   person,
		Decision: decision,
		Aliases:  aliases,
	}
	if verification != nil {
		evidence, valid := postgresReviewerDirectoryEvidence(
			proposal,
			*verification,
			input.Email,
			recordedAt,
		)
		if valid &&
			postgres.ValidateReviewerDirectoryEvidence(evidence, decision) == nil {
			command.DirectoryEvidence = &evidence
		}
	}
	persisted, err := repository.store.CreatePerson(ctx, command)
	if err != nil {
		return cli.ReviewDecision{}, err
	}
	return decisionView(persisted), nil
}

func (repository *ReviewRepository) AcceptDirectoryCandidate(
	ctx context.Context,
	input cli.AcceptDirectoryInput,
) (cli.ReviewDecision, error) {
	proposal, err := repository.loadProposal(ctx, input.ProposalID)
	if err != nil {
		return cli.ReviewDecision{}, err
	}
	entityID := identity.EntityID(input.EntityID)
	if entityID == "" {
		for _, candidate := range proposal.Candidates {
			if candidate.Directory != nil &&
				candidate.Directory.SnapshotID == input.DirectoryProfileID {
				entityID = candidate.Candidate.EntityID()
				break
			}
		}
	}
	recordedAt, err := repository.recordedAt()
	if err != nil {
		return cli.ReviewDecision{}, err
	}
	decision, err := repository.newDecision(
		proposal.Proposal.ID(),
		identity.DecisionAccepted,
		entityID,
		"",
		reviewerAcceptedReason,
		recordedAt,
	)
	if err != nil {
		return cli.ReviewDecision{}, err
	}
	aliases, err := repository.aliases(
		decision,
		recordedAt,
		identity.Alias{Type: identity.AliasTypeName, Value: proposal.Mention.NormalizedName()},
	)
	if err != nil {
		return cli.ReviewDecision{}, err
	}
	persisted, err := repository.store.AcceptDirectoryCandidate(
		ctx,
		postgres.ReviewerDirectoryDecisionCommand{
			Decision: decision, Aliases: aliases, SnapshotID: input.DirectoryProfileID,
		},
	)
	if err != nil {
		return cli.ReviewDecision{}, err
	}
	return decisionView(persisted), nil
}

func (repository *ReviewRepository) CorrectReviewDecision(
	ctx context.Context,
	effectiveDecisionID, entityID string,
) (cli.ReviewDecision, error) {
	if err := repository.validate(); err != nil {
		return cli.ReviewDecision{}, err
	}
	predecessor, err := repository.store.LoadDecision(
		ctx,
		identity.DecisionID(effectiveDecisionID),
	)
	if err != nil {
		return cli.ReviewDecision{}, err
	}
	proposal, err := repository.store.LoadProposal(ctx, predecessor.ProposalID())
	if err != nil {
		return cli.ReviewDecision{}, err
	}
	recordedAt, err := repository.recordedAt()
	if err != nil {
		return cli.ReviewDecision{}, err
	}
	decision, err := repository.newDecision(
		predecessor.ProposalID(),
		identity.DecisionAccepted,
		identity.EntityID(entityID),
		predecessor.ID(),
		reviewerCorrectedReason,
		recordedAt,
	)
	if err != nil {
		return cli.ReviewDecision{}, err
	}
	aliases, err := repository.aliases(
		decision,
		recordedAt,
		identity.Alias{Type: identity.AliasTypeName, Value: proposal.Mention.NormalizedName()},
	)
	if err != nil {
		return cli.ReviewDecision{}, err
	}
	persisted, err := repository.store.AppendDecision(ctx, postgres.ReviewerDecisionCommand{
		Decision: decision, Aliases: aliases,
	})
	if err != nil {
		return cli.ReviewDecision{}, err
	}
	return decisionView(persisted), nil
}

func (repository *ReviewRepository) loadProposal(
	ctx context.Context,
	proposalID string,
) (postgres.ReviewerProposalRecord, error) {
	if err := repository.validate(); err != nil {
		return postgres.ReviewerProposalRecord{}, err
	}
	return repository.store.LoadProposal(ctx, identity.ProposalID(proposalID))
}

func (repository *ReviewRepository) newDecision(
	proposalID identity.ProposalID,
	outcome identity.DecisionOutcome,
	entityID identity.EntityID,
	supersedesID identity.DecisionID,
	reason string,
	recordedAt time.Time,
) (identity.ResolutionDecision, error) {
	id, err := repository.newID("decision")
	if err != nil {
		return identity.ResolutionDecision{}, err
	}
	value, err := identity.NewResolutionDecision(identity.ResolutionDecisionInput{
		ID:           identity.DecisionID(id),
		ProposalID:   proposalID,
		Outcome:      outcome,
		EntityID:     entityID,
		Authority:    identity.AuthorityReviewer,
		ReasonCode:   reason,
		RecordedAt:   recordedAt,
		SupersedesID: supersedesID,
	})
	if err != nil {
		return identity.ResolutionDecision{}, fmt.Errorf("construct reviewer decision: %w", err)
	}
	return value, nil
}

func (repository *ReviewRepository) aliases(
	decision identity.ResolutionDecision,
	recordedAt time.Time,
	values ...identity.Alias,
) ([]identity.AliasAssertion, error) {
	unique := make(map[string]identity.Alias)
	for _, alias := range values {
		switch alias.Type {
		case identity.AliasTypeName:
			alias.Value = identity.NormalizeName(alias.Value)
		case identity.AliasTypeEmail:
			alias.Value = identity.NormalizeEmail(alias.Value)
		}
		if strings.TrimSpace(alias.Value) == "" {
			continue
		}
		unique[string(alias.Type)+"\x00"+alias.Value] = alias
	}
	keys := make([]string, 0, len(unique))
	for key := range unique {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]identity.AliasAssertion, 0, len(keys))
	for _, key := range keys {
		id, err := repository.newID("alias assertion")
		if err != nil {
			return nil, err
		}
		assertion, err := identity.NewAliasAssertion(identity.AliasAssertionInput{
			ID:         identity.AliasAssertionID(id),
			DecisionID: decision.ID(),
			EntityID:   decision.EntityID(),
			Alias:      unique[key],
			RecordedAt: recordedAt,
		})
		if err != nil {
			return nil, fmt.Errorf("construct reviewer alias: %w", err)
		}
		result = append(result, assertion)
	}
	return result, nil
}

func (repository *ReviewRepository) recordedAt() (time.Time, error) {
	if repository.clock == nil {
		return time.Time{}, fmt.Errorf("canonical reviewer clock is not configured")
	}
	value := timepoint.Normalize(repository.clock.Now())
	if value.IsZero() {
		return time.Time{}, fmt.Errorf("canonical reviewer clock returned zero")
	}
	return value, nil
}

func (repository *ReviewRepository) newID(kind string) (string, error) {
	if repository.ids == nil {
		return "", fmt.Errorf("canonical reviewer ID generator is not configured")
	}
	value := strings.TrimSpace(repository.ids.NewID())
	if value == "" {
		return "", fmt.Errorf("canonical reviewer %s ID is empty", kind)
	}
	return value, nil
}

func (repository *ReviewRepository) validate() error {
	if repository == nil || repository.store == nil {
		return fmt.Errorf("canonical reviewer store is not configured")
	}
	if repository.ids == nil {
		return fmt.Errorf("canonical reviewer ID generator is not configured")
	}
	if repository.clock == nil {
		return fmt.Errorf("canonical reviewer clock is not configured")
	}
	return nil
}

func decisionView(value identity.ResolutionDecision) cli.ReviewDecision {
	return cli.ReviewDecision{
		ID:           string(value.ID()),
		ProposalID:   string(value.ProposalID()),
		SupersedesID: string(value.SupersedesID()),
		EntityID:     string(value.EntityID()),
		Outcome:      string(value.Outcome()),
		Authority:    string(value.Authority()),
	}
}

func postgresReviewerDirectoryEvidence(
	proposal postgres.ReviewerProposalRecord,
	verification directory.ReviewerVerification,
	reviewerEmail string,
	recordedAt time.Time,
) (postgres.ReviewerDirectoryEvidenceCommand, bool) {
	if !verification.ValidForEmail(reviewerEmail) {
		return postgres.ReviewerDirectoryEvidenceCommand{}, false
	}
	var retryAfter *time.Time
	if verification.RetryAfter != nil {
		delay := verification.RetryAfter.Sub(verification.RecordedAt)
		if verification.RecordedAt.IsZero() || delay < 0 {
			return postgres.ReviewerDirectoryEvidenceCommand{}, false
		}
		value := timepoint.Normalize(recordedAt.Add(delay))
		if value.Before(recordedAt) {
			return postgres.ReviewerDirectoryEvidenceCommand{}, false
		}
		retryAfter = &value
	}
	nameQuote := ""
	if len(proposal.Evidence) > 0 {
		nameQuote = proposal.Evidence[0].Quote
	}
	return postgres.ReviewerDirectoryEvidenceCommand{
		Mention: postgres.DirectoryPendingMention{
			MentionID:      string(proposal.Mention.ID()),
			ProposalID:     string(proposal.Proposal.ID()),
			Surface:        proposal.Mention.Surface(),
			NormalizedName: proposal.Mention.NormalizedName(),
			ProposedEmail:  proposal.Mention.ProposedEmail(),
			NameQuote:      nameQuote,
		},
		Query: postgres.DirectoryQuery{
			Kind:          string(verification.Query.Kind),
			Name:          verification.Query.Name,
			Email:         verification.Query.Email,
			EmailEvidence: string(verification.Query.EmailEvidence),
		},
		Lookup: postgres.DirectoryLookupResult{
			Provider:   verification.Lookup.Provider,
			Outcome:    string(verification.Lookup.Outcome),
			Profiles:   postgresReviewerProfiles(verification.Lookup.Profiles),
			RetryAfter: verification.Lookup.RetryAfter,
		},
		Evaluation:   postgresReviewerEvaluation(verification.Evaluation),
		AttemptCount: verification.AttemptCount,
		RecordedAt:   recordedAt,
		RetryAfter:   retryAfter,
	}, true
}

func postgresReviewerEvaluation(
	value entity.DirectoryEvaluation,
) postgres.DirectoryEvaluation {
	result := postgres.DirectoryEvaluation{
		Outcome:       string(value.Outcome),
		EntityID:      value.EntityID,
		CreatePerson:  value.CreatePerson,
		AcceptedEmail: value.AcceptedEmail,
		Candidates:    postgresReviewerProfiles(value.Candidates),
	}
	if value.Profile != nil {
		profile := postgresReviewerProfile(*value.Profile)
		result.Profile = &profile
	}
	return result
}

func postgresReviewerProfiles(
	values []entity.DirectoryProfile,
) []postgres.DirectoryProfile {
	result := make([]postgres.DirectoryProfile, len(values))
	for index, value := range values {
		result[index] = postgresReviewerProfile(value)
	}
	return result
}

func postgresReviewerProfile(value entity.DirectoryProfile) postgres.DirectoryProfile {
	emails := make([]postgres.DirectoryEmail, len(value.Emails))
	for index, email := range value.Emails {
		emails[index] = postgres.DirectoryEmail{Value: email.Value, Primary: email.Primary}
	}
	return postgres.DirectoryProfile{
		Provider: value.Provider, SubjectID: value.SubjectID,
		Source: string(value.Source), DisplayName: value.DisplayName,
		Emails: emails, ObservedAt: value.ObservedAt,
	}
}
