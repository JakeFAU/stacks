package doctor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	openaioption "github.com/openai/openai-go/v3/option"
)

const (
	testOpenAIKey       = "synthetic-openai-key"
	testOpenAIModel     = "synthetic-openai-model"
	testOpenAIPrivate   = "PRIVATE OPENAI BODY"
	testOpenAIRequestID = "PRIVATE OPENAI REQUEST ID"
)

func TestOpenAIProbeUsesMetadataEndpointOnceAndCachesSuccess(t *testing.T) {
	transport := &modelMetadataTransport{status: http.StatusOK, body: `{"id":"synthetic-openai-model","created":1,"object":"model","owned_by":"synthetic"}`}
	probe := newOpenAIProbe(testOpenAIKey, testOpenAIModel, openaioption.WithHTTPClient(&http.Client{Transport: transport}))

	if err := probe.CheckCredentials(context.Background()); err != nil {
		t.Fatalf("CheckCredentials() error = %v", err)
	}
	if err := probe.CheckModel(context.Background()); err != nil {
		t.Fatalf("CheckModel() error = %v", err)
	}
	if len(transport.requests) != 1 {
		t.Fatalf("metadata requests = %d, want one", len(transport.requests))
	}
	request := transport.requests[0]
	if request.method != http.MethodGet || request.url != "https://api.openai.com/v1/models/"+testOpenAIModel || request.authorization != "Bearer "+testOpenAIKey {
		t.Fatalf("metadata request = %#v", request)
	}
}

func TestOpenAIProbeBoundsFailuresAndDisablesSDKRetries(t *testing.T) {
	tests := []struct {
		status int
		want   error
	}{
		{status: http.StatusUnauthorized, want: ErrModelAuthentication},
		{status: http.StatusForbidden, want: ErrModelAuthorization},
		{status: http.StatusNotFound, want: ErrModelNotFound},
		{status: http.StatusTooManyRequests, want: ErrModelUnavailable},
		{status: http.StatusServiceUnavailable, want: ErrModelUnavailable},
	}
	for _, testCase := range tests {
		t.Run(http.StatusText(testCase.status), func(t *testing.T) {
			transport := &modelMetadataTransport{status: testCase.status, body: `{"error":{"message":"` + testOpenAIPrivate + `"}}`, requestID: testOpenAIRequestID}
			probe := newOpenAIProbe(testOpenAIKey, testOpenAIModel, openaioption.WithHTTPClient(&http.Client{Transport: transport}))
			err := probe.CheckCredentials(context.Background())
			if !errors.Is(err, testCase.want) {
				t.Fatalf("CheckCredentials() error = %v, want %v", err, testCase.want)
			}
			if len(transport.requests) != 1 {
				t.Fatalf("metadata requests = %d, want one with SDK retries disabled", len(transport.requests))
			}
			assertBoundedProviderError(t, err, testOpenAIPrivate, testOpenAIRequestID, testOpenAIKey)
		})
	}
}

func TestOpenAIProbePreservesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := newOpenAIProbe(testOpenAIKey, testOpenAIModel).CheckModel(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("CheckModel() error = %v, want context.Canceled", err)
	}
	assertBoundedProviderError(t, err, testOpenAIKey)
}

type metadataRequest struct {
	method        string
	url           string
	authorization string
	apiKey        string
}

type modelMetadataTransport struct {
	status    int
	body      string
	requestID string
	requests  []metadataRequest
}

func (transport *modelMetadataTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	transport.requests = append(transport.requests, metadataRequest{
		method: request.Method, url: request.URL.String(), authorization: request.Header.Get("Authorization"), apiKey: request.Header.Get("X-Api-Key"),
	})
	header := http.Header{"Content-Type": []string{"application/json"}}
	if transport.requestID != "" {
		header.Set("request-id", transport.requestID)
		header.Set("x-request-id", transport.requestID)
	}
	return &http.Response{
		StatusCode: transport.status,
		Status:     fmt.Sprintf("%d %s", transport.status, http.StatusText(transport.status)),
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(transport.body)),
		Request:    request,
	}, nil
}

func assertBoundedProviderError(t *testing.T, err error, privateValues ...string) {
	t.Helper()
	if err == nil || len(err.Error()) > 160 {
		t.Fatalf("error = %q, want non-empty bounded error", err)
	}
	for _, privateValue := range privateValues {
		if strings.Contains(err.Error(), privateValue) {
			t.Fatalf("error leaked private provider value %q: %v", privateValue, err)
		}
	}
}
