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
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/JakeFAU/stacks/adapters/postgres/coremigrations"
	"github.com/JakeFAU/stacks/adapters/postgres/directorymigrations"
	"github.com/JakeFAU/stacks/adapters/postgres/migration"

	"stacks/internal/analysis"
	"stacks/internal/app"
	"stacks/internal/cli"
	"stacks/internal/config"
	"stacks/internal/directory"
	"stacks/internal/doctor"
	"stacks/internal/entity"
	"stacks/internal/extract"
	"stacks/internal/googledirectory"
	"stacks/internal/ingest"
	"stacks/internal/localdb"
	"stacks/internal/modelpolicy"
	"stacks/internal/modeltelemetry"
	"stacks/internal/observability"
	"stacks/internal/source"
	"stacks/internal/source/drive"
	"stacks/internal/storage"
)

const (
	observabilityShutdownTimeout      = 10 * time.Second
	awsAccessDeniedErrorCode          = "AccessDenied"
	awsAccessDeniedExceptionErrorCode = "AccessDeniedException"
	googleDirectoryMaximumResults     = 25
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
			invocations, err := modeltelemetry.NewMetricsRecorder(runtime.MeterProvider().Meter("stacks"))
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
	ctx context.Context,
	settings config.Settings,
	stdout, _ io.Writer,
	tracer trace.Tracer,
	decisions *observability.DecisionRecorder,
	invocations modeltelemetry.Recorder,
) (map[string]cli.Command, error) {
	return pocCommandProviderWithRuntime(ctx, settings, stdout, io.Discard, tracer, decisions, invocations, defaultPoCCommandRuntime())
}

type doctorDatabase interface {
	doctor.Database
	Close()
}

type pocCommandRuntime struct {
	newDriveAuthorizer      func(string, string, io.Writer) cli.GoogleAuthorizer
	newDirectoryAuthorizer  func(string, string, io.Writer) cli.GoogleAuthorizer
	newDoctorDatabase       func(string) doctorDatabase
	newScopedDoctorDatabase func(string, []migration.Scope) doctorDatabase
	newDoctorGoogle         func(config.PoCSettings) doctor.Google
	newDoctorDirectory      func(config.GoogleDirectorySettings) doctor.DirectoryProbe
	newDoctorProviderProbe  func(config.ModelSettings) (doctor.ModelProbe, doctor.DisclosureProbe, error)
	newSource               func(context.Context, config.PoCSettings) (source.Source, error)
	newDirectoryLookup      func(context.Context, config.GoogleDirectorySettings) (directory.Lookup, error)
	openSyncRepositories    func(context.Context, string, bool) (ingest.Repository, directory.Repository, func(), error)
	openReviewRepositories  func(context.Context, string, bool) (*storage.EntityRepository, directory.Repository, func(), error)
	openAnalysisRepository  func(context.Context, string) (analysis.Repository, func(), error)
	newModel                func(context.Context, config.ModelSettings, modeltelemetry.Recorder, trace.Tracer) (extract.Model, error)
	newMigrationApplier     func(config.DatabaseSettings) cli.MigrationApplier
	newMigrationInspector   func(config.DatabaseSettings) cli.MigrationInspector
	newDatabaseResetter     func(config.DatabaseSettings) cli.DatabaseResetter
}

func defaultPoCCommandRuntime() pocCommandRuntime {
	return pocCommandRuntime{
		newDriveAuthorizer: func(clientFile, tokenFile string, output io.Writer) cli.GoogleAuthorizer {
			return drive.NewAuthorizer(clientFile, tokenFile, output)
		},
		newDirectoryAuthorizer: func(clientFile, tokenFile string, output io.Writer) cli.GoogleAuthorizer {
			return googledirectory.NewAuthorizer(clientFile, tokenFile, output)
		},
		newDoctorDatabase: func(databaseURL string) doctorDatabase {
			return doctor.NewPostgresProbe(databaseURL)
		},
		newScopedDoctorDatabase: func(databaseURL string, scopes []migration.Scope) doctorDatabase {
			return doctor.NewPostgresProbeWithScopes(databaseURL, scopes)
		},
		newDoctorGoogle: func(settings config.PoCSettings) doctor.Google {
			return doctor.NewGoogleProbe(
				settings.GoogleOAuthClientFile, settings.GoogleOAuthTokenFile, settings.GoogleFolderID,
				drive.NewTabClassifier(settings.TranscriptTitles, settings.NotesTitles),
			)
		},
		newDoctorDirectory: func(settings config.GoogleDirectorySettings) doctor.DirectoryProbe {
			return googledirectory.NewProbe(settings.OAuthClientFile, settings.OAuthTokenFile)
		},
		newDoctorProviderProbe: newDoctorProviderProbe,
		newSource: func(ctx context.Context, settings config.PoCSettings) (source.Source, error) {
			httpClient, err := drive.NewAuthorizedHTTPClient(ctx, settings.GoogleOAuthClientFile, settings.GoogleOAuthTokenFile)
			if err != nil {
				return nil, err
			}
			return drive.NewClient(ctx, httpClient, drive.NewTabClassifier(settings.TranscriptTitles, settings.NotesTitles))
		},
		newDirectoryLookup: func(ctx context.Context, settings config.GoogleDirectorySettings) (directory.Lookup, error) {
			httpClient, err := googledirectory.NewAuthorizedHTTPClient(
				ctx,
				settings.OAuthClientFile,
				settings.OAuthTokenFile,
			)
			if err != nil {
				return nil, err
			}
			return googledirectory.NewClient(ctx, httpClient, googleDirectoryMaximumResults)
		},
		openSyncRepositories: func(ctx context.Context, databaseURL string, includeDirectory bool) (ingest.Repository, directory.Repository, func(), error) {
			pool, err := storage.Open(ctx, databaseURL)
			if err != nil {
				return nil, nil, nil, err
			}
			var directoryRepository directory.Repository
			if includeDirectory {
				directoryRepository = storage.NewDirectoryRepository(pool)
			}
			return storage.NewIngestionRepository(pool), directoryRepository, pool.Close, nil
		},
		openReviewRepositories: func(ctx context.Context, databaseURL string, includeDirectory bool) (*storage.EntityRepository, directory.Repository, func(), error) {
			pool, err := storage.Open(ctx, databaseURL)
			if err != nil {
				return nil, nil, nil, err
			}
			var directoryRepository directory.Repository
			if includeDirectory {
				directoryRepository = storage.NewDirectoryRepository(pool)
			}
			return storage.NewEntityRepository(pool), directoryRepository, pool.Close, nil
		},
		openAnalysisRepository: func(ctx context.Context, databaseURL string) (analysis.Repository, func(), error) {
			pool, err := storage.Open(ctx, databaseURL)
			if err != nil {
				return nil, nil, err
			}
			return storage.NewAnalysisRepository(pool), pool.Close, nil
		},
		newModel: newModelWithContext,
		newMigrationApplier: func(settings config.DatabaseSettings) cli.MigrationApplier {
			return embeddedMigrationApplier{settings: settings}
		},
		newMigrationInspector: func(settings config.DatabaseSettings) cli.MigrationInspector {
			return embeddedMigrationInspector{settings: settings}
		},
		newDatabaseResetter: func(settings config.DatabaseSettings) cli.DatabaseResetter {
			return localdb.Resetter{
				DatabaseURLs: []string{settings.URL, settings.MigrationURL},
				Runner:       localdb.ExecRunner{},
				Migrator:     embeddedMigrationApplier{settings: settings},
			}
		},
	}
}

func pocCommandProviderWithRuntime(
	_ context.Context,
	settings config.Settings,
	stdout, _ io.Writer,
	tracer trace.Tracer,
	decisions *observability.DecisionRecorder,
	invocations modeltelemetry.Recorder,
	runtime pocCommandRuntime,
) (map[string]cli.Command, error) {
	return map[string]cli.Command{
		string(config.CommandDBMigrate): cli.CommandFunc(func(ctx context.Context, args []string) error {
			return runDatabaseCommand(ctx, tracer, "database.migrate", func(ctx context.Context) error {
				if err := settings.Validate(config.CommandDBMigrate); err != nil {
					return err
				}
				if runtime.newMigrationApplier == nil {
					return errors.New("db-migrate command dependencies are not configured")
				}
				return (cli.DBMigrateCommand{
					Migrator: runtime.newMigrationApplier(settings.Database),
					Output:   stdout,
				}).Run(ctx, args)
			})
		}),
		string(config.CommandDBStatus): cli.CommandFunc(func(ctx context.Context, args []string) error {
			return runDatabaseCommand(ctx, tracer, "database.status", func(ctx context.Context) error {
				if err := settings.Validate(config.CommandDBStatus); err != nil {
					return err
				}
				if runtime.newMigrationInspector == nil {
					return errors.New("db-status command dependencies are not configured")
				}
				return (cli.DBStatusCommand{
					Inspector: runtime.newMigrationInspector(settings.Database),
					Output:    stdout,
				}).Run(ctx, args)
			})
		}),
		string(config.CommandDBReset): cli.CommandFunc(func(ctx context.Context, args []string) error {
			return runDatabaseCommand(ctx, tracer, "database.reset", func(ctx context.Context) error {
				if err := settings.Validate(config.CommandDBReset); err != nil {
					return err
				}
				if runtime.newDatabaseResetter == nil {
					return errors.New("db-reset command dependencies are not configured")
				}
				return (cli.DBResetCommand{
					Resetter: runtime.newDatabaseResetter(settings.Database),
					Output:   stdout,
				}).Run(ctx, args)
			})
		}),
		string(config.CommandAuth): cli.CommandFunc(func(ctx context.Context, args []string) error {
			if len(args) != 1 {
				return (cli.AuthCommand{}).Run(ctx, args)
			}
			switch config.GoogleAuthTarget(args[0]) {
			case config.GoogleAuthDrive:
				if err := settings.PoC.ValidateGoogleAuth(config.GoogleAuthDrive); err != nil {
					return err
				}
				if runtime.newDriveAuthorizer == nil {
					return errors.New("google authorization is not configured")
				}
				return (cli.AuthCommand{GoogleDrive: runtime.newDriveAuthorizer(
					settings.PoC.GoogleOAuthClientFile, settings.PoC.GoogleOAuthTokenFile, stdout,
				)}).Run(ctx, args)
			case config.GoogleAuthDirectory:
				if err := settings.PoC.ValidateGoogleAuth(config.GoogleAuthDirectory); err != nil {
					return err
				}
				if runtime.newDirectoryAuthorizer == nil {
					return errors.New("google directory authorization is not configured")
				}
				return (cli.AuthCommand{GoogleDirectory: runtime.newDirectoryAuthorizer(
					settings.PoC.Directory.OAuthClientFile, settings.PoC.Directory.OAuthTokenFile, stdout,
				)}).Run(ctx, args)
			default:
				return (cli.AuthCommand{}).Run(ctx, args)
			}
		}),
		string(config.CommandDoctor): cli.CommandFunc(func(ctx context.Context, args []string) error {
			if err := settings.PoC.Validate(config.CommandDoctor); err != nil {
				return err
			}
			if runtime.newDoctorDatabase == nil ||
				runtime.newDoctorGoogle == nil ||
				runtime.newDoctorProviderProbe == nil ||
				(settings.PoC.Directory.Enabled && runtime.newDoctorDirectory == nil) {
				return errors.New("doctor command dependencies are not configured")
			}
			database := runtime.newDoctorDatabase(settings.PoC.DatabaseURL)
			if runtime.newScopedDoctorDatabase != nil {
				database = runtime.newScopedDoctorDatabase(
					settings.PoC.DatabaseURL,
					configuredMigrationScopes(settings.Database.Scopes),
				)
			}
			defer database.Close()
			model, disclosure, err := runtime.newDoctorProviderProbe(settings.PoC.Model)
			if err != nil {
				return err
			}
			var directoryProbe doctor.DirectoryProbe
			if settings.PoC.Directory.Enabled {
				directoryProbe = runtime.newDoctorDirectory(settings.PoC.Directory)
			}
			return (cli.DoctorCommand{
				Service: doctor.Service{
					Database: database, Google: runtime.newDoctorGoogle(settings.PoC),
					DirectoryEnabled: settings.PoC.Directory.Enabled, Directory: directoryProbe,
					Invocation: modelInvocation(settings.PoC.Model), Model: model, Disclosure: disclosure,
				},
				Output: stdout,
			}).Run(ctx, args)
		}),
		string(config.CommandSync): cli.CommandFunc(func(ctx context.Context, args []string) error {
			if err := settings.PoC.Validate(config.CommandSync); err != nil {
				return err
			}
			if err := requireRestrictedDisclosure(ctx, settings.PoC.Model, runtime); err != nil {
				return err
			}
			if runtime.newSource == nil ||
				runtime.openSyncRepositories == nil ||
				runtime.newModel == nil ||
				(settings.PoC.Directory.Enabled && runtime.newDirectoryLookup == nil) {
				return errors.New("sync command dependencies are not configured")
			}
			sourceBoundary, err := runtime.newSource(ctx, settings.PoC)
			if err != nil {
				return err
			}
			var directoryLookup directory.Lookup
			if settings.PoC.Directory.Enabled {
				directoryLookup, err = runtime.newDirectoryLookup(ctx, settings.PoC.Directory)
				if cancellationErr := canonicalContextError(ctx, err); cancellationErr != nil {
					return cancellationErr
				}
				if err != nil {
					directoryLookup = nil
				}
			}
			repository, directoryRepository, closeRepository, err := runtime.openSyncRepositories(
				ctx,
				settings.PoC.DatabaseURL,
				settings.PoC.Directory.Enabled,
			)
			if err != nil {
				return err
			}
			if closeRepository != nil {
				defer closeRepository()
			}
			var directoryService *directory.Service
			if settings.PoC.Directory.Enabled {
				directoryService, err = newDirectoryService(
					settings.PoC.Directory,
					directoryLookup,
					directoryRepository,
					tracer,
					decisions,
					time.Now,
				)
				if err != nil {
					return err
				}
			}
			model, err := runtime.newModel(ctx, settings.PoC.Model, invocations, tracer)
			if err != nil {
				return err
			}
			service := newIngestionService(
				settings.PoC, sourceBoundary, model, repository,
				directoryService, tracer, decisions, time.Now,
			)
			return (cli.SyncCommand{Service: service, Output: stdout}).Run(ctx, args)
		}),
		string(config.CommandEntities): cli.CommandFunc(func(ctx context.Context, args []string) error {
			if err := settings.PoC.Validate(config.CommandEntities); err != nil {
				return err
			}
			pool, err := storage.Open(ctx, settings.PoC.DatabaseURL)
			if err != nil {
				return err
			}
			defer pool.Close()
			store := cli.NewStorageReviewStore(storage.NewEntityRepository(pool))
			return (cli.EntitiesCommand{Service: &cli.EntityService{Store: store}, Output: stdout}).Run(ctx, args)
		}),
		string(config.CommandReview): cli.CommandFunc(func(ctx context.Context, args []string) error {
			if err := settings.PoC.Validate(config.CommandReview); err != nil {
				return err
			}
			if settings.PoC.Directory.Enabled &&
				settings.PoC.Model.DataMode == modelpolicy.DataModeRestricted {
				if err := requireRestrictedDisclosure(ctx, settings.PoC.Model, runtime); err != nil {
					return err
				}
			}
			if runtime.openReviewRepositories == nil ||
				(settings.PoC.Directory.Enabled && runtime.newDirectoryLookup == nil) {
				return errors.New("review command dependencies are not configured")
			}
			var directoryLookup directory.Lookup
			var err error
			if settings.PoC.Directory.Enabled {
				directoryLookup, err = runtime.newDirectoryLookup(ctx, settings.PoC.Directory)
				if cancellationErr := canonicalContextError(ctx, err); cancellationErr != nil {
					return cancellationErr
				}
				if err != nil {
					directoryLookup = nil
				}
			}
			entityRepository, directoryRepository, closeRepositories, err := runtime.openReviewRepositories(
				ctx,
				settings.PoC.DatabaseURL,
				settings.PoC.Directory.Enabled,
			)
			if err != nil {
				return err
			}
			if closeRepositories != nil {
				defer closeRepositories()
			}
			var verifier cli.ReviewerEmailVerifier
			if settings.PoC.Directory.Enabled {
				directoryService, err := newDirectoryService(
					settings.PoC.Directory,
					directoryLookup,
					directoryRepository,
					tracer,
					decisions,
					time.Now,
				)
				if err != nil {
					return err
				}
				verifier = directoryService
			}
			store := cli.NewStorageReviewStore(entityRepository, verifier)
			return (cli.ReviewCommand{Service: &cli.ReviewService{Store: store}, Output: stdout}).Run(ctx, args)
		}),
		string(config.CommandAnalyze): cli.CommandFunc(func(ctx context.Context, args []string) error {
			if err := settings.PoC.Validate(config.CommandAnalyze); err != nil {
				return err
			}
			if err := requireRestrictedDisclosure(ctx, settings.PoC.Model, runtime); err != nil {
				return err
			}
			if runtime.openAnalysisRepository == nil || runtime.newModel == nil {
				return errors.New("analyze command dependencies are not configured")
			}
			repository, closeRepository, err := runtime.openAnalysisRepository(ctx, settings.PoC.DatabaseURL)
			if err != nil {
				return err
			}
			if closeRepository != nil {
				defer closeRepository()
			}
			model, err := runtime.newModel(ctx, settings.PoC.Model, invocations, tracer)
			if err != nil {
				return err
			}
			service := newAnalysisService(
				settings.PoC, repository, model,
				tracer, decisions, time.Now,
			)
			return (cli.AnalyzeCommand{
				Service: service, EmployeeID: settings.PoC.EmployeeEntityID,
				ManagerID: settings.PoC.ManagerEntityID, Output: stdout,
			}).Run(ctx, args)
		}),
	}, nil
}

func runDatabaseCommand(
	ctx context.Context,
	tracer trace.Tracer,
	name string,
	run func(context.Context) error,
) (runErr error) {
	if tracer == nil {
		return run(ctx)
	}
	ctx, span := tracer.Start(ctx, name)
	defer func() {
		outcome := "success"
		if runErr != nil {
			outcome = "failure"
			span.SetStatus(codes.Error, "database operation failed")
		} else {
			span.SetStatus(codes.Ok, "")
		}
		span.SetAttributes(attribute.String("stacks.outcome", outcome))
		span.End()
	}()
	return run(ctx)
}

type embeddedMigrationApplier struct {
	settings config.DatabaseSettings
}

func (applier embeddedMigrationApplier) Apply(
	ctx context.Context,
) (migration.ApplyResult, error) {
	manifests, err := selectedMigrationManifests(applier.settings.Scopes)
	if err != nil {
		return migration.ApplyResult{}, err
	}
	return (migration.Migrator{
		DatabaseURL:     applier.settings.MigrationURL,
		ApplicationRole: applier.settings.ApplicationRole,
		Manifests:       manifests,
	}).Apply(ctx)
}

type embeddedMigrationInspector struct {
	settings config.DatabaseSettings
}

func (inspector embeddedMigrationInspector) Status(
	ctx context.Context,
) ([]migration.ScopeStatus, error) {
	manifests, err := knownMigrationManifests()
	if err != nil {
		return nil, err
	}
	return (migration.Inspector{
		DatabaseURL: inspector.settings.URL,
		Manifests:   manifests,
		Configured:  configuredMigrationScopes(inspector.settings.Scopes),
	}).Status(ctx)
}

func selectedMigrationManifests(
	scopes []config.DatabaseScope,
) ([]migration.Manifest, error) {
	known, err := knownMigrationManifests()
	if err != nil {
		return nil, err
	}
	selected := make(map[migration.Scope]bool, len(scopes))
	for _, scope := range configuredMigrationScopes(scopes) {
		selected[scope] = true
	}
	result := make([]migration.Manifest, 0, len(selected))
	for _, manifest := range known {
		if selected[manifest.Scope] {
			result = append(result, manifest)
		}
	}
	return result, nil
}

func knownMigrationManifests() ([]migration.Manifest, error) {
	core, err := coremigrations.Manifest()
	if err != nil {
		return nil, err
	}
	directoryManifest, err := directorymigrations.Manifest()
	if err != nil {
		return nil, err
	}
	return []migration.Manifest{core, directoryManifest}, nil
}

func configuredMigrationScopes(scopes []config.DatabaseScope) []migration.Scope {
	if len(scopes) == 0 {
		return []migration.Scope{"core"}
	}
	result := make([]migration.Scope, len(scopes))
	for index, scope := range scopes {
		result[index] = migration.Scope(scope)
	}
	return result
}

func requireRestrictedDisclosure(ctx context.Context, settings config.ModelSettings, runtime pocCommandRuntime) error {
	if settings.DataMode == modelpolicy.DataModePersonal {
		return nil
	}
	if runtime.newDoctorProviderProbe == nil {
		return doctor.ErrDisclosureNotConfirmed
	}
	_, disclosure, err := runtime.newDoctorProviderProbe(settings)
	if err != nil {
		return err
	}
	return doctor.RequireRestrictedDisclosure(ctx, modelInvocation(settings), disclosure)
}

func canonicalContextError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); errors.Is(ctxErr, context.Canceled) {
		return context.Canceled
	}
	if ctxErr := ctx.Err(); errors.Is(ctxErr, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return nil
}

func modelInvocation(settings config.ModelSettings) modelpolicy.Invocation {
	region := ""
	if settings.Provider == modelpolicy.ProviderBedrock {
		region = strings.TrimSpace(settings.AWSRegion)
	}
	return modelpolicy.Invocation{Provider: settings.Provider, DataMode: settings.DataMode, Region: region}
}

func newIngestionService(
	settings config.PoCSettings,
	sourceBoundary source.Source,
	model extract.Model,
	repository ingest.Repository,
	identityEnricher ingest.IdentityEnricher,
	tracer trace.Tracer,
	decisions ingest.DecisionRecorder,
	now func() time.Time,
) *ingest.Service {
	return &ingest.Service{
		Source: sourceBoundary, Model: model, Resolver: entity.Resolver{}, Repository: repository,
		CollectionID: settings.GoogleFolderID, PromptVersion: settings.ExtractionPromptVersion,
		Provider: settings.Model.Provider, DataMode: settings.Model.DataMode,
		Region: modelInvocation(settings.Model).Region, ModelID: strings.TrimSpace(settings.Model.ModelID),
		MaxTokens:      settings.Model.MaxOutputTokens,
		LeaseDuration:  settings.IngestionLeaseDuration,
		AttemptTimeout: settings.IngestionAttemptTimeout,
		Tracer:         tracer, Decisions: decisions, IdentityEnricher: identityEnricher, Now: now,
	}
}

func newDirectoryService(
	settings config.GoogleDirectorySettings,
	lookup directory.Lookup,
	repository directory.Repository,
	tracer trace.Tracer,
	decisions directory.DecisionRecorder,
	now func() time.Time,
) (*directory.Service, error) {
	policy, err := entity.NewDirectoryPolicy(settings.EmailDomains)
	if err != nil {
		return nil, errors.New("construct directory service: approved-domain policy is invalid")
	}
	return &directory.Service{
		Lookup:      lookup,
		Repository:  repository,
		Policy:      policy,
		Enabled:     settings.Enabled,
		Freshness:   settings.Freshness,
		RetryAfter:  settings.RetryAfter,
		MaxAttempts: settings.MaxAttempts,
		Tracer:      tracer,
		Decisions:   decisions,
		Now:         now,
		Wait:        waitForDirectoryRetry,
	}, nil
}

func waitForDirectoryRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func newAnalysisService(
	settings config.PoCSettings,
	repository analysis.Repository,
	model extract.Model,
	tracer trace.Tracer,
	decisions analysis.DecisionRecorder,
	now func() time.Time,
) *analysis.Service {
	return &analysis.Service{
		Repository: repository, Model: model, PromptVersion: settings.AnalysisPromptVersion,
		Provider: settings.Model.Provider, DataMode: settings.Model.DataMode,
		Region: modelInvocation(settings.Model).Region, ModelID: strings.TrimSpace(settings.Model.ModelID),
		MaxTokens: settings.Model.MaxOutputTokens, Tracer: tracer,
		Decisions: decisions, Now: now,
	}
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
