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
	return append([]byte(nil), output...), nil
}

type fakeMigrator struct {
	calls int
}

func (migrator *fakeMigrator) Apply(context.Context) (migration.ApplyResult, error) {
	migrator.calls++
	return migration.ApplyResult{}, nil
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

func TestResetRejectsEveryNonLoopbackDatabaseURLBeforeInspectionOrMutation(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		urls []string
	}{
		{name: "application URL", urls: []string{"postgres://app:secret@database.example/stacks", testMigrationURL}},
		{name: "migration URL", urls: []string{testAppURL, "postgres://admin:secret@192.0.2.10/stacks"}},
		{name: "missing host", urls: []string{"postgres:///stacks", testMigrationURL}},
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
		"docker compose stop postgres",
		"docker compose rm -f postgres",
		"docker volume rm stacks_postgres_data",
		"docker compose up -d --wait postgres",
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
		"docker compose stop postgres",
		"docker compose rm -f postgres",
		"docker volume rm stacks_postgres_data",
		"docker compose up -d --wait postgres",
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
		"docker compose stop postgres",
		"docker compose rm -f postgres",
		"docker volume rm stacks_postgres_data",
		"docker compose up -d --wait postgres",
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
	return &fakeRunner{outputs: map[string][]byte{
		"docker compose config --format json":                            []byte(configJSON),
		"docker compose ps -q postgres":                                  []byte(psOutput),
		"docker inspect --format {{json .Config.Labels}} container-one":  []byte(containerLabels),
		"docker volume inspect --format {{json .}} stacks_postgres_data": []byte(volumeInspection),
	}}
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
	if call.name == "docker" && len(call.args) > 1 &&
		call.args[0] == "compose" {
		switch call.args[1] {
		case "stop", "rm", "up", "down":
			return true
		}
	}
	return call.name == "docker" && len(call.args) > 1 &&
		call.args[0] == "volume" && call.args[1] == "rm"
}
