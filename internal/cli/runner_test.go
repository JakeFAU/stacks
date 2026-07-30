package cli

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/JakeFAU/stacks/core/identity"
	"github.com/JakeFAU/stacks/core/observation"
	"github.com/JakeFAU/stacks/core/temporal"

	"stacks/internal/query"
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

func TestRunnerRejectsRetiredAnalyzeBeforeExecution(t *testing.T) {
	calls := 0
	err := (Runner{Execute: func(context.Context, Invocation) error {
		calls++
		return nil
	}}).Run(t.Context(), []string{"analyze"})
	if err == nil {
		t.Fatal("Run(analyze) error = nil, want retired command rejection")
	}
	if calls != 0 {
		t.Fatalf("Execute calls = %d, want 0", calls)
	}
}

func TestRunnerHelpOmitsRetiredAnalyze(t *testing.T) {
	var output strings.Builder
	err := (Runner{Output: &output}).Run(t.Context(), []string{"--help"})
	if err != nil {
		t.Fatalf("Run(--help) error = %v", err)
	}
	if strings.Contains(output.String(), "analyze") {
		t.Fatalf("root help retains retired analyze command:\n%s", output.String())
	}
}

func TestRunnerCarriesExplicitConfigBeforeAndAfterCommand(t *testing.T) {
	for _, args := range [][]string{
		{"--config", "/synthetic/stacks.yaml", "sync"},
		{"sync", "--config", "/synthetic/stacks.yaml"},
	} {
		var got Invocation
		err := (Runner{Execute: func(_ context.Context, invocation Invocation) error {
			got = invocation
			return nil
		}}).Run(t.Context(), args)
		if err != nil {
			t.Fatalf("Run(%q) error = %v", args, err)
		}
		if got.ConfigFile == nil || *got.ConfigFile != "/synthetic/stacks.yaml" {
			t.Fatalf("ConfigFile = %#v", got.ConfigFile)
		}
	}
}

func TestRunnerDistinguishesOmittedAndBlankConfig(t *testing.T) {
	var omitted Invocation
	if err := (Runner{Execute: func(_ context.Context, invocation Invocation) error {
		omitted = invocation
		return nil
	}}).Run(t.Context(), []string{"serve"}); err != nil {
		t.Fatal(err)
	}
	if omitted.ConfigFile != nil {
		t.Fatalf("omitted ConfigFile = %#v, want nil", omitted.ConfigFile)
	}

	for _, value := range []string{"", " \t "} {
		calls := 0
		err := (Runner{Execute: func(context.Context, Invocation) error {
			calls++
			return nil
		}}).Run(t.Context(), []string{"--config", value, "serve"})
		if err == nil || err.Error() != invalidCommandSyntaxMessage || calls != 0 {
			t.Fatalf("config %q error/calls = %v/%d, want %q/0", value, err, calls, invalidCommandSyntaxMessage)
		}
	}
}

func TestRunnerClearsConfigBetweenExecutions(t *testing.T) {
	var invocations []Invocation
	runner := Runner{Execute: func(_ context.Context, invocation Invocation) error {
		invocations = append(invocations, invocation)
		return nil
	}}
	if err := runner.Run(t.Context(), []string{"--config", "/synthetic/first.yaml", "sync"}); err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	if err := runner.Run(t.Context(), []string{"sync"}); err != nil {
		t.Fatalf("second Run() error = %v", err)
	}
	if len(invocations) != 2 || invocations[0].ConfigFile == nil || *invocations[0].ConfigFile != "/synthetic/first.yaml" || invocations[1].ConfigFile != nil {
		t.Fatalf("invocations = %#v, want independent config selections", invocations)
	}
}

func TestRunnerParsesConfigValidationTargets(t *testing.T) {
	tests := []struct {
		args        []string
		wantCommand CommandName
		wantAction  Action
	}{
		{[]string{"config", "validate", "serve"}, CommandServe, ""},
		{[]string{"config", "validate", "doctor"}, CommandDoctor, ""},
		{[]string{"config", "validate", "sync"}, CommandSync, ""},
		{[]string{"config", "validate", "entities"}, CommandEntities, ""},
		{[]string{"config", "validate", "review"}, CommandReview, ""},
		{[]string{"config", "validate", "query"}, CommandQuery, ""},
		{[]string{"config", "validate", "query", "ask"}, CommandQuery, ActionAsk},
		{[]string{"config", "validate", "db-migrate"}, CommandDBMigrate, ""},
		{[]string{"config", "validate", "db-status"}, CommandDBStatus, ""},
		{[]string{"config", "validate", "db-reset"}, CommandDBReset, ""},
		{[]string{"config", "validate", "auth", "google"}, CommandAuth, ActionAuthGoogle},
		{[]string{"config", "validate", "auth", "google-directory"}, CommandAuth, ActionAuthGoogleDirectory},
	}
	for _, testCase := range tests {
		t.Run(strings.Join(testCase.args, " "), func(t *testing.T) {
			var got Invocation
			err := (Runner{Execute: func(_ context.Context, invocation Invocation) error {
				got = invocation
				return nil
			}}).Run(t.Context(), testCase.args)
			if err != nil {
				t.Fatalf("Run(%q) error = %v", testCase.args, err)
			}
			if got.Command != CommandConfig || got.Action != ActionValidate ||
				got.ConfigValidation == nil ||
				got.ConfigValidation.Command != testCase.wantCommand ||
				got.ConfigValidation.Action != testCase.wantAction {
				t.Fatalf("Run(%q) invocation = %#v", testCase.args, got)
			}
		})
	}
}

func TestRunnerParsesQueryAskWithoutReadingInput(t *testing.T) {
	var got Invocation
	input := &readTrackingReader{payload: "synthetic private question"}
	err := (Runner{
		Input: input,
		Execute: func(_ context.Context, invocation Invocation) error {
			got = invocation
			return nil
		},
	}).Run(t.Context(), []string{
		"query", "ask", "--entity", "entity-atlas-001", "--entity", "entity-atlas-002",
		"--reference-time", "2026-07-29T12:00:00-04:00", "--output", "json",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if input.reads != 0 {
		t.Fatalf("stdin reads = %d, want none during Cobra parsing", input.reads)
	}
	if got.Command != CommandQuery || got.Action != ActionAsk || got.QueryAsk == nil ||
		got.QueryAsk.Output != QueryOutputJSON ||
		!reflect.DeepEqual(got.QueryAsk.EntityIDs, []identity.EntityID{"entity-atlas-001", "entity-atlas-002"}) ||
		got.QueryAsk.ReferenceTime.Format(time.RFC3339) != "2026-07-29T16:00:00Z" {
		t.Fatalf("invocation = %#v, want parsed query ask", got)
	}
}

func TestRunnerRejectsInvalidQueryAskSyntaxBeforeExecution(t *testing.T) {
	valid := []string{"query", "ask", "--entity", "entity-atlas-001", "--reference-time", "2026-07-29T12:00:00Z"}
	tests := []struct {
		name string
		args []string
	}{
		{"query group", []string{"query"}},
		{"ask group misuse", []string{"query", "ask", "unexpected"}},
		{"missing entity", withoutQueryFlag(valid, "--entity")},
		{"blank entity", replaceQueryFlag(valid, "--entity", " \t ")},
		{"duplicate entity", append(append([]string{}, valid...), "--entity", "entity-atlas-001")},
		{"missing reference", withoutQueryFlag(valid, "--reference-time")},
		{"invalid reference", replaceQueryFlag(valid, "--reference-time", "not-a-time")},
		{"invalid output", append(append([]string{}, valid...), "--output", "yaml")},
		{"positional question", append(append([]string{}, valid...), "private question")},
		{"question flag", append(append([]string{}, valid...), "--question", "private question")},
		{"extra args", append(append([]string{}, valid...), "extra", "arguments")},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			calls := 0
			err := (Runner{Execute: func(context.Context, Invocation) error {
				calls++
				return nil
			}}).Run(t.Context(), testCase.args)
			if err == nil {
				t.Fatal("Run() error = nil, want syntax rejection")
			}
			if calls != 0 {
				t.Fatalf("Execute calls = %d, want 0", calls)
			}
		})
	}
}

func TestRunnerParsesTrendQueryIntoTypedNormalizedInvocation(t *testing.T) {
	var got Invocation
	args := []string{
		"query", "trend",
		"--entity", "entity-b",
		"--entity", "entity-a",
		"--entity-match", "any",
		"--predicate", "stacks.example.v1/manages",
		"--predicate", "stacks.example.v1/reports-to",
		"--before", "2025-01-01T00:00:00-05:00/2025-02-01T00:00:00-05:00",
		"--after", "2025-03-01T00:00:00+02:00/2025-04-01T00:00:00+02:00",
		"--known-as-of", "2025-05-01T00:00:00-04:00",
		"--output", "json",
	}
	err := (Runner{Execute: func(_ context.Context, invocation Invocation) error {
		got = invocation
		return nil
	}}).Run(t.Context(), args)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got.Command != CommandQuery || got.Action != ActionTrend || len(got.Arguments) != 0 || got.Query == nil {
		t.Fatalf("invocation = %#v, want typed query trend", got)
	}
	wantRequest := query.Request{
		Intent:         temporal.IntentTrendComparison,
		EntityIDs:      []identity.EntityID{"entity-b", "entity-a"},
		EntityMatch:    query.EntityMatchAny,
		Predicates:     []observation.Predicate{"stacks.example.v1/manages", "stacks.example.v1/reports-to"},
		Selections:     got.Query.Request.Selections,
		KnowledgeScope: got.Query.Request.KnowledgeScope,
	}
	if !reflect.DeepEqual(got.Query.Request, wantRequest) || got.Query.Output != QueryOutputJSON {
		t.Fatalf("Query = %#v, want %#v with JSON output", got.Query, wantRequest)
	}
	assertQueryWindow(t, got.Query.Request.Selections[0], "before",
		"2025-01-01T05:00:00Z", "2025-02-01T05:00:00Z")
	assertQueryWindow(t, got.Query.Request.Selections[1], "after",
		"2025-02-28T22:00:00Z", "2025-03-31T22:00:00Z")
	cutoff, ok := got.Query.Request.KnowledgeScope.AsOf()
	if !ok || cutoff.Location() != time.UTC || cutoff.Format(time.RFC3339) != "2025-05-01T04:00:00Z" {
		t.Fatalf("knowledge cutoff = %v/%t, want normalized UTC", cutoff, ok)
	}
}

func TestRunnerDefaultsTrendQueryMatchOutputAndKnowledgeScope(t *testing.T) {
	var got Invocation
	err := (Runner{Execute: func(_ context.Context, invocation Invocation) error {
		got = invocation
		return nil
	}}).Run(t.Context(), []string{
		"query", "trend",
		"--entity", "entity-a",
		"--before", "2025-01-01T00:00:00Z/2025-02-01T00:00:00Z",
		"--after", "2025-03-01T00:00:00Z/2025-04-01T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got.Query == nil || got.Query.Request.EntityMatch != query.EntityMatchAll ||
		got.Query.Output != QueryOutputText ||
		got.Query.Request.KnowledgeScope.Kind() != temporal.KnowledgeCurrent ||
		got.Query.Request.Predicates == nil || len(got.Query.Request.Predicates) != 0 {
		t.Fatalf("default Query = %#v, want all/text/current with empty predicates", got.Query)
	}
}

func TestRunnerRejectsInvalidTrendSyntaxBeforeExecution(t *testing.T) {
	valid := []string{
		"query", "trend",
		"--entity", "entity-a",
		"--before", "2025-01-01T00:00:00Z/2025-02-01T00:00:00Z",
		"--after", "2025-03-01T00:00:00Z/2025-04-01T00:00:00Z",
	}
	tests := []struct {
		name         string
		args         []string
		privateInput string
	}{
		{name: "query group", args: []string{"query"}},
		{name: "unsupported action", args: []string{"query", "trajectory"}},
		{name: "positional input", args: append(append([]string{}, valid...), "synthetic-private-position"), privateInput: "synthetic-private-position"},
		{name: "unknown flag", args: append(append([]string{}, valid...), "--synthetic-private-flag", "value"), privateInput: "synthetic-private-flag"},
		{name: "missing entity", args: withoutQueryFlag(valid, "--entity")},
		{name: "blank entity", args: replaceQueryFlag(valid, "--entity", " \t ")},
		{name: "duplicate entity", args: append(append([]string{}, valid...), "--entity", "synthetic-private-entity", "--entity", "synthetic-private-entity"), privateInput: "synthetic-private-entity"},
		{name: "blank predicate", args: append(append([]string{}, valid...), "--predicate", " \t ")},
		{name: "duplicate predicate", args: append(append([]string{}, valid...), "--predicate", "synthetic-private-predicate", "--predicate", "synthetic-private-predicate"), privateInput: "synthetic-private-predicate"},
		{name: "invalid match", args: append(append([]string{}, valid...), "--entity-match", "synthetic-private-match"), privateInput: "synthetic-private-match"},
		{name: "invalid output", args: append(append([]string{}, valid...), "--output", "synthetic-private-output"), privateInput: "synthetic-private-output"},
		{name: "before without slash", args: replaceQueryFlag(valid, "--before", "2025-01-01T00:00:00Z")},
		{name: "before multiple slashes", args: replaceQueryFlag(valid, "--before", "2025-01-01T00:00:00Z/2025-02-01T00:00:00Z/synthetic-private-window"), privateInput: "synthetic-private-window"},
		{name: "blank window bound", args: replaceQueryFlag(valid, "--before", "/2025-02-01T00:00:00Z")},
		{name: "non RFC3339", args: replaceQueryFlag(valid, "--before", "2025-01-01/2025-02-01")},
		{name: "empty half open window", args: replaceQueryFlag(valid, "--before", "2025-01-01T00:00:00Z/2025-01-01T00:00:00Z")},
		{name: "reversed half open window", args: replaceQueryFlag(valid, "--before", "2025-02-01T00:00:00Z/2025-01-01T00:00:00Z")},
		{name: "unordered windows", args: replaceQueryFlag(valid, "--after", "2024-03-01T00:00:00Z/2024-04-01T00:00:00Z")},
		{name: "invalid cutoff", args: append(append([]string{}, valid...), "--known-as-of", "synthetic-private-cutoff"), privateInput: "synthetic-private-cutoff"},
		{name: "duplicate before flag", args: append(append([]string{}, valid...), "--before", "2026-01-01T00:00:00Z/2026-02-01T00:00:00Z")},
		{name: "duplicate match flag", args: append(append([]string{}, valid...), "--entity-match", "any", "--entity-match", "all")},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			var stderr strings.Builder
			calls := 0
			err := (Runner{
				Error: &stderr,
				Execute: func(context.Context, Invocation) error {
					calls++
					return nil
				},
			}).Run(t.Context(), testCase.args)
			if err == nil {
				t.Fatal("Run() error = nil, want syntax rejection")
			}
			if calls != 0 {
				t.Fatalf("Execute calls = %d, want 0", calls)
			}
			if testCase.privateInput != "" &&
				(strings.Contains(err.Error(), testCase.privateInput) || strings.Contains(stderr.String(), testCase.privateInput)) {
				t.Fatalf("syntax failure exposed private input: error=%q stderr=%q", err, stderr.String())
			}
		})
	}
}

func TestRunnerUsesFreshTrendFlagsForEachExecution(t *testing.T) {
	var got []Invocation
	runner := Runner{Execute: func(_ context.Context, invocation Invocation) error {
		got = append(got, invocation)
		return nil
	}}
	first := []string{
		"query", "trend", "--entity", "entity-a", "--entity-match", "any",
		"--predicate", "predicate-a",
		"--before", "2025-01-01T00:00:00Z/2025-02-01T00:00:00Z",
		"--after", "2025-03-01T00:00:00Z/2025-04-01T00:00:00Z",
		"--known-as-of", "2025-05-01T00:00:00Z", "--output", "json",
	}
	second := []string{
		"query", "trend", "--entity", "entity-b",
		"--before", "2026-01-01T00:00:00Z/2026-02-01T00:00:00Z",
		"--after", "2026-03-01T00:00:00Z/2026-04-01T00:00:00Z",
	}
	if err := runner.Run(t.Context(), first); err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	if err := runner.Run(t.Context(), second); err != nil {
		t.Fatalf("second Run() error = %v", err)
	}
	if len(got) != 2 || got[1].Query == nil ||
		!reflect.DeepEqual(got[1].Query.Request.EntityIDs, []identity.EntityID{"entity-b"}) ||
		len(got[1].Query.Request.Predicates) != 0 ||
		got[1].Query.Request.EntityMatch != query.EntityMatchAll ||
		got[1].Query.Request.KnowledgeScope.Kind() != temporal.KnowledgeCurrent ||
		got[1].Query.Output != QueryOutputText {
		t.Fatalf("second invocation = %#v, want isolated default query flags", got)
	}
}

func TestRunnerCarriesConfigAtExactTrendCommandPlacements(t *testing.T) {
	for _, args := range [][]string{
		{"--config", "/synthetic/stacks.yaml", "query", "trend", "--entity", "entity-a", "--before", "2025-01-01T00:00:00Z/2025-02-01T00:00:00Z", "--after", "2025-03-01T00:00:00Z/2025-04-01T00:00:00Z"},
		{"query", "--config", "/synthetic/stacks.yaml", "trend", "--entity", "entity-a", "--before", "2025-01-01T00:00:00Z/2025-02-01T00:00:00Z", "--after", "2025-03-01T00:00:00Z/2025-04-01T00:00:00Z"},
		{"query", "trend", "--config", "/synthetic/stacks.yaml", "--entity", "entity-a", "--before", "2025-01-01T00:00:00Z/2025-02-01T00:00:00Z", "--after", "2025-03-01T00:00:00Z/2025-04-01T00:00:00Z"},
	} {
		var got Invocation
		err := (Runner{Execute: func(_ context.Context, invocation Invocation) error {
			got = invocation
			return nil
		}}).Run(t.Context(), args)
		if err != nil {
			t.Fatalf("Run(%q) error = %v", args, err)
		}
		if got.ConfigFile == nil || *got.ConfigFile != "/synthetic/stacks.yaml" || got.Query == nil {
			t.Fatalf("Run(%q) invocation = %#v, want config on typed query", args, got)
		}
	}
}

func assertQueryWindow(t *testing.T, selection temporal.TemporalSelection, label, start, end string) {
	t.Helper()
	gotStart, gotEnd, ok := selection.Window()
	if !ok || selection.Label() != label ||
		gotStart.Location() != time.UTC || gotEnd.Location() != time.UTC ||
		gotStart.Format(time.RFC3339) != start || gotEnd.Format(time.RFC3339) != end {
		t.Fatalf("selection = %q %v/%v/%t, want %q %s/%s UTC", selection.Label(), gotStart, gotEnd, ok, label, start, end)
	}
}

func withoutQueryFlag(args []string, name string) []string {
	result := append([]string{}, args...)
	for index := 0; index < len(result)-1; index++ {
		if result[index] == name {
			return append(result[:index], result[index+2:]...)
		}
	}
	return result
}

func replaceQueryFlag(args []string, name, value string) []string {
	result := append([]string{}, args...)
	for index := 0; index < len(result)-1; index++ {
		if result[index] == name {
			result[index+1] = value
			return result
		}
	}
	return result
}

func TestRunnerRejectsInvalidConfigValidationTargetsWithoutPrivateInput(t *testing.T) {
	const privateInput = "synthetic-private-config-target"
	for _, args := range [][]string{
		{"config", "validate"},
		{"config", "validate", "serve", "unexpected"},
		{"config", "validate", "auth"},
		{"config", "validate", privateInput},
	} {
		var errorOutput strings.Builder
		calls := 0
		err := (Runner{
			Error: &errorOutput,
			Execute: func(context.Context, Invocation) error {
				calls++
				return nil
			},
		}).Run(t.Context(), args)
		if err == nil || calls != 0 {
			t.Fatalf("Run(%q) error/calls = %v/%d", args, err, calls)
		}
		if strings.Contains(err.Error(), privateInput) || strings.Contains(errorOutput.String(), privateInput) {
			t.Fatalf("Run(%q) exposed private input: error=%q stderr=%q", args, err, errorOutput.String())
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
