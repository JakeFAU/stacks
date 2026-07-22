package bedrock

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/aws/smithy-go"
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

	response, err := client.Generate(context.Background(), extract.Request{
		PromptVersion: testPromptVersion,
		SystemPrompt:  "bounded-system-prompt",
		Input:         testPrivateInput,
		SchemaName:    extract.ExtractionSchemaName,
		JSONSchema:    extract.ExtractionJSONSchema(),
	})
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
	if observation.ModelID != testModelID || observation.PromptVersion != testPromptVersion || observation.Outcome != OutcomeSuccess || observation.InputTokens != 11 || observation.OutputTokens != 7 || observation.Latency != 47*time.Millisecond || observation.Attempts != 1 {
		t.Errorf("telemetry observation = %+v", observation)
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
	if got := recorder.observations[0]; got.InputTokens != 11 || got.OutputTokens != 7 || got.Latency != 47*time.Millisecond || got.Outcome != OutcomeInvalidOutput {
		t.Fatalf("invalid-output telemetry = %+v, want provider usage without private output", got)
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
	return extract.Request{
		PromptVersion: testPromptVersion,
		SystemPrompt:  "bounded-system-prompt",
		Input:         testPrivateInput,
		SchemaName:    extract.ExtractionSchemaName,
		JSONSchema:    extract.ExtractionJSONSchema(),
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

func newTestClient(t *testing.T, api ConverseAPI, recorder InvocationRecorder, maxAttempts int) *Client {
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

func (retryer *zeroRetryer) IsErrorRetryable(err error) bool { return isTransient(err) }
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
