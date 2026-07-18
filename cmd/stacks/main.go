package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"stacks/internal/app"
	"stacks/internal/config"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	settings, err := config.Load()
	if err != nil {
		logger.Error("load configuration", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := app.Run(ctx, settings, logger); err != nil {
		logger.Error("run stacks", "error", err)
		os.Exit(1)
	}
}
