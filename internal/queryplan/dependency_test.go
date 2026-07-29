package queryplan

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

func TestProductionImportsStayProviderNeutral(t *testing.T) {
	command := exec.Command("go", "list", "-json", ".")
	output, err := command.Output()
	if err != nil {
		t.Fatalf("go list -json .: %v", err)
	}
	var packageInfo struct {
		Imports []string
	}
	if err := json.Unmarshal(output, &packageInfo); err != nil {
		t.Fatalf("decode go list output: %v", err)
	}
	for _, imported := range packageInfo.Imports {
		for _, forbidden := range []string{
			"stacks/internal/openai",
			"stacks/internal/anthropic",
			"stacks/internal/bedrock",
			"github.com/openai/",
			"github.com/anthropics/",
			"github.com/aws/",
			"github.com/spf13/cobra",
			"github.com/spf13/viper",
			"github.com/jackc/pgx/",
			"github.com/JakeFAU/stacks/adapters/postgres",
			"stacks/internal/cli",
			"stacks/internal/config",
			"stacks/internal/httpapi",
		} {
			if strings.HasPrefix(imported, forbidden) {
				t.Errorf("production import %q has forbidden prefix %q", imported, forbidden)
			}
		}
	}
	if len(packageInfo.Imports) == 0 {
		t.Fatal("go list -json . returned no production imports")
	}
}
