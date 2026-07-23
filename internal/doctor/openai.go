package doctor

import (
	"context"
	"errors"
	"net/http"

	openaisdk "github.com/openai/openai-go/v3"
	openaioption "github.com/openai/openai-go/v3/option"
)

type openAIModelsAPI interface {
	Get(context.Context, string, ...openaioption.RequestOption) (*openaisdk.Model, error)
}

// OpenAIProbe performs one read-only model metadata request. A successful
// result is cached so the credential and availability checks share one call.
type OpenAIProbe struct {
	modelID string
	models  openAIModelsAPI
	checked bool
}

// NewOpenAIProbe constructs a probe fixed to the production API with SDK
// retries disabled. Construction performs no network access.
func NewOpenAIProbe(apiKey, modelID string) *OpenAIProbe {
	return newOpenAIProbe(apiKey, modelID)
}

func newOpenAIProbe(apiKey, modelID string, testOptions ...openaioption.RequestOption) *OpenAIProbe {
	options := []openaioption.RequestOption{
		openaioption.WithEnvironmentProduction(),
		openaioption.WithAPIKey(apiKey),
		openaioption.WithMaxRetries(0),
	}
	options = append(options, testOptions...)
	service := openaisdk.NewModelService(options...)
	return &OpenAIProbe{modelID: modelID, models: &service}
}

func (probe *OpenAIProbe) CheckCredentials(ctx context.Context) error {
	return probe.checkMetadata(ctx)
}

func (probe *OpenAIProbe) CheckModel(ctx context.Context) error {
	return probe.checkMetadata(ctx)
}

func (probe *OpenAIProbe) checkMetadata(ctx context.Context) error {
	if probe.checked {
		return nil
	}
	model, err := probe.models.Get(ctx, probe.modelID)
	if err != nil {
		return boundedOpenAIModelError(ctx, err)
	}
	if model == nil || model.ID != probe.modelID {
		return ErrModelNotFound
	}
	probe.checked = true
	return nil
}

func boundedOpenAIModelError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	var apiError *openaisdk.Error
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
