package cli

import (
	"context"
	"flag"
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
	Source             string
	Confidence         *float64
	Reason             string
}

// ReviewProposal is the private review projection of one resolution proposal.
type ReviewProposal struct {
	ID         string
	Context    string
	Candidates []ReviewCandidate
}

// ReviewDecision identifies an immutable review action.
type ReviewDecision struct {
	ID           string
	ProposalID   string
	SupersedesID string
	EntityID     string
	Outcome      string
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
func (command ReviewCommand) Run(ctx context.Context, args []string) error {
	if command.Service == nil {
		return fmt.Errorf("review command: service is not configured")
	}
	output := command.Output
	if output == nil {
		output = io.Discard
	}
	if len(args) == 1 && args[0] == "list" {
		proposals, err := command.Service.List(ctx)
		if err != nil {
			return err
		}
		for _, proposal := range proposals {
			renderReviewListProposal(output, proposal)
		}
		return nil
	}
	if len(args) > 0 && args[0] == "accept" && len(args) != 3 {
		return fmt.Errorf("review accept: proposal ID and entity ID are required")
	}
	if len(args) == 2 && args[0] == "show" {
		proposal, err := command.Service.Show(ctx, args[1])
		if err != nil {
			return err
		}
		renderProposal(output, proposal)
		return nil
	}
	if len(args) == 3 && args[0] == "accept" {
		_, err := command.Service.Accept(ctx, args[1], args[2])
		return err
	}
	if len(args) > 0 && args[0] == "accept-directory" {
		input, err := parseAcceptDirectory(args[1:])
		if err != nil {
			return err
		}
		_, err = command.Service.AcceptDirectory(ctx, input)
		return err
	}
	if len(args) > 0 && args[0] == "reject" && len(args) != 2 {
		return fmt.Errorf("review reject: proposal ID is required")
	}
	if len(args) == 2 && args[0] == "reject" {
		_, err := command.Service.Reject(ctx, args[1])
		return err
	}
	if len(args) >= 2 && args[0] == "create" {
		input, err := parseCreatePerson(args[2:])
		if err != nil {
			return err
		}
		_, err = command.Service.Create(ctx, args[1], input)
		return err
	}
	if len(args) > 0 && args[0] == "correct" && len(args) != 3 {
		return fmt.Errorf("review correct: effective decision ID and entity ID are required")
	}
	if len(args) == 3 && args[0] == "correct" {
		_, err := command.Service.Correct(ctx, args[1], args[2])
		return err
	}
	return fmt.Errorf("review command usage: review list | review show <proposal-id> | review accept <proposal-id> <entity-id> | review accept-directory <proposal-id> <directory-profile-id> [--entity <entity-id>] | review reject <proposal-id> | review create <proposal-id> --name <name> [--email <email>] | review correct <effective-decision-id> <entity-id>")
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
				highestRanked.Source,
				confidence,
				alternatives,
				boundedReviewListText(reason, maximumReviewListReasonRunes),
				boundedReviewListText(proposal.Context, maximumReviewListContextRunes),
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
		boundedReviewListText(proposal.Context, maximumReviewListContextRunes),
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

func parseCreatePerson(args []string) (CreatePersonInput, error) {
	flags := flag.NewFlagSet("review create", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	name := flags.String("name", "", "")
	email := flags.String("email", "", "")
	if err := flags.Parse(args); err != nil {
		return CreatePersonInput{}, fmt.Errorf("review create: %w", err)
	}
	if flags.NArg() != 0 || strings.TrimSpace(*name) == "" {
		return CreatePersonInput{}, fmt.Errorf("review create: --name is required")
	}
	return CreatePersonInput{Name: *name, Email: *email}, nil
}

func parseAcceptDirectory(args []string) (AcceptDirectoryInput, error) {
	if len(args) < 2 ||
		strings.TrimSpace(args[0]) == "" ||
		strings.TrimSpace(args[1]) == "" {
		return AcceptDirectoryInput{}, fmt.Errorf("review accept-directory: proposal ID and directory profile ID are required")
	}
	flags := flag.NewFlagSet("review accept-directory", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	entityID := flags.String("entity", "", "")
	if err := flags.Parse(args[2:]); err != nil {
		return AcceptDirectoryInput{}, fmt.Errorf("review accept-directory: %w", err)
	}
	if flags.NArg() != 0 {
		return AcceptDirectoryInput{}, fmt.Errorf("review accept-directory: unexpected arguments")
	}
	entityProvided := false
	flags.Visit(func(current *flag.Flag) {
		if current.Name == "entity" {
			entityProvided = true
		}
	})
	if entityProvided && strings.TrimSpace(*entityID) == "" {
		return AcceptDirectoryInput{}, fmt.Errorf("review accept-directory: --entity requires an entity ID")
	}
	return AcceptDirectoryInput{
		ProposalID:         args[0],
		DirectoryProfileID: args[1],
		EntityID:           *entityID,
	}, nil
}

func renderProposal(output io.Writer, proposal ReviewProposal) {
	fmt.Fprintf(output, "proposal %s\ncontext: %s\n", proposal.ID, proposal.Context)
	for _, candidate := range proposal.Candidates {
		if candidate.DirectoryProfileID != "" {
			if candidate.Confidence == nil {
				fmt.Fprintf(output, "directory candidate: %s display: %s email: %s source: %s reason: %s\n",
					candidate.DirectoryProfileID,
					candidate.DisplayName,
					candidate.MaskedEmail,
					candidate.Source,
					candidate.Reason,
				)
				continue
			}
			fmt.Fprintf(output, "directory candidate: %s display: %s email: %s source: %s confidence: %.6f reason: %s\n",
				candidate.DirectoryProfileID,
				candidate.DisplayName,
				candidate.MaskedEmail,
				candidate.Source,
				*candidate.Confidence,
				candidate.Reason,
			)
			continue
		}
		if candidate.Confidence == nil {
			fmt.Fprintf(output, "candidate: %s reason: %s\n", candidate.EntityID, candidate.Reason)
			continue
		}
		fmt.Fprintf(output, "candidate: %s confidence: %.6f reason: %s\n", candidate.EntityID, *candidate.Confidence, candidate.Reason)
	}
}
