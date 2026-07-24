package openai

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	openaisdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"stacks/internal/extract"
	"stacks/internal/modelpolicy"
	"stacks/internal/modeltelemetry"
)

const (
	testAPIKey        = "synthetic-openai-key"
	testModelID       = "synthetic-openai-model"
	testPrivateInput  = "PRIVATE REQUEST INPUT"
	testPrivateOutput = "PRIVATE RESPONSE OUTPUT"
	testPrivateError  = "PRIVATE PROVIDER ERROR BODY"
)

func TestNewRequiresExplicitValidatedConfiguration(t *testing.T) {
	tests := []struct {
		name    string
		apiKey  string
		options Options
	}{
		{name: "missing API key", options: validOptions()},
		{name: "padded API key", apiKey: " padded-key ", options: validOptions()},
		{name: "missing model", apiKey: testAPIKey, options: optionsWith(func(options *Options) { options.ModelID = "" })},
		{name: "padded model", apiKey: testAPIKey, options: optionsWith(func(options *Options) { options.ModelID = " padded-model " })},
		{name: "invalid data mode", apiKey: testAPIKey, options: optionsWith(func(options *Options) { options.DataMode = modelpolicy.DataModeLegacy })},
		{name: "zero tokens", apiKey: testAPIKey, options: optionsWith(func(options *Options) { options.MaxOutputTokens = 0 })},
		{name: "zero attempts", apiKey: testAPIKey, options: optionsWith(func(options *Options) { options.MaxAttempts = 0 })},
		{name: "too many attempts", apiKey: testAPIKey, options: optionsWith(func(options *Options) { options.MaxAttempts = MaxAttemptsLimit + 1 })},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := New(test.apiKey, test.options); err == nil {
				t.Fatal("New() error = nil")
			}
		})
	}
}

func TestGenerateRejectsUnknownMutatedOrMalformedRequestBeforeProvider(t *testing.T) {
	tests := map[string]func(*extract.Request){
		"unknown version": func(request *extract.Request) { request.PromptVersion = "unknown-v1" },
		"mutated prompt":  func(request *extract.Request) { request.SystemPrompt = "mutated" },
		"mutated schema name": func(request *extract.Request) {
			request.SchemaName = "mutated_schema"
		},
		"mutated schema": func(request *extract.Request) { request.JSONSchema = []byte(`{}`) },
		"malformed schema": func(request *extract.Request) {
			request.JSONSchema = []byte(`not-json`)
		},
		"empty input": func(request *extract.Request) { request.Input = "" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			api := &fakeResponsesAPI{}
			recorder := &recordingInvocationRecorder{}
			client := newTestClient(t, api, recorder, 3)
			request := validRequest()
			mutate(&request)

			_, err := client.Generate(context.Background(), request)
			if !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("Generate() error = %v, want invalid request", err)
			}
			if len(api.params) != 0 {
				t.Fatalf("Responses calls = %d, want 0", len(api.params))
			}
			assertOneObservation(t, recorder, OutcomeInvalidRequest, 0)
		})
	}
}

func TestGenerateSendsOnlyStrictStatelessStructuredResponseRequest(t *testing.T) {
	api := &fakeResponsesAPI{outputs: []*responses.Response{successfulResponse(t, `{}`)}}
	client := newTestClient(t, api, &recordingInvocationRecorder{}, 3)
	request := validRequest()

	if _, err := client.Generate(context.Background(), request); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if len(api.params) != 1 || api.optionCounts[0] != 0 {
		t.Fatalf("Responses calls/options = %d/%d, want 1/0", len(api.params), api.optionCounts[0])
	}
	params := api.params[0]
	if !params.Instructions.Valid() || params.Instructions.Value != request.SystemPrompt {
		t.Fatalf("Instructions = %#v", params.Instructions)
	}
	if !params.Input.OfString.Valid() || params.Input.OfString.Value != testPrivateInput || len(params.Input.OfInputItemList) != 0 {
		t.Fatalf("Input = %#v", params.Input)
	}
	if params.Model != responses.ResponsesModel(testModelID) {
		t.Fatalf("Model = %q", params.Model)
	}
	if !params.MaxOutputTokens.Valid() || params.MaxOutputTokens.Value != 321 {
		t.Fatalf("MaxOutputTokens = %#v", params.MaxOutputTokens)
	}
	if !params.Store.Valid() || params.Store.Value || !params.Background.Valid() || params.Background.Value {
		t.Fatalf("Store/Background = %#v/%#v, want explicit false", params.Store, params.Background)
	}
	if params.Reasoning.Effort != shared.ReasoningEffortNone {
		t.Fatalf("Reasoning.Effort = %q, want none", params.Reasoning.Effort)
	}
	format := params.Text.Format.OfJSONSchema
	if format == nil || format.Name != request.SchemaName || !format.Strict.Valid() || !format.Strict.Value {
		t.Fatalf("Text.Format = %#v, want strict JSON Schema", params.Text.Format)
	}
	var schema map[string]any
	if err := json.Unmarshal(request.JSONSchema, &schema); err != nil {
		t.Fatalf("decode expected schema: %v", err)
	}
	if !reflect.DeepEqual(format.Schema, schema) {
		t.Fatal("submitted schema differs semantically from reviewed schema")
	}

	encoded, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	assertExactRequestJSON(t, encoded, request)
}

func TestNewFixesProductionRoutingAndDisablesSDKRetries(t *testing.T) {
	t.Setenv("OPENAI_BASE_URL", "https://hostile.invalid/v1")
	t.Setenv("OPENAI_API_KEY", "hostile-ambient-key")
	t.Setenv("OPENAI_ORG_ID", "hostile-ambient-organization")
	t.Setenv("OPENAI_PROJECT_ID", "hostile-ambient-project")

	transport := &recordingRoundTripper{}
	originalClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: transport}
	t.Cleanup(func() { http.DefaultClient = originalClient })

	recorder := &recordingInvocationRecorder{}
	client, err := New(testAPIKey, Options{
		ModelID: testModelID, DataMode: modelpolicy.DataModePersonal,
		MaxOutputTokens: 321, MaxAttempts: 2, Recorder: recorder,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	client.wait = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }

	request := validRequest()
	if _, err := client.Generate(context.Background(), request); err != nil {
		t.Fatalf("Generate() error = %v after %d HTTP requests", err, len(transport.requests))
	}
	if len(transport.requests) != 2 {
		t.Fatalf("HTTP requests = %d, want exactly two adapter attempts with zero SDK retries", len(transport.requests))
	}
	for index, recorded := range transport.requests {
		if recorded.url != "https://api.openai.com/v1/responses" {
			t.Fatalf("request %d URL = %q", index, recorded.url)
		}
		if recorded.authorization != "Bearer "+testAPIKey || recorded.organization != "" || recorded.project != "" {
			t.Fatalf("request %d routing headers = authorization %q, organization %q, project %q", index, recorded.authorization, recorded.organization, recorded.project)
		}
		assertExactRequestJSON(t, recorded.body, request)
	}
	assertOneObservation(t, recorder, OutcomeSuccess, 2)
}

func TestGenerateAcceptsOneCompletedJSONTextResult(t *testing.T) {
	recorder := &recordingInvocationRecorder{}
	client := newTestClient(t, &fakeResponsesAPI{outputs: []*responses.Response{successfulResponse(t, `{"answer":true}`)}}, recorder, 3)

	response, err := client.Generate(context.Background(), validRequest())
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if string(response.Output) != `{"answer":true}` || response.ModelID != testModelID || response.PromptVersion != extract.ExtractionPromptVersion || response.Outcome != OutcomeSuccess {
		t.Fatalf("Generate() response = %+v", response)
	}
	if response.Usage != (extract.Usage{InputTokens: 11, OutputTokens: 7, TotalTokens: 18}) || response.Latency != 0 {
		t.Fatalf("Generate() metadata = %+v", response)
	}
	assertOneObservation(t, recorder, OutcomeSuccess, 1)
}

func TestGenerateAcceptsCompletedReasoningItemBeforeSingleJSONMessage(t *testing.T) {
	output := decodedResponse(t, responseJSON(testModelID, "completed", `[
		{"type":"reasoning","status":"completed","summary":[]},
		{"type":"message","status":"completed","content":[{"type":"output_text","text":"{\"answer\":true}"}]}
	]`, validUsageJSON))
	client := newTestClient(t, &fakeResponsesAPI{outputs: []*responses.Response{output}}, &recordingInvocationRecorder{}, 1)

	response, err := client.Generate(context.Background(), validRequest())
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if string(response.Output) != `{"answer":true}` {
		t.Fatalf("Generate() output = %q", response.Output)
	}
}

func TestGenerateRejectsEveryNonCanonicalResponseShape(t *testing.T) {
	tests := map[string]string{
		"malformed JSON": responseJSON(testModelID, "completed", `[{
			"type":"message","status":"completed","content":[{"type":"output_text","text":"not-json"}]
		}]`, validUsageJSON),
		"refusal": responseJSON(testModelID, "completed", `[{
			"type":"message","status":"completed","content":[{"type":"refusal","refusal":"`+testPrivateOutput+`"}]
		}]`, validUsageJSON),
		"incomplete response": responseJSON(testModelID, "incomplete", `[{
			"type":"message","status":"completed","content":[{"type":"output_text","text":"{}"}]
		}]`, validUsageJSON),
		"incomplete message": responseJSON(testModelID, "completed", `[{
			"type":"message","status":"incomplete","content":[{"type":"output_text","text":"{}"}]
		}]`, validUsageJSON),
		"multiple output items": responseJSON(testModelID, "completed", `[
			{"type":"message","status":"completed","content":[{"type":"output_text","text":"{}"}]},
			{"type":"message","status":"completed","content":[{"type":"output_text","text":"{}"}]}
		]`, validUsageJSON),
		"non-message output": responseJSON(testModelID, "completed", `[
			{"type":"reasoning","status":"completed"}
		]`, validUsageJSON),
		"multiple content blocks": responseJSON(testModelID, "completed", `[{
			"type":"message","status":"completed","content":[
				{"type":"output_text","text":"{}"},{"type":"output_text","text":"{}"}
			]
		}]`, validUsageJSON),
		"missing usage": responseJSON(testModelID, "completed", `[{
			"type":"message","status":"completed","content":[{"type":"output_text","text":"{}"}]
		}]`, ""),
		"negative usage": responseJSON(testModelID, "completed", `[{
			"type":"message","status":"completed","content":[{"type":"output_text","text":"{}"}]
		}]`, `{"input_tokens":11,"output_tokens":-1,"total_tokens":10}`),
		"returned model mismatch": responseJSON("different-model", "completed", `[{
			"type":"message","status":"completed","content":[{"type":"output_text","text":"{}"}]
		}]`, validUsageJSON),
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			api := &fakeResponsesAPI{outputs: []*responses.Response{decodedResponse(t, body)}}
			client := newTestClient(t, api, &recordingInvocationRecorder{}, 3)
			_, err := client.Generate(context.Background(), validRequest())
			if !errors.Is(err, ErrInvalidOutput) {
				t.Fatalf("Generate() error = %v, want invalid output", err)
			}
			if strings.Contains(err.Error(), testPrivateOutput) {
				t.Fatalf("Generate() error leaks response: %v", err)
			}
			if len(api.params) != 1 {
				t.Fatalf("Responses calls = %d, want terminal single attempt", len(api.params))
			}
		})
	}
}

func TestGenerateRetriesOnlyRetryableHTTPStatusesToExactBound(t *testing.T) {
	for _, status := range []int{http.StatusRequestTimeout, http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout, 599} {
		name := http.StatusText(status)
		if name == "" {
			name = fmt.Sprintf("status %d", status)
		}
		t.Run(name, func(t *testing.T) {
			providerErr := syntheticAPIError(t, status)
			api := &fakeResponsesAPI{errors: []error{providerErr, providerErr, providerErr, providerErr}}
			recorder := &recordingInvocationRecorder{}
			client := newTestClient(t, api, recorder, 3)

			_, err := client.Generate(context.Background(), validRequest())
			if !errors.Is(err, ErrInvocation) {
				t.Fatalf("Generate() error = %v, want invocation failure", err)
			}
			if len(api.params) != 3 {
				t.Fatalf("Responses calls = %d, want exact bound 3", len(api.params))
			}
			if strings.Contains(err.Error(), testPrivateError) || strings.Contains(err.Error(), testPrivateInput) || strings.Contains(err.Error(), testAPIKey) {
				t.Fatalf("Generate() error leaks private provider data: %v", err)
			}
			assertOneObservation(t, recorder, expectedHTTPOutcome(status), 3)
		})
	}
}

func TestGenerateRetriesOnlyExplicitTransientTransportFailures(t *testing.T) {
	tests := []struct {
		name         string
		transportErr error
		wantAttempts int
		wantOutcome  string
		wantSuccess  bool
	}{
		{
			name: "connection reset",
			transportErr: &url.Error{Op: "Post", URL: "https://api.openai.com/v1/responses", Err: &net.OpError{
				Op: "read", Net: "tcp", Err: syscall.ECONNRESET,
			}},
			wantAttempts: 2, wantOutcome: OutcomeSuccess, wantSuccess: true,
		},
		{
			name: "live context connection timeout",
			transportErr: &url.Error{Op: "Post", URL: "https://api.openai.com/v1/responses", Err: &net.OpError{
				Op: "dial", Net: "tcp", Err: syntheticTimeoutError{},
			}},
			wantAttempts: 2, wantOutcome: OutcomeSuccess, wantSuccess: true,
		},
		{
			name:         "permanent TLS certificate validation",
			transportErr: &url.Error{Op: "Post", URL: "https://api.openai.com/v1/responses", Err: x509.UnknownAuthorityError{}},
			wantAttempts: 1, wantOutcome: OutcomeProviderError,
		},
		{
			name: "permanent DNS failure",
			transportErr: &url.Error{Op: "Post", URL: "https://api.openai.com/v1/responses", Err: &net.DNSError{
				Err: "no such host", Name: "api.openai.com", IsNotFound: true,
			}},
			wantAttempts: 1, wantOutcome: OutcomeProviderError,
		},
		{
			name: "timed out DNS failure",
			transportErr: &url.Error{Op: "Post", URL: "https://api.openai.com/v1/responses", Err: &net.DNSError{
				Err: "i/o timeout", Name: "api.openai.com", IsTimeout: true,
			}},
			wantAttempts: 1, wantOutcome: OutcomeTimeout,
		},
		{
			name:         "TLS handshake timeout",
			transportErr: &url.Error{Op: "Post", URL: "https://api.openai.com/v1/responses", Err: syntheticTLSHandshakeTimeoutError{}},
			wantAttempts: 1, wantOutcome: OutcomeTimeout,
		},
		{
			name:         "redirect policy failure",
			transportErr: &url.Error{Op: "Get", URL: "https://api.openai.com/v1/responses", Err: errors.New("stopped after redirects")},
			wantAttempts: 1, wantOutcome: OutcomeProviderError,
		},
		{
			name:         "non-transport provider deadline",
			transportErr: fmt.Errorf("provider wrapper: %w", context.DeadlineExceeded),
			wantAttempts: 1, wantOutcome: OutcomeTimeout,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := &recordingInvocationRecorder{}
			api := &fakeResponsesAPI{
				errors:  []error{test.transportErr, nil},
				outputs: []*responses.Response{nil, successfulResponse(t, `{}`)},
			}
			client := newTestClient(t, api, recorder, 3)

			_, err := client.Generate(context.Background(), validRequest())
			if test.wantSuccess && err != nil {
				t.Fatalf("Generate() error = %v", err)
			}
			if !test.wantSuccess && !errors.Is(err, ErrInvocation) {
				t.Fatalf("Generate() error = %v, want terminal invocation failure", err)
			}
			if !test.wantSuccess && strings.Contains(err.Error(), test.transportErr.Error()) {
				t.Fatalf("Generate() error leaks transport details: %v", err)
			}
			if len(api.params) != test.wantAttempts {
				t.Fatalf("Responses calls = %d, want %d", len(api.params), test.wantAttempts)
			}
			assertOneObservation(t, recorder, test.wantOutcome, test.wantAttempts)
		})
	}
}

func TestGenerateTreatsStatus600AsTerminalProviderError(t *testing.T) {
	providerErr := syntheticAPIError(t, 600)
	api := &fakeResponsesAPI{errors: []error{providerErr, providerErr, providerErr, providerErr, providerErr}}
	recorder := &recordingInvocationRecorder{}
	client := newTestClient(t, api, recorder, 5)

	_, err := client.Generate(context.Background(), validRequest())
	if !errors.Is(err, ErrInvocation) {
		t.Fatalf("Generate() error = %v, want terminal invocation failure", err)
	}
	if len(api.params) != 1 {
		t.Fatalf("Responses calls = %d, want 1", len(api.params))
	}
	assertOneObservation(t, recorder, OutcomeProviderError, 1)
	if got := outcomeForHTTPStatus(600); got != OutcomeProviderError {
		t.Fatalf("outcomeForHTTPStatus(600) = %q, want %q", got, OutcomeProviderError)
	}
}

func TestGenerateTreatsNonRetryableHTTPStatusesAsTerminal(t *testing.T) {
	tests := []struct {
		status int
		want   error
	}{
		{status: http.StatusBadRequest, want: ErrInvocation},
		{status: http.StatusUnauthorized, want: extract.ErrAuthentication},
		{status: http.StatusForbidden, want: extract.ErrAuthorization},
		{status: http.StatusNotFound, want: ErrInvocation},
	}
	for _, test := range tests {
		t.Run(http.StatusText(test.status), func(t *testing.T) {
			api := &fakeResponsesAPI{errors: []error{syntheticAPIError(t, test.status)}}
			client := newTestClient(t, api, &recordingInvocationRecorder{}, 5)
			_, err := client.Generate(context.Background(), validRequest())
			if !errors.Is(err, test.want) {
				t.Fatalf("Generate() error = %v, want %v", err, test.want)
			}
			if len(api.params) != 1 {
				t.Fatalf("Responses calls = %d, want terminal single attempt", len(api.params))
			}
			if strings.Contains(err.Error(), testPrivateError) {
				t.Fatalf("Generate() error leaks API body: %v", err)
			}
		})
	}
}

func TestGenerateHonorsCancellationBeforeAndDuringRetry(t *testing.T) {
	t.Run("before first attempt", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		api := &fakeResponsesAPI{}
		client := newTestClient(t, api, &recordingInvocationRecorder{}, 3)
		_, err := client.Generate(ctx, validRequest())
		if !errors.Is(err, context.Canceled) || len(api.params) != 0 {
			t.Fatalf("Generate() error/calls = %v/%d", err, len(api.params))
		}
	})

	t.Run("before retry", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		api := &fakeResponsesAPI{call: func() { cancel() }, errors: []error{syntheticAPIError(t, http.StatusServiceUnavailable)}}
		client := newTestClient(t, api, &recordingInvocationRecorder{}, 3)
		_, err := client.Generate(ctx, validRequest())
		if !errors.Is(err, context.Canceled) || len(api.params) != 1 {
			t.Fatalf("Generate() error/calls = %v/%d", err, len(api.params))
		}
	})

	t.Run("deadline before first attempt", func(t *testing.T) {
		ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		defer cancel()
		api := &fakeResponsesAPI{}
		recorder := &recordingInvocationRecorder{}
		client := newTestClient(t, api, recorder, 3)
		_, err := client.Generate(ctx, validRequest())
		if !errors.Is(err, context.DeadlineExceeded) || len(api.params) != 0 {
			t.Fatalf("Generate() error/calls = %v/%d", err, len(api.params))
		}
		assertOneObservation(t, recorder, OutcomeTimeout, 0)
	})

	t.Run("deadline during retry wait", func(t *testing.T) {
		api := &fakeResponsesAPI{errors: []error{syntheticAPIError(t, http.StatusServiceUnavailable)}}
		recorder := &recordingInvocationRecorder{}
		options := validOptions()
		options.Recorder = recorder
		client, err := newClient(api, options, func(context.Context, time.Duration) error { return context.DeadlineExceeded })
		if err != nil {
			t.Fatalf("newClient() error = %v", err)
		}
		_, err = client.Generate(context.Background(), validRequest())
		if !errors.Is(err, context.DeadlineExceeded) || len(api.params) != 1 {
			t.Fatalf("Generate() error/calls = %v/%d", err, len(api.params))
		}
		assertOneObservation(t, recorder, OutcomeTimeout, 1)
	})
}

func TestGenerateRecordsOneObservationAndFinishesOwningSpan(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	recorder := &recordingInvocationRecorder{}
	client := newTestClient(t, &fakeResponsesAPI{outputs: []*responses.Response{successfulResponse(t, `{}`)}}, recorder, 3)
	client.tracer = provider.Tracer("stacks")

	if _, err := client.Generate(context.Background(), validRequest()); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if len(recorder.observations) != 1 {
		t.Fatalf("observations = %d, want one per Generate", len(recorder.observations))
	}
	spans := exporter.GetSpans()
	if len(spans) != 1 || spans[0].Name != "stacks.model.generate" || spans[0].Status.Code != codes.Ok {
		t.Fatalf("spans = %+v, want one explicitly successful owning span", spans)
	}
}

const validUsageJSON = `{"input_tokens":11,"output_tokens":7,"total_tokens":18}`

func responseJSON(model, status, output, usage string) string {
	usageField := ""
	if usage != "" {
		usageField = `,"usage":` + usage
	}
	return fmt.Sprintf(`{"model":%q,"status":%q,"output":%s%s}`, model, status, output, usageField)
}

func expectedHTTPOutcome(status int) string {
	switch status {
	case http.StatusRequestTimeout:
		return OutcomeTimeout
	case http.StatusTooManyRequests:
		return OutcomeThrottled
	case http.StatusInternalServerError:
		return OutcomeInternal
	default:
		return OutcomeUnavailable
	}
}

func successfulResponse(t *testing.T, output string) *responses.Response {
	t.Helper()
	return decodedResponse(t, completeResponseJSON(output))
}

func completeResponseJSON(output string) string {
	return fmt.Sprintf(`{
		"id":"resp_synthetic","created_at":1,"error":null,"incomplete_details":null,
		"instructions":"synthetic instructions","metadata":{},"model":%q,"object":"response",
		"output":[{"id":"msg_synthetic","content":[{"annotations":[],"text":%q,"type":"output_text","logprobs":[]}],"role":"assistant","status":"completed","type":"message","phase":"final_answer"}],
		"parallel_tool_calls":false,"temperature":1,"tool_choice":"auto","tools":[],"top_p":1,
		"background":false,"completed_at":2,"conversation":null,"max_output_tokens":321,"max_tool_calls":null,
		"previous_response_id":null,"prompt":null,"prompt_cache_key":"","prompt_cache_retention":null,
		"reasoning":null,"safety_identifier":"","service_tier":"default","status":"completed",
		"text":{"format":{"name":"synthetic","schema":{},"strict":true,"type":"json_schema"}},
		"top_logprobs":null,"truncation":"disabled",
		"usage":{"input_tokens":11,"input_tokens_details":{"cached_tokens":0},"output_tokens":7,"output_tokens_details":{"reasoning_tokens":0},"total_tokens":18},
		"user":""
	}`, testModelID, output)
}

func decodedResponse(t *testing.T, body string) *responses.Response {
	t.Helper()
	var response responses.Response
	if err := json.Unmarshal([]byte(body), &response); err != nil {
		t.Fatalf("decode synthetic response: %v", err)
	}
	return &response
}

func syntheticAPIError(t *testing.T, status int) error {
	t.Helper()
	body := fmt.Sprintf(`{"error":{"message":%q,"type":"synthetic","code":"synthetic"}}`, testPrivateError)
	var providerErr openaisdk.Error
	if err := json.Unmarshal([]byte(body), &providerErr); err != nil {
		t.Fatalf("decode synthetic API error: %v", err)
	}
	providerErr.StatusCode = status
	providerErr.Request = &http.Request{Method: http.MethodPost, URL: &url.URL{Scheme: "https", Host: "api.openai.com", Path: "/v1/responses"}}
	providerErr.Response = &http.Response{StatusCode: status, Status: fmt.Sprintf("%d %s", status, http.StatusText(status))}
	return &providerErr
}

func validRequest() extract.Request {
	contract, err := extract.PromptContract(extract.ExtractionPromptVersion)
	if err != nil {
		panic(err)
	}
	return extract.Request{
		PromptVersion: contract.Version,
		SystemPrompt:  contract.SystemPrompt,
		Input:         testPrivateInput,
		SchemaName:    contract.SchemaName,
		JSONSchema:    contract.JSONSchema,
	}
}

func validOptions() Options {
	return Options{
		ModelID: testModelID, DataMode: modelpolicy.DataModePersonal,
		MaxOutputTokens: 321, MaxAttempts: 3,
	}
}

func optionsWith(mutate func(*Options)) Options {
	options := validOptions()
	mutate(&options)
	return options
}

type fakeResponsesAPI struct {
	params       []responses.ResponseNewParams
	optionCounts []int
	outputs      []*responses.Response
	errors       []error
	call         func()
}

func (fake *fakeResponsesAPI) New(_ context.Context, params responses.ResponseNewParams, options ...option.RequestOption) (*responses.Response, error) {
	fake.params = append(fake.params, params)
	fake.optionCounts = append(fake.optionCounts, len(options))
	index := len(fake.params) - 1
	if fake.call != nil {
		fake.call()
	}
	if index < len(fake.errors) && fake.errors[index] != nil {
		return nil, fake.errors[index]
	}
	if index < len(fake.outputs) {
		return fake.outputs[index], nil
	}
	return successfulResponseFromFake(), nil
}

func successfulResponseFromFake() *responses.Response {
	var response responses.Response
	_ = json.Unmarshal([]byte(completeResponseJSON(`{}`)), &response)
	return &response
}

func assertExactRequestJSON(t *testing.T, encoded []byte, request extract.Request) {
	t.Helper()
	var schema map[string]any
	if err := json.Unmarshal(request.JSONSchema, &schema); err != nil {
		t.Fatalf("decode expected schema: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("decode marshaled request: %v", err)
	}
	want := map[string]any{
		"background":        false,
		"instructions":      request.SystemPrompt,
		"input":             request.Input,
		"max_output_tokens": float64(321),
		"model":             testModelID,
		"reasoning":         map[string]any{"effort": "none"},
		"store":             false,
		"text": map[string]any{"format": map[string]any{
			"name": request.SchemaName, "schema": schema, "strict": true, "type": "json_schema",
		}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("complete SDK request = %#v, want %#v", got, want)
	}
}

type recordedHTTPRequest struct {
	url           string
	authorization string
	organization  string
	project       string
	body          []byte
}

type recordingRoundTripper struct {
	requests []recordedHTTPRequest
}

func (transport *recordingRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, err
	}
	transport.requests = append(transport.requests, recordedHTTPRequest{
		url: request.URL.String(), authorization: request.Header.Get("Authorization"),
		organization: request.Header.Get("OpenAI-Organization"), project: request.Header.Get("OpenAI-Project"), body: body,
	})
	status := http.StatusServiceUnavailable
	responseBody := `{"error":{"message":"synthetic unavailable","type":"synthetic","code":"synthetic","param":null}}`
	if len(transport.requests) == 2 {
		status = http.StatusOK
		responseBody = completeResponseJSON(`{}`)
	}
	return &http.Response{
		StatusCode: status, Status: fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header: http.Header{"Content-Type": []string{"application/json"}},
		Body:   io.NopCloser(strings.NewReader(responseBody)), Request: request,
	}, nil
}

type recordingInvocationRecorder struct {
	observations []modeltelemetry.Observation
}

func (recorder *recordingInvocationRecorder) Record(_ context.Context, observation modeltelemetry.Observation) {
	recorder.observations = append(recorder.observations, observation)
}

func assertOneObservation(t *testing.T, recorder *recordingInvocationRecorder, outcome string, attempts int) {
	t.Helper()
	if len(recorder.observations) != 1 {
		t.Fatalf("observations = %d, want 1", len(recorder.observations))
	}
	got := recorder.observations[0]
	if got.Provider != modelpolicy.ProviderOpenAI || got.DataMode != modelpolicy.DataModePersonal || got.ModelID != testModelID || got.Outcome != outcome || got.Attempts != attempts {
		t.Fatalf("observation = %+v", got)
	}
}

func newTestClient(t *testing.T, api responsesAPI, recorder modeltelemetry.Recorder, maxAttempts int) *Client {
	t.Helper()
	options := validOptions()
	options.MaxAttempts = maxAttempts
	options.Recorder = recorder
	client, err := newClient(api, options, func(ctx context.Context, _ time.Duration) error { return ctx.Err() })
	if err != nil {
		t.Fatalf("newClient() error = %v", err)
	}
	return client
}

type syntheticTimeoutError struct{}

func (syntheticTimeoutError) Error() string   { return "synthetic timeout" }
func (syntheticTimeoutError) Timeout() bool   { return true }
func (syntheticTimeoutError) Temporary() bool { return true }

type syntheticTLSHandshakeTimeoutError struct{}

func (syntheticTLSHandshakeTimeoutError) Error() string   { return "net/http: TLS handshake timeout" }
func (syntheticTLSHandshakeTimeoutError) Timeout() bool   { return true }
func (syntheticTLSHandshakeTimeoutError) Temporary() bool { return true }
