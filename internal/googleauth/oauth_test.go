package googleauth

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
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

func TestAuthorizerUsesOnlySuppliedScopes(t *testing.T) {
	providedScopes := []string{"https://example.test/first.readonly", "https://example.test/second.readonly"}
	fixture := newAuthorizationFixture(t, providedScopes, successfulTokenTransport(t))
	result := fixture.start(t)
	parsed := parseURL(t, fixture.authorizationURL(t))

	gotScopes := strings.Fields(parsed.Query().Get("scope"))
	sort.Strings(gotScopes)
	wantScopes := append([]string(nil), providedScopes...)
	sort.Strings(wantScopes)
	if !reflect.DeepEqual(gotScopes, wantScopes) {
		t.Fatalf("OAuth scopes = %#v, want only supplied scopes %#v", gotScopes, wantScopes)
	}
	if parsed.Query().Get("access_type") != "offline" {
		t.Errorf("access_type = %q, want offline", parsed.Query().Get("access_type"))
	}
	if parsed.Query().Get("prompt") != "consent" {
		t.Errorf("prompt = %q, want consent", parsed.Query().Get("prompt"))
	}
	redirect := parseURL(t, parsed.Query().Get("redirect_uri"))
	if redirect.Scheme != "http" || redirect.Path != oauthCallbackPath {
		t.Errorf("redirect URI = %q, want loopback callback", redirect.String())
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
	if output := fixture.output.String(); strings.Contains(output, "synthetic-code") || strings.Contains(output, "synthetic-access-token") || strings.Contains(output, "synthetic-refresh-token") {
		t.Errorf("command output disclosed OAuth token or authorization code: %q", output)
	}
}

func TestAuthorizerRejectsEmptyScopesBeforeListening(t *testing.T) {
	authorizer := NewAuthorizer("/synthetic/client.json", "/synthetic/token.json", nil, io.Discard)
	called := false
	authorizer.listen = func(string, string) (net.Listener, error) {
		called = true
		return nil, errors.New("must not listen")
	}

	err := authorizer.Authorize(context.Background())
	if err == nil || !strings.Contains(err.Error(), "scopes") {
		t.Fatalf("Authorize() error = %v, want empty scope rejection", err)
	}
	if called {
		t.Fatal("Authorize() listened despite an empty scope list")
	}
}

func TestNewAuthorizedHTTPClientDoesNotExposeCredentialFileContents(t *testing.T) {
	const privateCredential = "synthetic-private-client-secret"
	clientFile := filepath.Join(t.TempDir(), "client.json")
	if err := os.WriteFile(clientFile, []byte(`{"installed":{"client_secret":"`+privateCredential+`"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := NewAuthorizedHTTPClient(context.Background(), clientFile, "/synthetic/token.json", []string{"https://example.test/readonly"})
	if err == nil {
		t.Fatal("NewAuthorizedHTTPClient() error = nil, want invalid credential error")
	}
	if strings.Contains(err.Error(), privateCredential) {
		t.Fatalf("NewAuthorizedHTTPClient() disclosed credential contents: %v", err)
	}
}

func TestOAuthCallbackHandlerIgnoresMismatchedStateUntilValidCallback(t *testing.T) {
	results := make(chan oauthCallback, 1)
	handler := oauthCallbackHandler("matching-state", results)

	mismatchedResponse := httptest.NewRecorder()
	handler.ServeHTTP(mismatchedResponse, newOAuthCallbackRequest(t, "untrusted-code", "wrong-state"))
	if mismatchedResponse.Code != http.StatusBadRequest {
		t.Fatalf("mismatched callback status = %d, want %d", mismatchedResponse.Code, http.StatusBadRequest)
	}
	select {
	case result := <-results:
		t.Fatalf("mismatched callback completed authorization: %#v", result)
	default:
	}

	handler.ServeHTTP(httptest.NewRecorder(), newOAuthCallbackRequest(t, "trusted-code", "matching-state"))
	if result := <-results; result.code != "trusted-code" || result.err != nil {
		t.Fatalf("published callback = %#v, want trusted code", result)
	}
}

func TestAuthorizerRejectsIncompleteCallbackHeadersWithinBoundedTime(t *testing.T) {
	fixture := newAuthorizationFixture(
		t,
		[]string{"https://example.test/readonly"},
		successfulTokenTransport(t),
	)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	result := make(chan error, 1)
	go func() { result <- fixture.authorizer.Authorize(ctx) }()

	parsed := parseURL(t, fixture.authorizationURL(t))
	redirect := parseURL(t, parsed.Query().Get("redirect_uri"))
	connection, err := net.Dial("tcp", redirect.Host)
	if err != nil {
		t.Fatalf("dial OAuth callback: %v", err)
	}
	defer connection.Close()
	if _, err := io.WriteString(
		connection,
		"GET "+oauthCallbackPath+" HTTP/1.1\r\nHost: "+redirect.Host+"\r\n",
	); err != nil {
		t.Fatalf("write incomplete OAuth callback headers: %v", err)
	}
	if err := connection.SetReadDeadline(time.Now().Add(7 * time.Second)); err != nil {
		t.Fatalf("set OAuth callback read deadline: %v", err)
	}
	if _, err := io.ReadAll(connection); err != nil {
		t.Fatalf("OAuth callback left incomplete headers open: %v", err)
	}

	fixture.callback(t, redirect, "synthetic-code", parsed.Query().Get("state"))
	if err := <-result; err != nil {
		t.Fatalf("Authorize() error after incomplete callback = %v", err)
	}
}

func TestAuthorizerDoesNotReplaceTokenWithoutRefreshToken(t *testing.T) {
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return jsonResponse(request, `{"access_token":"synthetic-access-token","token_type":"Bearer","expires_in":3600}`), nil
	})
	fixture := newAuthorizationFixture(t, []string{"https://example.test/readonly"}, transport)
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
}

func TestAuthorizerRedactsTokenEndpointResponse(t *testing.T) {
	const secret = "secret-from-token-endpoint"
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusBadRequest, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":"invalid_grant","access_token":"` + secret + `"}`)), Request: request}, nil
	})
	fixture := newAuthorizationFixture(t, []string{"https://example.test/readonly"}, transport)
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

func TestAuthorizerPreservesTokenExchangeCancellation(t *testing.T) {
	for _, testCase := range []struct {
		name string
		err  error
	}{{name: "canceled", err: context.Canceled}, {name: "deadline exceeded", err: context.DeadlineExceeded}} {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newAuthorizationFixture(t, []string{"https://example.test/readonly"}, roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, testCase.err }))
			result := fixture.start(t)
			parsed := parseURL(t, fixture.authorizationURL(t))
			fixture.callback(t, parseURL(t, parsed.Query().Get("redirect_uri")), "synthetic-code", parsed.Query().Get("state"))
			if err := <-result; !errors.Is(err, testCase.err) {
				t.Fatalf("Authorize() error = %v, want errors.Is(_, %v)", err, testCase.err)
			}
		})
	}
}

func TestOAuthCallbackHandlerPublishesOnlyFirstCallback(t *testing.T) {
	results := make(chan oauthCallback, 1)
	handler := oauthCallbackHandler("matching-state", results)
	first := newOAuthCallbackRequest(t, "first-code", "matching-state")
	handler.ServeHTTP(httptest.NewRecorder(), first)
	secondDone := make(chan struct{})
	go func() {
		defer close(secondDone)
		handler.ServeHTTP(httptest.NewRecorder(), newOAuthCallbackRequest(t, "second-code", "matching-state"))
	}()
	select {
	case <-secondDone:
	case <-time.After(time.Second):
		t.Fatal("duplicate OAuth callback blocked its handler")
	}
	if result := <-results; result.code != "first-code" {
		t.Fatalf("published callback code = %q, want first-code", result.code)
	}
}

func TestLoadInstalledOAuthConfigRejectsWebAndMixedCredentials(t *testing.T) {
	for _, contents := range []string{
		`{"web":{"client_id":"web-client","client_secret":"web-secret"}}`,
		`{"web":{"client_secret":"secret-web"},"installed":{"client_id":"installed-client","client_secret":"installed-secret","redirect_uris":["http://127.0.0.1"],"auth_uri":"https://accounts.google.test/auth","token_uri":"https://oauth.google.test/token"}}`,
	} {
		clientFile := filepath.Join(t.TempDir(), "client.json")
		if err := os.WriteFile(clientFile, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := loadInstalledOAuthConfig(clientFile, []string{"https://example.test/readonly"})
		if err == nil {
			t.Fatal("loadInstalledOAuthConfig() error = nil, want credential rejection")
		}
		if strings.Contains(err.Error(), "secret") {
			t.Fatalf("loadInstalledOAuthConfig() disclosed credential contents: %v", err)
		}
	}
}

func TestNewAuthorizedHTTPClientLoadsStoredRefreshTokenWithoutNetwork(t *testing.T) {
	directory := t.TempDir()
	clientFile := writeInstalledClient(t, directory)
	tokenFile := filepath.Join(directory, "token.json")
	if err := os.WriteFile(tokenFile, []byte(`{"access_token":"synthetic-access","token_type":"Bearer","refresh_token":"synthetic-refresh","expiry":"2099-07-21T12:00:00Z"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	client, err := NewAuthorizedHTTPClient(context.Background(), clientFile, tokenFile, []string{"https://example.test/readonly"})
	if err != nil {
		t.Fatalf("NewAuthorizedHTTPClient() error = %v", err)
	}
	if client == nil {
		t.Fatal("NewAuthorizedHTTPClient() = nil")
	}
}

type authorizationFixture struct {
	authorizer *Authorizer
	tokenFile  string
	output     *notifyingBuffer
}

func newAuthorizationFixture(t *testing.T, scopes []string, tokenTransport http.RoundTripper) *authorizationFixture {
	t.Helper()
	directory := t.TempDir()
	clientFile := writeInstalledClient(t, directory)
	tokenFile := filepath.Join(directory, "token.json")
	output := newNotifyingBuffer()
	authorizer := NewAuthorizer(clientFile, tokenFile, scopes, output)
	authorizer.exchangeClient = &http.Client{Transport: tokenTransport}
	return &authorizationFixture{authorizer: authorizer, tokenFile: tokenFile, output: output}
}

func writeInstalledClient(t *testing.T, directory string) string {
	t.Helper()
	clientFile := filepath.Join(directory, "client.json")
	contents := `{"installed":{"client_id":"synthetic-client-id","client_secret":"synthetic-client-secret","redirect_uris":["http://127.0.0.1"],"auth_uri":"https://accounts.google.test/auth","token_uri":"https://oauth.google.test/token"}}`
	if err := os.WriteFile(clientFile, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return clientFile
}

func (fixture *authorizationFixture) start(t *testing.T) <-chan error {
	t.Helper()
	result := make(chan error, 1)
	go func() { result <- fixture.authorizer.Authorize(context.Background()) }()
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

func (fixture *authorizationFixture) callback(t *testing.T, redirect *url.URL, code, state string) {
	t.Helper()
	fixture.callbackExpectingStatus(t, redirect, code, state, http.StatusOK)
}

func (fixture *authorizationFixture) callbackExpectingStatus(t *testing.T, redirect *url.URL, code, state string, wantStatus int) {
	t.Helper()
	query := redirect.Query()
	query.Set("code", code)
	query.Set("state", state)
	redirect.RawQuery = query.Encode()
	response, err := http.Get(redirect.String())
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
		return jsonResponse(request, `{"access_token":"synthetic-access-token","refresh_token":"synthetic-refresh-token","token_type":"Bearer","expires_in":3600}`), nil
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

func newOAuthCallbackRequest(t *testing.T, code, state string) *http.Request {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, "http://127.0.0.1"+oauthCallbackPath+"?code="+url.QueryEscape(code)+"&state="+url.QueryEscape(state), nil)
	if err != nil {
		t.Fatal(err)
	}
	return request
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

type roundTripFunc func(*http.Request) (*http.Response, error)

func (roundTrip roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

func jsonResponse(request *http.Request, body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: request}
}
