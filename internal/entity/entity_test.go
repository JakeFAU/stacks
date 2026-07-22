package entity

import (
	"testing"
	"time"
)

func TestEntitySnapshotPreservesRecordedTime(t *testing.T) {
	recordedAt := time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC)
	snapshot := EntitySnapshot{ID: "person-1", Kind: KindPerson, RecordedAt: recordedAt}
	if !snapshot.RecordedAt.Equal(recordedAt) {
		t.Fatalf("RecordedAt = %s, want %s", snapshot.RecordedAt, recordedAt)
	}
}

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
