package drive

import (
	"context"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/api/docs/v1"
	googledrive "google.golang.org/api/drive/v3"

	"stacks/internal/googleauth"
)

func TestAuthorizerReturnsSharedScopedAuthorizer(t *testing.T) {
	var authorizer *googleauth.Authorizer = NewAuthorizer("/synthetic/client.json", "/synthetic/token.json", io.Discard)
	if authorizer == nil {
		t.Fatal("NewAuthorizer() = nil")
	}
}

func TestAuthorizerUsesExactDriveAndDocsReadOnlyScopes(t *testing.T) {
	clientFile := writeInstalledClient(t)
	output := newURLWriter()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- NewAuthorizer(clientFile, filepath.Join(t.TempDir(), "token.json"), output).Authorize(ctx)
	}()

	parsed := parseAuthorizationURL(t, output.next(t))
	gotScopes := strings.Fields(parsed.Query().Get("scope"))
	sort.Strings(gotScopes)
	wantScopes := []string{googledrive.DriveReadonlyScope, docs.DocumentsReadonlyScope}
	sort.Strings(wantScopes)
	if !reflect.DeepEqual(gotScopes, wantScopes) {
		t.Fatalf("OAuth scopes = %#v, want Drive and Docs read-only scopes %#v", gotScopes, wantScopes)
	}
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("Authorize() error = %v, want cancellation after URL inspection", err)
	}
}

func TestNewAuthorizedHTTPClientLoadsStoredRefreshTokenWithoutNetwork(t *testing.T) {
	directory := t.TempDir()
	clientFile := filepath.Join(directory, "client.json")
	if err := os.WriteFile(clientFile, []byte(installedClientJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	tokenFile := filepath.Join(directory, "token.json")
	if err := os.WriteFile(tokenFile, []byte(`{"access_token":"synthetic-access","token_type":"Bearer","refresh_token":"synthetic-refresh","expiry":"2099-07-21T12:00:00Z"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	client, err := NewAuthorizedHTTPClient(context.Background(), clientFile, tokenFile)
	if err != nil {
		t.Fatalf("NewAuthorizedHTTPClient() error = %v", err)
	}
	if client == nil {
		t.Fatal("NewAuthorizedHTTPClient() = nil")
	}
}

const installedClientJSON = `{"installed":{"client_id":"synthetic-client-id","client_secret":"synthetic-client-secret","redirect_uris":["http://127.0.0.1"],"auth_uri":"https://accounts.google.test/auth","token_uri":"https://oauth.google.test/token"}}`

func writeInstalledClient(t *testing.T) string {
	t.Helper()
	clientFile := filepath.Join(t.TempDir(), "client.json")
	if err := os.WriteFile(clientFile, []byte(installedClientJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	return clientFile
}

type urlWriter struct {
	mu      sync.Mutex
	urlLine chan string
}

func newURLWriter() *urlWriter { return &urlWriter{urlLine: make(chan string, 1)} }

func (writer *urlWriter) Write(value []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	line := string(value)
	select {
	case writer.urlLine <- line:
	default:
	}
	return len(value), nil
}

func (writer *urlWriter) next(t *testing.T) string {
	t.Helper()
	select {
	case line := <-writer.urlLine:
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

func parseAuthorizationURL(t *testing.T, value string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(value)
	if err != nil {
		t.Fatalf("parse authorization URL: %v", err)
	}
	return parsed
}
