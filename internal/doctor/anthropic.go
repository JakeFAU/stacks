package doctor

import (
	"context"
	"errors"
	"net/http"

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"
	anthropicoption "github.com/anthropics/anthropic-sdk-go/option"
)

type anthropicModelsAPI interface {
	Get(context.Context, string, anthropicsdk.ModelGetParams, ...anthropicoption.RequestOption) (*anthropicsdk.ModelInfo, error)
}

// AnthropicProbe performs one read-only model metadata request. A successful
// result is cached so the credential and availability checks share one call.
type AnthropicProbe struct {
	modelID string
	models  anthropicModelsAPI
	checked bool
}

// NewAnthropicProbe constructs a probe fixed to the production API with SDK
// retries disabled. Construction performs no network access.
func NewAnthropicProbe(apiKey, modelID string) *AnthropicProbe {
	return newAnthropicProbe(apiKey, modelID)
}

func newAnthropicProbe(apiKey, modelID string, testOptions ...anthropicoption.RequestOption) *AnthropicProbe {
	options := []anthropicoption.RequestOption{
		anthropicoption.WithEnvironmentProduction(),
		anthropicoption.WithAPIKey(apiKey),
		anthropicoption.WithMaxRetries(0),
	}
	options = append(options, testOptions...)
	service := anthropicsdk.NewModelService(options...)
	return &AnthropicProbe{modelID: modelID, models: &service}
}

func (probe *AnthropicProbe) CheckCredentials(ctx context.Context) error {
	return probe.checkMetadata(ctx)
}

func (probe *AnthropicProbe) CheckModel(ctx context.Context) error {
	return probe.checkMetadata(ctx)
}

func (probe *AnthropicProbe) checkMetadata(ctx context.Context) error {
	if probe.checked {
		return nil
	}
	model, err := probe.models.Get(ctx, probe.modelID, anthropicsdk.ModelGetParams{})
	if err != nil {
		return boundedAnthropicModelError(ctx, err)
	}
	if model == nil || model.ID != probe.modelID {
		return ErrModelNotFound
	}
	probe.checked = true
	return nil
}

func boundedAnthropicModelError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	var apiError *anthropicsdk.Error
	if errors.As(err, &apiError) {
		switch apiError.StatusCode {
		case http.StatusUnauthorized:
			return ErrModelAuthentication
		case http.StatusForbidden:
			return ErrModelAuthorization
		case http.StatusNotFound:
			return ErrModelNotFound
		case http.StatusTooManyRequests:
			return ErrModelUnavailable
		default:
			if apiError.StatusCode >= http.StatusInternalServerError {
				return ErrModelUnavailable
			}
		}
		return ErrModelInspection
	}
	return ErrModelUnavailable
}
