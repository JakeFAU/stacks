package googledirectory

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/oauth2"
)

func TestProbeChecksOnlyDirectoryTokenReadinessWithExactScope(t *testing.T) {
	tokenSource := &recordingTokenSource{
		token: &oauth2.Token{AccessToken: "synthetic-access-token"},
	}
	probe := NewProbe("/synthetic/client.json", "/synthetic/token.json")
	var gotScopes []string
	probe.newTokenSource = func(
		_ context.Context,
		clientFile string,
		tokenFile string,
		scopes []string,
	) (oauth2.TokenSource, error) {
		if clientFile != "/synthetic/client.json" ||
			tokenFile != "/synthetic/token.json" {
			t.Fatal("probe did not preserve its configured OAuth paths")
		}
		gotScopes = append([]string(nil), scopes...)
		return tokenSource, nil
	}

	if err := probe.CheckAuthorization(context.Background()); err != nil {
		t.Fatalf("CheckAuthorization() error = %v", err)
	}
	if !reflect.DeepEqual(gotScopes, []string{ReadOnlyScope}) {
		t.Fatalf("OAuth scopes = %#v, want only directory readonly", gotScopes)
	}
	if tokenSource.calls != 1 {
		t.Fatalf("Token() calls = %d, want 1", tokenSource.calls)
	}
}

func TestProbeBoundsTokenFailuresAndPreservesCancellation(t *testing.T) {
	const privateTokenMarker = "synthetic-private-token-marker"
	for _, testCase := range []struct {
		name    string
		ctx     func(*testing.T) context.Context
		err     error
		wantErr error
	}{
		{
			name: "provider failure",
			ctx:  func(*testing.T) context.Context { return context.Background() },
			err:  errors.New(privateTokenMarker),
		},
		{
			name: "canceled",
			ctx: func(*testing.T) context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			},
			err:     errors.New(privateTokenMarker),
			wantErr: context.Canceled,
		},
		{
			name: "deadline exceeded",
			ctx: func(t *testing.T) context.Context {
				ctx, cancel := context.WithTimeout(context.Background(), 0)
				t.Cleanup(cancel)
				return ctx
			},
			err:     context.DeadlineExceeded,
			wantErr: context.DeadlineExceeded,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			probe := NewProbe("/synthetic/client.json", "/synthetic/token.json")
			probe.newTokenSource = func(
				context.Context,
				string,
				string,
				[]string,
			) (oauth2.TokenSource, error) {
				return &recordingTokenSource{err: testCase.err}, nil
			}

			err := probe.CheckAuthorization(testCase.ctx(t))
			if testCase.wantErr != nil {
				if !errors.Is(err, testCase.wantErr) {
					t.Fatalf("CheckAuthorization() error = %v, want %v", err, testCase.wantErr)
				}
				return
			}
			if err == nil {
				t.Fatal("CheckAuthorization() error = nil, want bounded readiness failure")
			}
			if strings.Contains(err.Error(), privateTokenMarker) {
				t.Fatalf("CheckAuthorization() disclosed token failure: %v", err)
			}
		})
	}
}

func TestProbeDoesNotRequestATokenWhenAuthorizationConstructionFails(t *testing.T) {
	const privateConfigurationMarker = "synthetic-private-configuration-marker"
	probe := NewProbe("/synthetic/client.json", "/synthetic/token.json")
	probe.newTokenSource = func(
		context.Context,
		string,
		string,
		[]string,
	) (oauth2.TokenSource, error) {
		return nil, errors.New(privateConfigurationMarker)
	}

	err := probe.CheckAuthorization(context.Background())

	if err == nil {
		t.Fatal("CheckAuthorization() error = nil, want bounded construction failure")
	}
	if strings.Contains(err.Error(), privateConfigurationMarker) {
		t.Fatalf("CheckAuthorization() disclosed authorization construction failure: %v", err)
	}
}

type recordingTokenSource struct {
	token *oauth2.Token
	err   error
	calls int
}

func (source *recordingTokenSource) Token() (*oauth2.Token, error) {
	source.calls++
	return source.token, source.err
}
