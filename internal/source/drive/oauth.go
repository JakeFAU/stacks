package drive

import (
	"context"
	"io"
	"net/http"

	"google.golang.org/api/docs/v1"
	googledrive "google.golang.org/api/drive/v3"

	"stacks/internal/googleauth"
)

var googleReadOnlyScopes = []string{
	googledrive.DriveReadonlyScope,
	docs.DocumentsReadonlyScope,
}

// NewAuthorizer constructs the existing Drive and Docs installed-application
// authorizer with only the Drive and Docs read-only scopes.
func NewAuthorizer(clientFile, tokenFile string, output io.Writer) *googleauth.Authorizer {
	return googleauth.NewAuthorizer(clientFile, tokenFile, googleReadOnlyScopes, output)
}

// NewAuthorizedHTTPClient loads the configured installed-application client
// and owner-only refresh token for non-interactive Drive and Docs commands.
func NewAuthorizedHTTPClient(ctx context.Context, clientFile, tokenFile string) (*http.Client, error) {
	return googleauth.NewAuthorizedHTTPClient(ctx, clientFile, tokenFile, googleReadOnlyScopes)
}
