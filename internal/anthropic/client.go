// Package anthropic adapts the Anthropic Messages API to the provider-neutral
// structured generation boundary.
package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"stacks/internal/extract"
	"stacks/internal/modelpolicy"
	"stacks/internal/modeltelemetry"
	"stacks/internal/observability"
)

const (
	// MaxAttemptsLimit prevents a malformed runtime value from creating an
	// unexpectedly large number of external disclosures.
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

	invocationSpanName = "stacks.model.generate"
	baseRetryDelay     = 100 * time.Millisecond
)

var (
	ErrInvalidRequest = errors.New("anthropic request is invalid")
	ErrInvalidOutput  = errors.New("anthropic output is invalid")
	ErrInvocation     = errors.New("anthropic invocation failed")
)

type messagesAPI interface {
	New(context.Context, anthropicsdk.MessageNewParams, ...option.RequestOption) (*anthropicsdk.Message, error)
}

type waitFunc func(context.Context, time.Duration) error

// Options contains required, validated runtime policy. Anthropic models are
// deliberately explicit because capability, cost, and structured-output
// support change independently.
type Options struct {
	ModelID         string
	DataMode        modelpolicy.DataMode
	MaxOutputTokens int
	MaxAttempts     int
	Recorder        modeltelemetry.Recorder
	Tracer          trace.Tracer
}

// Client implements extract.Model with one stateless Anthropic Messages
// request. It owns retry policy; SDK retries are disabled at construction.
type Client struct {
	api             messagesAPI
	modelID         string
	dataMode        modelpolicy.DataMode
	maxOutputTokens int64
	maxAttempts     int
	recorder        modeltelemetry.Recorder
	tracer          trace.Tracer
	wait            waitFunc
	now             func() time.Time
}

var _ extract.Model = (*Client)(nil)

// New creates an Anthropic Messages adapter fixed to the production API. It
// does not inherit SDK environment settings for profiles, auth tokens, or base
// URLs.
func New(apiKey string, options Options) (*Client, error) {
	if strings.TrimSpace(apiKey) == "" || apiKey != strings.TrimSpace(apiKey) {
		return nil, fmt.Errorf("create Anthropic client: API key is required")
	}
	service := anthropicsdk.NewMessageService(
		option.WithEnvironmentProduction(),
		option.WithAPIKey(apiKey),
		option.WithMaxRetries(0),
	)
	return newClient(&service, options, waitForRetry)
}

func newClient(api messagesAPI, options Options, wait waitFunc) (*Client, error) {
	if api == nil || wait == nil {
		return nil, fmt.Errorf("create Anthropic client: dependencies are required")
	}
	modelID := strings.TrimSpace(options.ModelID)
	if modelID == "" || modelID != options.ModelID {
		return nil, fmt.Errorf("create Anthropic client: model ID is required")
	}
	if options.DataMode != modelpolicy.DataModePersonal {
		return nil, fmt.Errorf("create Anthropic client: personal data mode is required")
	}
	if options.MaxOutputTokens <= 0 {
		return nil, fmt.Errorf("create Anthropic client: maximum output tokens are invalid")
	}
	if options.MaxAttempts <= 0 || options.MaxAttempts > MaxAttemptsLimit {
		return nil, fmt.Errorf("create Anthropic client: maximum attempts are invalid")
	}
	tracer := options.Tracer
	if tracer == nil {
		tracer = tracenoop.NewTracerProvider().Tracer("stacks")
	}
	return &Client{
		api: api, modelID: modelID, dataMode: options.DataMode,
		maxOutputTokens: int64(options.MaxOutputTokens), maxAttempts: options.MaxAttempts,
		recorder: options.Recorder, tracer: tracer, wait: wait, now: time.Now,
	}, nil
}

// Generate invokes the Messages API and returns only a natural end-turn with
// one JSON text block. Request input, response content, API keys, and raw
// provider errors are never included in returned errors or telemetry.
func (client *Client) Generate(ctx context.Context, request extract.Request) (response extract.Response, resultErr error) {
	ctx, span := client.tracer.Start(ctx, invocationSpanName)
	started := client.now()
	defer func() { observability.FinishSpan(span, resultErr) }()

	params, err := client.messageParams(request)
	if err != nil {
		client.record(ctx, started, "", OutcomeInvalidRequest, extract.Usage{}, 0)
		return extract.Response{}, err
	}

	for attempt := 1; attempt <= client.maxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			client.record(ctx, started, request.PromptVersion, outcomeForError(err), extract.Usage{}, attempt-1)
			return extract.Response{}, fmt.Errorf("%w: %w", ErrInvocation, err)
		}

		output, invokeErr := client.api.New(ctx, params)
		if invokeErr == nil {
			response, outputErr := client.response(request.PromptVersion, output)
			if outputErr != nil {
				client.record(ctx, started, request.PromptVersion, OutcomeInvalidOutput, boundedUsage(output), attempt)
				return extract.Response{}, outputErr
			}
			client.record(ctx, started, request.PromptVersion, OutcomeSuccess, response.Usage, attempt)
			return response, nil
		}

		if err := ctx.Err(); err != nil {
			client.record(ctx, started, request.PromptVersion, outcomeForError(err), extract.Usage{}, attempt)
			return extract.Response{}, fmt.Errorf("%w: %w", ErrInvocation, err)
		}
		if errors.Is(invokeErr, context.Canceled) {
			client.record(ctx, started, request.PromptVersion, OutcomeCanceled, extract.Usage{}, attempt)
			return extract.Response{}, fmt.Errorf("%w: %w", ErrInvocation, context.Canceled)
		}

		outcome := outcomeForError(invokeErr)
		if attempt == client.maxAttempts || !isRetryable(invokeErr) {
			client.record(ctx, started, request.PromptVersion, outcome, extract.Usage{}, attempt)
			return extract.Response{}, boundedInvocationError(invokeErr, outcome)
		}
		if err := client.wait(ctx, retryDelay(attempt)); err != nil {
			client.record(ctx, started, request.PromptVersion, outcomeForError(err), extract.Usage{}, attempt)
			return extract.Response{}, fmt.Errorf("%w: %w", ErrInvocation, err)
		}
	}

	return extract.Response{}, fmt.Errorf("%w: retry policy", ErrInvocation)
}

func (client *Client) messageParams(request extract.Request) (anthropicsdk.MessageNewParams, error) {
	contract, err := extract.PromptContract(request.PromptVersion)
	if err != nil || request.SystemPrompt != contract.SystemPrompt || request.SchemaName != contract.SchemaName || !bytes.Equal(request.JSONSchema, contract.JSONSchema) || request.Input == "" {
		return anthropicsdk.MessageNewParams{}, ErrInvalidRequest
	}
	var schema map[string]any
	if err := json.Unmarshal(request.JSONSchema, &schema); err != nil || schema == nil {
		return anthropicsdk.MessageNewParams{}, ErrInvalidRequest
	}

	return anthropicsdk.MessageNewParams{
		MaxTokens: client.maxOutputTokens,
		Messages: []anthropicsdk.MessageParam{
			anthropicsdk.NewUserMessage(anthropicsdk.NewTextBlock(request.Input)),
		},
		Model: anthropicsdk.Model(client.modelID),
		OutputConfig: anthropicsdk.OutputConfigParam{
			Format: anthropicsdk.JSONOutputFormatParam{Schema: schema},
		},
		System: []anthropicsdk.TextBlockParam{{Text: request.SystemPrompt}},
	}, nil
}

func (client *Client) response(promptVersion string, output *anthropicsdk.Message) (extract.Response, error) {
	if output == nil || !output.JSON.Content.Valid() || !output.JSON.Model.Valid() || !output.JSON.StopReason.Valid() ||
		output.StopReason != anthropicsdk.StopReasonEndTurn || string(output.Model) != client.modelID || len(output.Content) != 1 {
		return extract.Response{}, ErrInvalidOutput
	}
	content := output.Content[0]
	if !content.JSON.Type.Valid() || !content.JSON.Text.Valid() || content.Type != "text" || !json.Valid([]byte(content.Text)) {
		return extract.Response{}, ErrInvalidOutput
	}
	usage, ok := messageUsage(output)
	if !ok {
		return extract.Response{}, ErrInvalidOutput
	}
	return extract.Response{
		Output: append(json.RawMessage(nil), content.Text...), Usage: usage,
		ModelID: client.modelID, PromptVersion: promptVersion, Outcome: OutcomeSuccess,
	}, nil
}

func messageUsage(output *anthropicsdk.Message) (extract.Usage, bool) {
	if output == nil || !output.JSON.Usage.Valid() || !output.Usage.JSON.InputTokens.Valid() ||
		!output.Usage.JSON.CacheCreationInputTokens.Valid() || !output.Usage.JSON.CacheReadInputTokens.Valid() ||
		!output.Usage.JSON.OutputTokens.Valid() || output.Usage.InputTokens < 0 ||
		output.Usage.CacheCreationInputTokens < 0 || output.Usage.CacheReadInputTokens < 0 || output.Usage.OutputTokens < 0 {
		return extract.Usage{}, false
	}
	inputTokens, ok := addTokens(output.Usage.InputTokens, output.Usage.CacheCreationInputTokens, output.Usage.CacheReadInputTokens)
	if !ok {
		return extract.Usage{}, false
	}
	totalTokens, ok := addTokens(inputTokens, output.Usage.OutputTokens)
	if !ok {
		return extract.Usage{}, false
	}
	return extract.Usage{InputTokens: inputTokens, OutputTokens: output.Usage.OutputTokens, TotalTokens: totalTokens}, true
}

func addTokens(values ...int64) (int64, bool) {
	var total int64
	for _, value := range values {
		if value < 0 || value > math.MaxInt64-total {
			return 0, false
		}
		total += value
	}
	return total, true
}

func boundedUsage(output *anthropicsdk.Message) extract.Usage {
	usage, ok := messageUsage(output)
	if !ok {
		return extract.Usage{}
	}
	return usage
}

func isRetryable(err error) bool {
	if errors.Is(err, context.Canceled) {
		return false
	}
	var apiErr *anthropicsdk.Error
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == http.StatusRequestTimeout || apiErr.StatusCode == http.StatusTooManyRequests || apiErr.StatusCode >= http.StatusInternalServerError
	}
	return isRetryableTransport(err)
}

func isRetryableTransport(err error) bool {
	var transportErr *url.Error
	if !errors.As(err, &transportErr) || transportErr.Err == nil {
		return false
	}
	var networkErr net.Error
	if errors.As(transportErr.Err, &networkErr) && networkErr.Timeout() {
		return true
	}
	return errors.Is(transportErr.Err, syscall.ECONNRESET)
}

func outcomeForError(err error) string {
	if errors.Is(err, context.Canceled) {
		return OutcomeCanceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return OutcomeTimeout
	}
	var apiErr *anthropicsdk.Error
	if errors.As(err, &apiErr) {
		return outcomeForHTTPStatus(apiErr.StatusCode)
	}
	var transportErr *url.Error
	if errors.As(err, &transportErr) {
		if transportErr.Timeout() {
			return OutcomeTimeout
		}
		if isRetryableTransport(transportErr) {
			return OutcomeUnavailable
		}
		return OutcomeProviderError
	}
	return OutcomeProviderError
}

func outcomeForHTTPStatus(status int) string {
	switch {
	case status == http.StatusRequestTimeout:
		return OutcomeTimeout
	case status == http.StatusTooManyRequests:
		return OutcomeThrottled
	case status == http.StatusUnauthorized:
		return OutcomeAuthentication
	case status == http.StatusForbidden:
		return OutcomeAccessDenied
	case status == http.StatusNotFound:
		return OutcomeNotFound
	case status == http.StatusBadRequest:
		return OutcomeInvalidRequest
	case status == http.StatusInternalServerError:
		return OutcomeInternal
	case status >= http.StatusInternalServerError:
		return OutcomeUnavailable
	default:
		return OutcomeProviderError
	}
}

func boundedInvocationError(err error, outcome string) error {
	if errors.Is(err, context.Canceled) {
		return fmt.Errorf("%w: %w", ErrInvocation, context.Canceled)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%w: %w", ErrInvocation, context.DeadlineExceeded)
	}
	switch outcome {
	case OutcomeAuthentication:
		return fmt.Errorf("%w: %w", ErrInvocation, extract.ErrAuthentication)
	case OutcomeAccessDenied:
		return fmt.Errorf("%w: %w", ErrInvocation, extract.ErrAuthorization)
	case OutcomeThrottled:
		return fmt.Errorf("%w: provider throttled the request", ErrInvocation)
	case OutcomeTimeout:
		return fmt.Errorf("%w: provider request timed out", ErrInvocation)
	case OutcomeUnavailable, OutcomeInternal:
		return fmt.Errorf("%w: provider is unavailable", ErrInvocation)
	case OutcomeNotFound:
		return fmt.Errorf("%w: configured model was not found", ErrInvocation)
	case OutcomeInvalidRequest:
		return fmt.Errorf("%w: provider rejected the request", ErrInvocation)
	default:
		return fmt.Errorf("%w: provider request failed", ErrInvocation)
	}
}

func retryDelay(attempt int) time.Duration {
	return baseRetryDelay * time.Duration(1<<(attempt-1))
}

func waitForRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (client *Client) record(ctx context.Context, started time.Time, promptVersion, outcome string, usage extract.Usage, attempts int) {
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
		Provider: modelpolicy.ProviderAnthropic, DataMode: client.dataMode,
		ModelID: client.modelID, PromptVersion: promptVersion, Outcome: outcome,
		InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens, TotalTokens: usage.TotalTokens,
		WallLatency: wallLatency, Attempts: attempts,
	})
}
