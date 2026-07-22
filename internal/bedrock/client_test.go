package bedrock

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"stacks/internal/extract"
)

const (
	testModelID       = "configured-model"
	testPromptVersion = "extract-v1"
	testPrivateInput  = "private-input-marker"
	testPrivateOutput = "private-output-marker"
)

func TestGenerateBuildsStructuredConverseRequestAndCapturesUsage(t *testing.T) {
	api := &fakeConverseAPI{outputs: []*bedrockruntime.ConverseOutput{successfulOutput(`{"signals":[]}`)}}
	recorder := &recordingInvocationRecorder{}
	client := newTestClient(t, api, recorder, 3)

	response, err := client.Generate(context.Background(), validRequest())
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if len(api.inputs) != 1 {
		t.Fatalf("Converse calls = %d, want 1", len(api.inputs))
	}
	input := api.inputs[0]
	if aws.ToString(input.ModelId) != testModelID {
		t.Errorf("ModelId = %q, want configured ID", aws.ToString(input.ModelId))
	}
	if input.InferenceConfig == nil || aws.ToInt32(input.InferenceConfig.MaxTokens) != 321 {
		t.Fatalf("MaxTokens = %#v, want 321", input.InferenceConfig)
	}
	if input.RequestMetadata != nil {
		t.Fatalf("RequestMetadata = %v, want nil to prevent private metadata", input.RequestMetadata)
	}
	assertStructuredOutput(t, input.OutputConfig)
	if response.Usage != (extract.Usage{InputTokens: 11, OutputTokens: 7, TotalTokens: 18}) {
		t.Errorf("Usage = %+v", response.Usage)
	}
	if response.Latency != 47*time.Millisecond {
		t.Errorf("Latency = %v, want 47ms", response.Latency)
	}
	if response.ModelID != testModelID || response.PromptVersion != testPromptVersion || response.Outcome != OutcomeSuccess {
		t.Errorf("Response metadata = %+v", response)
	}
	if len(recorder.observations) != 1 {
		t.Fatalf("telemetry observations = %d, want 1", len(recorder.observations))
	}
	observation := recorder.observations[0]
	if observation.ModelID != testModelID || observation.PromptVersion != testPromptVersion || observation.Outcome != OutcomeSuccess || observation.InputTokens != 11 || observation.OutputTokens != 7 || observation.TotalTokens != 18 || observation.ProviderLatency != 47*time.Millisecond || observation.Attempts != 1 {
		t.Errorf("telemetry observation = %+v", observation)
	}
}

func TestGenerateRecordsWallAndProviderLatencyAndExplicitSuccessSpan(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	recorder := &recordingInvocationRecorder{}
	client := newTestClient(t, &fakeConverseAPI{outputs: []*bedrockruntime.ConverseOutput{successfulOutput(`{}`)}}, recorder, 1)
	client.tracer = provider.Tracer("stacks")
	started := time.Date(2026, time.July, 22, 12, 0, 0, 0, time.UTC)
	times := []time.Time{started, started.Add(75 * time.Millisecond)}
	client.now = func() time.Time {
		value := times[0]
		times = times[1:]
		return value
	}

	if _, err := client.Generate(context.Background(), validRequest()); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if got := recorder.observations[0]; got.WallLatency != 75*time.Millisecond || got.ProviderLatency != 47*time.Millisecond {
		t.Fatalf("invocation latency = %#v, want wall=75ms provider=47ms", got)
	}
	spans := exporter.GetSpans()
	if len(spans) != 1 || spans[0].Name != invocationSpanName || spans[0].Status.Code != codes.Ok {
		t.Fatalf("invocation spans = %#v, want one explicit OK span", spans)
	}
}

func TestGenerateRecordsRealBoundedWallLatencyForProviderFailure(t *testing.T) {
	recorder := &recordingInvocationRecorder{}
	client := newTestClient(t, &fakeConverseAPI{errors: []error{&smithy.GenericAPIError{Code: "AccessDeniedException", Message: testPrivateInput}}}, recorder, 1)
	started := time.Date(2026, time.July, 22, 12, 0, 0, 0, time.UTC)
	times := []time.Time{started, started.Add(25 * time.Millisecond)}
	client.now = func() time.Time {
		value := times[0]
		times = times[1:]
		return value
	}

	_, _ = client.Generate(context.Background(), validRequest())
	if got := recorder.observations[0]; got.WallLatency != 25*time.Millisecond || got.ProviderLatency != 0 || got.Outcome != OutcomeAccessDenied {
		t.Fatalf("failure telemetry = %#v, want bounded non-zero wall latency", got)
	}
}

func TestNewFromConfigRequiresExplicitRegion(t *testing.T) {
	_, err := NewFromConfig(aws.Config{}, Options{
		ModelID: testModelID, MaxTokens: 321, MaxAttempts: 3,
	})
	if err == nil || !strings.Contains(err.Error(), "region") {
		t.Fatalf("NewFromConfig() error = %v, want explicit region rejection", err)
	}
}

func TestNewFromConfigRejectsPaddedRegion(t *testing.T) {
	_, err := NewFromConfig(aws.Config{Region: " us-east-1 "}, Options{
		ModelID: testModelID, MaxTokens: 321, MaxAttempts: 3,
	})
	if err == nil || !strings.Contains(err.Error(), "region") {
		t.Fatalf("NewFromConfig() error = %v, want padded region rejection", err)
	}
}

func TestNewFromConfigEnforcesHardAttemptLimit(t *testing.T) {
	tests := []struct {
		name        string
		maxAttempts int
		wantError   bool
	}{
		{name: "negative", maxAttempts: -1, wantError: true},
		{name: "zero", maxAttempts: 0, wantError: true},
		{name: "one", maxAttempts: 1},
		{name: "hard maximum", maxAttempts: MaxAttemptsLimit},
		{name: "above hard maximum", maxAttempts: MaxAttemptsLimit + 1, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewFromConfig(aws.Config{Region: "us-east-1"}, Options{
				ModelID: testModelID, MaxTokens: 321, MaxAttempts: test.maxAttempts,
			})
			if (err != nil) != test.wantError {
				t.Fatalf("NewFromConfig() error = %v, wantError %t", err, test.wantError)
			}
		})
	}
}

func TestGenerateRejectsUnknownOrMutatedPromptContract(t *testing.T) {
	tests := map[string]func(*extract.Request){
		"unknown version": func(request *extract.Request) { request.PromptVersion = "unknown-v1" },
		"mutated prompt":  func(request *extract.Request) { request.SystemPrompt = "mutated" },
		"mutated schema name": func(request *extract.Request) {
			request.SchemaName = "mutated_schema"
		},
		"mutated schema": func(request *extract.Request) { request.JSONSchema = []byte(`{}`) },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			api := &fakeConverseAPI{outputs: []*bedrockruntime.ConverseOutput{successfulOutput(`{}`)}}
			recorder := &recordingInvocationRecorder{}
			client := newTestClient(t, api, recorder, 3)
			request := validRequest()
			mutate(&request)

			_, err := client.Generate(context.Background(), request)
			if err == nil || !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("Generate() error = %v, want invalid reviewed contract", err)
			}
			if len(api.inputs) != 0 {
				t.Fatalf("Converse calls = %d, want 0", len(api.inputs))
			}
			if len(recorder.observations) != 1 || recorder.observations[0].Outcome != OutcomeInvalidRequest || recorder.observations[0].PromptVersion != "" {
				t.Fatalf("telemetry = %+v, want bounded invalid request", recorder.observations)
			}
		})
	}
}

func TestGenerateUsesReviewedAnalysisContract(t *testing.T) {
	contract, err := extract.PromptContract(extract.AnalysisPromptVersion)
	if err != nil {
		t.Fatalf("PromptContract() error = %v", err)
	}
	api := &fakeConverseAPI{outputs: []*bedrockruntime.ConverseOutput{successfulOutput(`{}`)}}
	client := newTestClient(t, api, &recordingInvocationRecorder{}, 3)

	response, err := client.Generate(context.Background(), extract.Request{
		PromptVersion: contract.Version,
		SystemPrompt:  contract.SystemPrompt,
		Input:         testPrivateInput,
		SchemaName:    contract.SchemaName,
		JSONSchema:    contract.JSONSchema,
	})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	input := api.inputs[0]
	format := input.OutputConfig.TextFormat.Structure.(*types.OutputFormatStructureMemberJsonSchema)
	if response.PromptVersion != extract.AnalysisPromptVersion || aws.ToString(format.Value.Name) != extract.AnalysisSchemaName || aws.ToString(format.Value.Schema) != string(extract.AnalysisJSONSchema()) {
		t.Fatalf("analysis contract was not preserved")
	}
}

func TestGenerateRetriesThrottlingWithinConfiguredBound(t *testing.T) {
	throttled := &smithy.GenericAPIError{Code: "ThrottlingException", Message: testPrivateInput}
	api := &fakeConverseAPI{errors: []error{throttled, throttled, throttled, throttled}}
	recorder := &recordingInvocationRecorder{}
	client := newTestClient(t, api, recorder, 3)

	_, err := client.Generate(context.Background(), validRequest())
	if err == nil || !errors.Is(err, ErrInvocation) {
		t.Fatalf("Generate() error = %v, want invocation error", err)
	}
	if len(api.inputs) != 3 {
		t.Fatalf("Converse calls = %d, want bounded 3 attempts", len(api.inputs))
	}
	if strings.Contains(err.Error(), testPrivateInput) {
		t.Fatalf("error leaks private provider message: %v", err)
	}
	if got := recorder.observations[0]; got.Outcome != OutcomeThrottled || got.Attempts != 3 {
		t.Fatalf("telemetry = %+v, want throttled/3", got)
	}
}

func TestGenerateDoesNotRetryAccessDenied(t *testing.T) {
	api := &fakeConverseAPI{errors: []error{&smithy.GenericAPIError{
		Code: "AccessDeniedException", Message: testPrivateInput,
	}}}
	client := newTestClient(t, api, &recordingInvocationRecorder{}, 5)

	_, err := client.Generate(context.Background(), validRequest())
	if err == nil || !errors.Is(err, ErrInvocation) {
		t.Fatalf("Generate() error = %v, want invocation error", err)
	}
	if len(api.inputs) != 1 {
		t.Fatalf("Converse calls = %d, want no retry", len(api.inputs))
	}
}

func TestGenerateReturnsBoundedTypedAuthenticationAndAuthorizationFailures(t *testing.T) {
	tests := []struct {
		name string
		code string
		want error
	}{
		{name: "expired credentials", code: "ExpiredTokenException", want: extract.ErrAuthentication},
		{name: "unrecognized credentials", code: "UnrecognizedClientException", want: extract.ErrAuthentication},
		{name: "access denied", code: "AccessDeniedException", want: extract.ErrAuthorization},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := newTestClient(t, &fakeConverseAPI{errors: []error{&smithy.GenericAPIError{Code: test.code, Message: testPrivateInput}}}, &recordingInvocationRecorder{}, 1)
			_, err := client.Generate(context.Background(), validRequest())
			if !errors.Is(err, test.want) {
				t.Fatalf("Generate() error = %v, want typed auth failure", err)
			}
			if strings.Contains(err.Error(), testPrivateInput) {
				t.Fatalf("typed auth error leaked provider text: %v", err)
			}
		})
	}
}

func TestGeneratePreservesBoundedCredentialProviderAuthenticationFailure(t *testing.T) {
	providerErr := fmt.Errorf("synthetic signing boundary: %w", extract.ErrAuthentication)
	recorder := &recordingInvocationRecorder{}
	client := newTestClient(t, &fakeConverseAPI{errors: []error{providerErr}}, recorder, 5)

	_, err := client.Generate(context.Background(), validRequest())
	if !errors.Is(err, extract.ErrAuthentication) {
		t.Fatalf("Generate() error = %v, want typed authentication failure", err)
	}
	if len(recorder.observations) != 1 || recorder.observations[0].Outcome != OutcomeAuthentication {
		t.Fatalf("telemetry = %+v, want one authentication outcome", recorder.observations)
	}
}

func TestGenerateDoesNotRetryErrorsOutsideExactAllowlist(t *testing.T) {
	tests := map[string]error{
		"connection failure": &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET},
		"other throttling": &smithy.GenericAPIError{
			Code: "ProvisionedThroughputExceededException", Message: testPrivateInput,
		},
		"HTTP server error": &smithyhttp.ResponseError{
			Response: &smithyhttp.Response{Response: &http.Response{StatusCode: http.StatusInternalServerError}},
			Err:      errors.New("synthetic transport failure"),
		},
	}
	for name, providerErr := range tests {
		t.Run(name, func(t *testing.T) {
			api := &fakeConverseAPI{errors: []error{providerErr}}
			client, err := newWithAPI(api, Options{ModelID: testModelID, MaxTokens: 321, MaxAttempts: 3})
			if err != nil {
				t.Fatalf("newWithAPI() error = %v", err)
			}
			client.retryer = &noDelayRetryer{Retryer: client.retryer}

			if _, err := client.Generate(context.Background(), validRequest()); err == nil {
				t.Fatal("Generate() error = nil")
			}
			if len(api.inputs) != 1 {
				t.Fatalf("Converse calls = %d, want exact-policy single attempt", len(api.inputs))
			}
		})
	}
}

func TestGenerateRetriesLiveContextTransportAndRequestTimeouts(t *testing.T) {
	tests := map[string]error{
		"deadline returned by request": context.DeadlineExceeded,
		"request timeout service code": &smithy.GenericAPIError{Code: "RequestTimeout", Message: testPrivateInput},
		"network timeout":              syntheticTimeoutError{},
	}
	for name, timeoutErr := range tests {
		t.Run(name, func(t *testing.T) {
			api := &fakeConverseAPI{
				errors:  []error{timeoutErr, nil},
				outputs: []*bedrockruntime.ConverseOutput{nil, successfulOutput(`{}`)},
			}
			client := newTestClient(t, api, &recordingInvocationRecorder{}, 2)
			if _, err := client.Generate(context.Background(), validRequest()); err != nil {
				t.Fatalf("Generate() error = %v", err)
			}
			if len(api.inputs) != 2 {
				t.Fatalf("Converse calls = %d, want one bounded timeout retry", len(api.inputs))
			}
		})
	}
}

func TestGenerateNeverRetriesExpiredCallerDeadline(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	api := &fakeConverseAPI{call: func() error {
		cancel()
		return context.DeadlineExceeded
	}}
	client := newTestClient(t, api, &recordingInvocationRecorder{}, 3)

	_, err := client.Generate(ctx, validRequest())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Generate() error = %v, want caller cancellation", err)
	}
	if len(api.inputs) != 1 {
		t.Fatalf("Converse calls = %d, want no caller-cancellation retry", len(api.inputs))
	}
}

func TestGenerateRetriesEveryAllowlistedBedrockFailure(t *testing.T) {
	tests := map[string]string{
		"throttling":          "ThrottlingException",
		"model timeout":       modelTimeoutErrorCode,
		"service unavailable": serviceUnavailableErrorCode,
		"internal service":    internalServerErrorCode,
	}
	for name, code := range tests {
		t.Run(name, func(t *testing.T) {
			api := &fakeConverseAPI{
				errors:  []error{&smithy.GenericAPIError{Code: code, Message: testPrivateInput}, nil},
				outputs: []*bedrockruntime.ConverseOutput{nil, successfulOutput(`{}`)},
			}
			client, err := newWithAPI(api, Options{ModelID: testModelID, MaxTokens: 321, MaxAttempts: 3})
			if err != nil {
				t.Fatalf("newWithAPI() error = %v", err)
			}
			client.retryer = &noDelayRetryer{Retryer: client.retryer}

			if _, err := client.Generate(context.Background(), validRequest()); err != nil {
				t.Fatalf("Generate() error = %v", err)
			}
			if len(api.inputs) != 2 {
				t.Fatalf("Converse calls = %d, want one bounded retry", len(api.inputs))
			}
		})
	}
}

func TestGenerateHonorsCancellationBeforeRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	api := &fakeConverseAPI{call: func() error {
		cancel()
		return &smithy.GenericAPIError{Code: "ServiceUnavailableException", Message: testPrivateInput}
	}}
	client := newTestClient(t, api, &recordingInvocationRecorder{}, 5)

	_, err := client.Generate(ctx, validRequest())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Generate() error = %v, want context cancellation", err)
	}
	if len(api.inputs) != 1 {
		t.Fatalf("Converse calls = %d, want 1", len(api.inputs))
	}
}

func TestGenerateRejectsUnsupportedStopReasonWithoutLeakingOutput(t *testing.T) {
	output := successfulOutput(testPrivateOutput)
	output.StopReason = types.StopReasonMaxTokens
	recorder := &recordingInvocationRecorder{}
	client := newTestClient(t, &fakeConverseAPI{outputs: []*bedrockruntime.ConverseOutput{output}}, recorder, 3)

	_, err := client.Generate(context.Background(), validRequest())
	if err == nil || !errors.Is(err, ErrInvalidOutput) {
		t.Fatalf("Generate() error = %v, want invalid output", err)
	}
	if strings.Contains(err.Error(), testPrivateOutput) {
		t.Fatalf("error leaks private model output: %v", err)
	}
	if got := recorder.observations[0]; got.InputTokens != 11 || got.OutputTokens != 7 || got.ProviderLatency != 47*time.Millisecond || got.Outcome != OutcomeInvalidOutput {
		t.Fatalf("invalid-output telemetry = %+v, want provider usage without private output", got)
	}
}

func TestGenerateRejectsNonAssistantResponseRole(t *testing.T) {
	output := successfulOutput(`{}`)
	message := output.Output.(*types.ConverseOutputMemberMessage)
	message.Value.Role = types.ConversationRoleUser
	client := newTestClient(t, &fakeConverseAPI{outputs: []*bedrockruntime.ConverseOutput{output}}, &recordingInvocationRecorder{}, 3)

	if _, err := client.Generate(context.Background(), validRequest()); err == nil || !errors.Is(err, ErrInvalidOutput) {
		t.Fatalf("Generate() error = %v, want assistant-role validation", err)
	}
}

func TestGenerateRejectsNonTextOutputAndInvalidRequestWithoutCallingProvider(t *testing.T) {
	tests := map[string]*bedrockruntime.ConverseOutput{
		"non-message output": {
			StopReason: types.StopReasonEndTurn,
			Output:     nil,
			Metrics:    &types.ConverseMetrics{LatencyMs: aws.Int64(1)},
			Usage:      &types.TokenUsage{InputTokens: aws.Int32(1), OutputTokens: aws.Int32(1), TotalTokens: aws.Int32(2)},
		},
		"non-text content": {
			StopReason: types.StopReasonEndTurn,
			Output: &types.ConverseOutputMemberMessage{Value: types.Message{
				Role: types.ConversationRoleAssistant, Content: []types.ContentBlock{nil},
			}},
			Metrics: &types.ConverseMetrics{LatencyMs: aws.Int64(1)},
			Usage:   &types.TokenUsage{InputTokens: aws.Int32(1), OutputTokens: aws.Int32(1), TotalTokens: aws.Int32(2)},
		},
	}
	for name, output := range tests {
		t.Run(name, func(t *testing.T) {
			client := newTestClient(t, &fakeConverseAPI{outputs: []*bedrockruntime.ConverseOutput{output}}, &recordingInvocationRecorder{}, 2)
			if _, err := client.Generate(context.Background(), validRequest()); err == nil || !errors.Is(err, ErrInvalidOutput) {
				t.Fatalf("Generate() error = %v, want invalid output", err)
			}
		})
	}

	api := &fakeConverseAPI{}
	client := newTestClient(t, api, &recordingInvocationRecorder{}, 2)
	request := validRequest()
	request.JSONSchema = []byte(`not-json`)
	if _, err := client.Generate(context.Background(), request); err == nil || !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Generate() error = %v, want invalid request", err)
	}
	if len(api.inputs) != 0 {
		t.Fatalf("Converse calls = %d, want 0", len(api.inputs))
	}
}

func assertStructuredOutput(t *testing.T, config *types.OutputConfig) {
	t.Helper()
	if config == nil || config.TextFormat == nil || config.TextFormat.Type != types.OutputFormatTypeJsonSchema {
		t.Fatalf("OutputConfig = %#v, want JSON Schema", config)
	}
	structure, ok := config.TextFormat.Structure.(*types.OutputFormatStructureMemberJsonSchema)
	if !ok {
		t.Fatalf("OutputConfig structure = %T", config.TextFormat.Structure)
	}
	if aws.ToString(structure.Value.Name) != extract.ExtractionSchemaName {
		t.Errorf("schema name = %q", aws.ToString(structure.Value.Name))
	}
	if aws.ToString(structure.Value.Schema) == "" {
		t.Error("schema is empty")
	}
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

func successfulOutput(text string) *bedrockruntime.ConverseOutput {
	return &bedrockruntime.ConverseOutput{
		StopReason: types.StopReasonEndTurn,
		Output: &types.ConverseOutputMemberMessage{Value: types.Message{
			Role:    types.ConversationRoleAssistant,
			Content: []types.ContentBlock{&types.ContentBlockMemberText{Value: text}},
		}},
		Metrics: &types.ConverseMetrics{LatencyMs: aws.Int64(47)},
		Usage: &types.TokenUsage{
			InputTokens: aws.Int32(11), OutputTokens: aws.Int32(7), TotalTokens: aws.Int32(18),
		},
	}
}

type fakeConverseAPI struct {
	inputs  []*bedrockruntime.ConverseInput
	outputs []*bedrockruntime.ConverseOutput
	errors  []error
	call    func() error
}

func (fake *fakeConverseAPI) Converse(_ context.Context, input *bedrockruntime.ConverseInput, _ ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseOutput, error) {
	fake.inputs = append(fake.inputs, input)
	index := len(fake.inputs) - 1
	if fake.call != nil {
		if err := fake.call(); err != nil {
			return nil, err
		}
	}
	if index < len(fake.errors) && fake.errors[index] != nil {
		return nil, fake.errors[index]
	}
	if index < len(fake.outputs) {
		return fake.outputs[index], nil
	}
	return nil, &smithy.GenericAPIError{Code: "InternalServerException", Message: testPrivateOutput}
}

type recordingInvocationRecorder struct {
	observations []InvocationObservation
}

func (recorder *recordingInvocationRecorder) Record(_ context.Context, observation InvocationObservation) {
	recorder.observations = append(recorder.observations, observation)
}

func newTestClient(t *testing.T, api converseAPI, recorder InvocationRecorder, maxAttempts int) *Client {
	t.Helper()
	client, err := newClient(api, Options{
		ModelID: testModelID, MaxTokens: 321, MaxAttempts: maxAttempts, Recorder: recorder,
	}, &zeroRetryer{maxAttempts: maxAttempts})
	if err != nil {
		t.Fatalf("newClient() error = %v", err)
	}
	return client
}

type zeroRetryer struct {
	maxAttempts int
}

func (retryer *zeroRetryer) IsErrorRetryable(err error) bool { return isAllowlistedRetry(err) }
func (retryer *zeroRetryer) MaxAttempts() int                { return retryer.maxAttempts }
func (retryer *zeroRetryer) RetryDelay(int, error) (time.Duration, error) {
	return 0, nil
}
func (retryer *zeroRetryer) GetRetryToken(context.Context, error) (func(error) error, error) {
	return func(error) error { return nil }, nil
}
func (retryer *zeroRetryer) GetInitialToken() func(error) error {
	return func(error) error { return nil }
}
func (retryer *zeroRetryer) GetAttemptToken(context.Context) (func(error) error, error) {
	return func(error) error { return nil }, nil
}

var _ aws.RetryerV2 = (*zeroRetryer)(nil)

type noDelayRetryer struct {
	aws.Retryer
}

func (retryer *noDelayRetryer) RetryDelay(int, error) (time.Duration, error) {
	return 0, nil
}

type syntheticTimeoutError struct{}

func (syntheticTimeoutError) Error() string   { return "synthetic timeout" }
func (syntheticTimeoutError) Timeout() bool   { return true }
func (syntheticTimeoutError) Temporary() bool { return true }
