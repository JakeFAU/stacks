package cli

import (
	"context"
	"strings"
	"testing"
)

func TestAuthCommandRunsGoogleAuthorization(t *testing.T) {
	authorizer := &recordingAuthorizer{}
	command := AuthCommand{Google: authorizer}

	if err := command.Run(context.Background(), []string{"google"}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if authorizer.calls != 1 {
		t.Fatalf("Authorize() calls = %d, want 1", authorizer.calls)
	}
}

func TestAuthCommandRejectsDoctorWithoutAuthorizing(t *testing.T) {
	authorizer := &recordingAuthorizer{}
	command := AuthCommand{Google: authorizer}

	err := command.Run(context.Background(), []string{"doctor"})
	if err == nil || !strings.Contains(err.Error(), "usage") {
		t.Fatalf("Run() error = %v, want usage error", err)
	}
	if authorizer.calls != 0 {
		t.Fatalf("Authorize() calls = %d, want 0", authorizer.calls)
	}
}

type recordingAuthorizer struct {
	calls int
}

func (authorizer *recordingAuthorizer) Authorize(context.Context) error {
	authorizer.calls++
	return nil
}
