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

func TestRunnerClearsAcceptDirectoryEntityBetweenExecutions(t *testing.T) {
	var invocations []Invocation
	runner := Runner{Execute: func(_ context.Context, invocation Invocation) error {
		invocations = append(invocations, invocation)
		return nil
	}}
	if err := runner.Run(t.Context(), []string{
		"review", "accept-directory", "proposal-1", "profile-1", "--entity", "person-1",
	}); err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	if err := runner.Run(t.Context(), []string{
		"review", "accept-directory", "proposal-2", "profile-2",
	}); err != nil {
		t.Fatalf("second Run() error = %v", err)
	}

	if len(invocations) != 2 {
		t.Fatalf("Execute invocations = %#v, want 2", invocations)
	}
	if invocations[0].AcceptDirectory == nil || *invocations[0].AcceptDirectory != (AcceptDirectoryInput{
		ProposalID: "proposal-1", DirectoryProfileID: "profile-1", EntityID: "person-1",
	}) {
		t.Fatalf("first accept-directory invocation = %#v, want entity person-1", invocations[0])
	}
	if invocations[1].AcceptDirectory == nil || *invocations[1].AcceptDirectory != (AcceptDirectoryInput{
		ProposalID: "proposal-2", DirectoryProfileID: "profile-2",
	}) {
		t.Fatalf("second accept-directory invocation = %#v, want an empty entity ID", invocations[1])
	}
}

func TestRunnerRejectsExplicitBlankCreateNameWithoutExecutingOrWritingStderr(t *testing.T) {
	var errorOutput strings.Builder
	calls := 0
	runner := Runner{
		Error: &errorOutput,
		Execute: func(context.Context, Invocation) error {
			calls++
			return nil
		},
	}
	err := runner.Run(t.Context(), []string{"review", "create", "proposal-1", "--name", ""})
	if err == nil || !strings.Contains(err.Error(), "--name is required") {
		t.Fatalf("Run() error = %v, want blank name rejection", err)
	}
	if calls != 0 {
		t.Fatalf("Execute calls = %d, want 0", calls)
	}
	if errorOutput.Len() != 0 {
		t.Fatalf("stderr = %q, want no private flag value", errorOutput.String())
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
		{"serve", []string{"serve", "unexpected"}, invalidCommandSyntaxMessage},
		{"doctor", []string{"doctor", "unexpected"}, invalidCommandSyntaxMessage},
		{"sync", []string{"sync", "unexpected"}, invalidCommandSyntaxMessage},
		{"analyze", []string{"analyze", "unexpected"}, invalidCommandSyntaxMessage},
		{"db migrate", []string{"db-migrate", "unexpected"}, invalidCommandSyntaxMessage},
		{"db status", []string{"db-status", "unexpected"}, invalidCommandSyntaxMessage},
		{"db reset", []string{"db-reset"}, invalidCommandSyntaxMessage},
		{"auth google", []string{"auth", "google", "unexpected"}, invalidCommandSyntaxMessage},
		{"auth directory", []string{"auth", "google-directory", "unexpected"}, invalidCommandSyntaxMessage},
		{"entities list", []string{"entities", "list", "unexpected"}, invalidCommandSyntaxMessage},
		{"entities show", []string{"entities", "show"}, invalidCommandSyntaxMessage},
		{"review list", []string{"review", "list", "unexpected"}, invalidCommandSyntaxMessage},
		{"review show", []string{"review", "show"}, invalidCommandSyntaxMessage},
		{"review accept", []string{"review", "accept", "proposal-1"}, invalidCommandSyntaxMessage},
		{"review accept directory", []string{"review", "accept-directory", "proposal-1"}, invalidCommandSyntaxMessage},
		{"review reject", []string{"review", "reject"}, invalidCommandSyntaxMessage},
		{"review create", []string{"review", "create"}, invalidCommandSyntaxMessage},
		{"review correct", []string{"review", "correct", "decision-1"}, invalidCommandSyntaxMessage},
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

func TestRunnerDoesNotExposeUnexpectedPositionalInputInSyntaxErrors(t *testing.T) {
	const privateToken = "synthetic-private-positional-token"
	var errorOutput strings.Builder
	err := (Runner{
		Error: &errorOutput,
		Execute: func(context.Context, Invocation) error {
			t.Fatal("Execute must not run for syntax errors")
			return nil
		},
	}).Run(t.Context(), []string{"serve", privateToken})
	if err == nil {
		t.Fatal("Run() error = nil, want syntax failure")
	}
	if strings.Contains(err.Error(), privateToken) || strings.Contains(errorOutput.String(), privateToken) {
		t.Fatalf("syntax failure exposed positional input: error=%q stderr=%q", err, errorOutput.String())
	}
}
