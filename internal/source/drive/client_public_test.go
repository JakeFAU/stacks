package drive_test

import (
	"context"
	"net/http"
	"testing"

	"stacks/internal/source/drive"
)

func TestNewClientUsesStandardLibraryBoundary(t *testing.T) {
	client, err := drive.NewClient(
		context.Background(),
		&http.Client{},
		drive.NewTabClassifier([]string{"Transcript"}, nil),
	)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if client == nil {
		t.Fatal("NewClient() = nil, want client")
	}
}
