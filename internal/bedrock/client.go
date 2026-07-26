package bedrock

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsretry "github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/aws/smithy-go"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"stacks/internal/extract"
	"stacks/internal/modelpolicy"
	"stacks/internal/modeltelemetry"
	"stacks/internal/observability"
)

const (
	// MaxAttemptsLimit matches the configured application default and prevents retry
	// amplification from an unexpectedly large runtime value.
	MaxAttemptsLimit = 5

	OutcomeSuccess        = modeltelemetry.OutcomeSuccess
	OutcomeThrottled      = modeltelemetry.OutcomeThrottled
	OutcomeTimeout        = modeltelemetry.OutcomeTimeout
	OutcomeUnavailable    = modeltelemetry.OutcomeUnavailable
	OutcomeInternal       = modeltelemetry.OutcomeInternal
	OutcomeAuthentication = modeltelemetry.OutcomeAuthentication
	OutcomeAccessDenied   = modeltelemetry.OutcomeAccessDenied
	OutcomeNotFound       = modeltelemetry.OutcomeNotFound
	OutcomeInvalidRequest = modeltelemetry.OutcomeInvalidRequest
	OutcomeInvalidOutput  = modeltelemetry.OutcomeInvalidOutput
	OutcomeCanceled       = modeltelemetry.OutcomeCanceled
	OutcomeProviderError  = modeltelemetry.OutcomeProviderError
)

var (
	ErrInvalidRequest = errors.New("bedrock request is invalid")
	ErrInvalidOutput  = errors.New("bedrock output is invalid")
	ErrInvocation     = errors.New("bedrock invocation failed")
)

const (
	modelTimeoutErrorCode                  = "ModelTimeoutException"
	requestTimeoutErrorCode                = "RequestTimeout"
	requestTimeoutExceptionErrorCode       = "RequestTimeoutException"
	serviceUnavailableErrorCode            = "ServiceUnavailableException"
	internalServerErrorCode                = "InternalServerException"
	maxLatencyMilliseconds           int64 = (1<<63 - 1) / int64(time.Millisecond)
	invocationSpanName                     = "stacks.model.generate"
)

type converseAPI interface {
	Converse(context.Context, *bedrockruntime.ConverseInput, ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseOutput, error)
}

// Options contains required, validated runtime policy.
type Options struct {
	ModelID     string
	DataMode    modelpolicy.DataMode
	MaxTokens   int
	MaxAttempts int
	Recorder    modeltelemetry.Recorder
	Tracer      trace.Tracer
}

// Client implements extract.Model with Bedrock Runtime Converse structured
// output. It owns retries so the wrapped SDK client must not add another retry
// layer.
type Client struct {
	api       converseAPI
	modelID   string
	dataMode  modelpolicy.DataMode
	maxTokens int32
	retryer   aws.Retryer
	recorder  modeltelemetry.Recorder
	tracer    trace.Tracer
	now       func() time.Time
}

var _ extract.Model = (*Client)(nil)

func newWithAPI(api converseAPI, options Options) (*Client, error) {
	adaptiveRetryer := awsretry.NewAdaptiveMode(func(adaptive *awsretry.AdaptiveModeOptions) {
		adaptive.StandardOptions = append(adaptive.StandardOptions, func(standard *awsretry.StandardOptions) {
			standard.MaxAttempts = options.MaxAttempts
		})
	})
	return newClient(api, options, &exactAdaptiveRetryer{RetryerV2: adaptiveRetryer})
}

// NewFromConfig creates a Runtime client with SDK retries disabled because
// Client owns the bounded adaptive retry lifecycle.
func NewFromConfig(configuration aws.Config, options Options) (*Client, error) {
	region := strings.TrimSpace(configuration.Region)
	if region == "" || region != configuration.Region {
		return nil, fmt.Errorf("create Bedrock client: AWS region is required")
	}
	configuration.Retryer = func() aws.Retryer { return aws.NopRetryer{} }
	return newWithAPI(bedrockruntime.NewFromConfig(configuration), options)
}

func newClient(api converseAPI, options Options, retryer aws.Retryer) (*Client, error) {
	if api == nil || retryer == nil {
		return nil, fmt.Errorf("create Bedrock client: dependencies are required")
	}
	modelID := strings.TrimSpace(options.ModelID)
	if modelID == "" {
		return nil, fmt.Errorf("create Bedrock client: model ID is required")
	}
	if !options.DataMode.ValidForNewRun() {
		return nil, fmt.Errorf("create Bedrock client: data mode is invalid")
	}
	if options.MaxTokens <= 0 || options.MaxTokens > math.MaxInt32 {
		return nil, fmt.Errorf("create Bedrock client: maximum output tokens are invalid")
	}
	if options.MaxAttempts <= 0 || options.MaxAttempts > MaxAttemptsLimit || retryer.MaxAttempts() != options.MaxAttempts {
		return nil, fmt.Errorf("create Bedrock client: maximum attempts are invalid")
	}
	tracer := options.Tracer
	if tracer == nil {
		tracer = tracenoop.NewTracerProvider().Tracer("stacks")
	}
	return &Client{
		api: api, modelID: modelID, dataMode: options.DataMode, maxTokens: int32(options.MaxTokens),
		retryer: retryer, recorder: options.Recorder, tracer: tracer, now: time.Now,
	}, nil
}

// Generate invokes Converse and returns untrusted structured JSON plus bounded
// metadata. It never includes private request or response data in errors or
// telemetry.
func (client *Client) Generate(ctx context.Context, request extract.Request) (response extract.Response, resultErr error) {
	ctx, span := client.tracer.Start(ctx, invocationSpanName)
	started := client.now()
	defer func() { observability.FinishSpan(span, resultErr) }()

	input, err := client.converseInput(request)
	if err != nil {
		client.record(ctx, started, "", OutcomeInvalidRequest, extract.Usage{}, 0, 0)
		return extract.Response{}, err
	}

	var retryToken func(error) error
	for attempt := 1; attempt <= client.retryer.MaxAttempts(); attempt++ {
		if err := ctx.Err(); err != nil {
			if retryToken != nil {
				_ = retryToken(err)
			}
			client.record(ctx, started, request.PromptVersion, OutcomeCanceled, extract.Usage{}, 0, attempt-1)
			return extract.Response{}, fmt.Errorf("%w: %w", ErrInvocation, err)
		}

		attemptToken, tokenErr := getAttemptToken(ctx, client.retryer)
		if tokenErr != nil {
			if retryToken != nil {
				_ = retryToken(tokenErr)
			}
			client.record(ctx, started, request.PromptVersion, outcomeForError(tokenErr), extract.Usage{}, 0, attempt-1)
			return extract.Response{}, fmt.Errorf("%w: retry policy", ErrInvocation)
		}
		output, invokeErr := client.api.Converse(ctx, input)
		_ = attemptToken(invokeErr)
		if retryToken != nil {
			_ = retryToken(invokeErr)
			retryToken = nil
		}

		if invokeErr == nil {
			response, outputErr := client.response(request.PromptVersion, output)
			if outputErr != nil {
				usage, latency := boundedMetadata(output)
				client.record(ctx, started, request.PromptVersion, OutcomeInvalidOutput, usage, latency, attempt)
				return extract.Response{}, outputErr
			}
			client.record(ctx, started, request.PromptVersion, OutcomeSuccess, response.Usage, response.Latency, attempt)
			return response, nil
		}

		if ctx.Err() != nil {
			client.record(ctx, started, request.PromptVersion, OutcomeCanceled, extract.Usage{}, 0, attempt)
			return extract.Response{}, fmt.Errorf("%w: %w", ErrInvocation, ctx.Err())
		}
		if attempt == client.retryer.MaxAttempts() || !client.retryer.IsErrorRetryable(invokeErr) {
			outcome := outcomeForError(invokeErr)
			client.record(ctx, started, request.PromptVersion, outcome, extract.Usage{}, 0, attempt)
			return extract.Response{}, boundedInvocationError(invokeErr, outcome)
		}

		retryToken, tokenErr = client.retryer.GetRetryToken(ctx, invokeErr)
		if tokenErr != nil {
			client.record(ctx, started, request.PromptVersion, outcomeForError(invokeErr), extract.Usage{}, 0, attempt)
			return extract.Response{}, fmt.Errorf("%w: retry policy", ErrInvocation)
		}
		delay, delayErr := client.retryer.RetryDelay(attempt, invokeErr)
		if delayErr != nil {
			_ = retryToken(delayErr)
			retryToken = nil
			client.record(ctx, started, request.PromptVersion, outcomeForError(invokeErr), extract.Usage{}, 0, attempt)
			return extract.Response{}, fmt.Errorf("%w: retry policy", ErrInvocation)
		}
		if err := wait(ctx, delay); err != nil {
			_ = retryToken(err)
			retryToken = nil
			client.record(ctx, started, request.PromptVersion, OutcomeCanceled, extract.Usage{}, 0, attempt)
			return extract.Response{}, fmt.Errorf("%w: %w", ErrInvocation, err)
		}
	}
	return extract.Response{}, fmt.Errorf("%w: retry policy", ErrInvocation)
}

func boundedMetadata(output *bedrockruntime.ConverseOutput) (extract.Usage, time.Duration) {
	if output == nil {
		return extract.Usage{}, 0
	}
	var usage extract.Usage
	if output.Usage != nil {
		if value := aws.ToInt32(output.Usage.InputTokens); value >= 0 {
			usage.InputTokens = int64(value)
		}
		if value := aws.ToInt32(output.Usage.OutputTokens); value >= 0 {
			usage.OutputTokens = int64(value)
		}
		if value := aws.ToInt32(output.Usage.TotalTokens); value >= 0 {
			usage.TotalTokens = int64(value)
		}
	}
	var latency time.Duration
	if output.Metrics != nil {
		if value := aws.ToInt64(output.Metrics.LatencyMs); value >= 0 && value <= maxLatencyMilliseconds {
			latency = time.Duration(value) * time.Millisecond
		}
	}
	return usage, latency
}

func (client *Client) converseInput(request extract.Request) (*bedrockruntime.ConverseInput, error) {
	contract, err := extract.PromptContract(request.PromptVersion)
	if err != nil || request.SystemPrompt != contract.SystemPrompt || request.SchemaName != contract.SchemaName || !bytes.Equal(request.JSONSchema, contract.JSONSchema) {
		return nil, ErrInvalidRequest
	}
	if request.Input == "" || !json.Valid(request.JSONSchema) {
		return nil, ErrInvalidRequest
	}
	schema := string(request.JSONSchema)
	return &bedrockruntime.ConverseInput{
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
				Name: aws.String(request.SchemaName), Schema: aws.String(schema),
			}},
		}},
		RequestMetadata: nil,
	}, nil
}

func (client *Client) response(promptVersion string, output *bedrockruntime.ConverseOutput) (extract.Response, error) {
	if output == nil || output.StopReason != types.StopReasonEndTurn || output.Metrics == nil || output.Metrics.LatencyMs == nil || output.Usage == nil || output.Usage.InputTokens == nil || output.Usage.OutputTokens == nil || output.Usage.TotalTokens == nil {
		return extract.Response{}, ErrInvalidOutput
	}
	message, ok := output.Output.(*types.ConverseOutputMemberMessage)
	if !ok || message.Value.Role != types.ConversationRoleAssistant || len(message.Value.Content) != 1 {
		return extract.Response{}, ErrInvalidOutput
	}
	text, ok := message.Value.Content[0].(*types.ContentBlockMemberText)
	if !ok || !json.Valid([]byte(text.Value)) {
		return extract.Response{}, ErrInvalidOutput
	}
	latencyMillis := aws.ToInt64(output.Metrics.LatencyMs)
	inputTokens := aws.ToInt32(output.Usage.InputTokens)
	outputTokens := aws.ToInt32(output.Usage.OutputTokens)
	totalTokens := aws.ToInt32(output.Usage.TotalTokens)
	if latencyMillis < 0 || latencyMillis > maxLatencyMilliseconds || inputTokens < 0 || outputTokens < 0 || totalTokens < 0 {
		return extract.Response{}, ErrInvalidOutput
	}
	usage := extract.Usage{InputTokens: int64(inputTokens), OutputTokens: int64(outputTokens), TotalTokens: int64(totalTokens)}
	return extract.Response{
		Output:        append(json.RawMessage(nil), text.Value...),
		Usage:         usage,
		Latency:       time.Duration(latencyMillis) * time.Millisecond,
		ModelID:       client.modelID,
		PromptVersion: promptVersion,
		Outcome:       OutcomeSuccess,
	}, nil
}

func (client *Client) record(ctx context.Context, started time.Time, promptVersion, outcome string, usage extract.Usage, providerLatency time.Duration, attempts int) {
	if client.recorder == nil {
		return
	}
	wallLatency := client.now().Sub(started)
	if wallLatency < 0 {
		wallLatency = 0
	}
	promptVersion = strings.TrimSpace(promptVersion)
	if promptVersion == "" {
		promptVersion = "invalid"
	}
	client.recorder.Record(ctx, modeltelemetry.Observation{
		Provider: modelpolicy.ProviderBedrock, DataMode: client.dataMode,
		ModelID: client.modelID, PromptVersion: promptVersion, Outcome: outcome,
		InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens, TotalTokens: usage.TotalTokens,
		WallLatency: wallLatency, ProviderLatency: providerLatency, Attempts: attempts,
	})
}

func getAttemptToken(ctx context.Context, retryer aws.Retryer) (func(error) error, error) {
	if retryerV2, ok := retryer.(aws.RetryerV2); ok {
		return retryerV2.GetAttemptToken(ctx)
	}
	return retryer.GetInitialToken(), nil
}

func wait(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func isAllowlistedRetry(err error) bool {
	if errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var apiError smithy.APIError
	if errors.As(err, &apiError) {
		switch apiError.ErrorCode() {
		case "ThrottlingException", modelTimeoutErrorCode, requestTimeoutErrorCode,
			requestTimeoutExceptionErrorCode, serviceUnavailableErrorCode, internalServerErrorCode:
			return true
		default:
			return false
		}
	}
	var networkError net.Error
	return errors.As(err, &networkError) && networkError.Timeout()
}

type exactAdaptiveRetryer struct {
	aws.RetryerV2
}

func (retryer *exactAdaptiveRetryer) IsErrorRetryable(err error) bool {
	return isAllowlistedRetry(err)
}

func outcomeForError(err error) string {
	if errors.Is(err, context.Canceled) {
		return OutcomeCanceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return OutcomeTimeout
	}
	if errors.Is(err, extract.ErrAuthentication) {
		return OutcomeAuthentication
	}
	if errors.Is(err, extract.ErrAuthorization) {
		return OutcomeAccessDenied
	}
	var apiError smithy.APIError
	if errors.As(err, &apiError) {
		switch apiError.ErrorCode() {
		case "ThrottlingException":
			return OutcomeThrottled
		case modelTimeoutErrorCode, requestTimeoutErrorCode, requestTimeoutExceptionErrorCode:
			return OutcomeTimeout
		case serviceUnavailableErrorCode:
			return OutcomeUnavailable
		case internalServerErrorCode:
			return OutcomeInternal
		case "ExpiredTokenException", "InvalidClientTokenId", "UnrecognizedClientException":
			return OutcomeAuthentication
		case "AccessDeniedException":
			return OutcomeAccessDenied
		case "ResourceNotFoundException":
			return OutcomeNotFound
		case "ValidationException":
			return OutcomeInvalidRequest
		}
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return OutcomeTimeout
	}
	return OutcomeProviderError
}

func boundedInvocationError(err error, outcome string) error {
	if errors.Is(err, extract.ErrAuthentication) {
		return fmt.Errorf("%w: %w", ErrInvocation, extract.ErrAuthentication)
	}
	if errors.Is(err, extract.ErrAuthorization) {
		return fmt.Errorf("%w: %w", ErrInvocation, extract.ErrAuthorization)
	}
	var apiError smithy.APIError
	if errors.As(err, &apiError) {
		switch apiError.ErrorCode() {
		case "ExpiredTokenException", "InvalidClientTokenId", "UnrecognizedClientException":
			return fmt.Errorf("%w: %w", ErrInvocation, extract.ErrAuthentication)
		case "AccessDeniedException":
			return fmt.Errorf("%w: %w", ErrInvocation, extract.ErrAuthorization)
		}
	}
	return fmt.Errorf("%w: %s", ErrInvocation, outcome)
}
