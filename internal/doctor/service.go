// Package doctor performs bounded, read-only preflight checks for the manager
// confidence workflow. Provider errors are deliberately converted to fixed
// messages so private identifiers and account details cannot reach the CLI.
package doctor

import (
	"context"
	"errors"
	"fmt"

	"stacks/internal/source"
)

// CheckName identifies one stable doctor result.
type CheckName string

const (
	CheckDatabaseConnectivity CheckName = "database.connectivity"
	CheckDatabaseMigrations   CheckName = "database.migrations"
	CheckGoogleAuthorization  CheckName = "google.authorization"
	CheckGoogleFolder         CheckName = "google.folder"
	CheckGoogleTabs           CheckName = "google.tabs"
	CheckAWSCredentials       CheckName = "aws.credentials"
	CheckBedrockModel         CheckName = "bedrock.model"
	CheckInvocationLogging    CheckName = "bedrock.invocation_logging"
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
	MigrationsCurrent(context.Context) (bool, error)
}

// Google exposes only authorization validation and bounded source inspection.
type Google interface {
	CheckAuthorization(context.Context) error
	ListFolder(context.Context) ([]source.Document, error)
	GetDocument(context.Context, string) (source.Document, error)
}

// AWS exposes only credential, model, and logging-configuration inspection.
type AWS interface {
	CheckCredentials(context.Context) error
	CheckModel(context.Context) error
	InvocationLogging(context.Context) (InvocationLoggingState, error)
}

// Service coordinates the read-only checks. It never authenticates, invokes a
// model, applies migrations, synchronizes documents, or changes cloud config.
type Service struct {
	Database Database
	Google   Google
	AWS      AWS
}

// Check performs a fixed number of dependency calls and returns only bounded
// messages. Dependent checks are not attempted after their prerequisite fails.
func (service Service) Check(ctx context.Context) Report {
	report := Report{}
	if service.Database == nil {
		report.Checks = append(report.Checks,
			failed(CheckDatabaseConnectivity, "PostgreSQL check is not configured", "configure STACKS_DATABASE_URL"),
			failed(CheckDatabaseMigrations, "not checked because PostgreSQL is unavailable", "restore PostgreSQL connectivity"),
		)
	} else if err := service.Database.Ping(ctx); err != nil {
		if stop(&report, ctx, CheckDatabaseConnectivity, err) {
			return report
		}
		report.Checks = append(report.Checks,
			failed(CheckDatabaseConnectivity, "PostgreSQL is unavailable", "verify STACKS_DATABASE_URL and start PostgreSQL"),
			failed(CheckDatabaseMigrations, "not checked because PostgreSQL is unavailable", "restore PostgreSQL connectivity"),
		)
	} else {
		report.Checks = append(report.Checks, ok(CheckDatabaseConnectivity, "PostgreSQL is reachable"))
		current, err := service.Database.MigrationsCurrent(ctx)
		if err != nil {
			if stop(&report, ctx, CheckDatabaseMigrations, err) {
				return report
			}
			report.Checks = append(report.Checks, failed(CheckDatabaseMigrations, "database migration state could not be inspected", "run `make db-migrate`"))
		} else if !current {
			report.Checks = append(report.Checks, failed(CheckDatabaseMigrations, "database migrations are pending", "run `make db-migrate`"))
		} else {
			report.Checks = append(report.Checks, ok(CheckDatabaseMigrations, "database migrations are current"))
		}
	}

	if service.Google == nil {
		report.Checks = appendGoogleUnavailable(report.Checks, "Google check is not configured")
	} else if err := service.Google.CheckAuthorization(ctx); err != nil {
		if stop(&report, ctx, CheckGoogleAuthorization, err) {
			return report
		}
		report.Checks = appendGoogleUnavailable(report.Checks, "Google authorization is missing, expired, or invalid")
	} else {
		report.Checks = append(report.Checks, ok(CheckGoogleAuthorization, "Google OAuth configuration and token are readable"))
		documents, err := service.Google.ListFolder(ctx)
		if err != nil {
			if stop(&report, ctx, CheckGoogleFolder, err) {
				return report
			}
			report.Checks = append(report.Checks,
				failed(CheckGoogleFolder, "configured Google Drive folder is unavailable", "verify folder access and STACKS_GOOGLE_FOLDER_ID"),
				failed(CheckGoogleTabs, "not checked because the Google Drive folder is unavailable", "restore configured folder access"),
			)
		} else {
			report.Checks = append(report.Checks, ok(CheckGoogleFolder, "configured Google Drive folder is readable"))
			if len(documents) == 0 {
				report.Checks = append(report.Checks, failed(CheckGoogleTabs, "no supported Google Docs are available for representative tab inspection", "add an in-scope Google Doc or verify folder configuration"))
			} else {
				document, err := service.Google.GetDocument(ctx, documents[0].ID)
				if err != nil {
					if stop(&report, ctx, CheckGoogleTabs, err) {
						return report
					}
					report.Checks = append(report.Checks, failed(CheckGoogleTabs, "representative all-tabs classification failed", "verify configured tab titles and document access"))
				} else {
					report.Checks = append(report.Checks, classifyTabs(document.Tabs))
				}
			}
		}
	}

	if service.AWS == nil {
		report.Checks = appendAWSUnavailable(report.Checks, "AWS check is not configured")
	} else if err := service.AWS.CheckCredentials(ctx); err != nil {
		if stop(&report, ctx, CheckAWSCredentials, err) {
			return report
		}
		report.Checks = appendAWSUnavailable(report.Checks, "AWS credentials are unavailable or expired")
	} else {
		report.Checks = append(report.Checks, ok(CheckAWSCredentials, "AWS credentials are valid"))
		if err := service.AWS.CheckModel(ctx); err != nil {
			if stop(&report, ctx, CheckBedrockModel, err) {
				return report
			}
			report.Checks = append(report.Checks, failed(CheckBedrockModel, "configured Bedrock model or inference profile is unavailable", "verify STACKS_AWS_REGION, STACKS_BEDROCK_MODEL_ID, model access, and quota"))
		} else {
			report.Checks = append(report.Checks, ok(CheckBedrockModel, "configured Bedrock model or inference profile is available"))
		}
		state, err := service.AWS.InvocationLogging(ctx)
		if err != nil {
			if stop(&report, ctx, CheckInvocationLogging, err) {
				return report
			}
			state = InvocationLoggingUnknown
		}
		report.Checks = append(report.Checks, loggingCheck(state))
	}
	return report
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

func loggingCheck(state InvocationLoggingState) Check {
	switch state {
	case InvocationLoggingDisabled:
		return ok(CheckInvocationLogging, string(InvocationLoggingDisabled))
	case InvocationLoggingEnabled:
		return Check{Name: CheckInvocationLogging, Status: StatusWarning, Message: "enabled: model inputs and outputs may be disclosed to configured log destinations"}
	default:
		return Check{Name: CheckInvocationLogging, Status: StatusWarning, Message: "unknown: invocation logging could not be inspected; do not assume it is disabled"}
	}
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

func appendAWSUnavailable(checks []Check, credentialsMessage string) []Check {
	return append(checks,
		failed(CheckAWSCredentials, credentialsMessage, "refresh the configured AWS profile credentials"),
		failed(CheckBedrockModel, "not checked because AWS credentials are unavailable", "restore AWS credentials"),
		loggingCheck(InvocationLoggingUnknown),
	)
}

func ok(name CheckName, message string) Check {
	return Check{Name: name, Status: StatusOK, Message: message}
}

func failed(name CheckName, message, remediation string) Check {
	return Check{Name: name, Status: StatusFailed, Message: message, Remediation: remediation}
}
