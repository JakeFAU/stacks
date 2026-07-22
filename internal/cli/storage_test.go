package cli

import (
	"context"
	"strings"
	"testing"
)

func TestStorageReviewStoreRejectsTransitionsWithoutRepository(t *testing.T) {
	store := NewStorageReviewStore(nil)
	cases := []struct {
		name string
		call func() error
	}{
		{name: "accept", call: func() error {
			_, err := store.AcceptReviewProposal(context.Background(), "proposal-1", "person-1")
			return err
		}},
		{name: "reject", call: func() error { _, err := store.RejectReviewProposal(context.Background(), "proposal-1"); return err }},
		{name: "correct", call: func() error {
			_, err := store.CorrectReviewDecision(context.Background(), "decision-1", "person-1")
			return err
		}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			err := testCase.call()
			if err == nil || !strings.Contains(err.Error(), "repository is not configured") {
				t.Fatalf("transition error = %v, want repository configuration error", err)
			}
		})
	}
}
