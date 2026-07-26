package postgres

import (
	"context"
	"strings"
	"testing"

	"github.com/JakeFAU/stacks/core/identity"
)

func TestCanonicalReviewerTransactionsRequireDatabase(t *testing.T) {
	store := ReviewerStore{}
	cases := []struct {
		name string
		call func() error
	}{
		{
			name: "append",
			call: func() error {
				_, err := store.AppendDecision(context.Background(), ReviewerDecisionCommand{})
				return err
			},
		},
		{
			name: "create",
			call: func() error {
				_, err := store.CreatePerson(context.Background(), ReviewerCreatePersonCommand{})
				return err
			},
		},
		{
			name: "directory",
			call: func() error {
				_, err := store.AcceptDirectoryCandidate(context.Background(), ReviewerDirectoryDecisionCommand{})
				return err
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			err := testCase.call()
			if err == nil || !strings.Contains(err.Error(), "database is not configured") {
				t.Fatalf("error = %v, want database configuration error", err)
			}
		})
	}
}

func TestReviewerDecisionCommandKeepsCanonicalValues(t *testing.T) {
	command := ReviewerDecisionCommand{
		Decision: identity.ResolutionDecision{},
		Aliases:  []identity.AliasAssertion{{}},
	}
	command.Aliases[0] = identity.AliasAssertion{}
	if len(command.Aliases) != 1 {
		t.Fatalf("alias count = %d, want 1", len(command.Aliases))
	}
}
