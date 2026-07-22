package main

import (
	"context"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/zap"

	"stacks/internal/app"
	"stacks/internal/cli"
	"stacks/internal/config"
	"stacks/internal/observability"
	"stacks/internal/storage"
)

const observabilityShutdownTimeout = 10 * time.Second

func main() {
	bootstrapLogger, err := zap.NewProduction()
	if err != nil {
		panic(err)
	}

	settings, err := config.Load()
	if err != nil {
		bootstrapLogger.Error("load configuration", zap.Error(err))
		os.Exit(1)
	}

	runtime, err := observability.New(context.Background(), settings)
	if err != nil {
		bootstrapLogger.Error("initialize observability", zap.Error(err))
		os.Exit(1)
	}
	logger := runtime.Logger()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	runErr := app.Execute(
		ctx,
		os.Args[1:],
		settings,
		app.RuntimeFunc(func(ctx context.Context, settings config.Settings) error {
			return app.Run(ctx, settings, logger, runtime.TracerProvider(), runtime.MeterProvider())
		}),
		app.CommandProviderFunc(reviewCommandProvider),
		os.Stdout,
		os.Stderr,
	)
	if runErr != nil {
		logger.Error("run stacks", zap.Error(runErr))
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), observabilityShutdownTimeout)
	shutdownErr := runtime.Shutdown(shutdownCtx)
	cancel()
	if shutdownErr != nil {
		bootstrapLogger.Error("shut down observability", zap.Error(shutdownErr))
	}
	if runErr != nil || shutdownErr != nil {
		os.Exit(1)
	}
}

func reviewCommandProvider(_ context.Context, settings config.Settings, stdout, _ io.Writer) (map[string]cli.Command, error) {
	return map[string]cli.Command{
		string(config.CommandEntities): cli.CommandFunc(func(ctx context.Context, args []string) error {
			pool, err := storage.Open(ctx, settings.PoC.DatabaseURL)
			if err != nil {
				return err
			}
			defer pool.Close()
			store := cli.NewStorageReviewStore(storage.NewEntityRepository(pool))
			return (cli.EntitiesCommand{Service: &cli.EntityService{Store: store}, Output: stdout}).Run(ctx, args)
		}),
		string(config.CommandReview): cli.CommandFunc(func(ctx context.Context, args []string) error {
			pool, err := storage.Open(ctx, settings.PoC.DatabaseURL)
			if err != nil {
				return err
			}
			defer pool.Close()
			store := cli.NewStorageReviewStore(storage.NewEntityRepository(pool))
			return (cli.ReviewCommand{Service: &cli.ReviewService{Store: store}, Output: stdout}).Run(ctx, args)
		}),
	}, nil
}
