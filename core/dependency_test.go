package core_test

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestCoreDependencyBoundary(t *testing.T) {
	t.Parallel()

	command := exec.Command("go", "list", "-deps", "./...")
	command.Env = append(os.Environ(), "GOWORK=off")
	output, err := command.Output()
	if err != nil {
		t.Fatalf("list core dependencies: %v", err)
	}

	for _, dependency := range strings.Fields(string(output)) {
		for _, prefix := range forbiddenDependencyPrefixes {
			if strings.HasPrefix(dependency, prefix) {
				t.Errorf("core dependency %q matches forbidden prefix %q", dependency, prefix)
			}
		}
	}
}

var forbiddenDependencyPrefixes = []string{
	"stacks/internal/",
	"github.com/JakeFAU/stacks/adapters/",
	"github.com/JakeFAU/stacks/examples/",
	"github.com/JakeFAU/stacks/app/",
	"github.com/jackc/pgx/",
	"go.uber.org/zap",
	"go.opentelemetry.io/",
	"google.golang.org/api/",
	"github.com/aws/",
	"github.com/anthropics/",
	"github.com/openai/",
}
