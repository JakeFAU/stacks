package identity_test

import (
	"testing"
	"time"

	"github.com/JakeFAU/stacks/core/identity"
)

func TestEntitySnapshotPreservesRecordedTime(t *testing.T) {
	recordedAt := time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC)
	snapshot := identity.EntitySnapshot{ID: "person-1", Kind: identity.KindPerson, RecordedAt: recordedAt}
	if !snapshot.RecordedAt.Equal(recordedAt) {
		t.Fatalf("RecordedAt = %s, want %s", snapshot.RecordedAt, recordedAt)
	}
}

func TestNormalizeNameCanonicalizesUnicodeCaseAndWhitespace(t *testing.T) {
	got := identity.NormalizeName("  R\u0069ya\u00a0Chen  ")
	if got != "riya chen" {
		t.Fatalf("NormalizeName() = %q, want %q", got, "riya chen")
	}
}

func TestNormalizeEmailCanonicalizesUnicodeCaseWithoutNameRules(t *testing.T) {
	got := identity.NormalizeEmail("  Riya.Chen@Synthetic.Example  ")
	if got != "riya.chen@synthetic.example" {
		t.Fatalf("NormalizeEmail() = %q, want %q", got, "riya.chen@synthetic.example")
	}
}

func TestValidEmailRejectsMalformedOptionalIdentifiers(t *testing.T) {
	for _, value := range []string{
		"Riya Chen",
		"riya@@synthetic.example",
		"riya@",
		"@synthetic.example",
		"riya chen@synthetic.example",
		"Display Name <riya@synthetic.example>",
	} {
		t.Run(value, func(t *testing.T) {
			if identity.ValidEmail(value) {
				t.Fatalf("ValidEmail(%q) = true, want false", value)
			}
		})
	}
	if !identity.ValidEmail("Riya.Chen@Synthetic.Example") {
		t.Fatal("ValidEmail(valid address) = false")
	}
}
