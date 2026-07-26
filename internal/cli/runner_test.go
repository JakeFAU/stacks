package cli

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
)

func TestRunnerPreservesSelectedCommandErrorIdentity(t *testing.T) {
	want := errors.New("selected command sentinel")
	err := (Runner{Execute: func(context.Context, Invocation) error { return want }}).Run(t.Context(), []string{"doctor"})
	if !errors.Is(err, want) {
		t.Fatalf("Run() error = %v, want selected command error identity", err)
	}
}

func TestRunnerDefaultsToServeAndSupportsExplicitServe(t *testing.T) {
	for _, args := range [][]string{nil, {"serve"}} {
		var got Invocation
		runner := Runner{Execute: func(_ context.Context, invocation Invocation) error {
			got = invocation
			return nil
		}}
		if err := runner.Run(t.Context(), args); err != nil {
			t.Fatalf("Run(%q) error = %v", args, err)
		}
		if got.Command != CommandServe || got.Action != "" || len(got.Arguments) != 0 {
			t.Fatalf("Run(%q) invocation = %#v, want serve", args, got)
		}
	}
}

func TestRunnerHelpDoesNotExecuteApplicationCommand(t *testing.T) {
	var output strings.Builder
	calls := 0
	runner := Runner{
		Output: &output,
		Error:  io.Discard,
		Execute: func(context.Context, Invocation) error {
			calls++
			return nil
		},
	}
	if err := runner.Run(t.Context(), []string{"review", "create", "--help"}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if calls != 0 {
		t.Fatalf("Execute calls = %d, want 0 for help", calls)
	}
	for _, want := range []string{"create <proposal-id>", "--name", "--email"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("help = %q, want %q", output.String(), want)
		}
	}
}

func TestRunnerParsesReviewCreateIntoTypedInvocation(t *testing.T) {
	var got Invocation
	runner := Runner{Execute: func(_ context.Context, invocation Invocation) error {
		got = invocation
		return nil
	}}
	args := []string{"review", "create", "proposal-1", "--name", "Synthetic Person", "--email", "person@example.test"}
	if err := runner.Run(t.Context(), args); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got.Command != CommandReview || got.Action != ActionCreate ||
		!reflect.DeepEqual(got.Arguments, []string{"proposal-1"}) ||
		got.CreatePerson == nil ||
		*got.CreatePerson != (CreatePersonInput{Name: "Synthetic Person", Email: "person@example.test"}) {
		t.Fatalf("invocation = %#v, want typed review create", got)
	}
}

func TestRunnerRejectsExplicitBlankDirectoryEntityWithoutExecuting(t *testing.T) {
	calls := 0
	runner := Runner{Execute: func(context.Context, Invocation) error {
		calls++
		return nil
	}}
	err := runner.Run(t.Context(), []string{
		"review", "accept-directory", "proposal-1", "profile-1", "--entity", "",
	})
	if err == nil || !strings.Contains(err.Error(), "--entity requires an entity ID") {
		t.Fatalf("Run() error = %v, want blank entity rejection", err)
	}
	if calls != 0 {
		t.Fatalf("Execute calls = %d, want 0", calls)
	}
}

func TestRunnerUsesFreshFlagsAndWritersForEachExecution(t *testing.T) {
	var first, second Invocation
	firstOutput := new(strings.Builder)
	secondOutput := new(strings.Builder)
	firstRunner := Runner{Output: firstOutput, Execute: func(_ context.Context, invocation Invocation) error {
		first = invocation
		return nil
	}}
	secondRunner := Runner{Output: secondOutput, Execute: func(_ context.Context, invocation Invocation) error {
		second = invocation
		return nil
	}}
	if err := firstRunner.Run(t.Context(), []string{"review", "create", "proposal-1", "--name", "One", "--email", "one@example.test"}); err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	if err := secondRunner.Run(t.Context(), []string{"review", "create", "proposal-2", "--name", "Two"}); err != nil {
		t.Fatalf("second Run() error = %v", err)
	}
	if first.CreatePerson == nil || first.CreatePerson.Email != "one@example.test" || second.CreatePerson == nil || second.CreatePerson.Email != "" {
		t.Fatalf("create invocations = %#v / %#v, want independent flag values", first, second)
	}
	if err := firstRunner.Run(t.Context(), []string{"review", "create", "--help"}); err != nil {
		t.Fatalf("first help error = %v", err)
	}
	if err := secondRunner.Run(t.Context(), []string{"review", "accept-directory", "--help"}); err != nil {
		t.Fatalf("second help error = %v", err)
	}
	if strings.Contains(firstOutput.String(), "accept-directory") || strings.Contains(secondOutput.String(), "--email") {
		t.Fatalf("help writers crossed execution boundaries: first=%q second=%q", firstOutput.String(), secondOutput.String())
	}
}

func TestRunnerRejectsInvalidLeafArityWithoutExecuting(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"serve", []string{"serve", "unexpected"}, "unknown command"},
		{"doctor", []string{"doctor", "unexpected"}, "unknown command"},
		{"sync", []string{"sync", "unexpected"}, "unknown command"},
		{"analyze", []string{"analyze", "unexpected"}, "unknown command"},
		{"db migrate", []string{"db-migrate", "unexpected"}, "unknown command"},
		{"db status", []string{"db-status", "unexpected"}, "unknown command"},
		{"db reset", []string{"db-reset"}, "accepts"},
		{"auth google", []string{"auth", "google", "unexpected"}, "unknown command"},
		{"auth directory", []string{"auth", "google-directory", "unexpected"}, "unknown command"},
		{"entities list", []string{"entities", "list", "unexpected"}, "unknown command"},
		{"entities show", []string{"entities", "show"}, "accepts"},
		{"review list", []string{"review", "list", "unexpected"}, "unknown command"},
		{"review show", []string{"review", "show"}, "accepts"},
		{"review accept", []string{"review", "accept", "proposal-1"}, "accepts"},
		{"review accept directory", []string{"review", "accept-directory", "proposal-1"}, "accepts"},
		{"review reject", []string{"review", "reject"}, "accepts"},
		{"review create", []string{"review", "create"}, "accepts"},
		{"review correct", []string{"review", "correct", "decision-1"}, "accepts"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			calls := 0
			err := (Runner{Execute: func(context.Context, Invocation) error {
				calls++
				return nil
			}}).Run(t.Context(), testCase.args)
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), testCase.want) {
				t.Fatalf("Run(%q) error = %v, want %q category", testCase.args, err, testCase.want)
			}
			if calls != 0 {
				t.Fatalf("Execute calls = %d, want 0", calls)
			}
		})
	}
}
