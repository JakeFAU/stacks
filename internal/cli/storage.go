package cli

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"stacks/internal/entity"
	"stacks/internal/storage"
)

// StorageReviewStore adapts the PostgreSQL entity repository to private CLI
// projections. It performs no logging or telemetry of review content.
type StorageReviewStore struct {
	repository *storage.EntityRepository
}

// NewStorageReviewStore creates a CLI store backed by the entity repository.
func NewStorageReviewStore(repository *storage.EntityRepository) *StorageReviewStore {
	return &StorageReviewStore{repository: repository}
}

// ListEntities implements EntityStore.
func (store *StorageReviewStore) ListEntities(ctx context.Context) ([]EntityView, error) {
	if store.repository == nil {
		return nil, fmt.Errorf("list entities: repository is not configured")
	}
	details, err := store.repository.ListEntityDetails(ctx)
	if err != nil {
		return nil, err
	}
	entities := make([]EntityView, len(details))
	for index, detail := range details {
		entities[index] = EntityView{ID: detail.ID, DisplayName: detail.DisplayName, RecordedAt: detail.RecordedAt, Aliases: detail.Aliases, MentionCount: detail.MentionCount, Evidence: detail.Evidence}
	}
	return entities, nil
}

// ShowEntity implements EntityStore.
func (store *StorageReviewStore) ShowEntity(ctx context.Context, entityID string) (EntityView, error) {
	if store.repository == nil {
		return EntityView{}, fmt.Errorf("show entity: repository is not configured")
	}
	detail, err := store.repository.ShowEntityDetail(ctx, entityID)
	if err != nil {
		return EntityView{}, err
	}
	return EntityView{ID: detail.ID, DisplayName: detail.DisplayName, RecordedAt: detail.RecordedAt, Aliases: detail.Aliases, MentionCount: detail.MentionCount, Evidence: detail.Evidence}, nil
}

// ListReviewProposals implements ReviewStore.
func (store *StorageReviewStore) ListReviewProposals(ctx context.Context) ([]ReviewProposal, error) {
	if store.repository == nil {
		return nil, fmt.Errorf("list review proposals: repository is not configured")
	}
	details, err := store.repository.ListResolutionProposalDetails(ctx)
	if err != nil {
		return nil, err
	}
	proposals := make([]ReviewProposal, len(details))
	for index, detail := range details {
		proposals[index] = reviewProposalFromStorage(detail)
	}
	return proposals, nil
}

// ShowReviewProposal implements ReviewStore.
func (store *StorageReviewStore) ShowReviewProposal(ctx context.Context, proposalID string) (ReviewProposal, error) {
	if store.repository == nil {
		return ReviewProposal{}, fmt.Errorf("show review proposal: repository is not configured")
	}
	detail, err := store.repository.ShowResolutionProposalDetail(ctx, proposalID)
	if err != nil {
		return ReviewProposal{}, err
	}
	return reviewProposalFromStorage(detail), nil
}

// AcceptReviewProposal implements ReviewStore.
func (store *StorageReviewStore) AcceptReviewProposal(ctx context.Context, proposalID, entityID string) (ReviewDecision, error) {
	if store.repository == nil {
		return ReviewDecision{}, fmt.Errorf("accept review proposal: repository is not configured")
	}
	decision, err := store.repository.RecordReviewDecision(ctx, storage.ResolutionDecisionInput{ProposalID: proposalID, Outcome: storage.ResolutionOutcomeAccepted, EntityID: entityID})
	if err != nil {
		return ReviewDecision{}, err
	}
	return reviewDecisionFromStorage(decision), nil
}

// RejectReviewProposal implements ReviewStore.
func (store *StorageReviewStore) RejectReviewProposal(ctx context.Context, proposalID string) (ReviewDecision, error) {
	if store.repository == nil {
		return ReviewDecision{}, fmt.Errorf("reject review proposal: repository is not configured")
	}
	decision, err := store.repository.RecordReviewDecision(ctx, storage.ResolutionDecisionInput{ProposalID: proposalID, Outcome: storage.ResolutionOutcomeRejected})
	if err != nil {
		return ReviewDecision{}, err
	}
	return reviewDecisionFromStorage(decision), nil
}

// CreateReviewPerson implements ReviewStore.
func (store *StorageReviewStore) CreateReviewPerson(ctx context.Context, proposalID string, input CreatePersonInput) (ReviewDecision, error) {
	if store.repository == nil {
		return ReviewDecision{}, fmt.Errorf("create review person: repository is not configured")
	}
	aliases := []storage.AliasInput{{Type: string(entity.AliasTypeName), NormalizedValue: entity.NormalizeName(input.Name)}}
	if input.Email != "" {
		aliases = append(aliases, storage.AliasInput{Type: string(entity.AliasTypeEmail), NormalizedValue: entity.NormalizeEmail(input.Email)})
	}
	_, decision, err := store.repository.CreateReviewPerson(ctx, storage.CreateReviewPersonInput{
		ProposalID:  proposalID,
		EntityID:    uuid.NewString(),
		Kind:        string(entity.KindPerson),
		DisplayName: input.Name,
		Aliases:     aliases,
	})
	if err != nil {
		return ReviewDecision{}, err
	}
	return reviewDecisionFromStorage(decision), nil
}

// CorrectReviewDecision implements ReviewStore.
func (store *StorageReviewStore) CorrectReviewDecision(ctx context.Context, effectiveDecisionID, entityID string) (ReviewDecision, error) {
	if store.repository == nil {
		return ReviewDecision{}, fmt.Errorf("correct review decision: repository is not configured")
	}
	decision, err := store.repository.CorrectReviewDecision(ctx, effectiveDecisionID, storage.ResolutionDecisionInput{Outcome: storage.ResolutionOutcomeAccepted, EntityID: entityID})
	if err != nil {
		return ReviewDecision{}, err
	}
	return reviewDecisionFromStorage(decision), nil
}

func reviewProposalFromStorage(detail storage.ResolutionProposalDetail) ReviewProposal {
	proposal := ReviewProposal{ID: detail.ID, Context: detail.Context, Candidates: make([]ReviewCandidate, len(detail.Candidates))}
	for index, candidate := range detail.Candidates {
		proposal.Candidates[index] = ReviewCandidate{EntityID: candidate.EntityID, Confidence: candidate.Confidence, Reason: candidate.Reason}
	}
	return proposal
}

func reviewDecisionFromStorage(decision storage.ResolutionDecision) ReviewDecision {
	return ReviewDecision{ID: decision.ID, ProposalID: decision.ProposalID, SupersedesID: decision.SupersedesID, EntityID: decision.EntityID, Outcome: string(decision.Outcome)}
}
