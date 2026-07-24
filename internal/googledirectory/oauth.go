// Package googledirectory contains Google Workspace directory boundaries.
package googledirectory

import (
	"context"
	"io"
	"net/http"

	"stacks/internal/googleauth"
)

// ReadOnlyScope allows read-only Google Workspace directory access.
const ReadOnlyScope = "https://www.googleapis.com/auth/directory.readonly"

var readOnlyScopes = []string{ReadOnlyScope}

// NewAuthorizer constructs an installed-application directory authorizer with
// no Drive or Docs scope.
func NewAuthorizer(clientFile, tokenFile string, output io.Writer) *googleauth.Authorizer {
	return googleauth.NewAuthorizer(clientFile, tokenFile, readOnlyScopes, output)
}

// NewAuthorizedHTTPClient loads the owner-only directory authorization token.
func NewAuthorizedHTTPClient(ctx context.Context, clientFile, tokenFile string) (*http.Client, error) {
	return googleauth.NewAuthorizedHTTPClient(ctx, clientFile, tokenFile, readOnlyScopes)
}
