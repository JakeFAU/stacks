package googledirectory

import (
	"context"
	"errors"

	"golang.org/x/oauth2"

	"stacks/internal/googleauth"
)

// Probe validates the locally configured directory-only OAuth authorization
// without querying or enumerating Google People.
type Probe struct {
	clientFile     string
	tokenFile      string
	scopes         []string
	newTokenSource func(context.Context, string, string, []string) (oauth2.TokenSource, error)
}

// NewProbe constructs a non-enumerating directory authorization probe.
func NewProbe(clientFile, tokenFile string) *Probe {
	return &Probe{
		clientFile:     clientFile,
		tokenFile:      tokenFile,
		scopes:         append([]string(nil), readOnlyScopes...),
		newTokenSource: newAuthorizedTokenSource,
	}
}

// CheckAuthorization validates the installed OAuth configuration and token
// source, including an in-memory refresh when the stored access token requires
// one. It never performs a People API request.
func (probe *Probe) CheckAuthorization(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if probe == nil ||
		probe.newTokenSource == nil ||
		len(probe.scopes) != 1 ||
		probe.scopes[0] != ReadOnlyScope {
		return errors.New("check Google directory authorization: probe is not configured")
	}
	tokenSource, err := probe.newTokenSource(
		ctx,
		probe.clientFile,
		probe.tokenFile,
		probe.scopes,
	)
	if cancellationErr := probeCancellation(ctx, err); cancellationErr != nil {
		return cancellationErr
	}
	if err != nil || tokenSource == nil {
		return errors.New("check Google directory authorization: local OAuth configuration is unavailable")
	}
	token, err := tokenSource.Token()
	if cancellationErr := probeCancellation(ctx, err); cancellationErr != nil {
		return cancellationErr
	}
	if err != nil || token == nil || !token.Valid() {
		return errors.New("check Google directory authorization: token is unavailable or invalid")
	}
	return nil
}

func newAuthorizedTokenSource(
	ctx context.Context,
	clientFile string,
	tokenFile string,
	scopes []string,
) (oauth2.TokenSource, error) {
	client, err := googleauth.NewAuthorizedHTTPClient(ctx, clientFile, tokenFile, scopes)
	if err != nil {
		return nil, err
	}
	transport, ok := client.Transport.(*oauth2.Transport)
	if !ok || transport.Source == nil {
		return nil, errors.New("construct Google OAuth token source")
	}
	return transport.Source, nil
}

func probeCancellation(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return nil
}
