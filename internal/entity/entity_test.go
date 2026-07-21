package entity

import "testing"

func TestNormalizeNameCanonicalizesUnicodeCaseAndWhitespace(t *testing.T) {
	got := NormalizeName("  R\u0069ya\u00a0Chen  ")
	if got != "riya chen" {
		t.Fatalf("NormalizeName() = %q, want %q", got, "riya chen")
	}
}

func TestNormalizeEmailCanonicalizesUnicodeCaseWithoutNameRules(t *testing.T) {
	got := NormalizeEmail("  Riya.Chen@Synthetic.Example  ")
	if got != "riya.chen@synthetic.example" {
		t.Fatalf("NormalizeEmail() = %q, want %q", got, "riya.chen@synthetic.example")
	}
}
