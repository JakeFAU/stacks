package reasoncode_test

import (
	"strings"
	"testing"

	"github.com/JakeFAU/stacks/core/internal/reasoncode"
)

func TestValidatePreservesExactCodeAtMaximumRuneBoundary(t *testing.T) {
	input := " " + strings.Repeat("界", 126) + " "

	got, err := reasoncode.Validate(input)
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if got != input {
		t.Fatalf("Validate() = %q, want exact input preserved", got)
	}
}

func TestValidateRejectsMoreThanMaximumRunes(t *testing.T) {
	input := strings.Repeat("界", 129)

	if _, err := reasoncode.Validate(input); err == nil {
		t.Fatal("Validate() error = nil, want 129-rune code rejected")
	}
}
