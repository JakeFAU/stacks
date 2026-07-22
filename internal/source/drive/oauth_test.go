package drive

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func TestAuthorizerUsesReadOnlyScopesAndLoopbackCallback(t *testing.T) {
	fixture := newAuthorizationFixture(t, successfulTokenTransport(t))
	result := fixture.start(t)
	authorizationURL := fixture.authorizationURL(t)
	parsed := parseURL(t, authorizationURL)

	gotScopes := strings.Fields(parsed.Query().Get("scope"))
	sort.Strings(gotScopes)
	wantScopes := append([]string(nil), googleReadOnlyScopes...)
	sort.Strings(wantScopes)
	if !reflect.DeepEqual(gotScopes, wantScopes) {
		t.Fatalf("OAuth scopes = %#v, want read-only scopes %#v", gotScopes, wantScopes)
	}
	if parsed.Query().Get("access_type") != "offline" {
		t.Errorf("access_type = %q, want offline", parsed.Query().Get("access_type"))
	}
	if parsed.Query().Get("prompt") != "consent" {
		t.Errorf("prompt = %q, want consent so the exchange returns a refresh token", parsed.Query().Get("prompt"))
	}
	redirect := parseURL(t, parsed.Query().Get("redirect_uri"))
	if redirect.Scheme != "http" || redirect.Host != fixture.listener.Addr().String() || redirect.Path != oauthCallbackPath {
		t.Errorf("redirect URI = %q, want ephemeral loopback callback", redirect.String())
	}
	if host, _, err := net.SplitHostPort(redirect.Host); err != nil || host != "127.0.0.1" {
		t.Errorf("redirect host = %q, want 127.0.0.1", redirect.Host)
	}

	fixture.callback(t, redirect, "synthetic-code", parsed.Query().Get("state"))
	if err := <-result; err != nil {
		t.Fatalf("Authorize() error = %v", err)
	}

	info, err := os.Stat(fixture.tokenFile)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("token permissions = %04o, want 0600", got)
	}
	tokenJSON, err := os.ReadFile(fixture.tokenFile)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(tokenJSON, []byte(`"refresh_token":"synthetic-refresh-token"`)) {
		t.Errorf("stored token does not contain exchanged refresh token")
	}
	if output := fixture.output.String(); strings.Contains(output, "synthetic-code") || strings.Contains(output, "synthetic-access-token") || strings.Contains(output, "synthetic-refresh-token") {
		t.Errorf("command output disclosed OAuth token or authorization code: %q", output)
	}
}

func TestAuthorizerRejectsMismatchedStateWithoutWritingToken(t *testing.T) {
	fixture := newAuthorizationFixture(t, successfulTokenTransport(t))
	result := fixture.start(t)
	parsed := parseURL(t, fixture.authorizationURL(t))
	redirect := parseURL(t, parsed.Query().Get("redirect_uri"))

	fixture.callbackExpectingStatus(t, redirect, "synthetic-code", "wrong-state", http.StatusBadRequest)
	err := <-result
	if err == nil || !strings.Contains(err.Error(), "state mismatch") {
		t.Fatalf("Authorize() error = %v, want state mismatch", err)
	}
	if _, statErr := os.Stat(fixture.tokenFile); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("token file exists after rejected callback: %v", statErr)
	}
}

func TestAuthorizerDoesNotReplaceTokenWithoutRefreshToken(t *testing.T) {
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return jsonResponse(request, `{"access_token":"synthetic-access-token","token_type":"Bearer","expires_in":3600}`), nil
	})
	fixture := newAuthorizationFixture(t, transport)
	if err := os.WriteFile(fixture.tokenFile, []byte("existing-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	result := fixture.start(t)
	parsed := parseURL(t, fixture.authorizationURL(t))
	fixture.callback(t, parseURL(t, parsed.Query().Get("redirect_uri")), "synthetic-code", parsed.Query().Get("state"))

	err := <-result
	if err == nil || !strings.Contains(err.Error(), "refresh token") {
		t.Fatalf("Authorize() error = %v, want missing refresh token error", err)
	}
	contents, readErr := os.ReadFile(fixture.tokenFile)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(contents) != "existing-token" {
		t.Errorf("token contents = %q, want existing token preserved", contents)
	}
}

func TestOAuthStateIsRandomAcrossAuthorizations(t *testing.T) {
	first, err := generateOAuthState()
	if err != nil {
		t.Fatal(err)
	}
	second, err := generateOAuthState()
	if err != nil {
		t.Fatal(err)
	}
	if first == "" || second == "" || first == second {
		t.Fatalf("generated OAuth states = %q and %q, want distinct nonempty values", first, second)
	}
}

func TestReplaceTokenFilePreservesExistingFileWhenRenameFails(t *testing.T) {
	directory := t.TempDir()
	tokenFile := filepath.Join(directory, "token.json")
	if err := os.WriteFile(tokenFile, []byte("existing-token"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := replaceTokenFile(tokenFile, &oauth2.Token{RefreshToken: "replacement-token"}, func(string, string) error {
		return errors.New("synthetic rename failure")
	})
	if err == nil {
		t.Fatal("replaceTokenFile() error = nil, want rename failure")
	}
	contents, readErr := os.ReadFile(tokenFile)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(contents) != "existing-token" {
		t.Errorf("token contents = %q, want existing file preserved", contents)
	}
	entries, readDirErr := os.ReadDir(directory)
	if readDirErr != nil {
		t.Fatal(readDirErr)
	}
	if len(entries) != 1 || entries[0].Name() != "token.json" {
		t.Errorf("token directory entries = %#v, want failed temporary file removed", entries)
	}
}

func TestAuthorizerRedactsTokenEndpointResponse(t *testing.T) {
	const secret = "secret-from-token-endpoint"
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":"invalid_grant","access_token":"` + secret + `"}`)),
			Request:    request,
		}, nil
	})
	fixture := newAuthorizationFixture(t, transport)
	result := fixture.start(t)
	parsed := parseURL(t, fixture.authorizationURL(t))
	fixture.callback(t, parseURL(t, parsed.Query().Get("redirect_uri")), "synthetic-code", parsed.Query().Get("state"))

	err := <-result
	if err == nil {
		t.Fatal("Authorize() error = nil, want token exchange error")
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(fixture.output.String(), secret) {
		t.Fatalf("OAuth error disclosed token endpoint response: %v", err)
	}
}

type authorizationFixture struct {
	authorizer     *Authorizer
	listener       *pipeListener
	tokenFile      string
	output         *notifyingBuffer
	callbackClient *http.Client
	callback       func(*testing.T, *url.URL, string, string)
}

func newAuthorizationFixture(t *testing.T, tokenTransport http.RoundTripper) *authorizationFixture {
	t.Helper()
	directory := t.TempDir()
	clientFile := filepath.Join(directory, "client.json")
	tokenFile := filepath.Join(directory, "token.json")
	clientJSON := `{"installed":{"client_id":"synthetic-client-id","client_secret":"synthetic-client-secret","redirect_uris":["http://127.0.0.1"],"auth_uri":"https://accounts.google.test/auth","token_uri":"https://oauth.google.test/token"}}`
	if err := os.WriteFile(clientFile, []byte(clientJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	listener := newPipeListener()
	output := newNotifyingBuffer()
	authorizer := NewAuthorizer(clientFile, tokenFile, output)
	authorizer.listen = func(network, address string) (net.Listener, error) {
		if network != "tcp" || address != "127.0.0.1:0" {
			t.Errorf("listen(%q, %q), want tcp 127.0.0.1:0", network, address)
		}
		return listener, nil
	}

	callbackClient := &http.Client{Transport: &http.Transport{
		DialContext:       listener.DialContext,
		DisableKeepAlives: true,
	}}
	fixture := &authorizationFixture{
		authorizer:     authorizer,
		listener:       listener,
		tokenFile:      tokenFile,
		output:         output,
		callbackClient: callbackClient,
	}
	fixture.callback = func(t *testing.T, redirect *url.URL, code, state string) {
		t.Helper()
		fixture.callbackExpectingStatus(t, redirect, code, state, http.StatusOK)
	}
	authorizer.exchangeClient = &http.Client{Transport: tokenTransport}
	return fixture
}

func (fixture *authorizationFixture) start(t *testing.T) <-chan error {
	t.Helper()
	result := make(chan error, 1)
	go func() {
		result <- fixture.authorizer.Authorize(context.Background())
	}()
	return result
}

func (fixture *authorizationFixture) authorizationURL(t *testing.T) string {
	t.Helper()
	select {
	case output := <-fixture.output.notifications:
		const prefix = "Open this URL to authorize Stacks: "
		index := strings.Index(output, prefix)
		if index < 0 {
			t.Fatalf("authorization output = %q, want URL prompt", output)
		}
		return strings.TrimSpace(output[index+len(prefix):])
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for authorization URL")
		return ""
	}
}

func (fixture *authorizationFixture) callbackExpectingStatus(t *testing.T, redirect *url.URL, code, state string, wantStatus int) {
	t.Helper()
	query := redirect.Query()
	query.Set("code", code)
	query.Set("state", state)
	redirect.RawQuery = query.Encode()
	response, err := fixture.callbackClient.Get(redirect.String())
	if err != nil {
		t.Fatalf("send OAuth callback: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != wantStatus {
		t.Errorf("callback status = %d, want %d", response.StatusCode, wantStatus)
	}
}

func successfulTokenTransport(t *testing.T) http.RoundTripper {
	t.Helper()
	return roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != "https://oauth.google.test/token" {
			return nil, errors.New("unexpected token endpoint")
		}
		if err := request.ParseForm(); err != nil {
			t.Errorf("parse token exchange: %v", err)
		} else if got := request.Form.Get("code"); got != "synthetic-code" {
			t.Errorf("exchanged code = %q, want synthetic-code", got)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(`{
				"access_token":"synthetic-access-token",
				"refresh_token":"synthetic-refresh-token",
				"token_type":"Bearer",
				"expires_in":3600
			}`)),
			Request: request,
		}, nil
	})
}

func parseURL(t *testing.T, value string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(value)
	if err != nil {
		t.Fatalf("parse URL %q: %v", value, err)
	}
	return parsed
}

type notifyingBuffer struct {
	mu            sync.Mutex
	buffer        bytes.Buffer
	notifications chan string
}

func newNotifyingBuffer() *notifyingBuffer {
	return &notifyingBuffer{notifications: make(chan string, 1)}
}

func (buffer *notifyingBuffer) Write(value []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	written, err := buffer.buffer.Write(value)
	select {
	case buffer.notifications <- buffer.buffer.String():
	default:
	}
	return written, err
}

func (buffer *notifyingBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.String()
}

type pipeListener struct {
	connections chan net.Conn
	closed      chan struct{}
	closeOnce   sync.Once
	address     net.Addr
}

func newPipeListener() *pipeListener {
	return &pipeListener{
		connections: make(chan net.Conn),
		closed:      make(chan struct{}),
		address:     &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 43119},
	}
}

func (listener *pipeListener) Accept() (net.Conn, error) {
	select {
	case connection := <-listener.connections:
		return connection, nil
	case <-listener.closed:
		return nil, net.ErrClosed
	}
}

func (listener *pipeListener) Close() error {
	listener.closeOnce.Do(func() { close(listener.closed) })
	return nil
}

func (listener *pipeListener) Addr() net.Addr {
	return listener.address
}

func (listener *pipeListener) DialContext(ctx context.Context, _, _ string) (net.Conn, error) {
	client, server := net.Pipe()
	select {
	case listener.connections <- server:
		return client, nil
	case <-ctx.Done():
		client.Close()
		server.Close()
		return nil, ctx.Err()
	case <-listener.closed:
		client.Close()
		server.Close()
		return nil, net.ErrClosed
	}
}
