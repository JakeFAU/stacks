package main

import (
	"context"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"stacks/internal/analysis"
	"stacks/internal/app"
	"stacks/internal/bedrock"
	"stacks/internal/cli"
	"stacks/internal/config"
	"stacks/internal/entity"
	"stacks/internal/ingest"
	"stacks/internal/observability"
	"stacks/internal/source/drive"
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
		app.CommandProviderFunc(func(ctx context.Context, settings config.Settings, stdout, stderr io.Writer) (map[string]cli.Command, error) {
			decisions, err := runtime.DecisionRecorder()
			if err != nil {
				return nil, err
			}
			return pocCommandProvider(ctx, settings, stdout, stderr, runtime.TracerProvider().Tracer("stacks"), decisions)
		}),
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

func pocCommandProvider(
	_ context.Context,
	settings config.Settings,
	stdout, _ io.Writer,
	tracer trace.Tracer,
	decisions *observability.DecisionRecorder,
) (map[string]cli.Command, error) {
	return map[string]cli.Command{
		string(config.CommandAuth): cli.AuthCommand{Google: drive.NewAuthorizer(
			settings.PoC.GoogleOAuthClientFile,
			settings.PoC.GoogleOAuthTokenFile,
			stdout,
		)},
		string(config.CommandSync): cli.CommandFunc(func(ctx context.Context, args []string) error {
			httpClient, err := drive.NewAuthorizedHTTPClient(
				ctx,
				settings.PoC.GoogleOAuthClientFile,
				settings.PoC.GoogleOAuthTokenFile,
			)
			if err != nil {
				return err
			}
			sourceBoundary, err := drive.NewClient(ctx, httpClient, drive.NewTabClassifier(
				settings.PoC.TranscriptTitles,
				settings.PoC.NotesTitles,
			))
			if err != nil {
				return err
			}
			awsConfiguration, err := awsconfig.LoadDefaultConfig(
				ctx,
				awsconfig.WithRegion(settings.PoC.AWSRegion),
				awsconfig.WithSharedConfigProfile(settings.PoC.AWSProfile),
			)
			if err != nil {
				return err
			}
			model, err := bedrock.NewFromConfig(awsConfiguration, bedrock.Options{
				ModelID: settings.PoC.BedrockModelID, MaxTokens: settings.PoC.BedrockMaxTokens,
				MaxAttempts: settings.PoC.BedrockMaxAttempts,
			})
			if err != nil {
				return err
			}
			pool, err := storage.Open(ctx, settings.PoC.DatabaseURL)
			if err != nil {
				return err
			}
			defer pool.Close()
			service := &ingest.Service{
				Source: sourceBoundary, Model: model, Resolver: entity.Resolver{},
				Repository:   storage.NewIngestionRepository(pool),
				CollectionID: settings.PoC.GoogleFolderID, PromptVersion: settings.PoC.ExtractionPromptVersion,
				Tracer: tracer, Decisions: decisions, Now: time.Now,
			}
			return (cli.SyncCommand{Service: service, Output: stdout}).Run(ctx, args)
		}),
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
		string(config.CommandAnalyze): cli.CommandFunc(func(ctx context.Context, args []string) error {
			awsConfiguration, err := awsconfig.LoadDefaultConfig(
				ctx,
				awsconfig.WithRegion(settings.PoC.AWSRegion),
				awsconfig.WithSharedConfigProfile(settings.PoC.AWSProfile),
			)
			if err != nil {
				return err
			}
			model, err := bedrock.NewFromConfig(awsConfiguration, bedrock.Options{
				ModelID: settings.PoC.BedrockModelID, MaxTokens: settings.PoC.BedrockMaxTokens,
				MaxAttempts: settings.PoC.BedrockMaxAttempts,
			})
			if err != nil {
				return err
			}
			pool, err := storage.Open(ctx, settings.PoC.DatabaseURL)
			if err != nil {
				return err
			}
			defer pool.Close()
			service := &analysis.Service{
				Repository: storage.NewAnalysisRepository(pool), Model: model,
				PromptVersion: settings.PoC.AnalysisPromptVersion,
				Region:        settings.PoC.AWSRegion, ModelID: settings.PoC.BedrockModelID,
				MaxTokens: settings.PoC.BedrockMaxTokens, Tracer: tracer,
				Decisions: decisions, Now: time.Now,
			}
			return (cli.AnalyzeCommand{
				Service: service, EmployeeID: settings.PoC.EmployeeEntityID,
				ManagerID: settings.PoC.ManagerEntityID, Output: stdout,
			}).Run(ctx, args)
		}),
	}, nil
}
