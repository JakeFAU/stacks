// Package localdb owns the narrowly guarded local Compose PostgreSQL reset.
package localdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/JakeFAU/stacks/adapters/postgres/migration"
	"github.com/JakeFAU/stacks/adapters/postgres/pgconfig"
)

const (
	ConfirmationToken = "delete-local-stacks-postgres"

	postgresService         = "postgres"
	postgresVolumeKey       = "postgres_data"
	postgresMount           = "/var/lib/postgresql"
	composeFileName         = "compose.yaml"
	composeProject          = "stacks"
	composeProjectLabel     = "com.docker.compose.project"
	composeServiceLabel     = "com.docker.compose.service"
	composeVolumeKeyLabel   = "com.docker.compose.volume"
	composeWorkingDirLabel  = "com.docker.compose.project.working_dir"
	composeConfigFilesLabel = "com.docker.compose.project.config_files"
)

var dockerRedirectEnvironment = []string{
	"DOCKER_HOST",
	"DOCKER_CONTEXT",
	"COMPOSE_FILE",
	"COMPOSE_PROJECT_NAME",
}

// CommandRunner executes an exact local command and returns its bounded output.
type CommandRunner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

// MigrationApplier applies the already-selected embedded manifests.
type MigrationApplier interface {
	Apply(context.Context) (migration.ApplyResult, error)
}

// ExecRunner runs local commands without a shell.
type ExecRunner struct{}

// Run executes one exact argv vector.
func (ExecRunner) Run(
	ctx context.Context,
	name string,
	args ...string,
) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// Resetter validates the exact Compose service and volume before changing
// local state, then recreates PostgreSQL and applies embedded migrations.
type Resetter struct {
	DatabaseURLs []string
	Runner       CommandRunner
	Migrator     MigrationApplier
}

// Reset performs the exact confirmed local reset.
func (resetter Resetter) Reset(
	ctx context.Context,
	confirmation string,
	output io.Writer,
) error {
	if confirmation != ConfirmationToken {
		return fmt.Errorf("reset local PostgreSQL: exact confirmation is required")
	}
	for _, databaseURL := range resetter.DatabaseURLs {
		if _, err := pgconfig.ParseLocal(databaseURL); err != nil {
			return fmt.Errorf("reset local PostgreSQL: database URL must use a loopback host")
		}
	}
	if len(resetter.DatabaseURLs) == 0 {
		return fmt.Errorf("reset local PostgreSQL: database URLs are required")
	}
	for _, variable := range dockerRedirectEnvironment {
		if value, present := os.LookupEnv(variable); present && strings.TrimSpace(value) != "" {
			return fmt.Errorf("reset local PostgreSQL: ambient Docker or Compose redirection is not allowed")
		}
	}
	if resetter.Runner == nil {
		return fmt.Errorf("reset local PostgreSQL: command runner is required")
	}
	if resetter.Migrator == nil {
		return fmt.Errorf("reset local PostgreSQL: migrator is required")
	}

	projectDirectory, composePath, err := repositoryComposeTarget()
	if err != nil {
		return err
	}
	target, err := resetter.inspectTarget(ctx, projectDirectory, composePath)
	if err != nil {
		return err
	}
	if output == nil {
		output = io.Discard
	}
	if _, err := fmt.Fprintf(
		output,
		"service=%s volume=%s warning=local PostgreSQL data is unrecoverable\n",
		postgresService,
		target.volumeName,
	); err != nil {
		return errors.New("reset local PostgreSQL: write warning operation failed")
	}

	commands := []struct {
		stage string
		name  string
		args  []string
	}{
		{
			stage: "stop",
			name:  "docker",
			args: resetter.composeArgs(
				target.endpoint,
				projectDirectory,
				composePath,
				"stop",
				postgresService,
			),
		},
		{
			stage: "container removal",
			name:  "docker",
			args: resetter.composeArgs(
				target.endpoint,
				projectDirectory,
				composePath,
				"rm",
				"-f",
				postgresService,
			),
		},
		{
			stage: "volume removal",
			name:  "docker",
			args:  []string{"--host", target.endpoint, "volume", "rm", target.volumeName},
		},
		{
			stage: "recreate and wait",
			name:  "docker",
			args: resetter.composeArgs(
				target.endpoint,
				projectDirectory,
				composePath,
				"up",
				"-d",
				"--wait",
				postgresService,
			),
		},
	}
	for _, command := range commands {
		if _, err := resetter.Runner.Run(ctx, command.name, command.args...); err != nil {
			return newOperationError(ctx, command.stage, err)
		}
	}
	if _, err := resetter.Migrator.Apply(ctx); err != nil {
		return newOperationError(ctx, "migrate", err)
	}
	return nil
}

type resetTarget struct {
	endpoint   string
	volumeName string
}

type composeModel struct {
	Name     string                    `json:"name"`
	Services map[string]composeService `json:"services"`
	Volumes  map[string]composeVolume  `json:"volumes"`
}

type composeService struct {
	Volumes []composeMount `json:"volumes"`
}

type composeMount struct {
	Type   string `json:"type"`
	Source string `json:"source"`
	Target string `json:"target"`
}

type composeVolume struct {
	Name string `json:"name"`
}

type volumeInspection struct {
	Name   string            `json:"Name"`
	Labels map[string]string `json:"Labels"`
}

func (resetter Resetter) inspectTarget(
	ctx context.Context,
	projectDirectory string,
	composePath string,
) (resetTarget, error) {
	endpoint, err := resetter.inspectLocalContext(ctx)
	if err != nil {
		return resetTarget{}, err
	}
	configOutput, err := resetter.Runner.Run(
		ctx,
		"docker",
		resetter.composeArgs(
			endpoint,
			projectDirectory,
			composePath,
			"config",
			"--format",
			"json",
		)...,
	)
	if err != nil {
		return resetTarget{}, fmt.Errorf("reset local PostgreSQL: inspect Compose configuration")
	}
	var config composeModel
	if err := json.Unmarshal(configOutput, &config); err != nil {
		return resetTarget{}, fmt.Errorf("reset local PostgreSQL: invalid Compose configuration")
	}
	if config.Name != composeProject {
		return resetTarget{}, fmt.Errorf("reset local PostgreSQL: Compose project does not match")
	}
	service, ok := config.Services[postgresService]
	if !ok {
		return resetTarget{}, fmt.Errorf("reset local PostgreSQL: service %q is unresolved", postgresService)
	}
	volume, ok := config.Volumes[postgresVolumeKey]
	if !ok || strings.TrimSpace(volume.Name) == "" {
		return resetTarget{}, fmt.Errorf("reset local PostgreSQL: volume key %q is unresolved", postgresVolumeKey)
	}
	matches := make([]composeMount, 0, 1)
	for _, mount := range service.Volumes {
		if mount.Type == "volume" && mount.Target == postgresMount {
			matches = append(matches, mount)
		}
	}
	if len(matches) != 1 ||
		(matches[0].Source != postgresVolumeKey && matches[0].Source != volume.Name) {
		return resetTarget{}, fmt.Errorf("reset local PostgreSQL: PostgreSQL volume mount is unresolved or ambiguous")
	}

	containerOutput, err := resetter.Runner.Run(
		ctx,
		"docker",
		resetter.composeArgs(
			endpoint,
			projectDirectory,
			composePath,
			"ps",
			"-q",
			postgresService,
		)...,
	)
	if err != nil {
		return resetTarget{}, fmt.Errorf("reset local PostgreSQL: inspect PostgreSQL service")
	}
	containerIDs := strings.Fields(string(containerOutput))
	if len(containerIDs) != 1 {
		return resetTarget{}, fmt.Errorf("reset local PostgreSQL: PostgreSQL service is unresolved or ambiguous")
	}
	containerInspectionOutput, err := resetter.Runner.Run(
		ctx,
		"docker",
		"--host",
		endpoint,
		"inspect",
		"--format",
		"{{json .}}",
		containerIDs[0],
	)
	if err != nil {
		return resetTarget{}, fmt.Errorf("reset local PostgreSQL: inspect live PostgreSQL service")
	}
	var container containerInspection
	if err := json.Unmarshal(containerInspectionOutput, &container); err != nil {
		return resetTarget{}, fmt.Errorf("reset local PostgreSQL: invalid live PostgreSQL service inspection")
	}
	if container.Config.Labels[composeProjectLabel] != composeProject ||
		container.Config.Labels[composeServiceLabel] != postgresService {
		return resetTarget{}, fmt.Errorf("reset local PostgreSQL: PostgreSQL service labels do not match")
	}
	if !matchesComposeProvenance(
		container.Config.Labels,
		projectDirectory,
		composePath,
	) {
		return resetTarget{}, fmt.Errorf("reset local PostgreSQL: PostgreSQL service provenance does not match")
	}
	mounts := make([]containerMount, 0, 1)
	for _, mount := range container.Mounts {
		if mount.Destination == postgresMount {
			mounts = append(mounts, mount)
		}
	}
	if len(mounts) != 1 ||
		mounts[0].Type != "volume" ||
		mounts[0].Name != volume.Name {
		return resetTarget{}, fmt.Errorf("reset local PostgreSQL: live PostgreSQL volume mount does not match")
	}

	volumeOutput, err := resetter.Runner.Run(
		ctx,
		"docker",
		"--host",
		endpoint,
		"volume",
		"inspect",
		"--format",
		"{{json .}}",
		volume.Name,
	)
	if err != nil {
		return resetTarget{}, fmt.Errorf("reset local PostgreSQL: inspect PostgreSQL volume")
	}
	var inspected volumeInspection
	if err := json.Unmarshal(volumeOutput, &inspected); err != nil {
		return resetTarget{}, fmt.Errorf("reset local PostgreSQL: invalid PostgreSQL volume inspection")
	}
	if inspected.Name != volume.Name ||
		inspected.Labels[composeProjectLabel] != config.Name ||
		inspected.Labels[composeVolumeKeyLabel] != postgresVolumeKey {
		return resetTarget{}, fmt.Errorf("reset local PostgreSQL: PostgreSQL volume labels do not match")
	}
	return resetTarget{endpoint: endpoint, volumeName: volume.Name}, nil
}

type containerInspection struct {
	Config struct {
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
	Mounts []containerMount `json:"Mounts"`
}

type containerMount struct {
	Type        string `json:"Type"`
	Name        string `json:"Name"`
	Destination string `json:"Destination"`
}

func (resetter Resetter) inspectLocalContext(ctx context.Context) (string, error) {
	output, err := resetter.Runner.Run(ctx, "docker", "context", "show")
	if err != nil {
		return "", fmt.Errorf("reset local PostgreSQL: inspect Docker context")
	}
	fields := strings.Fields(string(output))
	if len(fields) != 1 {
		return "", fmt.Errorf("reset local PostgreSQL: Docker context is unresolved")
	}
	contextName := fields[0]
	output, err = resetter.Runner.Run(
		ctx,
		"docker",
		"--context",
		contextName,
		"context",
		"inspect",
		contextName,
		"--format",
		"{{json .Endpoints.docker.Host}}",
	)
	if err != nil {
		return "", fmt.Errorf("reset local PostgreSQL: inspect Docker context endpoint")
	}
	var endpoint string
	if err := json.Unmarshal(output, &endpoint); err != nil ||
		(!strings.HasPrefix(endpoint, "unix:///") &&
			!strings.HasPrefix(endpoint, "npipe:////./pipe/")) {
		return "", fmt.Errorf("reset local PostgreSQL: Docker context endpoint is not local")
	}
	return endpoint, nil
}

func (resetter Resetter) composeArgs(
	endpoint string,
	projectDirectory string,
	composePath string,
	arguments ...string,
) []string {
	result := []string{
		"--host",
		endpoint,
		"compose",
		"--project-directory",
		projectDirectory,
		"--file",
		composePath,
		"--project-name",
		composeProject,
	}
	return append(result, arguments...)
}

func repositoryComposeTarget() (string, string, error) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok || !filepath.IsAbs(sourceFile) {
		return "", "", errors.New("reset local PostgreSQL: repository Compose target is unresolved")
	}
	projectDirectory := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", ".."))
	composePath := filepath.Clean(filepath.Join(projectDirectory, composeFileName))
	if !filepath.IsAbs(projectDirectory) ||
		!filepath.IsAbs(composePath) ||
		filepath.Dir(composePath) != projectDirectory {
		return "", "", errors.New("reset local PostgreSQL: repository Compose target is unresolved")
	}
	info, err := os.Stat(composePath)
	if err != nil || info.IsDir() {
		return "", "", errors.New("reset local PostgreSQL: repository Compose target is unresolved")
	}
	return projectDirectory, composePath, nil
}

func matchesComposeProvenance(
	labels map[string]string,
	projectDirectory string,
	composePath string,
) bool {
	workingDirectory := labels[composeWorkingDirLabel]
	configFiles := strings.Split(labels[composeConfigFilesLabel], ",")
	return filepath.IsAbs(workingDirectory) &&
		filepath.Clean(workingDirectory) == workingDirectory &&
		workingDirectory == projectDirectory &&
		len(configFiles) == 1 &&
		filepath.IsAbs(configFiles[0]) &&
		filepath.Clean(configFiles[0]) == configFiles[0] &&
		configFiles[0] == composePath
}

type operationError struct {
	message string
	cause   error
}

func (err operationError) Error() string {
	return err.message
}

func (err operationError) Unwrap() error {
	return err.cause
}

func newOperationError(ctx context.Context, stage string, err error) error {
	var safeCause error
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		safeCause = context.DeadlineExceeded
	case errors.Is(ctx.Err(), context.Canceled):
		safeCause = context.Canceled
	case errors.Is(err, context.DeadlineExceeded):
		safeCause = context.DeadlineExceeded
	case errors.Is(err, context.Canceled):
		safeCause = context.Canceled
	}
	return operationError{
		message: fmt.Sprintf(
			"reset local PostgreSQL: %s operation failed",
			stage,
		),
		cause: safeCause,
	}
}
