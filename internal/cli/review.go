package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"stacks/internal/directory"
)

const (
	maximumReviewListReasonRunes  = 96
	maximumReviewListContextRunes = 160
	maximumReviewListDisplayRunes = 96
	reviewListTruncationMarker    = "..."
)

// ReviewCandidate is one ranked, non-authoritative identity suggestion.
type ReviewCandidate struct {
	EntityID           string
	DirectoryProfileID string
	DisplayName        string
	MaskedEmail        string
	SourceKind         string
	SourceReference    string
	DirectorySource    string
	Confidence         *float64
	Reason             string
}

// ReviewEvidence is one exact private citation.
type ReviewEvidence struct {
	ID    string
	Quote string
}

// ReviewProposal is the private review projection of one resolution proposal.
type ReviewProposal struct {
	ID                string
	Evidence          []ReviewEvidence
	Candidates        []ReviewCandidate
	EffectiveDecision *ReviewDecision
}

// ReviewDecision identifies an immutable review action.
type ReviewDecision struct {
	ID           string
	ProposalID   string
	SupersedesID string
	EntityID     string
	Outcome      string
	Authority    string
}

// CreatePersonInput is the validated user request for a new person entity.
type CreatePersonInput struct {
	Name  string
	Email string
}

// AcceptDirectoryInput identifies one exact directory-backed candidate and
// optionally the existing person the reviewer selected.
type AcceptDirectoryInput struct {
	ProposalID         string
	DirectoryProfileID string
	EntityID           string
}

// ReviewerEmailVerifier performs optional bounded directory verification for
// an email explicitly supplied by the local reviewer.
type ReviewerEmailVerifier interface {
	VerifyReviewerEmail(context.Context, string) (directory.ReviewerVerification, error)
}

// ReviewStore owns durable review transitions. Implementations must append
// decisions and reject stale effective-decision corrections.
type ReviewStore interface {
	ListReviewProposals(ctx context.Context) ([]ReviewProposal, error)
	ShowReviewProposal(ctx context.Context, proposalID string) (ReviewProposal, error)
	AcceptReviewProposal(ctx context.Context, proposalID, entityID string) (ReviewDecision, error)
	AcceptDirectoryCandidate(ctx context.Context, input AcceptDirectoryInput) (ReviewDecision, error)
	RejectReviewProposal(ctx context.Context, proposalID string) (ReviewDecision, error)
	CreateReviewPerson(ctx context.Context, proposalID string, input CreatePersonInput) (ReviewDecision, error)
	CorrectReviewDecision(ctx context.Context, effectiveDecisionID, entityID string) (ReviewDecision, error)
}

// ReviewService coordinates private review state without emitting review
// context to logs or telemetry.
type ReviewService struct {
	Store ReviewStore
}

// List returns pending proposals for local review.
func (service ReviewService) List(ctx context.Context) ([]ReviewProposal, error) {
	if service.Store == nil {
		return nil, fmt.Errorf("list review proposals: store is not configured")
	}
	return service.Store.ListReviewProposals(ctx)
}

// Show returns one proposal and its private context.
func (service ReviewService) Show(ctx context.Context, proposalID string) (ReviewProposal, error) {
	if strings.TrimSpace(proposalID) == "" {
		return ReviewProposal{}, fmt.Errorf("show review proposal: proposal ID is required")
	}
	if service.Store == nil {
		return ReviewProposal{}, fmt.Errorf("show review proposal: store is not configured")
	}
	return service.Store.ShowReviewProposal(ctx, proposalID)
}

// Accept appends an acceptance for an explicitly identified proposal.
func (service ReviewService) Accept(ctx context.Context, proposalID, entityID string) (ReviewDecision, error) {
	if strings.TrimSpace(proposalID) == "" || strings.TrimSpace(entityID) == "" {
		return ReviewDecision{}, fmt.Errorf("accept review proposal: proposal ID and entity ID are required")
	}
	if service.Store == nil {
		return ReviewDecision{}, fmt.Errorf("accept review proposal: store is not configured")
	}
	return service.Store.AcceptReviewProposal(ctx, proposalID, entityID)
}

// AcceptDirectory appends reviewer authority for one exact directory snapshot.
func (service ReviewService) AcceptDirectory(ctx context.Context, input AcceptDirectoryInput) (ReviewDecision, error) {
	if strings.TrimSpace(input.ProposalID) == "" ||
		strings.TrimSpace(input.DirectoryProfileID) == "" {
		return ReviewDecision{}, fmt.Errorf("accept directory candidate: proposal ID and directory profile ID are required")
	}
	if service.Store == nil {
		return ReviewDecision{}, fmt.Errorf("accept directory candidate: store is not configured")
	}
	return service.Store.AcceptDirectoryCandidate(ctx, input)
}

// Reject appends a rejection for an explicitly identified proposal.
func (service ReviewService) Reject(ctx context.Context, proposalID string) (ReviewDecision, error) {
	if strings.TrimSpace(proposalID) == "" {
		return ReviewDecision{}, fmt.Errorf("reject review proposal: proposal ID is required")
	}
	if service.Store == nil {
		return ReviewDecision{}, fmt.Errorf("reject review proposal: store is not configured")
	}
	return service.Store.RejectReviewProposal(ctx, proposalID)
}

// Create creates a person and appends its acceptance for a proposal.
func (service ReviewService) Create(ctx context.Context, proposalID string, input CreatePersonInput) (ReviewDecision, error) {
	if strings.TrimSpace(proposalID) == "" || strings.TrimSpace(input.Name) == "" {
		return ReviewDecision{}, fmt.Errorf("create review person: proposal ID and name are required")
	}
	if service.Store == nil {
		return ReviewDecision{}, fmt.Errorf("create review person: store is not configured")
	}
	return service.Store.CreateReviewPerson(ctx, proposalID, input)
}

// Correct appends a replacement decision for one currently effective decision.
func (service ReviewService) Correct(ctx context.Context, effectiveDecisionID, entityID string) (ReviewDecision, error) {
	if strings.TrimSpace(effectiveDecisionID) == "" || strings.TrimSpace(entityID) == "" {
		return ReviewDecision{}, fmt.Errorf("correct review decision: effective decision ID and entity ID are required")
	}
	if service.Store == nil {
		return ReviewDecision{}, fmt.Errorf("correct review decision: store is not configured")
	}
	return service.Store.CorrectReviewDecision(ctx, effectiveDecisionID, entityID)
}

// ReviewCommand renders and applies private review actions through stdout only.
type ReviewCommand struct {
	Service *ReviewService
	Output  io.Writer
}

// Run executes a review subcommand.
func (command ReviewCommand) Run(ctx context.Context, invocation Invocation) error {
	if command.Service == nil {
		return fmt.Errorf("review command: service is not configured")
	}
	output := command.Output
	if output == nil {
		output = io.Discard
	}
	switch invocation.Action {
	case ActionList:
		proposals, err := command.Service.List(ctx)
		if err != nil {
			return err
		}
		for _, proposal := range proposals {
			renderReviewListProposal(output, proposal)
		}
		return nil
	case ActionShow:
		if len(invocation.Arguments) != 1 {
			return fmt.Errorf("review command: invocation is invalid")
		}
		proposal, err := command.Service.Show(ctx, invocation.Arguments[0])
		if err != nil {
			return err
		}
		renderProposal(output, proposal)
		return nil
	case ActionAccept:
		if len(invocation.Arguments) != 2 {
			return fmt.Errorf("review command: invocation is invalid")
		}
		_, err := command.Service.Accept(ctx, invocation.Arguments[0], invocation.Arguments[1])
		return err
	case ActionAcceptDirectory:
		if invocation.AcceptDirectory == nil {
			return fmt.Errorf("review command: invocation is invalid")
		}
		_, err := command.Service.AcceptDirectory(ctx, *invocation.AcceptDirectory)
		return err
	case ActionReject:
		if len(invocation.Arguments) != 1 {
			return fmt.Errorf("review command: invocation is invalid")
		}
		_, err := command.Service.Reject(ctx, invocation.Arguments[0])
		return err
	case ActionCreate:
		if len(invocation.Arguments) != 1 || invocation.CreatePerson == nil {
			return fmt.Errorf("review command: invocation is invalid")
		}
		_, err := command.Service.Create(ctx, invocation.Arguments[0], *invocation.CreatePerson)
		return err
	case ActionCorrect:
		if len(invocation.Arguments) != 2 {
			return fmt.Errorf("review command: invocation is invalid")
		}
		_, err := command.Service.Correct(ctx, invocation.Arguments[0], invocation.Arguments[1])
		return err
	default:
		return fmt.Errorf("review command: invocation is invalid")
	}
}

func renderReviewListProposal(output io.Writer, proposal ReviewProposal) {
	guess := "none"
	confidence := "unknown"
	reason := ""
	alternatives := 0
	if len(proposal.Candidates) > 0 {
		highestRanked := proposal.Candidates[0]
		guess = highestRanked.EntityID
		if guess == "" {
			guess = highestRanked.DirectoryProfileID
		}
		reason = highestRanked.Reason
		alternatives = len(proposal.Candidates) - 1
		if highestRanked.Confidence != nil {
			confidence = fmt.Sprintf("%.3f", *highestRanked.Confidence)
		}
		if highestRanked.DirectoryProfileID != "" {
			fmt.Fprintf(output, "%s | guess=%s | display=%q | email=%q | source=%s | confidence=%s | alternatives=%d | reason=%q | context=%q\n",
				proposal.ID,
				guess,
				boundedReviewListText(highestRanked.DisplayName, maximumReviewListDisplayRunes),
				highestRanked.MaskedEmail,
				highestRanked.DirectorySource,
				confidence,
				alternatives,
				boundedReviewListText(reason, maximumReviewListReasonRunes),
				boundedReviewListText(reviewProposalContext(proposal), maximumReviewListContextRunes),
			)
			return
		}
	}
	fmt.Fprintf(output, "%s | guess=%s | confidence=%s | alternatives=%d | reason=%q | context=%q\n",
		proposal.ID,
		guess,
		confidence,
		alternatives,
		boundedReviewListText(reason, maximumReviewListReasonRunes),
		boundedReviewListText(reviewProposalContext(proposal), maximumReviewListContextRunes),
	)
}

func boundedReviewListText(value string, maximumRunes int) string {
	normalized := strings.Join(strings.Fields(value), " ")
	runes := []rune(normalized)
	if len(runes) <= maximumRunes {
		return normalized
	}
	marker := []rune(reviewListTruncationMarker)
	return string(runes[:maximumRunes-len(marker)]) + reviewListTruncationMarker
}

func renderProposal(output io.Writer, proposal ReviewProposal) {
	fmt.Fprintf(output, "proposal %s\n", proposal.ID)
	for _, evidence := range proposal.Evidence {
		fmt.Fprintf(output, "evidence %s: %s\n", evidence.ID, evidence.Quote)
	}
	if proposal.EffectiveDecision != nil {
		fmt.Fprintf(
			output,
			"effective decision: %s outcome: %s entity: %s authority: %s\n",
			proposal.EffectiveDecision.ID,
			proposal.EffectiveDecision.Outcome,
			proposal.EffectiveDecision.EntityID,
			proposal.EffectiveDecision.Authority,
		)
	}
	for _, candidate := range proposal.Candidates {
		source := ""
		if candidate.SourceKind != "" || candidate.SourceReference != "" {
			source = fmt.Sprintf(
				" source-kind: %s source-ref: %s",
				candidate.SourceKind,
				candidate.SourceReference,
			)
		}
		if candidate.DirectoryProfileID != "" {
			if candidate.Confidence == nil {
				fmt.Fprintf(output, "directory candidate: %s display: %s email: %s source: %s%s reason: %s\n",
					candidate.DirectoryProfileID,
					boundedReviewListText(
						candidate.DisplayName,
						maximumReviewListDisplayRunes,
					),
					candidate.MaskedEmail,
					candidate.DirectorySource,
					source,
					candidate.Reason,
				)
				continue
			}
			fmt.Fprintf(output, "directory candidate: %s display: %s email: %s source: %s%s confidence: %.6f reason: %s\n",
				candidate.DirectoryProfileID,
				boundedReviewListText(
					candidate.DisplayName,
					maximumReviewListDisplayRunes,
				),
				candidate.MaskedEmail,
				candidate.DirectorySource,
				source,
				*candidate.Confidence,
				candidate.Reason,
			)
			continue
		}
		if candidate.Confidence == nil {
			fmt.Fprintf(output, "candidate: %s%s reason: %s\n", candidate.EntityID, source, candidate.Reason)
			continue
		}
		fmt.Fprintf(output, "candidate: %s%s confidence: %.6f reason: %s\n", candidate.EntityID, source, *candidate.Confidence, candidate.Reason)
	}
}

func reviewProposalContext(proposal ReviewProposal) string {
	if len(proposal.Evidence) > 0 {
		return proposal.Evidence[0].Quote
	}
	return ""
}
