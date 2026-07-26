package localdb

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/JakeFAU/stacks/adapters/postgres/migration"
)

const (
	testAppURL       = "postgres://app:synthetic@127.0.0.1/stacks"
	testMigrationURL = "postgres://admin:synthetic@localhost/stacks"
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
	runner := successfulResetRunner(validLiveContainerInspection())
	if err := validResetter(runner, &fakeMigrator{}).Reset(
		t.Context(),
		ConfirmationToken,
		&strings.Builder{},
	); err != nil {
		t.Fatalf("Resetter.Reset() error = %v", err)
	}
	for _, call := range runner.calls {
		if call.name != "docker" || len(call.args) < 2 {
			continue
		}
		if call.args[0] == "context" && call.args[1] == "show" {
			continue
		}
		if !containsArgPair(call.args, "--context", "default") {
			t.Fatalf("Docker call did not pin validated context: %#v", call)
		}
		if containsArg(call.args, "compose") &&
			(!containsArgPair(call.args, "--file", "compose.yaml") ||
				!containsArgPair(call.args, "--project-name", "stacks")) {
			t.Fatalf("Compose call did not pin file and project: %#v", call)
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

func TestResetMutatesOnlyVerifiedPostgresServiceAndVolumeThenMigrates(t *testing.T) {
	t.Parallel()

	runner := inspectionRunner(
		validComposeConfig(),
		"container-one\n",
		validContainerLabels(),
		validVolumeInspection(),
	)
	for _, key := range []string{
		"docker --context default compose --file compose.yaml --project-name stacks stop postgres",
		"docker --context default compose --file compose.yaml --project-name stacks rm -f postgres",
		"docker --context default volume rm stacks_postgres_data",
		"docker --context default compose --file compose.yaml --project-name stacks up -d --wait postgres",
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
		"docker --context default compose --file compose.yaml --project-name stacks stop postgres",
		"docker --context default compose --file compose.yaml --project-name stacks rm -f postgres",
		"docker --context default volume rm stacks_postgres_data",
		"docker --context default compose --file compose.yaml --project-name stacks up -d --wait postgres",
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
		"docker --context default compose --file compose.yaml --project-name stacks stop postgres",
		"docker --context default compose --file compose.yaml --project-name stacks rm -f postgres",
		"docker --context default volume rm stacks_postgres_data",
		"docker --context default compose --file compose.yaml --project-name stacks up -d --wait postgres",
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
			failureKey: "docker --context default compose --file compose.yaml --project-name stacks stop postgres",
			wantError:  "reset local PostgreSQL: stop operation failed",
		},
		{
			name:       "container removal",
			failureKey: "docker --context default compose --file compose.yaml --project-name stacks rm -f postgres",
			wantError:  "reset local PostgreSQL: container removal operation failed",
		},
		{
			name:       "volume removal",
			failureKey: "docker --context default volume rm stacks_postgres_data",
			wantError:  "reset local PostgreSQL: volume removal operation failed",
		},
		{
			name:       "recreate and wait",
			failureKey: "docker --context default compose --file compose.yaml --project-name stacks up -d --wait postgres",
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
		"docker --context default context inspect default --format {{json .Endpoints.docker.Host}}":       []byte(`"unix:///var/run/docker.sock"`),
		"docker --context default compose --file compose.yaml --project-name stacks config --format json": []byte(configJSON),
		"docker --context default compose --file compose.yaml --project-name stacks ps -q postgres":       []byte(psOutput),
		"docker --context default inspect --format {{json .}} container-one":                              []byte(liveInspection),
		"docker --context default volume inspect --format {{json .}} stacks_postgres_data":                []byte(volumeInspection),
	}}
}

func successfulResetRunner(containerInspection string) *fakeRunner {
	runner := inspectionRunner(
		validComposeConfig(),
		"container-one\n",
		validContainerLabels(),
		validVolumeInspection(),
	)
	runner.outputs["docker --context default inspect --format {{json .}} container-one"] = []byte(containerInspection)
	for _, key := range []string{
		"docker --context default compose --file compose.yaml --project-name stacks stop postgres",
		"docker --context default compose --file compose.yaml --project-name stacks rm -f postgres",
		"docker --context default volume rm stacks_postgres_data",
		"docker --context default compose --file compose.yaml --project-name stacks up -d --wait postgres",
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
	return `{"com.docker.compose.project":"stacks","com.docker.compose.service":"postgres"}`
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
