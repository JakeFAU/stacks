package bedrock

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"reflect"
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
	"stacks/internal/modelpolicy"
	"stacks/internal/modeltelemetry"
	"stacks/internal/queryplan"
)

const (
	testModelID       = "configured-model"
	testPromptVersion = extract.ExtractionPromptVersion
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
	if observation.Provider != modelpolicy.ProviderBedrock || observation.DataMode != modelpolicy.DataModePersonal || observation.ModelID != testModelID || observation.PromptVersion != testPromptVersion || observation.Outcome != OutcomeSuccess || observation.InputTokens != 11 || observation.OutputTokens != 7 || observation.TotalTokens != 18 || observation.ProviderLatency != 47*time.Millisecond || observation.Attempts != 1 {
		t.Errorf("telemetry observation = %+v", observation)
	}
}

func TestClientPlanBuildsExactStructuredConverseRequest(t *testing.T) {
	api := &fakeConverseAPI{outputs: []*bedrockruntime.ConverseOutput{successfulOutput(`{"kind":"point"}`)}}
	client := newTestClient(t, api, &recordingInvocationRecorder{}, 1)
	request := validPlanRequest()

	response, err := client.Plan(context.Background(), request)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if len(api.inputs) != 1 {
		t.Fatalf("Converse calls = %d, want 1", len(api.inputs))
	}
	want := &bedrockruntime.ConverseInput{
		ModelId: aws.String(client.modelID),
		InferenceConfig: &types.InferenceConfiguration{
			MaxTokens: aws.Int32(client.maxTokens),
		},
		System: []types.SystemContentBlock{
			&types.SystemContentBlockMemberText{Value: request.SystemPrompt},
		},
		Messages: []types.Message{{
			Role: types.ConversationRoleUser,
			Content: []types.ContentBlock{
				&types.ContentBlockMemberText{Value: request.Input},
			},
		}},
		OutputConfig: &types.OutputConfig{TextFormat: &types.OutputFormat{
			Type: types.OutputFormatTypeJsonSchema,
			Structure: &types.OutputFormatStructureMemberJsonSchema{Value: types.JsonSchemaDefinition{
				Name: aws.String(queryplan.SchemaName), Schema: aws.String(string(request.JSONSchema)),
			}},
		}},
		RequestMetadata: nil,
	}
	if !reflect.DeepEqual(api.inputs[0], want) {
		t.Fatalf("Converse input = %#v, want %#v", api.inputs[0], want)
	}
	if response.Provider != modelpolicy.ProviderBedrock || response.ModelID != testModelID ||
		response.PromptVersion != request.PromptVersion || response.SchemaName != request.SchemaName ||
		response.Usage != (queryplan.Usage{InputTokens: 11, OutputTokens: 7, TotalTokens: 18}) ||
		response.Attempts != 1 || response.WallLatency < 0 || response.ProviderLatency != 47*time.Millisecond {
		t.Fatalf("Plan response = %+v", response)
	}
}

func TestClientPlanRejectsMutatedPromptContractWithoutCallingProvider(t *testing.T) {
	tests := map[string]func(*queryplan.ModelRequest){
		"unknown version": func(request *queryplan.ModelRequest) { request.PromptVersion = "query-plan-v2" },
		"mutated prompt":  func(request *queryplan.ModelRequest) { request.SystemPrompt = "mutated" },
		"mutated schema name": func(request *queryplan.ModelRequest) {
			request.SchemaName = "mutated_schema"
		},
		"mutated schema": func(request *queryplan.ModelRequest) { request.JSONSchema = []byte(`{}`) },
		"empty input":    func(request *queryplan.ModelRequest) { request.Input = "" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			api := &fakeConverseAPI{outputs: []*bedrockruntime.ConverseOutput{successfulOutput(`{}`)}}
			client := newTestClient(t, api, &recordingInvocationRecorder{}, 2)
			request := validPlanRequest()
			mutate(&request)

			_, err := client.Plan(context.Background(), request)
			if !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("Plan() error = %v, want invalid request", err)
			}
			if len(api.inputs) != 0 {
				t.Fatalf("Converse calls = %d, want 0", len(api.inputs))
			}
			if strings.Contains(err.Error(), testPrivateInput) {
				t.Fatalf("Plan() error leaks private input: %v", err)
			}
		})
	}
}

func TestClientPlanRetriesOnlyApprovedFailuresWithImmutableRequest(t *testing.T) {
	tests := map[string]error{
		"throttling":          &smithy.GenericAPIError{Code: "ThrottlingException", Message: testPrivateOutput},
		"service unavailable": &smithy.GenericAPIError{Code: serviceUnavailableErrorCode, Message: testPrivateOutput},
		"transport timeout":   syntheticTimeoutError{},
	}
	for name, retryable := range tests {
		t.Run(name, func(t *testing.T) {
			api := &fakeConverseAPI{
				errors:  []error{retryable, nil},
				outputs: []*bedrockruntime.ConverseOutput{nil, successfulOutput(`{"kind":"point"}`)},
			}
			client := newTestClient(t, api, &recordingInvocationRecorder{}, 2)
			request := validPlanRequest()
			response, err := client.Plan(context.Background(), request)
			if err != nil {
				t.Fatalf("Plan() error = %v", err)
			}
			if len(api.inputs) != 2 || response.Attempts != 2 {
				t.Fatalf("calls/attempts = %d/%d, want 2/2", len(api.inputs), response.Attempts)
			}
			if !reflect.DeepEqual(api.inputs[0], api.inputs[1]) {
				t.Fatalf("retry request changed: first=%#v second=%#v", api.inputs[0], api.inputs[1])
			}
		})
	}
}

func TestClientPlanSnapshotsPrivateRequestBeforeRetries(t *testing.T) {
	request := validPlanRequest()
	wantSchema := string(request.JSONSchema)
	api := &fakeConverseAPI{
		errors:  []error{&smithy.GenericAPIError{Code: "ThrottlingException", Message: testPrivateOutput}, nil},
		outputs: []*bedrockruntime.ConverseOutput{nil, successfulOutput(`{"kind":"point"}`)},
	}
	api.call = func() error {
		if len(api.inputs) == 1 {
			request.SystemPrompt = "mutated after snapshot"
			request.Input = "mutated after snapshot"
			request.SchemaName = "mutated_after_snapshot"
			request.JSONSchema[0] = 'x'
		}
		return nil
	}
	client := newTestClient(t, api, &recordingInvocationRecorder{}, 2)

	if _, err := client.Plan(context.Background(), request); err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if len(api.inputs) != 2 || !reflect.DeepEqual(api.inputs[0], api.inputs[1]) {
		t.Fatalf("retry inputs = %#v, want identical snapshot", api.inputs)
	}
	structure := api.inputs[0].OutputConfig.TextFormat.Structure.(*types.OutputFormatStructureMemberJsonSchema)
	if aws.ToString(structure.Value.Schema) != wantSchema || aws.ToString(structure.Value.Name) != queryplan.SchemaName {
		t.Fatalf("planner schema changed after snapshot: %#v", structure.Value)
	}
}

func TestClientPlanDoesNotRetryTerminalFailuresOrInvalidOutput(t *testing.T) {
	invalidStop := successfulOutput(testPrivateOutput)
	invalidStop.StopReason = types.StopReasonMaxTokens
	missingUsage := successfulOutput(testPrivateOutput)
	missingUsage.Usage = nil
	tests := map[string]struct {
		errors  []error
		outputs []*bedrockruntime.ConverseOutput
		want    error
	}{
		"unrecognized credentials": {
			errors: []error{&smithy.GenericAPIError{Code: "UnrecognizedClientException", Message: testPrivateOutput}}, want: extract.ErrAuthentication,
		},
		"access denied": {
			errors: []error{&smithy.GenericAPIError{Code: "AccessDeniedException", Message: testPrivateOutput}}, want: extract.ErrAuthorization,
		},
		"invalid stop reason": {outputs: []*bedrockruntime.ConverseOutput{invalidStop}, want: ErrInvalidOutput},
		"missing usage":       {outputs: []*bedrockruntime.ConverseOutput{missingUsage}, want: ErrInvalidOutput},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			recorder := &recordingInvocationRecorder{}
			api := &fakeConverseAPI{errors: test.errors, outputs: test.outputs}
			client := newTestClient(t, api, recorder, 2)
			_, err := client.Plan(context.Background(), validPlanRequest())
			if !errors.Is(err, test.want) {
				t.Fatalf("Plan() error = %v, want %v", err, test.want)
			}
			if len(api.inputs) != 1 {
				t.Fatalf("Converse calls = %d, want 1", len(api.inputs))
			}
			if strings.Contains(err.Error(), testPrivateInput) || strings.Contains(err.Error(), testPrivateOutput) ||
				strings.Contains(fmt.Sprintf("%+v", recorder.observations), testPrivateInput) || strings.Contains(fmt.Sprintf("%+v", recorder.observations), testPrivateOutput) {
				t.Fatalf("private marker escaped error or telemetry: error=%v telemetry=%+v", err, recorder.observations)
			}
		})
	}
}

func TestClientPlanCancellationPreventsFurtherAttempts(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	api := &fakeConverseAPI{call: func() error {
		cancel()
		return &smithy.GenericAPIError{Code: "ServiceUnavailableException", Message: testPrivateOutput}
	}}
	client := newTestClient(t, api, &recordingInvocationRecorder{}, 3)

	_, err := client.Plan(ctx, validPlanRequest())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Plan() error = %v, want caller cancellation", err)
	}
	if len(api.inputs) != 1 {
		t.Fatalf("Converse calls = %d, want 1", len(api.inputs))
	}
}

func TestClientPlanTreatsProviderContextErrorsAsTerminal(t *testing.T) {
	tests := map[string]struct {
		providerErr error
		want        error
		outcome     string
	}{
		"raw deadline exceeded": {
			providerErr: context.DeadlineExceeded, want: context.DeadlineExceeded, outcome: OutcomeTimeout,
		},
		"wrapped deadline exceeded": {
			providerErr: fmt.Errorf("%s: %w", testPrivateOutput, context.DeadlineExceeded),
			want:        context.DeadlineExceeded, outcome: OutcomeTimeout,
		},
		"deadline joined with retryable API failure": {
			providerErr: errors.Join(context.DeadlineExceeded, &smithy.GenericAPIError{Code: "ThrottlingException", Message: testPrivateOutput}),
			want:        context.DeadlineExceeded, outcome: OutcomeTimeout,
		},
		"deadline joined with retryable transport failure": {
			providerErr: fmt.Errorf("%s: %w", testPrivateOutput, errors.Join(context.DeadlineExceeded, syntheticTimeoutError{})),
			want:        context.DeadlineExceeded, outcome: OutcomeTimeout,
		},
		"raw canceled": {
			providerErr: context.Canceled, want: context.Canceled, outcome: OutcomeCanceled,
		},
		"wrapped canceled": {
			providerErr: fmt.Errorf("%s: %w", testPrivateOutput, context.Canceled),
			want:        context.Canceled, outcome: OutcomeCanceled,
		},
		"canceled joined with retryable API failure": {
			providerErr: errors.Join(context.Canceled, &smithy.GenericAPIError{Code: serviceUnavailableErrorCode, Message: testPrivateOutput}),
			want:        context.Canceled, outcome: OutcomeCanceled,
		},
		"canceled joined with retryable transport failure": {
			providerErr: fmt.Errorf("%s: %w", testPrivateOutput, errors.Join(context.Canceled, syntheticTimeoutError{})),
			want:        context.Canceled, outcome: OutcomeCanceled,
		},
		"canceled takes precedence over deadline": {
			providerErr: errors.Join(context.DeadlineExceeded, context.Canceled,
				&smithy.GenericAPIError{Code: "ThrottlingException", Message: testPrivateOutput}),
			want: context.Canceled, outcome: OutcomeCanceled,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			api := &fakeConverseAPI{
				errors:  []error{test.providerErr, nil},
				outputs: []*bedrockruntime.ConverseOutput{nil, successfulOutput(`{}`)},
			}
			retryer := &plannerPolicyRetryer{zeroRetryer: zeroRetryer{maxAttempts: 2}}
			recorder := &recordingInvocationRecorder{}
			client, err := newClient(api, Options{
				DataMode: modelpolicy.DataModePersonal, ModelID: testModelID, MaxTokens: 321, MaxAttempts: 2, Recorder: recorder,
			}, retryer)
			if err != nil {
				t.Fatalf("newClient() error = %v", err)
			}

			_, err = client.Plan(context.Background(), validPlanRequest())
			if !errors.Is(err, ErrInvocation) {
				t.Fatalf("Plan() error = %v, want terminal invocation error", err)
			}
			if !errors.Is(err, test.want) {
				t.Fatalf("Plan() error = %v, want sentinel %v", err, test.want)
			}
			if test.want == context.Canceled && errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("Plan() error = %v, want cancellation to take precedence over deadline", err)
			}
			if len(api.inputs) != 1 {
				t.Fatalf("Converse calls = %d, want 1", len(api.inputs))
			}
			if retryer.retryableCalls != 0 || retryer.retryTokenCalls != 0 || retryer.retryDelayCalls != 0 {
				t.Fatalf("retry policy calls = decision:%d token:%d delay:%d, want 0/0/0",
					retryer.retryableCalls, retryer.retryTokenCalls, retryer.retryDelayCalls)
			}
			if len(recorder.observations) != 1 || recorder.observations[0].Attempts != 1 ||
				recorder.observations[0].Outcome != test.outcome {
				t.Fatalf("telemetry = %+v, want one %s observation at attempt 1", recorder.observations, test.outcome)
			}
			telemetry := fmt.Sprintf("%+v", recorder.observations)
			if strings.Contains(err.Error(), testPrivateInput) || strings.Contains(err.Error(), testPrivateOutput) ||
				strings.Contains(telemetry, testPrivateInput) || strings.Contains(telemetry, testPrivateOutput) {
				t.Fatalf("private/provider marker escaped error or telemetry: error=%v telemetry=%s", err, telemetry)
			}
		})
	}
}

func TestClientPlanRecordsCallerContextOutcomeAtEachLifecycleBoundary(t *testing.T) {
	contextCases := map[string]struct {
		err     error
		outcome string
	}{
		"deadline":     {err: context.DeadlineExceeded, outcome: OutcomeTimeout},
		"cancellation": {err: context.Canceled, outcome: OutcomeCanceled},
	}
	boundaries := map[string]struct {
		wantCalls    int
		wantAttempts int
		configure    func(*controlledContext, *fakeConverseAPI, *plannerPolicyRetryer)
	}{
		"before invocation": {
			wantCalls: 0, wantAttempts: 0,
			configure: func(ctx *controlledContext, _ *fakeConverseAPI, _ *plannerPolicyRetryer) {
				ctx.fail()
			},
		},
		"after provider attempt": {
			wantCalls: 1, wantAttempts: 1,
			configure: func(ctx *controlledContext, api *fakeConverseAPI, _ *plannerPolicyRetryer) {
				api.call = func() error {
					ctx.fail()
					return nil
				}
			},
		},
		"during retry wait": {
			wantCalls: 1, wantAttempts: 1,
			configure: func(ctx *controlledContext, _ *fakeConverseAPI, retryer *plannerPolicyRetryer) {
				retryer.retryDelayHook = ctx.fail
			},
		},
	}
	for contextName, contextCase := range contextCases {
		for boundaryName, boundary := range boundaries {
			t.Run(contextName+"/"+boundaryName, func(t *testing.T) {
				ctx := newControlledContext(contextCase.err)
				api := &fakeConverseAPI{
					errors: []error{&smithy.GenericAPIError{Code: serviceUnavailableErrorCode, Message: testPrivateOutput}},
				}
				retryer := &plannerPolicyRetryer{zeroRetryer: zeroRetryer{maxAttempts: 2}}
				recorder := &recordingInvocationRecorder{}
				client, err := newClient(api, Options{
					DataMode: modelpolicy.DataModePersonal, ModelID: testModelID, MaxTokens: 321, MaxAttempts: 2, Recorder: recorder,
				}, retryer)
				if err != nil {
					t.Fatalf("newClient() error = %v", err)
				}
				boundary.configure(ctx, api, retryer)

				_, err = client.Plan(ctx, validPlanRequest())
				if !errors.Is(err, contextCase.err) {
					t.Fatalf("Plan() error = %v, want %v", err, contextCase.err)
				}
				if len(api.inputs) != boundary.wantCalls {
					t.Fatalf("Converse calls = %d, want %d", len(api.inputs), boundary.wantCalls)
				}
				if len(recorder.observations) != 1 || recorder.observations[0].Outcome != contextCase.outcome ||
					recorder.observations[0].Attempts != boundary.wantAttempts {
					t.Fatalf("telemetry = %+v, want outcome=%s attempts=%d",
						recorder.observations, contextCase.outcome, boundary.wantAttempts)
				}
				telemetry := fmt.Sprintf("%+v", recorder.observations)
				if strings.Contains(err.Error(), testPrivateInput) || strings.Contains(err.Error(), testPrivateOutput) ||
					strings.Contains(telemetry, testPrivateInput) || strings.Contains(telemetry, testPrivateOutput) {
					t.Fatalf("private/provider marker escaped error or telemetry: error=%v telemetry=%s", err, telemetry)
				}
			})
		}
	}
}

func TestClientPlanPreservesCallerContextAtAdaptiveTokenBoundaries(t *testing.T) {
	contextCases := map[string]struct {
		err     error
		outcome string
	}{
		"deadline":     {err: context.DeadlineExceeded, outcome: OutcomeTimeout},
		"cancellation": {err: context.Canceled, outcome: OutcomeCanceled},
	}
	boundaries := map[string]struct {
		wantCalls    int
		wantAttempts int
		configure    func(*controlledContext, *plannerPolicyRetryer)
	}{
		"attempt token": {
			wantCalls: 0, wantAttempts: 0,
			configure: func(ctx *controlledContext, retryer *plannerPolicyRetryer) {
				retryer.attemptTokenHook = ctx.fail
				retryer.attemptTokenErr = errors.New(testPrivateOutput)
			},
		},
		"retry token": {
			wantCalls: 1, wantAttempts: 1,
			configure: func(ctx *controlledContext, retryer *plannerPolicyRetryer) {
				retryer.retryTokenHook = ctx.fail
				retryer.retryTokenErr = errors.New(testPrivateOutput)
			},
		},
	}
	for contextName, contextCase := range contextCases {
		for boundaryName, boundary := range boundaries {
			t.Run(contextName+"/"+boundaryName, func(t *testing.T) {
				ctx := newControlledContext(contextCase.err)
				api := &fakeConverseAPI{
					errors: []error{&smithy.GenericAPIError{Code: "ThrottlingException", Message: testPrivateOutput}},
				}
				retryer := &plannerPolicyRetryer{zeroRetryer: zeroRetryer{maxAttempts: 2}}
				boundary.configure(ctx, retryer)
				recorder := &recordingInvocationRecorder{}
				client, err := newClient(api, Options{
					DataMode: modelpolicy.DataModePersonal, ModelID: testModelID, MaxTokens: 321, MaxAttempts: 2, Recorder: recorder,
				}, retryer)
				if err != nil {
					t.Fatalf("newClient() error = %v", err)
				}

				_, err = client.Plan(ctx, validPlanRequest())
				if !errors.Is(err, ErrInvocation) {
					t.Fatalf("Plan() error = %v, want invocation error", err)
				}
				if !errors.Is(err, contextCase.err) {
					t.Fatalf("Plan() error = %v, want caller context %v", err, contextCase.err)
				}
				if contextCase.err == context.Canceled && errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("Plan() error = %v, want cancellation to take precedence over deadline", err)
				}
				if len(api.inputs) != boundary.wantCalls {
					t.Fatalf("Converse calls = %d, want %d", len(api.inputs), boundary.wantCalls)
				}
				if len(recorder.observations) != 1 || recorder.observations[0].Outcome != contextCase.outcome ||
					recorder.observations[0].Attempts != boundary.wantAttempts {
					t.Fatalf("telemetry = %+v, want outcome=%s attempts=%d",
						recorder.observations, contextCase.outcome, boundary.wantAttempts)
				}
				telemetry := fmt.Sprintf("%+v", recorder.observations)
				if strings.Contains(err.Error(), testPrivateInput) || strings.Contains(err.Error(), testPrivateOutput) ||
					strings.Contains(telemetry, testPrivateInput) || strings.Contains(telemetry, testPrivateOutput) {
					t.Fatalf("private/provider marker escaped error or telemetry: error=%v telemetry=%s", err, telemetry)
				}
			})
		}
	}
}

func TestClientPlanStopsBeforeConverseWhenContextEndsDuringAttemptTokenAcquisition(t *testing.T) {
	contextCases := map[string]struct {
		err     error
		outcome string
	}{
		"deadline":     {err: context.DeadlineExceeded, outcome: OutcomeTimeout},
		"cancellation": {err: context.Canceled, outcome: OutcomeCanceled},
	}
	for name, contextCase := range contextCases {
		t.Run(name, func(t *testing.T) {
			ctx := newControlledContext(contextCase.err)
			var attemptTokenReleases []error
			retryer := &plannerPolicyRetryer{
				zeroRetryer:      zeroRetryer{maxAttempts: 2},
				attemptTokenHook: ctx.fail,
				attemptTokenRelease: func(err error) error {
					attemptTokenReleases = append(attemptTokenReleases, err)
					return nil
				},
			}
			api := &fakeConverseAPI{}
			recorder := &recordingInvocationRecorder{}
			client, err := newClient(api, Options{
				DataMode: modelpolicy.DataModePersonal, ModelID: testModelID, MaxTokens: 321, MaxAttempts: 2, Recorder: recorder,
			}, retryer)
			if err != nil {
				t.Fatalf("newClient() error = %v", err)
			}

			_, err = client.Plan(ctx, validPlanRequest())
			if !errors.Is(err, ErrInvocation) || !errors.Is(err, contextCase.err) {
				t.Fatalf("Plan() error = %v, want invocation wrapping %v", err, contextCase.err)
			}
			if len(api.inputs) != 0 {
				t.Fatalf("Converse calls = %d, want 0", len(api.inputs))
			}
			if len(recorder.observations) != 1 || recorder.observations[0].Outcome != contextCase.outcome ||
				recorder.observations[0].Attempts != 0 {
				t.Fatalf("telemetry = %+v, want outcome=%s attempts=0", recorder.observations, contextCase.outcome)
			}
			if len(attemptTokenReleases) != 1 || !errors.Is(attemptTokenReleases[0], contextCase.err) {
				t.Fatalf("attempt-token releases = %v, want one canonical %v release", attemptTokenReleases, contextCase.err)
			}
			telemetry := fmt.Sprintf("%+v", recorder.observations)
			if strings.Contains(err.Error(), testPrivateInput) || strings.Contains(err.Error(), testPrivateOutput) ||
				strings.Contains(telemetry, testPrivateInput) || strings.Contains(telemetry, testPrivateOutput) {
				t.Fatalf("private/provider marker escaped error or telemetry: error=%v telemetry=%s", err, telemetry)
			}
		})
	}
}

func TestClientPlanReleasesOutstandingTokensWhenContextEndsAfterLaterAttemptToken(t *testing.T) {
	contextCases := map[string]struct {
		err     error
		outcome string
	}{
		"deadline":     {err: context.DeadlineExceeded, outcome: OutcomeTimeout},
		"cancellation": {err: context.Canceled, outcome: OutcomeCanceled},
	}
	for name, contextCase := range contextCases {
		t.Run(name, func(t *testing.T) {
			ctx := newControlledContext(contextCase.err)
			var attemptTokenReleases []error
			var retryTokenReleases []error
			retryer := &plannerPolicyRetryer{
				zeroRetryer: zeroRetryer{maxAttempts: 2},
				attemptTokenRelease: func(err error) error {
					attemptTokenReleases = append(attemptTokenReleases, err)
					return nil
				},
				retryTokenRelease: func(err error) error {
					retryTokenReleases = append(retryTokenReleases, err)
					return nil
				},
			}
			retryer.attemptTokenHook = func() {
				if retryer.attemptTokenCalls == 2 {
					ctx.fail()
				}
			}
			api := &fakeConverseAPI{
				errors: []error{&smithy.GenericAPIError{Code: "ThrottlingException", Message: testPrivateOutput}},
			}
			recorder := &recordingInvocationRecorder{}
			client, err := newClient(api, Options{
				DataMode: modelpolicy.DataModePersonal, ModelID: testModelID, MaxTokens: 321, MaxAttempts: 2, Recorder: recorder,
			}, retryer)
			if err != nil {
				t.Fatalf("newClient() error = %v", err)
			}

			_, err = client.Plan(ctx, validPlanRequest())
			if !errors.Is(err, ErrInvocation) || !errors.Is(err, contextCase.err) {
				t.Fatalf("Plan() error = %v, want invocation wrapping %v", err, contextCase.err)
			}
			if len(api.inputs) != 1 {
				t.Fatalf("Converse calls = %d, want 1", len(api.inputs))
			}
			if len(recorder.observations) != 1 || recorder.observations[0].Outcome != contextCase.outcome ||
				recorder.observations[0].Attempts != 1 {
				t.Fatalf("telemetry = %+v, want outcome=%s attempts=1", recorder.observations, contextCase.outcome)
			}
			if len(attemptTokenReleases) != 2 || !errors.Is(attemptTokenReleases[1], contextCase.err) {
				t.Fatalf("attempt-token releases = %v, want later canonical %v release", attemptTokenReleases, contextCase.err)
			}
			if len(retryTokenReleases) != 1 || !errors.Is(retryTokenReleases[0], contextCase.err) {
				t.Fatalf("retry-token releases = %v, want one canonical %v release", retryTokenReleases, contextCase.err)
			}
			telemetry := fmt.Sprintf("%+v", recorder.observations)
			if strings.Contains(err.Error(), testPrivateInput) || strings.Contains(err.Error(), testPrivateOutput) ||
				strings.Contains(telemetry, testPrivateInput) || strings.Contains(telemetry, testPrivateOutput) {
				t.Fatalf("private/provider marker escaped error or telemetry: error=%v telemetry=%s", err, telemetry)
			}
		})
	}
}

func TestClientPlanStopsBeforeRetryDelayWhenContextEndsDuringRetryTokenAcquisition(t *testing.T) {
	contextCases := map[string]struct {
		err     error
		outcome string
	}{
		"deadline":     {err: context.DeadlineExceeded, outcome: OutcomeTimeout},
		"cancellation": {err: context.Canceled, outcome: OutcomeCanceled},
	}
	for name, contextCase := range contextCases {
		t.Run(name, func(t *testing.T) {
			ctx := newControlledContext(contextCase.err)
			var retryTokenReleases []error
			retryer := &plannerPolicyRetryer{
				zeroRetryer:    zeroRetryer{maxAttempts: 2},
				retryTokenHook: ctx.fail,
				retryTokenRelease: func(err error) error {
					retryTokenReleases = append(retryTokenReleases, err)
					return nil
				},
			}
			api := &fakeConverseAPI{
				errors: []error{&smithy.GenericAPIError{Code: "ThrottlingException", Message: testPrivateOutput}},
			}
			recorder := &recordingInvocationRecorder{}
			client, err := newClient(api, Options{
				DataMode: modelpolicy.DataModePersonal, ModelID: testModelID, MaxTokens: 321, MaxAttempts: 2, Recorder: recorder,
			}, retryer)
			if err != nil {
				t.Fatalf("newClient() error = %v", err)
			}

			_, err = client.Plan(ctx, validPlanRequest())
			if !errors.Is(err, ErrInvocation) || !errors.Is(err, contextCase.err) {
				t.Fatalf("Plan() error = %v, want invocation wrapping %v", err, contextCase.err)
			}
			if len(api.inputs) != 1 {
				t.Fatalf("Converse calls = %d, want 1", len(api.inputs))
			}
			if retryer.retryDelayCalls != 0 {
				t.Fatalf("RetryDelay calls = %d, want 0", retryer.retryDelayCalls)
			}
			if len(retryTokenReleases) != 1 || !errors.Is(retryTokenReleases[0], contextCase.err) {
				t.Fatalf("retry-token releases = %v, want one canonical %v release", retryTokenReleases, contextCase.err)
			}
			if len(recorder.observations) != 1 || recorder.observations[0].Outcome != contextCase.outcome ||
				recorder.observations[0].Attempts != 1 {
				t.Fatalf("telemetry = %+v, want outcome=%s attempts=1", recorder.observations, contextCase.outcome)
			}
			telemetry := fmt.Sprintf("%+v", recorder.observations)
			if strings.Contains(err.Error(), testPrivateInput) || strings.Contains(err.Error(), testPrivateOutput) ||
				strings.Contains(telemetry, testPrivateInput) || strings.Contains(telemetry, testPrivateOutput) {
				t.Fatalf("private/provider marker escaped error or telemetry: error=%v telemetry=%s", err, telemetry)
			}
		})
	}
}

func TestClientPlanBoundsRetryPolicyFailures(t *testing.T) {
	tests := map[string]struct {
		retryer   aws.RetryerV2
		wantCalls int
	}{
		"attempt token": {
			retryer: &plannerPolicyRetryer{zeroRetryer: zeroRetryer{maxAttempts: 2}, attemptTokenErr: errors.New("attempt token marker")},
		},
		"retry token": {
			retryer:   &plannerPolicyRetryer{zeroRetryer: zeroRetryer{maxAttempts: 2}, retryTokenErr: errors.New("retry token marker")},
			wantCalls: 1,
		},
		"retry delay": {
			retryer:   &plannerPolicyRetryer{zeroRetryer: zeroRetryer{maxAttempts: 2}, retryDelayErr: errors.New("retry delay marker")},
			wantCalls: 1,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			api := &fakeConverseAPI{errors: []error{&smithy.GenericAPIError{Code: "ThrottlingException", Message: testPrivateOutput}}}
			client, err := newClient(api, Options{
				DataMode: modelpolicy.DataModePersonal, ModelID: testModelID, MaxTokens: 321, MaxAttempts: 2,
			}, test.retryer)
			if err != nil {
				t.Fatalf("newClient() error = %v", err)
			}

			_, err = client.Plan(context.Background(), validPlanRequest())
			if !errors.Is(err, ErrInvocation) || strings.Contains(err.Error(), "marker") || strings.Contains(err.Error(), testPrivateOutput) {
				t.Fatalf("Plan() error = %v, want bounded retry-policy failure", err)
			}
			if len(api.inputs) != test.wantCalls {
				t.Fatalf("Converse calls = %d, want %d", len(api.inputs), test.wantCalls)
			}
		})
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
	if len(spans) != 1 || spans[0].Name != "stacks.model.generate" || spans[0].Status.Code != codes.Ok {
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
		DataMode: modelpolicy.DataModePersonal, ModelID: testModelID, MaxTokens: 321, MaxAttempts: 3,
	})
	if err == nil || !strings.Contains(err.Error(), "region") {
		t.Fatalf("NewFromConfig() error = %v, want explicit region rejection", err)
	}
}

func TestNewFromConfigRejectsPaddedRegion(t *testing.T) {
	_, err := NewFromConfig(aws.Config{Region: " us-east-1 "}, Options{
		DataMode: modelpolicy.DataModePersonal, ModelID: testModelID, MaxTokens: 321, MaxAttempts: 3,
	})
	if err == nil || !strings.Contains(err.Error(), "region") {
		t.Fatalf("NewFromConfig() error = %v, want padded region rejection", err)
	}
}

func TestNewFromConfigRequiresDataModeValidForNewInvocation(t *testing.T) {
	_, err := NewFromConfig(aws.Config{Region: "us-east-1"}, Options{
		DataMode: modelpolicy.DataModeLegacy, ModelID: testModelID, MaxTokens: 321, MaxAttempts: 3,
	})
	if err == nil || !strings.Contains(err.Error(), "data mode") {
		t.Fatalf("NewFromConfig() error = %v, want invalid data mode rejection", err)
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
				DataMode: modelpolicy.DataModePersonal, ModelID: testModelID, MaxTokens: 321, MaxAttempts: test.maxAttempts,
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
			if len(recorder.observations) != 1 || recorder.observations[0].Outcome != OutcomeInvalidRequest || recorder.observations[0].PromptVersion != "invalid" {
				t.Fatalf("telemetry = %+v, want bounded invalid request", recorder.observations)
			}
		})
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
			client, err := newWithAPI(api, Options{DataMode: modelpolicy.DataModePersonal, ModelID: testModelID, MaxTokens: 321, MaxAttempts: 3})
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
			client, err := newWithAPI(api, Options{DataMode: modelpolicy.DataModePersonal, ModelID: testModelID, MaxTokens: 321, MaxAttempts: 3})
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

func validPlanRequest() queryplan.ModelRequest {
	contract, err := queryplan.PromptContract(queryplan.PromptVersion)
	if err != nil {
		panic(err)
	}
	return queryplan.ModelRequest{
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
	observations []modeltelemetry.Observation
}

func (recorder *recordingInvocationRecorder) Record(_ context.Context, observation modeltelemetry.Observation) {
	recorder.observations = append(recorder.observations, observation)
}

func newTestClient(t *testing.T, api converseAPI, recorder modeltelemetry.Recorder, maxAttempts int) *Client {
	t.Helper()
	client, err := newClient(api, Options{
		DataMode: modelpolicy.DataModePersonal, ModelID: testModelID, MaxTokens: 321, MaxAttempts: maxAttempts, Recorder: recorder,
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

type plannerPolicyRetryer struct {
	zeroRetryer
	attemptTokenErr     error
	attemptTokenHook    func()
	attemptTokenRelease func(error) error
	retryTokenErr       error
	retryTokenHook      func()
	retryTokenRelease   func(error) error
	retryDelayErr       error
	retryDelayHook      func()
	retryableCalls      int
	attemptTokenCalls   int
	retryTokenCalls     int
	retryDelayCalls     int
}

func (retryer *plannerPolicyRetryer) IsErrorRetryable(err error) bool {
	retryer.retryableCalls++
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	return retryer.zeroRetryer.IsErrorRetryable(err)
}

func (retryer *plannerPolicyRetryer) GetAttemptToken(context.Context) (func(error) error, error) {
	retryer.attemptTokenCalls++
	if retryer.attemptTokenHook != nil {
		retryer.attemptTokenHook()
	}
	if retryer.attemptTokenErr != nil {
		return nil, retryer.attemptTokenErr
	}
	if retryer.attemptTokenRelease != nil {
		return retryer.attemptTokenRelease, nil
	}
	return func(error) error { return nil }, nil
}

func (retryer *plannerPolicyRetryer) GetRetryToken(context.Context, error) (func(error) error, error) {
	retryer.retryTokenCalls++
	if retryer.retryTokenHook != nil {
		retryer.retryTokenHook()
	}
	if retryer.retryTokenErr != nil {
		return nil, retryer.retryTokenErr
	}
	if retryer.retryTokenRelease != nil {
		return retryer.retryTokenRelease, nil
	}
	return func(error) error { return nil }, nil
}

func (retryer *plannerPolicyRetryer) RetryDelay(attempt int, err error) (time.Duration, error) {
	retryer.retryDelayCalls++
	if retryer.retryDelayHook != nil {
		retryer.retryDelayHook()
	}
	if retryer.retryDelayErr != nil {
		return 0, retryer.retryDelayErr
	}
	return retryer.zeroRetryer.RetryDelay(attempt, err)
}

var _ aws.RetryerV2 = (*plannerPolicyRetryer)(nil)

type controlledContext struct {
	context.Context
	done chan struct{}
	err  error
}

func newControlledContext(err error) *controlledContext {
	return &controlledContext{Context: context.Background(), done: make(chan struct{}), err: err}
}

func (ctx *controlledContext) Done() <-chan struct{} {
	return ctx.done
}

func (ctx *controlledContext) Err() error {
	select {
	case <-ctx.done:
		return ctx.err
	default:
		return nil
	}
}

func (ctx *controlledContext) fail() {
	close(ctx.done)
}

type syntheticTimeoutError struct{}

func (syntheticTimeoutError) Error() string   { return "synthetic timeout" }
func (syntheticTimeoutError) Timeout() bool   { return true }
func (syntheticTimeoutError) Temporary() bool { return true }
