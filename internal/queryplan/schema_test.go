package queryplan

import (
	"crypto/sha256"
	"fmt"
	"testing"
)

const (
	expectedPromptSHA256 = "da154eda05db8fcbf0fe75fd4512d7b3f5b0f3890ac08a40af9689ff9c5aa251"
	expectedSchemaSHA256 = "017a0a8a370df66feaca9aad672ba90d2ed768d8070f37f57a6ed45c16a88713"
)

func TestPromptAndSchemaDigests(t *testing.T) {
	contract, err := PromptContract(PromptVersion)
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256([]byte(contract.SystemPrompt))); got != expectedPromptSHA256 {
		t.Fatalf("prompt SHA-256 = %s", got)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(contract.JSONSchema)); got != expectedSchemaSHA256 {
		t.Fatalf("schema SHA-256 = %s", got)
	}
}
