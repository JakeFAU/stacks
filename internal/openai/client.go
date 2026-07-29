// Package openai adapts the OpenAI Responses API to the provider-neutral
// structured generation boundary.
package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"

	openaisdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"stacks/internal/extract"
	"stacks/internal/modelpolicy"
	"stacks/internal/modeltelemetry"
	"stacks/internal/observability"
	"stacks/internal/queryplan"
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

	invocationSpanName   = "stacks.model.generate"
	baseRetryDelay       = 100 * time.Millisecond
	httpStatusUpperBound = 600
)

var (
	ErrInvalidRequest = errors.New("openai request is invalid")
	ErrInvalidOutput  = errors.New("openai output is invalid")
	ErrInvocation     = errors.New("openai invocation failed")
)

type responsesAPI interface {
	New(context.Context, responses.ResponseNewParams, ...option.RequestOption) (*responses.Response, error)
}

type waitFunc func(context.Context, time.Duration) error

// Options contains required, validated runtime policy. OpenAI models are
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

// Client implements extract.Model with one stateless OpenAI Responses request.
// It owns retry policy; SDK retries are disabled at construction.
type Client struct {
	api             responsesAPI
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
var _ queryplan.Model = (*Client)(nil)

// New creates an OpenAI Responses adapter fixed to the production API. It does
// not inherit SDK environment settings for organization, project, or base URL.
func New(apiKey string, options Options) (*Client, error) {
	if strings.TrimSpace(apiKey) == "" || apiKey != strings.TrimSpace(apiKey) {
		return nil, fmt.Errorf("create OpenAI client: API key is required")
	}
	service := responses.NewResponseService(
		option.WithEnvironmentProduction(),
		option.WithAPIKey(apiKey),
		option.WithMaxRetries(0),
	)
	return newClient(&service, options, waitForRetry)
}

func newClient(api responsesAPI, options Options, wait waitFunc) (*Client, error) {
	if api == nil || wait == nil {
		return nil, fmt.Errorf("create OpenAI client: dependencies are required")
	}
	modelID := strings.TrimSpace(options.ModelID)
	if modelID == "" || modelID != options.ModelID {
		return nil, fmt.Errorf("create OpenAI client: model ID is required")
	}
	if options.DataMode != modelpolicy.DataModePersonal {
		return nil, fmt.Errorf("create OpenAI client: personal data mode is required")
	}
	if options.MaxOutputTokens <= 0 {
		return nil, fmt.Errorf("create OpenAI client: maximum output tokens are invalid")
	}
	if options.MaxAttempts <= 0 || options.MaxAttempts > MaxAttemptsLimit {
		return nil, fmt.Errorf("create OpenAI client: maximum attempts are invalid")
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

// Generate invokes the Responses API and returns only a completed single-block
// JSON text result. Request input, response content, API keys, and raw provider
// errors are never included in returned errors or telemetry.
func (client *Client) Generate(ctx context.Context, request extract.Request) (extract.Response, error) {
	response, err := client.generateStructured(ctx, extractionStructuredRequest(request))
	if err != nil {
		return extract.Response{}, err
	}
	return extract.Response{
		Output:        append(json.RawMessage(nil), response.output...),
		Usage:         extract.Usage{InputTokens: response.usage.inputTokens, OutputTokens: response.usage.outputTokens, TotalTokens: response.usage.totalTokens},
		Latency:       response.providerLatency,
		ModelID:       response.modelID,
		PromptVersion: response.promptVersion,
		Outcome:       OutcomeSuccess,
	}, nil
}

// Plan invokes the Responses API for an exact reviewed temporal query planner
// contract. The returned JSON remains untrusted until queryplan validates it.
func (client *Client) Plan(ctx context.Context, request queryplan.ModelRequest) (queryplan.ModelResponse, error) {
	response, err := client.generateStructured(ctx, plannerStructuredRequest(request))
	if err != nil {
		return queryplan.ModelResponse{}, err
	}
	return queryplan.ModelResponse{
		Output:        append(json.RawMessage(nil), response.output...),
		Provider:      modelpolicy.ProviderOpenAI,
		ModelID:       response.modelID,
		PromptVersion: response.promptVersion,
		SchemaName:    response.schemaName,
		Usage: queryplan.Usage{
			InputTokens: response.usage.inputTokens, OutputTokens: response.usage.outputTokens, TotalTokens: response.usage.totalTokens,
		},
		Attempts:        response.attempts,
		WallLatency:     response.wallLatency,
		ProviderLatency: response.providerLatency,
	}, nil
}

type structuredRequest struct {
	promptVersion string
	systemPrompt  string
	input         string
	schemaName    string
	jsonSchema    []byte
	validate      func(structuredRequest) error
}

type structuredUsage struct {
	inputTokens  int64
	outputTokens int64
	totalTokens  int64
}

type structuredResponse struct {
	output          json.RawMessage
	usage           structuredUsage
	modelID         string
	promptVersion   string
	schemaName      string
	attempts        int
	wallLatency     time.Duration
	providerLatency time.Duration
}

func extractionStructuredRequest(request extract.Request) structuredRequest {
	return structuredRequest{
		promptVersion: request.PromptVersion,
		systemPrompt:  request.SystemPrompt,
		input:         request.Input,
		schemaName:    request.SchemaName,
		jsonSchema:    append([]byte(nil), request.JSONSchema...),
		validate: func(snapshot structuredRequest) error {
			contract, err := extract.PromptContract(snapshot.promptVersion)
			if err != nil || snapshot.systemPrompt != contract.SystemPrompt || snapshot.schemaName != contract.SchemaName ||
				!bytes.Equal(snapshot.jsonSchema, contract.JSONSchema) || snapshot.input == "" {
				return ErrInvalidRequest
			}
			return nil
		},
	}
}

func plannerStructuredRequest(request queryplan.ModelRequest) structuredRequest {
	return structuredRequest{
		promptVersion: request.PromptVersion,
		systemPrompt:  request.SystemPrompt,
		input:         request.Input,
		schemaName:    request.SchemaName,
		jsonSchema:    append([]byte(nil), request.JSONSchema...),
		validate: func(snapshot structuredRequest) error {
			contract, err := queryplan.PromptContract(snapshot.promptVersion)
			if err != nil || snapshot.systemPrompt != contract.SystemPrompt || snapshot.schemaName != contract.SchemaName ||
				!bytes.Equal(snapshot.jsonSchema, contract.JSONSchema) || snapshot.input == "" {
				return ErrInvalidRequest
			}
			return nil
		},
	}
}

func (client *Client) generateStructured(ctx context.Context, request structuredRequest) (response structuredResponse, resultErr error) {
	ctx, span := client.tracer.Start(ctx, invocationSpanName)
	started := client.now()
	defer func() { observability.FinishSpan(span, resultErr) }()

	params, err := client.responseParams(request)
	if err != nil {
		client.record(ctx, started, "", OutcomeInvalidRequest, structuredUsage{}, 0)
		return structuredResponse{}, err
	}

	for attempt := 1; attempt <= client.maxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			client.record(ctx, started, request.promptVersion, outcomeForError(err), structuredUsage{}, attempt-1)
			return structuredResponse{}, fmt.Errorf("%w: %w", ErrInvocation, err)
		}

		output, invokeErr := client.api.New(ctx, params)
		if invokeErr == nil {
			response, outputErr := client.response(request, output)
			if outputErr != nil {
				usage := boundedUsage(output)
				client.record(ctx, started, request.promptVersion, OutcomeInvalidOutput, usage, attempt)
				return structuredResponse{}, outputErr
			}
			response.attempts = attempt
			response.wallLatency = client.elapsed(started)
			client.record(ctx, started, request.promptVersion, OutcomeSuccess, response.usage, attempt)
			return response, nil
		}

		if err := ctx.Err(); err != nil {
			client.record(ctx, started, request.promptVersion, outcomeForError(err), structuredUsage{}, attempt)
			return structuredResponse{}, fmt.Errorf("%w: %w", ErrInvocation, err)
		}
		if errors.Is(invokeErr, context.Canceled) {
			client.record(ctx, started, request.promptVersion, OutcomeCanceled, structuredUsage{}, attempt)
			return structuredResponse{}, fmt.Errorf("%w: %w", ErrInvocation, context.Canceled)
		}

		outcome := outcomeForError(invokeErr)
		if attempt == client.maxAttempts || !isRetryable(invokeErr) {
			client.record(ctx, started, request.promptVersion, outcome, structuredUsage{}, attempt)
			return structuredResponse{}, boundedInvocationError(invokeErr, outcome)
		}
		if err := client.wait(ctx, retryDelay(attempt)); err != nil {
			client.record(ctx, started, request.promptVersion, outcomeForError(err), structuredUsage{}, attempt)
			return structuredResponse{}, fmt.Errorf("%w: %w", ErrInvocation, err)
		}
	}

	return structuredResponse{}, fmt.Errorf("%w: retry policy", ErrInvocation)
}

func (client *Client) responseParams(request structuredRequest) (responses.ResponseNewParams, error) {
	if request.validate == nil || request.validate(request) != nil {
		return responses.ResponseNewParams{}, ErrInvalidRequest
	}
	var schema map[string]any
	if err := json.Unmarshal(request.jsonSchema, &schema); err != nil || schema == nil {
		return responses.ResponseNewParams{}, ErrInvalidRequest
	}

	format := responses.ResponseFormatTextConfigParamOfJSONSchema(request.schemaName, schema)
	format.OfJSONSchema.Strict = openaisdk.Bool(true)
	return responses.ResponseNewParams{
		Background:      openaisdk.Bool(false),
		Instructions:    openaisdk.String(request.systemPrompt),
		Input:           responses.ResponseNewParamsInputUnion{OfString: openaisdk.String(request.input)},
		MaxOutputTokens: openaisdk.Int(client.maxOutputTokens),
		Model:           responses.ResponsesModel(client.modelID),
		Reasoning:       shared.ReasoningParam{Effort: shared.ReasoningEffortNone},
		Store:           openaisdk.Bool(false),
		Text:            responses.ResponseTextConfigParam{Format: format},
	}, nil
}

func (client *Client) response(request structuredRequest, output *responses.Response) (structuredResponse, error) {
	if output == nil || output.Status != responses.ResponseStatusCompleted || string(output.Model) != client.modelID ||
		!output.JSON.Usage.Valid() {
		return structuredResponse{}, ErrInvalidOutput
	}
	messageIndex := -1
	for index := range output.Output {
		itemType := output.Output[index].Type
		if itemType == "reasoning" && messageIndex == -1 {
			continue
		}
		if itemType != "message" || messageIndex != -1 {
			return structuredResponse{}, ErrInvalidOutput
		}
		messageIndex = index
	}
	if messageIndex == -1 {
		return structuredResponse{}, ErrInvalidOutput
	}
	item := output.Output[messageIndex]
	if item.Type != "message" || item.Status != string(responses.ResponseOutputMessageStatusCompleted) || len(item.Content) != 1 {
		return structuredResponse{}, ErrInvalidOutput
	}
	content := item.Content[0]
	if content.Type != "output_text" || !json.Valid([]byte(content.Text)) {
		return structuredResponse{}, ErrInvalidOutput
	}
	if !output.Usage.JSON.InputTokens.Valid() || !output.Usage.JSON.OutputTokens.Valid() || !output.Usage.JSON.TotalTokens.Valid() ||
		output.Usage.InputTokens < 0 || output.Usage.OutputTokens < 0 || output.Usage.TotalTokens < 0 {
		return structuredResponse{}, ErrInvalidOutput
	}
	usage := structuredUsage{
		inputTokens: output.Usage.InputTokens, outputTokens: output.Usage.OutputTokens, totalTokens: output.Usage.TotalTokens,
	}
	return structuredResponse{
		output: append(json.RawMessage(nil), content.Text...), usage: usage,
		modelID: client.modelID, promptVersion: request.promptVersion, schemaName: request.schemaName,
	}, nil
}

func boundedUsage(output *responses.Response) structuredUsage {
	if output == nil || !output.JSON.Usage.Valid() {
		return structuredUsage{}
	}
	var usage structuredUsage
	if output.Usage.JSON.InputTokens.Valid() && output.Usage.InputTokens >= 0 {
		usage.inputTokens = output.Usage.InputTokens
	}
	if output.Usage.JSON.OutputTokens.Valid() && output.Usage.OutputTokens >= 0 {
		usage.outputTokens = output.Usage.OutputTokens
	}
	if output.Usage.JSON.TotalTokens.Valid() && output.Usage.TotalTokens >= 0 {
		usage.totalTokens = output.Usage.TotalTokens
	}
	return usage
}

func isRetryable(err error) bool {
	if errors.Is(err, context.Canceled) {
		return false
	}
	var apiErr *openaisdk.Error
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == http.StatusRequestTimeout || apiErr.StatusCode == http.StatusTooManyRequests ||
			apiErr.StatusCode >= http.StatusInternalServerError && apiErr.StatusCode < httpStatusUpperBound
	}
	return isRetryableTransport(err)
}

func isRetryableTransport(err error) bool {
	var transportErr *url.Error
	if !errors.As(err, &transportErr) || transportErr.Err == nil {
		return false
	}
	var dnsErr *net.DNSError
	if errors.As(transportErr.Err, &dnsErr) {
		return false
	}
	var operationErr *net.OpError
	if errors.As(transportErr.Err, &operationErr) && operationErr.Timeout() {
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
	var apiErr *openaisdk.Error
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
	case status >= http.StatusInternalServerError && status < httpStatusUpperBound:
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

func (client *Client) elapsed(started time.Time) time.Duration {
	wallLatency := client.now().Sub(started)
	if wallLatency < 0 {
		return 0
	}
	return wallLatency
}

func (client *Client) record(ctx context.Context, started time.Time, promptVersion, outcome string, usage structuredUsage, attempts int) {
	if client.recorder == nil {
		return
	}
	promptVersion = strings.TrimSpace(promptVersion)
	if promptVersion == "" {
		promptVersion = "invalid"
	}
	client.recorder.Record(ctx, modeltelemetry.Observation{
		Provider: modelpolicy.ProviderOpenAI, DataMode: client.dataMode,
		ModelID: client.modelID, PromptVersion: promptVersion, Outcome: outcome,
		InputTokens: usage.inputTokens, OutputTokens: usage.outputTokens, TotalTokens: usage.totalTokens,
		WallLatency: client.elapsed(started), Attempts: attempts,
	})
}
