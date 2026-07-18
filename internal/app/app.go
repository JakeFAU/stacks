package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"stacks/internal/config"
	"stacks/internal/httpapi"
)

const shutdownTimeout = 10 * time.Second

// Run owns the application lifecycle and blocks until the context is canceled
// or the HTTP server fails.
func Run(ctx context.Context, settings config.Settings, logger *slog.Logger) error {
	server := &http.Server{
		Addr:              settings.HTTPAddress,
		Handler:           httpapi.NewHandler(),
		ReadHeaderTimeout: settings.ReadHeaderTimeout,
	}

	serverError := make(chan error, 1)
	go func() {
		logger.Info("HTTP server listening", "address", settings.HTTPAddress)
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
