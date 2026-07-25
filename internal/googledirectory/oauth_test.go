package googledirectory

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAuthorizerUsesOnlyDirectoryReadOnlyScope(t *testing.T) {
	clientFile := filepath.Join(t.TempDir(), "client.json")
	clientJSON := `{"installed":{"client_id":"synthetic-directory-client","client_secret":"synthetic-directory-secret","redirect_uris":["http://127.0.0.1"],"auth_uri":"https://accounts.google.test/auth","token_uri":"https://oauth.google.test/token"}}`
	if err := os.WriteFile(clientFile, []byte(clientJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	output := newAuthorizationOutput()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- NewAuthorizer(clientFile, filepath.Join(t.TempDir(), "token.json"), output).Authorize(ctx)
	}()

	parsed := parseDirectoryAuthorizationURL(t, output.next(t))
	gotScopes := strings.Fields(parsed.Query().Get("scope"))
	if len(gotScopes) != 1 || gotScopes[0] != ReadOnlyScope {
		t.Fatalf("OAuth scopes = %#v, want only %q", gotScopes, ReadOnlyScope)
	}
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("Authorize() error = %v, want cancellation after URL inspection", err)
	}
}

type authorizationOutput struct{ lines chan string }

func newAuthorizationOutput() *authorizationOutput {
	return &authorizationOutput{lines: make(chan string, 1)}
}

func (output *authorizationOutput) Write(value []byte) (int, error) {
	select {
	case output.lines <- string(value):
	default:
	}
	return len(value), nil
}

func (output *authorizationOutput) next(t *testing.T) string {
	t.Helper()
	select {
	case line := <-output.lines:
		const prefix = "Open this URL to authorize Stacks: "
		if !strings.HasPrefix(line, prefix) {
			t.Fatalf("authorization output = %q, want URL prompt", line)
		}
		return strings.TrimSpace(strings.TrimPrefix(line, prefix))
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for authorization URL")
		return ""
	}
}

func parseDirectoryAuthorizationURL(t *testing.T, value string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(value)
	if err != nil {
		t.Fatalf("parse authorization URL: %v", err)
	}
	return parsed
}
