package drive

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/docs/v1"
	googledrive "google.golang.org/api/drive/v3"
)

const (
	oauthCallbackPath            = "/oauth2/callback"
	oauthStateBytes              = 32
	oauthCallbackShutdownTimeout = 5 * time.Second
)

var googleReadOnlyScopes = []string{
	googledrive.DriveReadonlyScope,
	docs.DocumentsReadonlyScope,
}

// Authorizer performs Google's installed-application OAuth flow and replaces
// the configured local token file atomically.
type Authorizer struct {
	clientFile     string
	tokenFile      string
	output         io.Writer
	listen         func(network, address string) (net.Listener, error)
	exchangeClient *http.Client
}

// NewAuthorizer constructs an installed-application Google authorizer.
func NewAuthorizer(clientFile, tokenFile string, output io.Writer) *Authorizer {
	return &Authorizer{
		clientFile: clientFile,
		tokenFile:  tokenFile,
		output:     output,
		listen:     net.Listen,
	}
}

// Authorize waits for one loopback callback, exchanges its matching code, and
// stores the resulting token without printing credentials.
func (authorizer *Authorizer) Authorize(ctx context.Context) error {
	config, err := loadInstalledOAuthConfig(authorizer.clientFile)
	if err != nil {
		return err
	}
	state, err := generateOAuthState()
	if err != nil {
		return errors.New("generate Google OAuth state")
	}

	listener, err := authorizer.listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listen for Google OAuth callback: %w", err)
	}
	defer listener.Close()
	if err := requireLoopbackAddress(listener.Addr()); err != nil {
		return err
	}
	config.RedirectURL = "http://" + listener.Addr().String() + oauthCallbackPath

	callbackResult := make(chan oauthCallback, 1)
	serverErrors := make(chan error, 1)
	server := &http.Server{Handler: oauthCallbackHandler(state, callbackResult)}
	defer server.Close()
	go func() {
		if serveErr := server.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) && !errors.Is(serveErr, net.ErrClosed) {
			serverErrors <- serveErr
		}
	}()

	authorizationURL := config.AuthCodeURL(
		state,
		oauth2.AccessTypeOffline,
		oauth2.SetAuthURLParam("prompt", "consent"),
	)
	if _, err := fmt.Fprintf(authorizer.output, "Open this URL to authorize Stacks: %s\n", authorizationURL); err != nil {
		return fmt.Errorf("print Google authorization URL: %w", err)
	}

	var callback oauthCallback
	select {
	case <-ctx.Done():
		return fmt.Errorf("wait for Google OAuth callback: %w", ctx.Err())
	case err := <-serverErrors:
		return fmt.Errorf("serve Google OAuth callback: %w", err)
	case callback = <-callbackResult:
	}
	shutdownContext, cancelShutdown := context.WithTimeout(context.WithoutCancel(ctx), oauthCallbackShutdownTimeout)
	shutdownErr := server.Shutdown(shutdownContext)
	cancelShutdown()
	if shutdownErr != nil {
		return fmt.Errorf("shut down Google OAuth callback: %w", shutdownErr)
	}
	if callback.err != nil {
		return callback.err
	}

	exchangeContext := ctx
	if authorizer.exchangeClient != nil {
		exchangeContext = context.WithValue(ctx, oauth2.HTTPClient, authorizer.exchangeClient)
	}
	token, err := config.Exchange(exchangeContext, callback.code)
	if err != nil {
		return errors.New("exchange Google authorization code: token endpoint rejected authorization")
	}
	if token.RefreshToken == "" {
		return errors.New("exchange Google authorization code: refresh token is missing")
	}
	if err := writeTokenAtomic(authorizer.tokenFile, token); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(authorizer.output, "Google authorization saved."); err != nil {
		return fmt.Errorf("print Google authorization result: %w", err)
	}
	return nil
}

type oauthCallback struct {
	code string
	err  error
}

func oauthCallbackHandler(wantState string, result chan<- oauthCallback) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != oauthCallbackPath {
			http.NotFound(writer, request)
			return
		}
		if request.URL.Query().Get("state") != wantState {
			http.Error(writer, "Google OAuth state mismatch", http.StatusBadRequest)
			result <- oauthCallback{err: errors.New("google OAuth state mismatch")}
			return
		}
		if request.URL.Query().Get("error") != "" {
			http.Error(writer, "Google authorization was rejected", http.StatusBadRequest)
			result <- oauthCallback{err: errors.New("google authorization was rejected")}
			return
		}
		code := request.URL.Query().Get("code")
		if code == "" {
			http.Error(writer, "Google authorization code is missing", http.StatusBadRequest)
			result <- oauthCallback{err: errors.New("google authorization code is missing")}
			return
		}
		writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(writer, "Authorization received. You may close this window.\n")
		result <- oauthCallback{code: code}
	})
}

func loadInstalledOAuthConfig(path string) (*oauth2.Config, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read Google OAuth client configuration: %w", err)
	}
	var envelope struct {
		Installed json.RawMessage `json:"installed"`
	}
	if err := json.Unmarshal(contents, &envelope); err != nil || len(envelope.Installed) == 0 {
		return nil, errors.New("parse Google OAuth client configuration: installed application credentials are required")
	}
	config, err := google.ConfigFromJSON(contents, googleReadOnlyScopes...)
	if err != nil {
		return nil, errors.New("parse Google OAuth client configuration")
	}
	return config, nil
}

func generateOAuthState() (string, error) {
	state := make([]byte, oauthStateBytes)
	if _, err := io.ReadFull(rand.Reader, state); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(state), nil
}

func requireLoopbackAddress(address net.Addr) error {
	host, _, err := net.SplitHostPort(address.String())
	if err != nil || host != "127.0.0.1" {
		return errors.New("google OAuth callback listener must use 127.0.0.1")
	}
	return nil
}

func writeTokenAtomic(path string, token *oauth2.Token) error {
	return replaceTokenFile(path, token, os.Rename)
}

func replaceTokenFile(path string, token *oauth2.Token, rename func(oldPath, newPath string) error) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create Google OAuth token directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".stacks-google-token-*")
	if err != nil {
		return fmt.Errorf("create temporary Google OAuth token file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}()

	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("secure temporary Google OAuth token file: %w", err)
	}
	if err := json.NewEncoder(temporary).Encode(token); err != nil {
		return errors.New("encode Google OAuth token")
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync temporary Google OAuth token file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary Google OAuth token file: %w", err)
	}
	if err := rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace Google OAuth token file: %w", err)
	}
	return nil
}
