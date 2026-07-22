package main

import (
	"context"
	"errors"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/smithy-go"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"stacks/internal/analysis"
	"stacks/internal/app"
	"stacks/internal/bedrock"
	"stacks/internal/cli"
	"stacks/internal/config"
	"stacks/internal/doctor"
	"stacks/internal/entity"
	"stacks/internal/extract"
	"stacks/internal/ingest"
	"stacks/internal/observability"
	"stacks/internal/source/drive"
	"stacks/internal/storage"
)

const (
	observabilityShutdownTimeout      = 10 * time.Second
	awsAccessDeniedErrorCode          = "AccessDenied"
	awsAccessDeniedExceptionErrorCode = "AccessDeniedException"
)

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
			invocations, err := bedrock.NewMetricsInvocationRecorder(runtime.MeterProvider().Meter("stacks"))
			if err != nil {
				return nil, err
			}
			return pocCommandProvider(ctx, settings, stdout, stderr, runtime.TracerProvider().Tracer("stacks"), decisions, invocations)
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
	invocations bedrock.InvocationRecorder,
) (map[string]cli.Command, error) {
	return map[string]cli.Command{
		string(config.CommandAuth): cli.AuthCommand{Google: drive.NewAuthorizer(
			settings.PoC.GoogleOAuthClientFile,
			settings.PoC.GoogleOAuthTokenFile,
			stdout,
		)},
		string(config.CommandDoctor): cli.CommandFunc(func(ctx context.Context, args []string) error {
			database := doctor.NewPostgresProbe(settings.PoC.DatabaseURL)
			defer database.Close()
			google := doctor.NewGoogleProbe(
				settings.PoC.GoogleOAuthClientFile,
				settings.PoC.GoogleOAuthTokenFile,
				settings.PoC.GoogleFolderID,
				drive.NewTabClassifier(settings.PoC.TranscriptTitles, settings.PoC.NotesTitles),
			)
			aws := doctor.NewAWSProbe(settings.PoC.AWSProfile, settings.PoC.AWSRegion, settings.PoC.BedrockModelID)
			return (cli.DoctorCommand{
				Service: doctor.Service{Database: database, Google: google, AWS: aws},
				Output:  stdout,
			}).Run(ctx, args)
		}),
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
			awsConfiguration, err := loadAWSConfiguration(ctx, settings.PoC.AWSProfile, settings.PoC.AWSRegion)
			if err != nil {
				return err
			}
			model, err := bedrock.NewFromConfig(awsConfiguration, bedrock.Options{
				ModelID: settings.PoC.BedrockModelID, MaxTokens: settings.PoC.BedrockMaxTokens,
				MaxAttempts: settings.PoC.BedrockMaxAttempts, Recorder: invocations, Tracer: tracer,
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
				Region: strings.TrimSpace(settings.PoC.AWSRegion), ModelID: strings.TrimSpace(settings.PoC.BedrockModelID),
				MaxTokens:      settings.PoC.BedrockMaxTokens,
				LeaseDuration:  settings.PoC.IngestionLeaseDuration,
				AttemptTimeout: settings.PoC.IngestionAttemptTimeout,
				Tracer:         tracer, Decisions: decisions, Now: time.Now,
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
			awsConfiguration, err := loadAWSConfiguration(ctx, settings.PoC.AWSProfile, settings.PoC.AWSRegion)
			if err != nil {
				return err
			}
			model, err := bedrock.NewFromConfig(awsConfiguration, bedrock.Options{
				ModelID: settings.PoC.BedrockModelID, MaxTokens: settings.PoC.BedrockMaxTokens,
				MaxAttempts: settings.PoC.BedrockMaxAttempts, Recorder: invocations, Tracer: tracer,
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
				Region:        strings.TrimSpace(settings.PoC.AWSRegion), ModelID: strings.TrimSpace(settings.PoC.BedrockModelID),
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

func awsLoadOptions(profile, region string) []func(*awsconfig.LoadOptions) error {
	options := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(strings.TrimSpace(region))}
	if profile = strings.TrimSpace(profile); profile != "" {
		options = append(options, awsconfig.WithSharedConfigProfile(profile))
	}
	return options
}

func loadAWSConfiguration(ctx context.Context, profile, region string) (aws.Config, error) {
	configuration, err := awsconfig.LoadDefaultConfig(ctx, awsLoadOptions(profile, region)...)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return aws.Config{}, ctxErr
		}
		return aws.Config{}, extract.ErrAuthentication
	}
	if err := validateAWSConfigurationCredentials(ctx, configuration); err != nil {
		return aws.Config{}, err
	}
	configuration.Credentials = boundedAWSCredentialsProvider{provider: configuration.Credentials}
	return configuration, nil
}

func validateAWSConfigurationCredentials(ctx context.Context, configuration aws.Config) error {
	if configuration.Credentials == nil {
		return extract.ErrAuthentication
	}
	_, err := (boundedAWSCredentialsProvider{provider: configuration.Credentials}).Retrieve(ctx)
	return err
}

type boundedAWSCredentialsProvider struct {
	provider aws.CredentialsProvider
}

func (provider boundedAWSCredentialsProvider) Retrieve(ctx context.Context) (aws.Credentials, error) {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return aws.Credentials{}, ctxErr
	}
	if provider.provider == nil {
		return aws.Credentials{}, extract.ErrAuthentication
	}
	credentials, err := provider.provider.Retrieve(ctx)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return aws.Credentials{}, ctxErr
		}
		var apiError smithy.APIError
		if errors.As(err, &apiError) && (apiError.ErrorCode() == awsAccessDeniedErrorCode || apiError.ErrorCode() == awsAccessDeniedExceptionErrorCode) {
			return aws.Credentials{}, extract.ErrAuthorization
		}
		return aws.Credentials{}, extract.ErrAuthentication
	}
	if !credentials.HasKeys() || credentials.Expired() {
		return aws.Credentials{}, extract.ErrAuthentication
	}
	return credentials, nil
}
