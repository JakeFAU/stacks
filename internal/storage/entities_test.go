package storage

import (
	"context"
	"strings"
	"testing"
)

func TestPutAliasRejectsMalformedEmailBeforePersistence(t *testing.T) {
	repository := &EntityRepository{}

	_, err := repository.PutAlias(context.Background(), AliasInput{
		EntityID:        "11111111-2222-3333-4444-555555555555",
		NormalizedValue: "riya@@synthetic.example",
		Type:            "email",
	})
	if err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("PutAlias() error = %v, want malformed email rejection", err)
	}
}

func TestCreateMentionRejectsMalformedEmailBeforePersistence(t *testing.T) {
	repository := &EntityRepository{}

	_, err := repository.CreateMention(context.Background(), MentionInput{
		EvidenceSpanID: "11111111-2222-3333-4444-555555555555",
		Surface:        "Riya Chen",
		Email:          "riya@@synthetic.example",
		Role:           "speaker",
	})
	if err == nil || !strings.Contains(err.Error(), "email is invalid") {
		t.Fatalf("CreateMention() error = %v, want malformed email rejection", err)
	}
}
