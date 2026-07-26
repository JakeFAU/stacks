package localdb

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JakeFAU/stacks/adapters/postgres/migration"
)

const (
	testAppURL         = "postgres://app:synthetic@127.0.0.1/stacks"
	testMigrationURL   = "postgres://admin:synthetic@localhost/stacks"
	testDockerEndpoint = "unix:///var/run/docker.sock"
)

type runnerCall struct {
	name string
	args []string
}

type fakeRunner struct {
	outputs map[string][]byte
	errors  map[string]error
	calls   []runnerCall
}

func (runner *fakeRunner) Run(
	_ context.Context,
	name string,
	args ...string,
) ([]byte, error) {
	runner.calls = append(runner.calls, runnerCall{
		name: name,
		args: append([]string(nil), args...),
	})
	key := name + " " + strings.Join(args, " ")
	output, ok := runner.outputs[key]
	if !ok {
		return nil, fmt.Errorf("unexpected synthetic command")
	}
	return append([]byte(nil), output...), runner.errors[key]
}

type fakeMigrator struct {
	calls int
	err   error
}

func (migrator *fakeMigrator) Apply(context.Context) (migration.ApplyResult, error) {
	migrator.calls++
	return migration.ApplyResult{}, migrator.err
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, fmt.Errorf("synthetic secret writer path")
}

func TestResetRejectsWrongConfirmationBeforeInspectionOrMutation(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{}
	err := validResetter(runner, &fakeMigrator{}).Reset(
		t.Context(),
		"delete-something-else",
		&strings.Builder{},
	)
	if err == nil {
		t.Fatal("Resetter.Reset() error = nil, want exact confirmation rejection")
	}
	if len(runner.calls) != 0 {
		t.Fatalf("runner calls = %#v, want none before confirmation", runner.calls)
	}
}

func TestResetRejectsAmbientDockerAndComposeRedirectorsBeforeInspection(t *testing.T) {
	for _, variable := range []string{
		"DOCKER_HOST",
		"DOCKER_CONTEXT",
		"COMPOSE_FILE",
		"COMPOSE_PROJECT_NAME",
	} {
		variable := variable
		t.Run(variable, func(t *testing.T) {
			t.Setenv(variable, "synthetic-redirect")
			runner := &fakeRunner{}
			err := validResetter(runner, &fakeMigrator{}).Reset(
				t.Context(),
				ConfirmationToken,
				&strings.Builder{},
			)
			if err == nil {
				t.Fatal("Resetter.Reset() error = nil, want ambient redirector rejection")
			}
			if len(runner.calls) != 0 {
				t.Fatalf("runner calls = %#v, want none before ambient validation", runner.calls)
			}
		})
	}
}

func TestResetPassesValidatedLocalContextAndPinnedComposeTargetToEveryDockerCommand(t *testing.T) {
	decoyDirectory := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(decoyDirectory, composeFileName),
		[]byte("name: decoy\n"),
		0o600,
	); err != nil {
		t.Fatalf("write decoy Compose file: %v", err)
	}
	t.Chdir(decoyDirectory)

	runner := successfulResetRunner(validLiveContainerInspection())
	if err := validResetter(runner, &fakeMigrator{}).Reset(
		t.Context(),
		ConfirmationToken,
		&strings.Builder{},
	); err != nil {
		t.Fatalf("Resetter.Reset() error = %v; calls = %#v", err, runner.calls)
	}
	projectDirectory, composePath, err := repositoryComposeTarget()
	if err != nil {
		t.Fatalf("resolve repository Compose target: %v", err)
	}
	validated := false
	for _, call := range runner.calls {
		if call.name != "docker" || len(call.args) < 2 {
			continue
		}
		if call.args[0] == "context" && call.args[1] == "show" {
			continue
		}
		if containsArg(call.args, "context") && containsArg(call.args, "inspect") {
			validated = true
			continue
		}
		if !validated {
			t.Fatalf("Docker call occurred before endpoint validation: %#v", call)
		}
		if !containsArgPair(call.args, "--host", testDockerEndpoint) ||
			containsArg(call.args, "--context") {
			t.Fatalf("Docker call did not pin validated endpoint: %#v", call)
		}
		if containsArg(call.args, "compose") &&
			(!containsArgPair(call.args, "--project-directory", projectDirectory) ||
				!containsArgPair(call.args, "--file", composePath) ||
				!containsArgPair(call.args, "--project-name", "stacks")) {
			t.Fatalf("Compose call did not pin repository target: %#v", call)
		}
	}
}

func TestResetRejectsRemoteDockerContextBeforeMutation(t *testing.T) {
	runner := inspectionRunner(
		validComposeConfig(),
		"container-one\n",
		validContainerLabels(),
		validVolumeInspection(),
	)
	runner.outputs["docker context show"] = []byte("remote\n")
	runner.outputs["docker --context remote context inspect remote --format {{json .Endpoints.docker.Host}}"] = []byte(`"ssh://remote.example"`)
	err := validResetter(runner, &fakeMigrator{}).Reset(
		t.Context(),
		ConfirmationToken,
		&strings.Builder{},
	)
	if err == nil {
		t.Fatal("Resetter.Reset() error = nil, want remote Docker context rejection")
	}
	assertNoResetMutation(t, runner.calls)
}

func TestResetRejectsLivePostgresMountMismatchBeforeMutation(t *testing.T) {
	tests := []struct {
		name       string
		inspection string
	}{
		{name: "absent", inspection: liveContainerInspection(nil)},
		{name: "bind", inspection: liveContainerInspection([]liveMountFixture{{Type: "bind", Source: "/synthetic", Destination: "/var/lib/postgresql"}})},
		{name: "wrong volume", inspection: liveContainerInspection([]liveMountFixture{{Type: "volume", Name: "other_postgres_data", Destination: "/var/lib/postgresql"}})},
		{name: "ambiguous", inspection: liveContainerInspection([]liveMountFixture{
			{Type: "volume", Name: "stacks_postgres_data", Destination: "/var/lib/postgresql"},
			{Type: "volume", Name: "other_postgres_data", Destination: "/var/lib/postgresql"},
		})},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			runner := successfulResetRunner(test.inspection)
			err := validResetter(runner, &fakeMigrator{}).Reset(
				t.Context(),
				ConfirmationToken,
				&strings.Builder{},
			)
			if err == nil {
				t.Fatal("Resetter.Reset() error = nil, want live mount rejection")
			}
			assertNoResetMutation(t, runner.calls)
		})
	}
}

func TestResetRejectsEveryNonLoopbackDatabaseURLBeforeInspectionOrMutation(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		urls []string
	}{
		{name: "application URL", urls: []string{"postgres://app:secret@database.example/stacks", testMigrationURL}},
		{name: "migration URL", urls: []string{testAppURL, "postgres://admin:secret@192.0.2.10/stacks"}},
		{name: "missing host", urls: []string{"postgres:///stacks", testMigrationURL}},
		{name: "query host override", urls: []string{"postgres://app:secret@127.0.0.1/stacks?host=database.example", testMigrationURL}},
		{name: "remote fallback host", urls: []string{"host=127.0.0.1,database.example user=app password=secret dbname=stacks", testMigrationURL}},
		{name: "service redirector", urls: []string{"postgres://app:secret@127.0.0.1/stacks?service=remote", testMigrationURL}},
		{name: "servicefile redirector", urls: []string{"postgres://app:secret@127.0.0.1/stacks?servicefile=/synthetic/config", testMigrationURL}},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			runner := &fakeRunner{}
			resetter := validResetter(runner, &fakeMigrator{})
			resetter.DatabaseURLs = test.urls
			err := resetter.Reset(t.Context(), ConfirmationToken, &strings.Builder{})
			if err == nil {
				t.Fatal("Resetter.Reset() error = nil, want non-loopback rejection")
			}
			if len(runner.calls) != 0 {
				t.Fatalf("runner calls = %#v, want none before URL validation", runner.calls)
			}
			for _, databaseURL := range test.urls {
				if strings.Contains(err.Error(), databaseURL) || strings.Contains(err.Error(), "secret") {
					t.Fatalf("Resetter.Reset() error exposed database URL: %q", err)
				}
			}
		})
	}
}

func TestResetRejectsUnresolvedOrAmbiguousPostgresServiceBeforeMutation(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name       string
		psOutput   string
		configJSON string
	}{
		{name: "absent", configJSON: validComposeConfig(), psOutput: ""},
		{name: "ambiguous", configJSON: validComposeConfig(), psOutput: "container-one\ncontainer-two\n"},
		{name: "service missing from config", configJSON: composeConfig("worker", "postgres_data", "/var/lib/postgresql"), psOutput: "container-one\n"},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			runner := inspectionRunner(test.configJSON, test.psOutput, validContainerLabels(), validVolumeInspection())
			err := validResetter(runner, &fakeMigrator{}).Reset(
				t.Context(),
				ConfirmationToken,
				&strings.Builder{},
			)
			if err == nil {
				t.Fatal("Resetter.Reset() error = nil, want service rejection")
			}
			assertNoResetMutation(t, runner.calls)
		})
	}
}

func TestResetRejectsUnresolvedAmbiguousOrWrongPostgresVolumeBeforeMutation(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name       string
		configJSON string
	}{
		{name: "missing key", configJSON: composeConfig("postgres", "other_data", "/var/lib/postgresql")},
		{name: "wrong mount", configJSON: composeConfig("postgres", "postgres_data", "/var/lib/postgresql/data")},
		{name: "ambiguous mount", configJSON: ambiguousVolumeComposeConfig()},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			runner := inspectionRunner(test.configJSON, "container-one\n", validContainerLabels(), validVolumeInspection())
			err := validResetter(runner, &fakeMigrator{}).Reset(
				t.Context(),
				ConfirmationToken,
				&strings.Builder{},
			)
			if err == nil {
				t.Fatal("Resetter.Reset() error = nil, want volume rejection")
			}
			assertNoResetMutation(t, runner.calls)
		})
	}
}

func TestResetRejectsComposeProjectAndVolumeLabelMismatchBeforeMutation(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name            string
		containerLabels string
		volumeInspect   string
	}{
		{name: "container project", containerLabels: `{"com.docker.compose.project":"other","com.docker.compose.service":"postgres"}`, volumeInspect: validVolumeInspection()},
		{name: "container service", containerLabels: `{"com.docker.compose.project":"stacks","com.docker.compose.service":"worker"}`, volumeInspect: validVolumeInspection()},
		{name: "volume project", containerLabels: validContainerLabels(), volumeInspect: `{"Name":"stacks_postgres_data","Labels":{"com.docker.compose.project":"other","com.docker.compose.volume":"postgres_data"}}`},
		{name: "volume key", containerLabels: validContainerLabels(), volumeInspect: `{"Name":"stacks_postgres_data","Labels":{"com.docker.compose.project":"stacks","com.docker.compose.volume":"other_data"}}`},
		{name: "volume name", containerLabels: validContainerLabels(), volumeInspect: `{"Name":"other_postgres_data","Labels":{"com.docker.compose.project":"stacks","com.docker.compose.volume":"postgres_data"}}`},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			runner := inspectionRunner(
				validComposeConfig(),
				"container-one\n",
				test.containerLabels,
				test.volumeInspect,
			)
			err := validResetter(runner, &fakeMigrator{}).Reset(
				t.Context(),
				ConfirmationToken,
				&strings.Builder{},
			)
			if err == nil {
				t.Fatal("Resetter.Reset() error = nil, want label rejection")
			}
			assertNoResetMutation(t, runner.calls)
		})
	}
}

func TestResetRejectsMissingOrMismatchedComposeProvenanceBeforeMutation(t *testing.T) {
	projectDirectory, composePath, err := repositoryComposeTarget()
	if err != nil {
		t.Fatalf("resolve repository Compose target: %v", err)
	}
	tests := []struct {
		name   string
		labels string
	}{
		{
			name: "missing working directory",
			labels: composeContainerLabels(
				"",
				composePath,
			),
		},
		{
			name: "missing config files",
			labels: composeContainerLabels(
				projectDirectory,
				"",
			),
		},
		{
			name: "relative working directory",
			labels: composeContainerLabels(
				".",
				composePath,
			),
		},
		{
			name: "relative config file",
			labels: composeContainerLabels(
				projectDirectory,
				composeFileName,
			),
		},
		{
			name: "multiple config files",
			labels: composeContainerLabels(
				projectDirectory,
				composePath+","+filepath.Join(projectDirectory, "other.yaml"),
			),
		},
		{
			name: "mismatched working directory",
			labels: composeContainerLabels(
				filepath.Dir(projectDirectory),
				composePath,
			),
		},
		{
			name: "mismatched config file",
			labels: composeContainerLabels(
				projectDirectory,
				filepath.Join(projectDirectory, "other.yaml"),
			),
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			runner := inspectionRunner(
				validComposeConfig(),
				"container-one\n",
				test.labels,
				validVolumeInspection(),
			)
			err := validResetter(runner, &fakeMigrator{}).Reset(
				t.Context(),
				ConfirmationToken,
				&strings.Builder{},
			)
			if err == nil {
				t.Fatal("Resetter.Reset() error = nil, want Compose provenance rejection")
			}
			assertNoResetMutation(t, runner.calls)
			for _, privatePath := range []string{
				projectDirectory,
				composePath,
				filepath.Dir(projectDirectory),
			} {
				if strings.Contains(err.Error(), privatePath) {
					t.Fatalf("provenance error exposed path: %q", err)
				}
			}
		})
	}
}

func TestResetMutatesOnlyVerifiedPostgresServiceAndVolumeThenMigrates(t *testing.T) {
	t.Parallel()

	runner := inspectionRunner(
		validComposeConfig(),
		"container-one\n",
		validContainerLabels(),
		validVolumeInspection(),
	)
	for _, key := range []string{
		composeCommand("stop", postgresService),
		composeCommand("rm", "-f", postgresService),
		dockerHostCommand("volume", "rm", "stacks_postgres_data"),
		composeCommand("up", "-d", "--wait", postgresService),
	} {
		runner.outputs[key] = nil
	}
	migrator := &fakeMigrator{}
	var output strings.Builder
	if err := validResetter(runner, migrator).Reset(
		t.Context(),
		ConfirmationToken,
		&output,
	); err != nil {
		t.Fatalf("Resetter.Reset() error = %v", err)
	}
	if migrator.calls != 1 {
		t.Fatalf("migrator calls = %d, want 1", migrator.calls)
	}
	wantOutput := "service=postgres volume=stacks_postgres_data warning=local PostgreSQL data is unrecoverable\n"
	if got := output.String(); got != wantOutput {
		t.Fatalf("output = %q, want %q", got, wantOutput)
	}
	for _, secret := range []string{testAppURL, testMigrationURL, "synthetic"} {
		if strings.Contains(output.String(), secret) {
			t.Fatalf("output exposed database configuration: %q", output.String())
		}
	}
	wantMutations := []string{
		composeCommand("stop", postgresService),
		composeCommand("rm", "-f", postgresService),
		dockerHostCommand("volume", "rm", "stacks_postgres_data"),
		composeCommand("up", "-d", "--wait", postgresService),
	}
	var gotMutations []string
	for _, call := range runner.calls {
		joined := call.name + " " + strings.Join(call.args, " ")
		if isResetMutation(call) {
			gotMutations = append(gotMutations, joined)
		}
		if strings.Contains(joined, "down") || strings.Contains(joined, "--volumes") {
			t.Fatalf("forbidden broad Compose command: %q", joined)
		}
	}
	if strings.Join(gotMutations, "\n") != strings.Join(wantMutations, "\n") {
		t.Fatalf("mutations = %#v, want %#v", gotMutations, wantMutations)
	}
}

func TestResetAcceptsComposeVolumeKeyAsNormalizedMountSource(t *testing.T) {
	t.Parallel()

	configJSON := strings.Replace(
		validComposeConfig(),
		`"source":"stacks_postgres_data"`,
		`"source":"postgres_data"`,
		1,
	)
	runner := inspectionRunner(
		configJSON,
		"container-one\n",
		validContainerLabels(),
		validVolumeInspection(),
	)
	for _, key := range []string{
		composeCommand("stop", postgresService),
		composeCommand("rm", "-f", postgresService),
		dockerHostCommand("volume", "rm", "stacks_postgres_data"),
		composeCommand("up", "-d", "--wait", postgresService),
	} {
		runner.outputs[key] = nil
	}
	if err := validResetter(runner, &fakeMigrator{}).Reset(
		t.Context(),
		ConfirmationToken,
		&strings.Builder{},
	); err != nil {
		t.Fatalf("Resetter.Reset() error = %v", err)
	}
}

func TestResetReturnsBoundedOperationSpecificErrors(t *testing.T) {
	tests := []struct {
		name       string
		failureKey string
		migrate    bool
		wantError  string
	}{
		{
			name:       "stop",
			failureKey: composeCommand("stop", postgresService),
			wantError:  "reset local PostgreSQL: stop operation failed",
		},
		{
			name:       "container removal",
			failureKey: composeCommand("rm", "-f", postgresService),
			wantError:  "reset local PostgreSQL: container removal operation failed",
		},
		{
			name:       "volume removal",
			failureKey: dockerHostCommand("volume", "rm", "stacks_postgres_data"),
			wantError:  "reset local PostgreSQL: volume removal operation failed",
		},
		{
			name:       "recreate and wait",
			failureKey: composeCommand("up", "-d", "--wait", postgresService),
			wantError:  "reset local PostgreSQL: recreate and wait operation failed",
		},
		{
			name:      "migrate",
			migrate:   true,
			wantError: "reset local PostgreSQL: migrate operation failed",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			runner := successfulResetRunner(validLiveContainerInspection())
			migrator := &fakeMigrator{}
			sensitiveFailure := fmt.Errorf("synthetic secret output /private/path")
			if test.migrate {
				migrator.err = sensitiveFailure
			} else {
				runner.errors = map[string]error{test.failureKey: sensitiveFailure}
			}
			err := validResetter(runner, migrator).Reset(
				t.Context(),
				ConfirmationToken,
				&strings.Builder{},
			)
			if err == nil {
				t.Fatal("Resetter.Reset() error = nil, want operation failure")
			}
			if got := err.Error(); got != test.wantError {
				t.Fatalf("Resetter.Reset() error = %q, want %q", got, test.wantError)
			}
			for _, forbidden := range []string{"synthetic", "secret", "/private/path"} {
				if strings.Contains(err.Error(), forbidden) {
					t.Fatalf("operation error exposed sensitive detail: %q", err)
				}
			}
		})
	}
}

func TestResetReturnsBoundedWarningWriteErrorBeforeMutation(t *testing.T) {
	runner := successfulResetRunner(validLiveContainerInspection())
	migrator := &fakeMigrator{}
	err := validResetter(runner, migrator).Reset(
		t.Context(),
		ConfirmationToken,
		failingWriter{},
	)
	if err == nil {
		t.Fatal("Resetter.Reset() error = nil, want warning write failure")
	}
	const expected = "reset local PostgreSQL: write warning operation failed"
	if err.Error() != expected {
		t.Fatalf("Resetter.Reset() error = %q, want %q", err, expected)
	}
	assertNoResetMutation(t, runner.calls)
	if migrator.calls != 0 {
		t.Fatalf("migrator calls = %d, want none after warning failure", migrator.calls)
	}
}

func TestResetPreservesBoundedCancellationAtEveryInspectionStage(t *testing.T) {
	stages := []struct {
		name       string
		failureKey string
		wantError  string
		wantCalls  int
	}{
		{
			name:       "Docker context selection",
			failureKey: "docker context show",
			wantError:  "reset local PostgreSQL: inspect Docker context operation failed",
			wantCalls:  1,
		},
		{
			name:       "Docker context endpoint inspection",
			failureKey: "docker --context default context inspect default --format {{json .Endpoints.docker.Host}}",
			wantError:  "reset local PostgreSQL: inspect Docker context endpoint operation failed",
			wantCalls:  2,
		},
		{
			name:       "Compose configuration inspection",
			failureKey: composeCommand("config", "--format", "json"),
			wantError:  "reset local PostgreSQL: inspect Compose configuration operation failed",
			wantCalls:  3,
		},
		{
			name:       "PostgreSQL service inspection",
			failureKey: composeCommand("ps", "-q", postgresService),
			wantError:  "reset local PostgreSQL: inspect PostgreSQL service operation failed",
			wantCalls:  4,
		},
		{
			name:       "live PostgreSQL container inspection",
			failureKey: dockerHostCommand("inspect", "--format", "{{json .}}", "container-one"),
			wantError:  "reset local PostgreSQL: inspect live PostgreSQL service operation failed",
			wantCalls:  5,
		},
		{
			name:       "PostgreSQL volume inspection",
			failureKey: dockerHostCommand("volume", "inspect", "--format", "{{json .}}", "stacks_postgres_data"),
			wantError:  "reset local PostgreSQL: inspect PostgreSQL volume operation failed",
			wantCalls:  6,
		},
	}
	causes := []struct {
		name  string
		cause error
	}{
		{name: "canceled", cause: context.Canceled},
		{name: "deadline", cause: context.DeadlineExceeded},
	}
	for _, stage := range stages {
		stage := stage
		t.Run(stage.name, func(t *testing.T) {
			for _, testCause := range causes {
				testCause := testCause
				t.Run(testCause.name, func(t *testing.T) {
					runner := successfulResetRunner(validLiveContainerInspection())
					privateFailure := fmt.Errorf(
						"synthetic secret /private/path: %w",
						testCause.cause,
					)
					runner.errors = map[string]error{stage.failureKey: privateFailure}
					migrator := &fakeMigrator{}
					var output strings.Builder
					err := validResetter(runner, migrator).Reset(
						t.Context(),
						ConfirmationToken,
						&output,
					)
					if err == nil {
						t.Fatal("Resetter.Reset() error = nil, want inspection failure")
					}
					if err.Error() != stage.wantError {
						t.Fatalf(
							"Resetter.Reset() error = %q, want %q",
							err,
							stage.wantError,
						)
					}
					if !errors.Is(err, testCause.cause) {
						t.Fatalf("errors.Is(%v) = false for %v", err, testCause.cause)
					}
					if errors.Is(err, privateFailure) {
						t.Fatalf("operation error unwrap exposed private runner error: %v", err)
					}
					if len(runner.calls) != stage.wantCalls {
						t.Fatalf(
							"runner calls = %d, want stop after %d: %#v",
							len(runner.calls),
							stage.wantCalls,
							runner.calls,
						)
					}
					if output.Len() != 0 {
						t.Fatalf("warning output = %q, want none before inspection completes", output.String())
					}
					assertNoResetMutation(t, runner.calls)
					if migrator.calls != 0 {
						t.Fatalf(
							"migrator calls = %d, want none after inspection failure",
							migrator.calls,
						)
					}
					for _, forbidden := range []string{"synthetic", "secret", "/private/path"} {
						if strings.Contains(err.Error(), forbidden) {
							t.Fatalf("inspection error exposed private detail: %q", err)
						}
					}
				})
			}
		})
	}
}

func TestResetPreservesCancellationIdentityAndStopsAtFailedStage(t *testing.T) {
	expectedMutations := []string{
		composeCommand("stop", postgresService),
		composeCommand("rm", "-f", postgresService),
		dockerHostCommand("volume", "rm", "stacks_postgres_data"),
		composeCommand("up", "-d", "--wait", postgresService),
	}
	tests := []struct {
		name              string
		failureKey        string
		migrate           bool
		cause             error
		wantMutationCount int
		wantMigratorCalls int
	}{
		{
			name:              "stop canceled",
			failureKey:        expectedMutations[0],
			cause:             context.Canceled,
			wantMutationCount: 1,
		},
		{
			name:              "container removal deadline",
			failureKey:        expectedMutations[1],
			cause:             context.DeadlineExceeded,
			wantMutationCount: 2,
		},
		{
			name:              "volume removal canceled",
			failureKey:        expectedMutations[2],
			cause:             context.Canceled,
			wantMutationCount: 3,
		},
		{
			name:              "recreate deadline",
			failureKey:        expectedMutations[3],
			cause:             context.DeadlineExceeded,
			wantMutationCount: 4,
		},
		{
			name:              "migration canceled",
			migrate:           true,
			cause:             context.Canceled,
			wantMutationCount: 4,
			wantMigratorCalls: 1,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			runner := successfulResetRunner(validLiveContainerInspection())
			migrator := &fakeMigrator{}
			if test.migrate {
				migrator.err = test.cause
			} else {
				runner.errors = map[string]error{test.failureKey: test.cause}
			}
			err := validResetter(runner, migrator).Reset(
				t.Context(),
				ConfirmationToken,
				&strings.Builder{},
			)
			if !errors.Is(err, test.cause) {
				t.Fatalf("errors.Is(%v) = false for %v", err, test.cause)
			}
			if migrator.calls != test.wantMigratorCalls {
				t.Fatalf(
					"migrator calls = %d, want %d",
					migrator.calls,
					test.wantMigratorCalls,
				)
			}
			var gotMutations []string
			for _, call := range runner.calls {
				if isResetMutation(call) {
					gotMutations = append(
						gotMutations,
						call.name+" "+strings.Join(call.args, " "),
					)
				}
			}
			wantMutations := expectedMutations[:test.wantMutationCount]
			if strings.Join(gotMutations, "\n") != strings.Join(wantMutations, "\n") {
				t.Fatalf("mutations = %#v, want %#v", gotMutations, wantMutations)
			}
			for _, mutation := range gotMutations {
				if strings.Contains(mutation, "down") ||
					strings.Contains(mutation, "--volumes") {
					t.Fatalf("forbidden broad Compose command: %q", mutation)
				}
			}
		})
	}
}

func validResetter(runner CommandRunner, migrator MigrationApplier) Resetter {
	return Resetter{
		DatabaseURLs: []string{testAppURL, testMigrationURL},
		Runner:       runner,
		Migrator:     migrator,
	}
}

func inspectionRunner(
	configJSON string,
	psOutput string,
	containerLabels string,
	volumeInspection string,
) *fakeRunner {
	liveInspection := fmt.Sprintf(
		`{"Config":{"Labels":%s},"Mounts":[{"Type":"volume","Name":"stacks_postgres_data","Destination":"/var/lib/postgresql"}]}`,
		containerLabels,
	)
	return &fakeRunner{outputs: map[string][]byte{
		"docker context show": []byte("default\n"),
		"docker --context default context inspect default --format {{json .Endpoints.docker.Host}}": []byte(`"` + testDockerEndpoint + `"`),
		composeCommand("config", "--format", "json"):                                                []byte(configJSON),
		composeCommand("ps", "-q", postgresService):                                                 []byte(psOutput),
		dockerHostCommand("inspect", "--format", "{{json .}}", "container-one"):                     []byte(liveInspection),
		dockerHostCommand("volume", "inspect", "--format", "{{json .}}", "stacks_postgres_data"):    []byte(volumeInspection),
	}}
}

func successfulResetRunner(containerInspection string) *fakeRunner {
	runner := inspectionRunner(
		validComposeConfig(),
		"container-one\n",
		validContainerLabels(),
		validVolumeInspection(),
	)
	runner.outputs[dockerHostCommand("inspect", "--format", "{{json .}}", "container-one")] = []byte(containerInspection)
	for _, key := range []string{
		composeCommand("stop", postgresService),
		composeCommand("rm", "-f", postgresService),
		dockerHostCommand("volume", "rm", "stacks_postgres_data"),
		composeCommand("up", "-d", "--wait", postgresService),
	} {
		runner.outputs[key] = nil
	}
	return runner
}

type liveMountFixture struct {
	Type        string
	Name        string
	Source      string
	Destination string
}

func validLiveContainerInspection() string {
	return liveContainerInspection([]liveMountFixture{{
		Type:        "volume",
		Name:        "stacks_postgres_data",
		Destination: "/var/lib/postgresql",
	}})
}

func liveContainerInspection(mounts []liveMountFixture) string {
	parts := make([]string, 0, len(mounts))
	for _, mount := range mounts {
		parts = append(parts, fmt.Sprintf(
			`{"Type":%q,"Name":%q,"Source":%q,"Destination":%q}`,
			mount.Type,
			mount.Name,
			mount.Source,
			mount.Destination,
		))
	}
	return fmt.Sprintf(
		`{"Config":{"Labels":%s},"Mounts":[%s]}`,
		validContainerLabels(),
		strings.Join(parts, ","),
	)
}

func containsArg(arguments []string, wanted string) bool {
	for _, argument := range arguments {
		if argument == wanted {
			return true
		}
	}
	return false
}

func containsArgPair(arguments []string, key, value string) bool {
	for index := 0; index+1 < len(arguments); index++ {
		if arguments[index] == key && arguments[index+1] == value {
			return true
		}
	}
	return false
}

func validComposeConfig() string {
	return composeConfig("postgres", "postgres_data", "/var/lib/postgresql")
}

func composeConfig(service, volumeKey, mount string) string {
	return fmt.Sprintf(
		`{"name":"stacks","services":{%q:{"volumes":[{"type":"volume","source":"stacks_postgres_data","target":%q}]}},"volumes":{%q:{"name":"stacks_postgres_data"}}}`,
		service,
		mount,
		volumeKey,
	)
}

func ambiguousVolumeComposeConfig() string {
	return `{"name":"stacks","services":{"postgres":{"volumes":[` +
		`{"type":"volume","source":"stacks_postgres_data","target":"/var/lib/postgresql"},` +
		`{"type":"volume","source":"stacks_other_data","target":"/var/lib/postgresql"}` +
		`]}},"volumes":{"postgres_data":{"name":"stacks_postgres_data"},"other_data":{"name":"stacks_other_data"}}}`
}

func validContainerLabels() string {
	projectDirectory, composePath, err := repositoryComposeTarget()
	if err != nil {
		panic(err)
	}
	return composeContainerLabels(projectDirectory, composePath)
}

func composeContainerLabels(projectDirectory, composePath string) string {
	return fmt.Sprintf(
		`{"com.docker.compose.project":"stacks",`+
			`"com.docker.compose.service":"postgres",`+
			`"com.docker.compose.project.working_dir":%q,`+
			`"com.docker.compose.project.config_files":%q}`,
		projectDirectory,
		composePath,
	)
}

func composeCommand(arguments ...string) string {
	projectDirectory, composePath, err := repositoryComposeTarget()
	if err != nil {
		panic(err)
	}
	command := []string{
		"docker",
		"--host",
		testDockerEndpoint,
		"compose",
		"--project-directory",
		projectDirectory,
		"--file",
		composePath,
		"--project-name",
		composeProject,
	}
	return strings.Join(append(command, arguments...), " ")
}

func dockerHostCommand(arguments ...string) string {
	return strings.Join(
		append([]string{"docker", "--host", testDockerEndpoint}, arguments...),
		" ",
	)
}

func validVolumeInspection() string {
	return `{"Name":"stacks_postgres_data","Labels":{"com.docker.compose.project":"stacks","com.docker.compose.volume":"postgres_data"}}`
}

func assertNoResetMutation(t *testing.T, calls []runnerCall) {
	t.Helper()
	for _, call := range calls {
		if isResetMutation(call) {
			t.Fatalf("mutation occurred before complete validation: %#v", call)
		}
	}
}

func isResetMutation(call runnerCall) bool {
	if call.name != "docker" {
		return false
	}
	for index, argument := range call.args {
		if argument == "compose" {
			for _, candidate := range call.args[index+1:] {
				switch candidate {
				case "stop", "rm", "up", "down":
					return true
				}
			}
		}
		if argument == "volume" && index+1 < len(call.args) && call.args[index+1] == "rm" {
			return true
		}
	}
	return false
}
