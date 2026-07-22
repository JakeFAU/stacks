package anthropic

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

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"stacks/internal/extract"
	"stacks/internal/modelpolicy"
	"stacks/internal/modeltelemetry"
)

const (
	testAPIKey        = "synthetic-anthropic-key"
	testModelID       = "synthetic-anthropic-model"
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
			api := &fakeMessagesAPI{}
			recorder := &recordingInvocationRecorder{}
			client := newTestClient(t, api, recorder, 3)
			request := validRequest()
			mutate(&request)

			_, err := client.Generate(context.Background(), request)
			if !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("Generate() error = %v, want invalid request", err)
			}
			if len(api.params) != 0 {
				t.Fatalf("Messages calls = %d, want 0", len(api.params))
			}
			assertOneObservation(t, recorder, OutcomeInvalidRequest, 0)
		})
	}
}

func TestGenerateSendsOnlyStatelessStructuredMessageRequest(t *testing.T) {
	api := &fakeMessagesAPI{outputs: []*anthropicsdk.Message{successfulMessage(t, `{}`)}}
	client := newTestClient(t, api, &recordingInvocationRecorder{}, 3)
	request := validRequest()

	if _, err := client.Generate(context.Background(), request); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if len(api.params) != 1 || api.optionCounts[0] != 0 {
		t.Fatalf("Messages calls/options = %d/%d, want 1/0", len(api.params), api.optionCounts[0])
	}
	params := api.params[0]
	if params.Model != anthropicsdk.Model(testModelID) || params.MaxTokens != 321 {
		t.Fatalf("Model/MaxTokens = %q/%d", params.Model, params.MaxTokens)
	}
	if len(params.System) != 1 || params.System[0].Text != request.SystemPrompt {
		t.Fatalf("System = %#v", params.System)
	}
	if len(params.Messages) != 1 || params.Messages[0].Role != anthropicsdk.MessageParamRoleUser || len(params.Messages[0].Content) != 1 || params.Messages[0].Content[0].OfText == nil || params.Messages[0].Content[0].OfText.Text != testPrivateInput {
		t.Fatalf("Messages = %#v", params.Messages)
	}
	var schema map[string]any
	if err := json.Unmarshal(request.JSONSchema, &schema); err != nil {
		t.Fatalf("decode expected schema: %v", err)
	}
	if !reflect.DeepEqual(params.OutputConfig.Format.Schema, schema) {
		t.Fatal("submitted schema differs semantically from reviewed schema")
	}
	encoded, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	assertExactRequestJSON(t, encoded, request)
}

func TestNewFixesProductionRoutingAndDisablesSDKRetries(t *testing.T) {
	t.Setenv("ANTHROPIC_BASE_URL", "https://hostile.invalid/")
	t.Setenv("ANTHROPIC_API_KEY", "hostile-ambient-key")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "hostile-ambient-token")
	t.Setenv("ANTHROPIC_PROFILE", "hostile-ambient-profile")

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
		if recorded.url != "https://api.anthropic.com/v1/messages" {
			t.Fatalf("request %d URL = %q", index, recorded.url)
		}
		if recorded.apiKey != testAPIKey || recorded.authorization != "" {
			t.Fatalf("request %d credential headers = API key %q, authorization %q", index, recorded.apiKey, recorded.authorization)
		}
		assertExactRequestJSON(t, recorded.body, request)
	}
	assertOneObservation(t, recorder, OutcomeSuccess, 2)
}

func TestGenerateAcceptsOneEndTurnJSONTextAndAccountsForCacheTokens(t *testing.T) {
	recorder := &recordingInvocationRecorder{}
	client := newTestClient(t, &fakeMessagesAPI{outputs: []*anthropicsdk.Message{successfulMessage(t, `{"answer":true}`)}}, recorder, 3)

	response, err := client.Generate(context.Background(), validRequest())
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if string(response.Output) != `{"answer":true}` || response.ModelID != testModelID || response.PromptVersion != extract.ExtractionPromptVersion || response.Outcome != OutcomeSuccess {
		t.Fatalf("Generate() response = %+v", response)
	}
	if response.Usage != (extract.Usage{InputTokens: 18, OutputTokens: 7, TotalTokens: 25}) || response.Latency != 0 {
		t.Fatalf("Generate() metadata = %+v", response)
	}
	assertOneObservation(t, recorder, OutcomeSuccess, 1)
	if got := recorder.observations[0]; got.InputTokens != 18 || got.OutputTokens != 7 || got.TotalTokens != 25 {
		t.Fatalf("observation usage = %+v", got)
	}
}

func TestGenerateRejectsEveryNonCanonicalResponseShape(t *testing.T) {
	tests := map[string]string{
		"malformed JSON":          messageJSON(testModelID, "end_turn", `[{"type":"text","text":"not-json","citations":[]}]`, validUsageJSON),
		"max tokens":              messageJSON(testModelID, "max_tokens", textContentJSON(`{}`), validUsageJSON),
		"refusal":                 messageJSON(testModelID, "refusal", textContentJSON(testPrivateOutput), validUsageJSON),
		"tool use":                messageJSON(testModelID, "tool_use", textContentJSON(`{}`), validUsageJSON),
		"pause turn":              messageJSON(testModelID, "pause_turn", textContentJSON(`{}`), validUsageJSON),
		"stop sequence":           messageJSON(testModelID, "stop_sequence", textContentJSON(`{}`), validUsageJSON),
		"multiple content blocks": messageJSON(testModelID, "end_turn", `[{"type":"text","text":"{}","citations":[]},{"type":"text","text":"{}","citations":[]}]`, validUsageJSON),
		"non-text block":          messageJSON(testModelID, "end_turn", `[{"type":"thinking","thinking":"`+testPrivateOutput+`","signature":"synthetic"}]`, validUsageJSON),
		"missing usage":           messageJSON(testModelID, "end_turn", textContentJSON(`{}`), ""),
		"negative input usage":    messageJSON(testModelID, "end_turn", textContentJSON(`{}`), usageJSON(-1, 5, 2, 7)),
		"negative cache creation": messageJSON(testModelID, "end_turn", textContentJSON(`{}`), usageJSON(11, -1, 2, 7)),
		"negative cache read":     messageJSON(testModelID, "end_turn", textContentJSON(`{}`), usageJSON(11, 5, -1, 7)),
		"negative output usage":   messageJSON(testModelID, "end_turn", textContentJSON(`{}`), usageJSON(11, 5, 2, -1)),
		"returned model mismatch": messageJSON("different-model", "end_turn", textContentJSON(`{}`), validUsageJSON),
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			api := &fakeMessagesAPI{outputs: []*anthropicsdk.Message{decodedMessage(t, body)}}
			client := newTestClient(t, api, &recordingInvocationRecorder{}, 3)
			_, err := client.Generate(context.Background(), validRequest())
			if !errors.Is(err, ErrInvalidOutput) {
				t.Fatalf("Generate() error = %v, want invalid output", err)
			}
			if strings.Contains(err.Error(), testPrivateOutput) {
				t.Fatalf("Generate() error leaks response: %v", err)
			}
			if len(api.params) != 1 {
				t.Fatalf("Messages calls = %d, want terminal single attempt", len(api.params))
			}
		})
	}
}

func TestGenerateRetriesOnlyRetryableHTTPStatusesToExactBound(t *testing.T) {
	for _, status := range []int{http.StatusRequestTimeout, http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			providerErr := syntheticAPIError(t, status)
			api := &fakeMessagesAPI{errors: []error{providerErr, providerErr, providerErr, providerErr}}
			recorder := &recordingInvocationRecorder{}
			client := newTestClient(t, api, recorder, 3)

			_, err := client.Generate(context.Background(), validRequest())
			if !errors.Is(err, ErrInvocation) {
				t.Fatalf("Generate() error = %v, want invocation failure", err)
			}
			if len(api.params) != 3 {
				t.Fatalf("Messages calls = %d, want exact bound 3", len(api.params))
			}
			assertNoPrivateData(t, err)
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
			transportErr: &url.Error{Op: "Post", URL: "https://api.anthropic.com/v1/messages", Err: &net.OpError{
				Op: "read", Net: "tcp", Err: syscall.ECONNRESET,
			}},
			wantAttempts: 2, wantOutcome: OutcomeSuccess, wantSuccess: true,
		},
		{
			name: "live context network timeout",
			transportErr: &url.Error{Op: "Post", URL: "https://api.anthropic.com/v1/messages", Err: &net.OpError{
				Op: "read", Net: "tcp", Err: syntheticTimeoutError{},
			}},
			wantAttempts: 2, wantOutcome: OutcomeSuccess, wantSuccess: true,
		},
		{
			name:         "permanent TLS certificate validation",
			transportErr: &url.Error{Op: "Post", URL: "https://api.anthropic.com/v1/messages", Err: x509.UnknownAuthorityError{}},
			wantAttempts: 1, wantOutcome: OutcomeProviderError,
		},
		{
			name: "permanent DNS failure",
			transportErr: &url.Error{Op: "Post", URL: "https://api.anthropic.com/v1/messages", Err: &net.DNSError{
				Err: "no such host", Name: "api.anthropic.com", IsNotFound: true,
			}},
			wantAttempts: 1, wantOutcome: OutcomeProviderError,
		},
		{
			name:         "redirect policy failure",
			transportErr: &url.Error{Op: "Get", URL: "https://api.anthropic.com/v1/messages", Err: errors.New("stopped after redirects")},
			wantAttempts: 1, wantOutcome: OutcomeProviderError,
		},
		{
			name:         "non-transport provider deadline",
			transportErr: fmt.Errorf("provider wrapper: %w", context.DeadlineExceeded),
			wantAttempts: 1, wantOutcome: OutcomeTimeout,
		},
		{
			name:         "bare timeout-looking net error",
			transportErr: syntheticTimeoutError{},
			wantAttempts: 1, wantOutcome: OutcomeProviderError,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := &recordingInvocationRecorder{}
			api := &fakeMessagesAPI{
				errors:  []error{test.transportErr, nil},
				outputs: []*anthropicsdk.Message{nil, successfulMessage(t, `{}`)},
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
				t.Fatalf("Messages calls = %d, want %d", len(api.params), test.wantAttempts)
			}
			assertOneObservation(t, recorder, test.wantOutcome, test.wantAttempts)
		})
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
			api := &fakeMessagesAPI{errors: []error{syntheticAPIError(t, test.status)}}
			client := newTestClient(t, api, &recordingInvocationRecorder{}, 5)
			_, err := client.Generate(context.Background(), validRequest())
			if !errors.Is(err, test.want) {
				t.Fatalf("Generate() error = %v, want %v", err, test.want)
			}
			if len(api.params) != 1 {
				t.Fatalf("Messages calls = %d, want terminal single attempt", len(api.params))
			}
			assertNoPrivateData(t, err)
		})
	}
}

func TestGenerateHonorsCancellationBeforeAndDuringRetry(t *testing.T) {
	t.Run("before first attempt", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		api := &fakeMessagesAPI{}
		client := newTestClient(t, api, &recordingInvocationRecorder{}, 3)
		_, err := client.Generate(ctx, validRequest())
		if !errors.Is(err, context.Canceled) || len(api.params) != 0 {
			t.Fatalf("Generate() error/calls = %v/%d", err, len(api.params))
		}
	})

	t.Run("before retry", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		api := &fakeMessagesAPI{call: func() { cancel() }, errors: []error{syntheticAPIError(t, http.StatusServiceUnavailable)}}
		client := newTestClient(t, api, &recordingInvocationRecorder{}, 3)
		_, err := client.Generate(ctx, validRequest())
		if !errors.Is(err, context.Canceled) || len(api.params) != 1 {
			t.Fatalf("Generate() error/calls = %v/%d", err, len(api.params))
		}
	})

	t.Run("deadline before first attempt", func(t *testing.T) {
		ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		defer cancel()
		api := &fakeMessagesAPI{}
		recorder := &recordingInvocationRecorder{}
		client := newTestClient(t, api, recorder, 3)
		_, err := client.Generate(ctx, validRequest())
		if !errors.Is(err, context.DeadlineExceeded) || len(api.params) != 0 {
			t.Fatalf("Generate() error/calls = %v/%d", err, len(api.params))
		}
		assertOneObservation(t, recorder, OutcomeTimeout, 0)
	})

	t.Run("deadline during retry wait", func(t *testing.T) {
		api := &fakeMessagesAPI{errors: []error{syntheticAPIError(t, http.StatusServiceUnavailable)}}
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
	client := newTestClient(t, &fakeMessagesAPI{outputs: []*anthropicsdk.Message{successfulMessage(t, `{}`)}}, recorder, 3)
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

const validUsageJSON = `{"cache_creation":{"ephemeral_1h_input_tokens":5,"ephemeral_5m_input_tokens":0},"cache_creation_input_tokens":5,"cache_read_input_tokens":2,"input_tokens":11,"output_tokens":7,"output_tokens_details":{"thinking_tokens":0},"server_tool_use":{"web_fetch_requests":0,"web_search_requests":0},"service_tier":"standard","inference_geo":"global"}`

func textContentJSON(output string) string {
	encoded, _ := json.Marshal(output)
	return `[{"type":"text","text":` + string(encoded) + `,"citations":[]}]`
}

func usageJSON(input, cacheCreation, cacheRead, output int64) string {
	return fmt.Sprintf(`{"cache_creation":{"ephemeral_1h_input_tokens":%d,"ephemeral_5m_input_tokens":0},"cache_creation_input_tokens":%d,"cache_read_input_tokens":%d,"input_tokens":%d,"output_tokens":%d,"output_tokens_details":{"thinking_tokens":0},"server_tool_use":{"web_fetch_requests":0,"web_search_requests":0},"service_tier":"standard","inference_geo":"global"}`, cacheCreation, cacheCreation, cacheRead, input, output)
}

func messageJSON(model, stopReason, content, usage string) string {
	usageField := ""
	if usage != "" {
		usageField = `,"usage":` + usage
	}
	return fmt.Sprintf(`{"id":"msg_synthetic","type":"message","role":"assistant","container":null,"content":%s,"model":%q,"stop_details":null,"stop_reason":%q,"stop_sequence":null%s}`, content, model, stopReason, usageField)
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

func successfulMessage(t *testing.T, output string) *anthropicsdk.Message {
	t.Helper()
	return decodedMessage(t, completeMessageJSON(output))
}

func completeMessageJSON(output string) string {
	return messageJSON(testModelID, "end_turn", textContentJSON(output), validUsageJSON)
}

func decodedMessage(t *testing.T, body string) *anthropicsdk.Message {
	t.Helper()
	var message anthropicsdk.Message
	if err := json.Unmarshal([]byte(body), &message); err != nil {
		t.Fatalf("decode synthetic response: %v", err)
	}
	return &message
}

func syntheticAPIError(t *testing.T, status int) error {
	t.Helper()
	body := fmt.Sprintf(`{"type":"error","error":{"type":"synthetic","message":%q}}`, testPrivateError)
	var providerErr anthropicsdk.Error
	if err := json.Unmarshal([]byte(body), &providerErr); err != nil {
		t.Fatalf("decode synthetic API error: %v", err)
	}
	providerErr.StatusCode = status
	providerErr.Request = &http.Request{Method: http.MethodPost, URL: &url.URL{Scheme: "https", Host: "api.anthropic.com", Path: "/v1/messages"}}
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

type fakeMessagesAPI struct {
	params       []anthropicsdk.MessageNewParams
	optionCounts []int
	outputs      []*anthropicsdk.Message
	errors       []error
	call         func()
}

func (fake *fakeMessagesAPI) New(_ context.Context, params anthropicsdk.MessageNewParams, options ...option.RequestOption) (*anthropicsdk.Message, error) {
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
	return successfulMessageFromFake(), nil
}

func successfulMessageFromFake() *anthropicsdk.Message {
	var message anthropicsdk.Message
	_ = json.Unmarshal([]byte(completeMessageJSON(`{}`)), &message)
	return &message
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
		"max_tokens": float64(321),
		"messages": []any{map[string]any{
			"role": "user", "content": []any{map[string]any{"type": "text", "text": request.Input}},
		}},
		"model": testModelID,
		"output_config": map[string]any{
			"format": map[string]any{"type": "json_schema", "schema": schema},
		},
		"system": []any{map[string]any{"type": "text", "text": request.SystemPrompt}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("complete SDK request = %#v, want %#v", got, want)
	}
}

type recordedHTTPRequest struct {
	url           string
	apiKey        string
	authorization string
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
		url: request.URL.String(), apiKey: request.Header.Get("X-Api-Key"),
		authorization: request.Header.Get("Authorization"), body: body,
	})
	status := http.StatusServiceUnavailable
	responseBody := `{"type":"error","error":{"type":"overloaded_error","message":"synthetic unavailable"}}`
	if len(transport.requests) == 2 {
		status = http.StatusOK
		responseBody = completeMessageJSON(`{}`)
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
	if got.Provider != modelpolicy.ProviderAnthropic || got.DataMode != modelpolicy.DataModePersonal || got.ModelID != testModelID || got.Outcome != outcome || got.Attempts != attempts {
		t.Fatalf("observation = %+v", got)
	}
}

func assertNoPrivateData(t *testing.T, err error) {
	t.Helper()
	for _, privateValue := range []string{testPrivateError, testPrivateInput, testPrivateOutput, testAPIKey} {
		if strings.Contains(err.Error(), privateValue) {
			t.Fatalf("Generate() error leaks private provider data: %v", err)
		}
	}
}

func newTestClient(t *testing.T, api messagesAPI, recorder modeltelemetry.Recorder, maxAttempts int) *Client {
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
