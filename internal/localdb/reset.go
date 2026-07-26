// Package localdb owns the narrowly guarded local Compose PostgreSQL reset.
package localdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os/exec"
	"strings"

	"github.com/JakeFAU/stacks/adapters/postgres/migration"
)

const (
	ConfirmationToken = "delete-local-stacks-postgres"

	postgresService       = "postgres"
	postgresVolumeKey     = "postgres_data"
	postgresMount         = "/var/lib/postgresql"
	composeProjectLabel   = "com.docker.compose.project"
	composeServiceLabel   = "com.docker.compose.service"
	composeVolumeKeyLabel = "com.docker.compose.volume"
)

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
		if err := validateLoopbackDatabaseURL(databaseURL); err != nil {
			return fmt.Errorf("reset local PostgreSQL: database URL must use a loopback host")
		}
	}
	if len(resetter.DatabaseURLs) == 0 {
		return fmt.Errorf("reset local PostgreSQL: database URLs are required")
	}
	if resetter.Runner == nil {
		return fmt.Errorf("reset local PostgreSQL: command runner is required")
	}
	if resetter.Migrator == nil {
		return fmt.Errorf("reset local PostgreSQL: migrator is required")
	}

	target, err := resetter.inspectTarget(ctx)
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
		return fmt.Errorf("reset local PostgreSQL: write warning: %w", err)
	}

	commands := []struct {
		name string
		args []string
	}{
		{name: "docker", args: []string{"compose", "stop", postgresService}},
		{name: "docker", args: []string{"compose", "rm", "-f", postgresService}},
		{name: "docker", args: []string{"volume", "rm", target.volumeName}},
		{name: "docker", args: []string{"compose", "up", "-d", "--wait", postgresService}},
	}
	for _, command := range commands {
		if _, err := resetter.Runner.Run(ctx, command.name, command.args...); err != nil {
			return fmt.Errorf("reset local PostgreSQL: local container operation failed")
		}
	}
	if _, err := resetter.Migrator.Apply(ctx); err != nil {
		return fmt.Errorf("reset local PostgreSQL: apply embedded migrations: %w", err)
	}
	return nil
}

type resetTarget struct {
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

func (resetter Resetter) inspectTarget(ctx context.Context) (resetTarget, error) {
	configOutput, err := resetter.Runner.Run(ctx, "docker", "compose", "config", "--format", "json")
	if err != nil {
		return resetTarget{}, fmt.Errorf("reset local PostgreSQL: inspect Compose configuration")
	}
	var config composeModel
	if err := json.Unmarshal(configOutput, &config); err != nil {
		return resetTarget{}, fmt.Errorf("reset local PostgreSQL: invalid Compose configuration")
	}
	if strings.TrimSpace(config.Name) == "" {
		return resetTarget{}, fmt.Errorf("reset local PostgreSQL: Compose project is unresolved")
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

	containerOutput, err := resetter.Runner.Run(ctx, "docker", "compose", "ps", "-q", postgresService)
	if err != nil {
		return resetTarget{}, fmt.Errorf("reset local PostgreSQL: inspect PostgreSQL service")
	}
	containerIDs := strings.Fields(string(containerOutput))
	if len(containerIDs) != 1 {
		return resetTarget{}, fmt.Errorf("reset local PostgreSQL: PostgreSQL service is unresolved or ambiguous")
	}
	containerLabelsOutput, err := resetter.Runner.Run(
		ctx,
		"docker",
		"inspect",
		"--format",
		"{{json .Config.Labels}}",
		containerIDs[0],
	)
	if err != nil {
		return resetTarget{}, fmt.Errorf("reset local PostgreSQL: inspect PostgreSQL service labels")
	}
	var containerLabels map[string]string
	if err := json.Unmarshal(containerLabelsOutput, &containerLabels); err != nil {
		return resetTarget{}, fmt.Errorf("reset local PostgreSQL: invalid PostgreSQL service labels")
	}
	if containerLabels[composeProjectLabel] != config.Name ||
		containerLabels[composeServiceLabel] != postgresService {
		return resetTarget{}, fmt.Errorf("reset local PostgreSQL: PostgreSQL service labels do not match")
	}

	volumeOutput, err := resetter.Runner.Run(
		ctx,
		"docker",
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
	return resetTarget{volumeName: volume.Name}, nil
}

func validateLoopbackDatabaseURL(databaseURL string) error {
	parsed, err := url.Parse(databaseURL)
	if err != nil ||
		(parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") ||
		parsed.Host == "" {
		return errors.New("invalid PostgreSQL URL")
	}
	host := parsed.Hostname()
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	address := net.ParseIP(host)
	if address == nil || !address.IsLoopback() {
		return errors.New("PostgreSQL host is not loopback")
	}
	return nil
}
