package bedrock

import (
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
	"stacks/internal/extract"
)

const (
	OutcomeSuccess        = "success"
	OutcomeThrottled      = "throttled"
	OutcomeTimeout        = "timeout"
	OutcomeUnavailable    = "unavailable"
	OutcomeInternal       = "internal_error"
	OutcomeAccessDenied   = "access_denied"
	OutcomeNotFound       = "not_found"
	OutcomeInvalidRequest = "invalid_request"
	OutcomeInvalidOutput  = "invalid_output"
	OutcomeCanceled       = "canceled"
	OutcomeProviderError  = "provider_error"
)

var (
	ErrInvalidRequest = errors.New("bedrock request is invalid")
	ErrInvalidOutput  = errors.New("bedrock output is invalid")
	ErrInvocation     = errors.New("bedrock invocation failed")
)

const (
	modelTimeoutErrorCode             = "ModelTimeoutException"
	serviceUnavailableErrorCode       = "ServiceUnavailableException"
	internalServerErrorCode           = "InternalServerException"
	maxLatencyMilliseconds      int64 = (1<<63 - 1) / int64(time.Millisecond)
)

// ConverseAPI is the narrow AWS Runtime surface used by Client.
type ConverseAPI interface {
	Converse(context.Context, *bedrockruntime.ConverseInput, ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseOutput, error)
}

// InvocationObservation is the bounded telemetry emitted for one completed
// Generate call. It deliberately excludes request and response text.
type InvocationObservation struct {
	ModelID       string
	PromptVersion string
	Outcome       string
	InputTokens   int64
	OutputTokens  int64
	Latency       time.Duration
	Attempts      int
}

// InvocationRecorder receives privacy-safe invocation telemetry.
type InvocationRecorder interface {
	Record(context.Context, InvocationObservation)
}

// Options contains required, validated runtime policy.
type Options struct {
	ModelID     string
	MaxTokens   int
	MaxAttempts int
	Recorder    InvocationRecorder
}

// Client implements extract.Model with Bedrock Runtime Converse structured
// output. It owns retries so the wrapped SDK client must not add another retry
// layer.
type Client struct {
	api       ConverseAPI
	modelID   string
	maxTokens int32
	retryer   aws.Retryer
	recorder  InvocationRecorder
}

var _ extract.Model = (*Client)(nil)

// New creates a Bedrock model boundary around a Converse client.
func New(api ConverseAPI, options Options) (*Client, error) {
	retryer := awsretry.NewAdaptiveMode(func(adaptive *awsretry.AdaptiveModeOptions) {
		adaptive.StandardOptions = append(adaptive.StandardOptions, func(standard *awsretry.StandardOptions) {
			standard.MaxAttempts = options.MaxAttempts
		})
	})
	configuredRetryer := awsretry.AddWithErrorCodes(
		retryer,
		modelTimeoutErrorCode,
		serviceUnavailableErrorCode,
		internalServerErrorCode,
	)
	return newClient(api, options, configuredRetryer)
}

// NewFromConfig creates a Runtime client with SDK retries disabled because
// Client owns the bounded adaptive retry lifecycle.
func NewFromConfig(configuration aws.Config, options Options) (*Client, error) {
	if strings.TrimSpace(configuration.Region) == "" {
		return nil, fmt.Errorf("create Bedrock client: AWS region is required")
	}
	configuration.Retryer = func() aws.Retryer { return aws.NopRetryer{} }
	return New(bedrockruntime.NewFromConfig(configuration), options)
}

func newClient(api ConverseAPI, options Options, retryer aws.Retryer) (*Client, error) {
	if api == nil || retryer == nil {
		return nil, fmt.Errorf("create Bedrock client: dependencies are required")
	}
	modelID := strings.TrimSpace(options.ModelID)
	if modelID == "" {
		return nil, fmt.Errorf("create Bedrock client: model ID is required")
	}
	if options.MaxTokens <= 0 || options.MaxTokens > math.MaxInt32 {
		return nil, fmt.Errorf("create Bedrock client: maximum output tokens are invalid")
	}
	if options.MaxAttempts <= 0 || retryer.MaxAttempts() != options.MaxAttempts {
		return nil, fmt.Errorf("create Bedrock client: maximum attempts are invalid")
	}
	return &Client{
		api: api, modelID: modelID, maxTokens: int32(options.MaxTokens),
		retryer: retryer, recorder: options.Recorder,
	}, nil
}

// Generate invokes Converse and returns untrusted structured JSON plus bounded
// metadata. It never includes private request or response data in errors or
// telemetry.
func (client *Client) Generate(ctx context.Context, request extract.Request) (extract.Response, error) {
	input, err := client.converseInput(request)
	if err != nil {
		client.record(ctx, request.PromptVersion, OutcomeInvalidRequest, extract.Usage{}, 0, 0)
		return extract.Response{}, err
	}

	var retryToken func(error) error
	for attempt := 1; attempt <= client.retryer.MaxAttempts(); attempt++ {
		if err := ctx.Err(); err != nil {
			if retryToken != nil {
				_ = retryToken(err)
			}
			client.record(ctx, request.PromptVersion, OutcomeCanceled, extract.Usage{}, 0, attempt-1)
			return extract.Response{}, fmt.Errorf("%w: %w", ErrInvocation, err)
		}

		attemptToken, tokenErr := getAttemptToken(ctx, client.retryer)
		if tokenErr != nil {
			if retryToken != nil {
				_ = retryToken(tokenErr)
			}
			client.record(ctx, request.PromptVersion, outcomeForError(tokenErr), extract.Usage{}, 0, attempt-1)
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
				client.record(ctx, request.PromptVersion, OutcomeInvalidOutput, usage, latency, attempt)
				return extract.Response{}, outputErr
			}
			client.record(ctx, request.PromptVersion, OutcomeSuccess, response.Usage, response.Latency, attempt)
			return response, nil
		}

		if ctx.Err() != nil {
			client.record(ctx, request.PromptVersion, OutcomeCanceled, extract.Usage{}, 0, attempt)
			return extract.Response{}, fmt.Errorf("%w: %w", ErrInvocation, ctx.Err())
		}
		if attempt == client.retryer.MaxAttempts() || !client.retryer.IsErrorRetryable(invokeErr) {
			outcome := outcomeForError(invokeErr)
			client.record(ctx, request.PromptVersion, outcome, extract.Usage{}, 0, attempt)
			return extract.Response{}, fmt.Errorf("%w: %s", ErrInvocation, outcome)
		}

		retryToken, tokenErr = client.retryer.GetRetryToken(ctx, invokeErr)
		if tokenErr != nil {
			client.record(ctx, request.PromptVersion, outcomeForError(invokeErr), extract.Usage{}, 0, attempt)
			return extract.Response{}, fmt.Errorf("%w: retry policy", ErrInvocation)
		}
		delay, delayErr := client.retryer.RetryDelay(attempt, invokeErr)
		if delayErr != nil {
			_ = retryToken(delayErr)
			retryToken = nil
			client.record(ctx, request.PromptVersion, outcomeForError(invokeErr), extract.Usage{}, 0, attempt)
			return extract.Response{}, fmt.Errorf("%w: retry policy", ErrInvocation)
		}
		if err := wait(ctx, delay); err != nil {
			_ = retryToken(err)
			retryToken = nil
			client.record(ctx, request.PromptVersion, OutcomeCanceled, extract.Usage{}, 0, attempt)
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
	if strings.TrimSpace(request.PromptVersion) == "" || strings.TrimSpace(request.SystemPrompt) == "" || request.Input == "" || strings.TrimSpace(request.SchemaName) == "" || !json.Valid(request.JSONSchema) {
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
	if !ok || len(message.Value.Content) != 1 {
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

func (client *Client) record(ctx context.Context, promptVersion, outcome string, usage extract.Usage, latency time.Duration, attempts int) {
	if client.recorder == nil {
		return
	}
	client.recorder.Record(ctx, InvocationObservation{
		ModelID: client.modelID, PromptVersion: promptVersion, Outcome: outcome,
		InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens,
		Latency: latency, Attempts: attempts,
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

func isTransient(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var apiError smithy.APIError
	if errors.As(err, &apiError) {
		switch apiError.ErrorCode() {
		case "ThrottlingException", modelTimeoutErrorCode, serviceUnavailableErrorCode, internalServerErrorCode:
			return true
		default:
			return false
		}
	}
	var networkError net.Error
	return errors.As(err, &networkError) && networkError.Timeout()
}

func outcomeForError(err error) string {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return OutcomeCanceled
	}
	var apiError smithy.APIError
	if errors.As(err, &apiError) {
		switch apiError.ErrorCode() {
		case "ThrottlingException":
			return OutcomeThrottled
		case modelTimeoutErrorCode:
			return OutcomeTimeout
		case serviceUnavailableErrorCode:
			return OutcomeUnavailable
		case internalServerErrorCode:
			return OutcomeInternal
		case "AccessDeniedException", "UnrecognizedClientException":
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
