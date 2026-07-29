// Package doctor performs bounded, read-only dependency, migration, source,
// and provider preflight checks. Provider errors are deliberately converted to
// fixed messages so private identifiers and account details cannot reach the
// CLI.
package doctor

import (
	"context"
	"errors"
	"fmt"

	"github.com/JakeFAU/stacks/adapters/postgres/migration"

	"stacks/internal/modelpolicy"
	"stacks/internal/source"
)

// CheckName identifies one stable doctor result.
type CheckName string

const (
	CheckDatabaseConnectivity        CheckName = "database.connectivity"
	CheckDatabaseMigrationsCore      CheckName = "database.migrations.core"
	CheckDatabaseMigrationsDirectory CheckName = "database.migrations.directory"
	CheckGoogleAuthorization         CheckName = "google.authorization"
	CheckGoogleFolder                CheckName = "google.folder"
	CheckGoogleTabs                  CheckName = "google.tabs"
	CheckDirectoryConfiguration      CheckName = "directory.configuration"
	CheckDirectoryAuthorization      CheckName = "directory.authorization"
	CheckModelCredentials            CheckName = "model.credentials"
	CheckModelAvailability           CheckName = "model.availability"
	CheckModelDisclosure             CheckName = "model.disclosure"
)

// Status is the bounded outcome vocabulary rendered by the doctor command.
type Status string

const (
	StatusOK      Status = "ok"
	StatusWarning Status = "warning"
	StatusFailed  Status = "failed"
)

// InvocationLoggingState is the exact safe-to-report Bedrock logging state.
type InvocationLoggingState string

const (
	InvocationLoggingDisabled InvocationLoggingState = "disabled"
	InvocationLoggingEnabled  InvocationLoggingState = "enabled"
	InvocationLoggingUnknown  InvocationLoggingState = "unknown"
)

// Check is one privacy-safe doctor result.
type Check struct {
	Name        CheckName
	Status      Status
	Message     string
	Remediation string
}

// Report contains the complete ordered set of checks reached before context
// cancellation. Err is set only for cancellation or deadline expiration.
type Report struct {
	Checks []Check
	Err    error
}

// Healthy reports whether every required check succeeded. Warnings describe
// risks that need operator attention but do not make dependencies unusable.
func (report Report) Healthy() bool {
	if report.Err != nil {
		return false
	}
	for _, check := range report.Checks {
		if check.Status == StatusFailed {
			return false
		}
	}
	return true
}

// Database exposes only read-only connectivity and migration inspection.
type Database interface {
	Ping(context.Context) error
}

type scopedMigrationDatabase interface {
	MigrationStatus(context.Context) ([]migration.ScopeStatus, error)
}

// Google exposes only authorization validation and bounded source inspection.
type Google interface {
	CheckAuthorization(context.Context) error
	CheckFolder(context.Context) error
	GetRepresentative(context.Context) (source.Document, bool, error)
	GetDocument(context.Context, string) (source.Document, error)
}

// ModelProbe exposes only non-invoking credential and model metadata checks.
type ModelProbe interface {
	CheckCredentials(context.Context) error
	CheckModel(context.Context) error
}

// DisclosureProbe exposes the read-only provider disclosure state inspected
// before restricted source content may be read.
type DisclosureProbe interface {
	InvocationLogging(context.Context) (InvocationLoggingState, error)
}

// DirectoryProbe exposes only local directory OAuth readiness. It must not
// enumerate profiles or perform a directory search.
type DirectoryProbe interface {
	CheckAuthorization(context.Context) error
}

// AWS is retained as the existing combined Bedrock control-plane probe
// contract until Task 9 updates the composition root.
type AWS interface {
	ModelProbe
	DisclosureProbe
}

// Service coordinates the read-only checks. It never authenticates, invokes a
// model, applies migrations, synchronizes documents, or changes cloud config.
type Service struct {
	Database         Database
	Google           Google
	DirectoryEnabled bool
	Directory        DirectoryProbe
	Invocation       modelpolicy.Invocation
	Model            ModelProbe
	Disclosure       DisclosureProbe
	AWS              AWS
}

// Check performs a fixed number of dependency calls and returns only bounded
// messages. Dependent checks are not attempted after their prerequisite fails.
func (service Service) Check(ctx context.Context) Report {
	report := Report{}
	if service.Database == nil {
		report.Checks = append(report.Checks,
			failed(CheckDatabaseConnectivity, "PostgreSQL check is not configured", "configure STACKS_DATABASE_URL"),
			failed(CheckDatabaseMigrationsCore, "not checked because PostgreSQL is unavailable", "restore PostgreSQL connectivity"),
			failed(CheckDatabaseMigrationsDirectory, "not checked because PostgreSQL is unavailable", "restore PostgreSQL connectivity"),
		)
	} else {
		err := service.Database.Ping(ctx)
		if stop(&report, ctx, CheckDatabaseConnectivity, err) {
			return report
		}
		if err != nil {
			report.Checks = append(report.Checks,
				failed(CheckDatabaseConnectivity, "PostgreSQL is unavailable", "verify STACKS_DATABASE_URL and start PostgreSQL"),
				failed(CheckDatabaseMigrationsCore, "not checked because PostgreSQL is unavailable", "restore PostgreSQL connectivity"),
				failed(CheckDatabaseMigrationsDirectory, "not checked because PostgreSQL is unavailable", "restore PostgreSQL connectivity"),
			)
		} else {
			report.Checks = append(report.Checks, ok(CheckDatabaseConnectivity, "PostgreSQL is reachable"))
			if scoped, found := service.Database.(scopedMigrationDatabase); found {
				statuses, err := scoped.MigrationStatus(ctx)
				if stop(&report, ctx, CheckDatabaseMigrationsCore, err) {
					return report
				}
				if err != nil {
					report.Checks = append(report.Checks,
						failed(CheckDatabaseMigrationsCore, "core migration state could not be inspected", "run `make db-status`"),
						failed(CheckDatabaseMigrationsDirectory, "directory migration state could not be inspected", "run `make db-status`"),
					)
				} else {
					report.Checks = append(report.Checks, migrationChecks(statuses)...)
				}
			} else {
				report.Checks = append(report.Checks,
					failed(CheckDatabaseMigrationsCore, "core migration check is not configured", "configure migration inspection"),
					failed(CheckDatabaseMigrationsDirectory, "directory migration check is not configured", "configure migration inspection"),
				)
			}
		}
	}

	invocation, model, disclosure := service.modelChecks()
	disclosureState := InvocationLoggingUnknown
	var disclosureErr error
	sourceAccessAllowed := invocation.DataMode == modelpolicy.DataModePersonal
	if invocation.DataMode == modelpolicy.DataModeRestricted && invocation.Provider == modelpolicy.ProviderBedrock && disclosure != nil {
		disclosureState, disclosureErr = disclosure.InvocationLogging(ctx)
		if stop(&report, ctx, CheckModelDisclosure, disclosureErr) {
			return report
		}
		if disclosureErr == nil && disclosureState == InvocationLoggingDisabled {
			sourceAccessAllowed = true
		}
	}

	if !sourceAccessAllowed {
		report.Checks = appendGoogleDisclosureBlocked(report.Checks)
	} else if service.Google == nil {
		report.Checks = appendGoogleUnavailable(report.Checks, "Google check is not configured")
	} else {
		err := service.Google.CheckAuthorization(ctx)
		if stop(&report, ctx, CheckGoogleAuthorization, err) {
			return report
		}
		if err != nil {
			report.Checks = appendGoogleUnavailable(report.Checks, "Google authorization is missing, expired, or invalid")
		} else {
			report.Checks = append(report.Checks, ok(CheckGoogleAuthorization, "Google OAuth configuration and token are readable"))
			err := service.Google.CheckFolder(ctx)
			if stop(&report, ctx, CheckGoogleFolder, err) {
				return report
			}
			if err != nil {
				report.Checks = append(report.Checks,
					failed(CheckGoogleFolder, "configured Google Drive folder is unavailable", "verify folder access and STACKS_GOOGLE_FOLDER_ID"),
					failed(CheckGoogleTabs, "not checked because the Google Drive folder is unavailable", "restore configured folder access"),
				)
			} else {
				report.Checks = append(report.Checks, ok(CheckGoogleFolder, "configured Google Drive folder is readable"))
				representative, found, err := service.Google.GetRepresentative(ctx)
				if stop(&report, ctx, CheckGoogleTabs, err) {
					return report
				}
				if err != nil {
					report.Checks = append(report.Checks, failed(CheckGoogleTabs, "representative Google Doc lookup failed", "verify folder contents and Google Drive access"))
				} else if !found {
					report.Checks = append(report.Checks, failed(CheckGoogleTabs, "no supported Google Docs are available for representative tab inspection", "add an in-scope Google Doc or verify folder configuration"))
				} else {
					document, err := service.Google.GetDocument(ctx, representative.ID)
					if stop(&report, ctx, CheckGoogleTabs, err) {
						return report
					}
					if err != nil {
						report.Checks = append(report.Checks, failed(CheckGoogleTabs, "representative all-tabs classification failed", "verify configured tab titles and document access"))
					} else {
						report.Checks = append(report.Checks, classifyTabs(document.Tabs))
					}
				}
			}
		}
	}

	report.Checks = append(report.Checks, directoryConfigurationCheck(service.DirectoryEnabled))
	if !service.DirectoryEnabled {
		report.Checks = append(report.Checks, warning(
			CheckDirectoryAuthorization,
			"not checked because disabled",
			"enable STACKS_GOOGLE_DIRECTORY_ENABLED to inspect directory authorization",
		))
	} else if !sourceAccessAllowed {
		report.Checks = append(report.Checks, warning(
			CheckDirectoryAuthorization,
			"not checked because restricted model disclosure safety is not confirmed",
			"confirm Bedrock invocation logging is disabled before directory authorization inspection",
		))
	} else if service.Directory == nil {
		report.Checks = append(report.Checks, warning(
			CheckDirectoryAuthorization,
			"Google directory authorization check is not configured",
			"configure the Google directory OAuth client and token paths",
		))
	} else {
		err := service.Directory.CheckAuthorization(ctx)
		if stop(&report, ctx, CheckDirectoryAuthorization, err) {
			return report
		}
		if err != nil {
			report.Checks = append(report.Checks, warning(
				CheckDirectoryAuthorization,
				"Google directory authorization is unavailable or invalid",
				"run `stacks auth google-directory`",
			))
		} else {
			report.Checks = append(report.Checks, ok(
				CheckDirectoryAuthorization,
				"Google directory authorization is locally ready; live lookup is unexercised",
			))
		}
	}

	if model == nil {
		report.Checks = appendModelUnavailable(report.Checks, invocation.Provider, fmt.Sprintf("%s model check is not configured", providerName(invocation.Provider)))
	} else {
		err := model.CheckCredentials(ctx)
		if stop(&report, ctx, CheckModelCredentials, err) {
			return report
		}
		if err != nil {
			report.Checks = appendModelUnavailable(report.Checks, invocation.Provider, fmt.Sprintf("%s credentials are unavailable or invalid", providerName(invocation.Provider)))
		} else {
			report.Checks = append(report.Checks, ok(CheckModelCredentials, fmt.Sprintf("%s credentials are valid", providerName(invocation.Provider))))
			err := model.CheckModel(ctx)
			if stop(&report, ctx, CheckModelAvailability, err) {
				return report
			}
			if err != nil {
				report.Checks = append(report.Checks, failed(CheckModelAvailability, fmt.Sprintf("configured %s model is unavailable", providerName(invocation.Provider)), "verify STACKS_MODEL_ID, provider access, and quota"))
			} else {
				report.Checks = append(report.Checks, ok(CheckModelAvailability, fmt.Sprintf("configured %s model is available", providerName(invocation.Provider))))
			}
		}
	}

	if invocation.DataMode == modelpolicy.DataModePersonal {
		report.Checks = append(report.Checks, ok(CheckModelDisclosure, "personal data mode selected; provider logging inspection is not required"))
		return report
	}
	report.Checks = append(report.Checks, restrictedDisclosureCheck(disclosureState, disclosureErr))
	return report
}

func migrationChecks(statuses []migration.ScopeStatus) []Check {
	byScope := make(map[migration.Scope]migration.ScopeStatus, len(statuses))
	for _, status := range statuses {
		byScope[status.Scope] = status
	}
	core, coreFound := byScope["core"]
	directory, directoryFound := byScope["directory"]
	checks := make([]Check, 0, 2)
	if !coreFound {
		checks = append(checks, failed(
			CheckDatabaseMigrationsCore,
			"core migration status is missing",
			"run `make db-status`",
		))
	} else if core.State != migration.StateCurrent {
		checks = append(checks, failed(
			CheckDatabaseMigrationsCore,
			fmt.Sprintf("core migrations are %s", core.State),
			"run `make db-status` and `make db-migrate`",
		))
	} else {
		checks = append(checks, ok(CheckDatabaseMigrationsCore, "core migrations are current"))
	}
	if !directoryFound {
		checks = append(checks, failed(
			CheckDatabaseMigrationsDirectory,
			"directory migration status is missing",
			"run `make db-status`",
		))
	} else if !directory.Configured && directory.State == migration.StateAbsent {
		checks = append(checks, ok(
			CheckDatabaseMigrationsDirectory,
			"directory migrations are not configured",
		))
	} else if directory.State == migration.StateCurrent {
		checks = append(checks, ok(
			CheckDatabaseMigrationsDirectory,
			"directory migrations are current",
		))
	} else if directory.Configured {
		checks = append(checks, failed(
			CheckDatabaseMigrationsDirectory,
			fmt.Sprintf("directory migrations are %s", directory.State),
			"run `make db-status` and `make db-migrate`",
		))
	} else {
		checks = append(checks, warning(
			CheckDatabaseMigrationsDirectory,
			fmt.Sprintf("unconfigured directory migrations are %s", directory.State),
			"run `make db-status`",
		))
	}
	return checks
}

func directoryConfigurationCheck(enabled bool) Check {
	if enabled {
		return ok(CheckDirectoryConfiguration, "Google directory enrichment is enabled")
	}
	return ok(CheckDirectoryConfiguration, "Google directory enrichment is disabled")
}

func classifyTabs(tabs []source.Tab) Check {
	var transcript, notes, other int
	for _, tab := range tabs {
		switch tab.Role {
		case source.TabRoleTranscript:
			transcript++
		case source.TabRoleGeminiNotes:
			notes++
		default:
			other++
		}
	}
	message := fmt.Sprintf("representative document classified %d tabs: transcript=%d gemini-notes=%d other=%d", len(tabs), transcript, notes, other)
	if transcript != 1 {
		return failed(CheckGoogleTabs, "representative document does not classify exactly one transcript tab", "correct STACKS_TRANSCRIPT_TITLES and STACKS_NOTES_TITLES")
	}
	return ok(CheckGoogleTabs, message)
}

func restrictedDisclosureCheck(state InvocationLoggingState, err error) Check {
	if err == nil && state == InvocationLoggingDisabled {
		return ok(CheckModelDisclosure, "restricted data mode selected; Bedrock invocation logging is disabled")
	}
	return failed(CheckModelDisclosure, "restricted data mode selected; model disclosure safety is not confirmed", "confirm Bedrock invocation logging is disabled before source access")
}

func stop(report *Report, ctx context.Context, name CheckName, err error) bool {
	if ctxErr := ctx.Err(); ctxErr != nil {
		report.Err = ctxErr
		report.Checks = append(report.Checks, failed(name, "check canceled", "retry after cancellation is resolved"))
		return true
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		if errors.Is(err, context.Canceled) {
			report.Err = context.Canceled
		} else {
			report.Err = context.DeadlineExceeded
		}
		report.Checks = append(report.Checks, failed(name, "check canceled", "retry after cancellation is resolved"))
		return true
	}
	return false
}

func appendGoogleUnavailable(checks []Check, authorizationMessage string) []Check {
	return append(checks,
		failed(CheckGoogleAuthorization, authorizationMessage, "run `stacks auth google`"),
		failed(CheckGoogleFolder, "not checked because Google authorization is unavailable", "run `stacks auth google`"),
		failed(CheckGoogleTabs, "not checked because Google authorization is unavailable", "run `stacks auth google`"),
	)
}

func appendGoogleDisclosureBlocked(checks []Check) []Check {
	const message = "not checked because restricted model disclosure safety is not confirmed"
	const remediation = "confirm Bedrock invocation logging is disabled before source access"
	return append(checks,
		failed(CheckGoogleAuthorization, message, remediation),
		failed(CheckGoogleFolder, message, remediation),
		failed(CheckGoogleTabs, message, remediation),
	)
}

func appendModelUnavailable(checks []Check, provider modelpolicy.Provider, credentialsMessage string) []Check {
	return append(checks,
		failed(CheckModelCredentials, credentialsMessage, modelCredentialsRemediation(provider)),
		failed(CheckModelAvailability, fmt.Sprintf("not checked because %s credentials are unavailable", providerName(provider)), "restore model provider credentials"),
	)
}

func (service Service) modelChecks() (modelpolicy.Invocation, ModelProbe, DisclosureProbe) {
	invocation := service.Invocation
	model := service.Model
	disclosure := service.Disclosure
	if service.AWS != nil {
		if model == nil {
			model = service.AWS
		}
		if disclosure == nil {
			disclosure = service.AWS
		}
		if invocation.Provider == "" && invocation.DataMode == "" {
			invocation = modelpolicy.Invocation{Provider: modelpolicy.ProviderBedrock, DataMode: modelpolicy.DataModePersonal}
		}
	}
	return invocation, model, disclosure
}

func providerName(provider modelpolicy.Provider) string {
	if provider.Valid() {
		return string(provider)
	}
	return "model provider"
}

func modelCredentialsRemediation(provider modelpolicy.Provider) string {
	if provider == modelpolicy.ProviderBedrock {
		return "refresh AWS credentials or the configured profile"
	}
	return "configure the selected model provider API key"
}

func ok(name CheckName, message string) Check {
	return Check{Name: name, Status: StatusOK, Message: message}
}

func failed(name CheckName, message, remediation string) Check {
	return Check{Name: name, Status: StatusFailed, Message: message, Remediation: remediation}
}

func warning(name CheckName, message, remediation string) Check {
	return Check{Name: name, Status: StatusWarning, Message: message, Remediation: remediation}
}
