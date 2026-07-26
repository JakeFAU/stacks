package cli

import (
	"context"
	"errors"
	"fmt"

	"stacks/internal/directory"
)

// CanonicalReviewRepository is the CLI-owned, adapter-neutral persistence
// port. The application layer supplies the PostgreSQL mapping and owns IDs,
// time, and transaction commands.
type CanonicalReviewRepository interface {
	ListEntities(context.Context) ([]EntityView, error)
	ShowEntity(context.Context, string) (EntityView, error)
	ListReviewProposals(context.Context) ([]ReviewProposal, error)
	ShowReviewProposal(context.Context, string) (ReviewProposal, error)
	AcceptReviewProposal(context.Context, string, string) (ReviewDecision, error)
	AcceptDirectoryCandidate(context.Context, AcceptDirectoryInput) (ReviewDecision, error)
	RejectReviewProposal(context.Context, string) (ReviewDecision, error)
	CreateReviewPerson(
		context.Context,
		string,
		CreatePersonInput,
		*directory.ReviewerVerification,
	) (ReviewDecision, error)
	CorrectReviewDecision(context.Context, string, string) (ReviewDecision, error)
}

type canonicalReviewRepository = CanonicalReviewRepository

// CanonicalReviewStore adapts the application-owned canonical repository to
// the entity and review CLI services. It emits no logs or telemetry.
type CanonicalReviewStore struct {
	repository            canonicalReviewRepository
	reviewerEmailVerifier ReviewerEmailVerifier
}

// NewCanonicalReviewStore constructs the private local review boundary.
func NewCanonicalReviewStore(
	repository canonicalReviewRepository,
	reviewerEmailVerifier ...ReviewerEmailVerifier,
) *CanonicalReviewStore {
	store := &CanonicalReviewStore{repository: repository}
	if len(reviewerEmailVerifier) > 0 {
		store.reviewerEmailVerifier = reviewerEmailVerifier[0]
	}
	return store
}

func (store *CanonicalReviewStore) ListEntities(
	ctx context.Context,
) ([]EntityView, error) {
	if store == nil || store.repository == nil {
		return nil, fmt.Errorf("list entities: repository is not configured")
	}
	return store.repository.ListEntities(ctx)
}

func (store *CanonicalReviewStore) ShowEntity(
	ctx context.Context,
	entityID string,
) (EntityView, error) {
	if store == nil || store.repository == nil {
		return EntityView{}, fmt.Errorf("show entity: repository is not configured")
	}
	return store.repository.ShowEntity(ctx, entityID)
}

func (store *CanonicalReviewStore) ListReviewProposals(
	ctx context.Context,
) ([]ReviewProposal, error) {
	if store == nil || store.repository == nil {
		return nil, fmt.Errorf("list review proposals: repository is not configured")
	}
	return store.repository.ListReviewProposals(ctx)
}

func (store *CanonicalReviewStore) ShowReviewProposal(
	ctx context.Context,
	proposalID string,
) (ReviewProposal, error) {
	if store == nil || store.repository == nil {
		return ReviewProposal{}, fmt.Errorf("show review proposal: repository is not configured")
	}
	return store.repository.ShowReviewProposal(ctx, proposalID)
}

func (store *CanonicalReviewStore) AcceptReviewProposal(
	ctx context.Context,
	proposalID, entityID string,
) (ReviewDecision, error) {
	if store == nil || store.repository == nil {
		return ReviewDecision{}, fmt.Errorf("accept review proposal: repository is not configured")
	}
	return store.repository.AcceptReviewProposal(ctx, proposalID, entityID)
}

func (store *CanonicalReviewStore) AcceptDirectoryCandidate(
	ctx context.Context,
	input AcceptDirectoryInput,
) (ReviewDecision, error) {
	if store == nil || store.repository == nil {
		return ReviewDecision{}, fmt.Errorf("accept directory candidate: repository is not configured")
	}
	return store.repository.AcceptDirectoryCandidate(ctx, input)
}

func (store *CanonicalReviewStore) RejectReviewProposal(
	ctx context.Context,
	proposalID string,
) (ReviewDecision, error) {
	if store == nil || store.repository == nil {
		return ReviewDecision{}, fmt.Errorf("reject review proposal: repository is not configured")
	}
	return store.repository.RejectReviewProposal(ctx, proposalID)
}

func (store *CanonicalReviewStore) CreateReviewPerson(
	ctx context.Context,
	proposalID string,
	input CreatePersonInput,
) (ReviewDecision, error) {
	if store == nil || store.repository == nil {
		return ReviewDecision{}, fmt.Errorf("create review person: repository is not configured")
	}
	verification, err := verifyReviewerEmail(
		ctx,
		store.reviewerEmailVerifier,
		input.Email,
	)
	if err != nil {
		return ReviewDecision{}, err
	}
	return store.repository.CreateReviewPerson(
		ctx,
		proposalID,
		input,
		verification,
	)
}

func (store *CanonicalReviewStore) CorrectReviewDecision(
	ctx context.Context,
	effectiveDecisionID, entityID string,
) (ReviewDecision, error) {
	if store == nil || store.repository == nil {
		return ReviewDecision{}, fmt.Errorf("correct review decision: repository is not configured")
	}
	return store.repository.CorrectReviewDecision(
		ctx,
		effectiveDecisionID,
		entityID,
	)
}

func verifyReviewerEmail(
	ctx context.Context,
	verifier ReviewerEmailVerifier,
	email string,
) (*directory.ReviewerVerification, error) {
	if verifier == nil || email == "" {
		return nil, nil
	}
	verification, err := verifier.VerifyReviewerEmail(ctx, email)
	if err == nil {
		if !verification.ValidForEmail(email) {
			return nil, nil
		}
		return &verification, nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	if errors.Is(err, context.Canceled) {
		return nil, context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return nil, context.DeadlineExceeded
	}
	return nil, nil
}
