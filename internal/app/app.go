package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"stacks/internal/config"
	"stacks/internal/httpapi"
)

const shutdownTimeout = 10 * time.Second

// Run owns the application lifecycle and blocks until the context is canceled
// or the HTTP server fails.
func Run(
	ctx context.Context,
	settings config.Settings,
	logger *zap.Logger,
	tracerProvider trace.TracerProvider,
	meterProvider metric.MeterProvider,
) error {
	server := &http.Server{
		Addr:              settings.HTTPAddress,
		Handler:           httpapi.NewHandler(tracerProvider, meterProvider),
		ReadHeaderTimeout: settings.ReadHeaderTimeout,
	}

	serverError := make(chan error, 1)
	go func() {
		logger.Info("HTTP server listening", zap.String("address", settings.HTTPAddress))
		serverError <- server.ListenAndServe()
	}()

	select {
	case err := <-serverError:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve HTTP: %w", err)
	case <-ctx.Done():
		logger.Info("shutting down HTTP server")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shut down HTTP server: %w", err)
	}

	return nil
}
