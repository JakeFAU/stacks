package doctor

import (
	"context"
	"errors"
	"net/http"
	"testing"

	anthropicoption "github.com/anthropics/anthropic-sdk-go/option"
)

const (
	testAnthropicKey       = "synthetic-anthropic-key"
	testAnthropicModel     = "synthetic-anthropic-model"
	testAnthropicPrivate   = "PRIVATE ANTHROPIC BODY"
	testAnthropicRequestID = "PRIVATE ANTHROPIC REQUEST ID"
)

func TestAnthropicProbeUsesMetadataEndpointOnceAndCachesSuccess(t *testing.T) {
	transport := &modelMetadataTransport{status: http.StatusOK, body: `{"id":"synthetic-anthropic-model","capabilities":{},"created_at":"2026-01-01T00:00:00Z","display_name":"Synthetic","max_input_tokens":1,"max_tokens":1,"type":"model"}`}
	probe := newAnthropicProbe(testAnthropicKey, testAnthropicModel, anthropicoption.WithHTTPClient(&http.Client{Transport: transport}))

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
	if request.method != http.MethodGet || request.url != "https://api.anthropic.com/v1/models/"+testAnthropicModel || request.apiKey != testAnthropicKey || request.authorization != "" {
		t.Fatalf("metadata request = %#v", request)
	}
}

func TestAnthropicProbeBoundsFailuresAndDisablesSDKRetries(t *testing.T) {
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
			transport := &modelMetadataTransport{status: testCase.status, body: `{"type":"error","error":{"type":"api_error","message":"` + testAnthropicPrivate + `"}}`, requestID: testAnthropicRequestID}
			probe := newAnthropicProbe(testAnthropicKey, testAnthropicModel, anthropicoption.WithHTTPClient(&http.Client{Transport: transport}))
			err := probe.CheckModel(context.Background())
			if !errors.Is(err, testCase.want) {
				t.Fatalf("CheckModel() error = %v, want %v", err, testCase.want)
			}
			if len(transport.requests) != 1 {
				t.Fatalf("metadata requests = %d, want one with SDK retries disabled", len(transport.requests))
			}
			assertBoundedProviderError(t, err, testAnthropicPrivate, testAnthropicRequestID, testAnthropicKey)
		})
	}
}

func TestAnthropicProbePreservesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := newAnthropicProbe(testAnthropicKey, testAnthropicModel).CheckCredentials(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("CheckCredentials() error = %v, want context.Canceled", err)
	}
	assertBoundedProviderError(t, err, testAnthropicKey)
}
