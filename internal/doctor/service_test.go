package doctor

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stacks/internal/source"
)

func TestDoctorChecksEveryReadOnlyDependencyWithoutMutation(t *testing.T) {
	database := &fakeDatabase{migrationsCurrent: true}
	google := &fakeGoogle{
		documents: []source.Document{{ID: "synthetic-document"}},
		document: source.Document{Tabs: []source.Tab{
			{Role: source.TabRoleGeminiNotes},
			{Role: source.TabRoleTranscript},
			{Role: source.TabRoleOther},
		}},
	}
	aws := &fakeAWS{loggingState: InvocationLoggingDisabled}

	report := (Service{Database: database, Google: google, AWS: aws}).Check(context.Background())

	assertCheck(t, report, CheckDatabaseConnectivity, StatusOK, "PostgreSQL is reachable")
	assertCheck(t, report, CheckDatabaseMigrations, StatusOK, "database migrations are current")
	assertCheck(t, report, CheckGoogleAuthorization, StatusOK, "Google OAuth configuration and token are readable")
	assertCheck(t, report, CheckGoogleFolder, StatusOK, "configured Google Drive folder is readable")
	assertCheck(t, report, CheckGoogleTabs, StatusOK, "representative document classified 3 tabs: transcript=1 gemini-notes=1 other=1")
	assertCheck(t, report, CheckAWSCredentials, StatusOK, "AWS credentials are valid")
	assertCheck(t, report, CheckBedrockModel, StatusOK, "configured Bedrock model or inference profile is available")
	assertCheck(t, report, CheckInvocationLogging, StatusOK, "disabled")
	if !report.Healthy() {
		t.Fatal("Report.Healthy() = false, want true")
	}
	if database.applyMigrationsCalls != 0 || google.authorizeCalls != 0 || google.syncCalls != 0 || aws.invokeCalls != 0 || aws.configureLoggingCalls != 0 {
		t.Fatalf("mutation calls = database:%d authorize:%d sync:%d invoke:%d logging:%d, want all zero",
			database.applyMigrationsCalls, google.authorizeCalls, google.syncCalls, aws.invokeCalls, aws.configureLoggingCalls)
	}
}

func TestDoctorReportsInvocationLoggingEnabledOrUnknownWithoutClaimingSafety(t *testing.T) {
	tests := []struct {
		name    string
		state   InvocationLoggingState
		err     error
		status  Status
		message string
	}{
		{name: "enabled", state: InvocationLoggingEnabled, status: StatusWarning, message: "enabled: model inputs and outputs may be disclosed to configured log destinations"},
		{name: "access denied", err: errors.New("AccessDeniedException: private account detail"), status: StatusWarning, message: "unknown: invocation logging could not be inspected; do not assume it is disabled"},
		{name: "other inspection error", err: errors.New("request ID private-request"), status: StatusWarning, message: "unknown: invocation logging could not be inspected; do not assume it is disabled"},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			report := healthyService(&fakeAWS{loggingState: testCase.state, loggingErr: testCase.err}).Check(context.Background())
			check := assertCheck(t, report, CheckInvocationLogging, testCase.status, testCase.message)
			if strings.Contains(check.Message, "private") {
				t.Fatalf("invocation logging message leaked provider error: %q", check.Message)
			}
		})
	}
}

func TestDoctorDirectsMissingOrExpiredGoogleAuthorizationToAuthCommand(t *testing.T) {
	google := &fakeGoogle{authorizationErr: errors.New("expired token containing private identifier")}
	report := (Service{
		Database: &fakeDatabase{migrationsCurrent: true},
		Google:   google,
		AWS:      &fakeAWS{loggingState: InvocationLoggingDisabled},
	}).Check(context.Background())

	check := assertCheck(t, report, CheckGoogleAuthorization, StatusFailed, "Google authorization is missing, expired, or invalid")
	if check.Remediation != "run `stacks auth google`" {
		t.Fatalf("Google remediation = %q, want stacks auth command", check.Remediation)
	}
	assertCheck(t, report, CheckGoogleFolder, StatusFailed, "not checked because Google authorization is unavailable")
	assertCheck(t, report, CheckGoogleTabs, StatusFailed, "not checked because Google authorization is unavailable")
	if google.listCalls != 0 || google.getCalls != 0 || strings.Contains(check.Message, "private") {
		t.Fatalf("dependent calls/leak = list:%d get:%d message:%q", google.listCalls, google.getCalls, check.Message)
	}
}

func TestDoctorBoundsProviderErrorsAndPreservesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	database := &fakeDatabase{ping: func(context.Context) error {
		cancel()
		return context.Canceled
	}}
	google := &fakeGoogle{}
	aws := &fakeAWS{}

	report := (Service{Database: database, Google: google, AWS: aws}).Check(ctx)

	assertCheck(t, report, CheckDatabaseConnectivity, StatusFailed, "check canceled")
	if !errors.Is(report.Err, context.Canceled) {
		t.Fatalf("Report.Err = %v, want context.Canceled", report.Err)
	}
	if database.migrationsCalls != 0 || google.authorizationCalls != 0 || aws.credentialsCalls != 0 {
		t.Fatalf("calls after cancellation = migrations:%d google:%d aws:%d, want zero", database.migrationsCalls, google.authorizationCalls, aws.credentialsCalls)
	}
}

func healthyService(aws AWS) Service {
	return Service{
		Database: &fakeDatabase{migrationsCurrent: true},
		Google: &fakeGoogle{
			documents: []source.Document{{ID: "synthetic-document"}},
			document:  source.Document{Tabs: []source.Tab{{Role: source.TabRoleTranscript}}},
		},
		AWS: aws,
	}
}

func assertCheck(t *testing.T, report Report, name CheckName, status Status, message string) Check {
	t.Helper()
	for _, check := range report.Checks {
		if check.Name == name {
			if check.Status != status || check.Message != message {
				t.Fatalf("check %s = (%s, %q), want (%s, %q)", name, check.Status, check.Message, status, message)
			}
			return check
		}
	}
	t.Fatalf("check %s is missing", name)
	return Check{}
}

type fakeDatabase struct {
	ping                 func(context.Context) error
	pingErr              error
	migrationsCurrent    bool
	migrationsErr        error
	pingCalls            int
	migrationsCalls      int
	applyMigrationsCalls int
}

func (fake *fakeDatabase) Ping(ctx context.Context) error {
	fake.pingCalls++
	if fake.ping != nil {
		return fake.ping(ctx)
	}
	return fake.pingErr
}

func (fake *fakeDatabase) MigrationsCurrent(context.Context) (bool, error) {
	fake.migrationsCalls++
	return fake.migrationsCurrent, fake.migrationsErr
}

func (fake *fakeDatabase) ApplyMigrations(context.Context) error {
	fake.applyMigrationsCalls++
	return nil
}

type fakeGoogle struct {
	authorizationErr   error
	listErr            error
	getErr             error
	documents          []source.Document
	document           source.Document
	authorizationCalls int
	listCalls          int
	getCalls           int
	authorizeCalls     int
	syncCalls          int
}

func (fake *fakeGoogle) CheckAuthorization(context.Context) error {
	fake.authorizationCalls++
	return fake.authorizationErr
}

func (fake *fakeGoogle) ListFolder(context.Context) ([]source.Document, error) {
	fake.listCalls++
	return fake.documents, fake.listErr
}

func (fake *fakeGoogle) GetDocument(context.Context, string) (source.Document, error) {
	fake.getCalls++
	return fake.document, fake.getErr
}

func (fake *fakeGoogle) Authorize(context.Context) error {
	fake.authorizeCalls++
	return nil
}

func (fake *fakeGoogle) Sync(context.Context) error {
	fake.syncCalls++
	return nil
}

type fakeAWS struct {
	credentialsErr        error
	modelErr              error
	loggingState          InvocationLoggingState
	loggingErr            error
	credentialsCalls      int
	modelCalls            int
	loggingCalls          int
	invokeCalls           int
	configureLoggingCalls int
}

func (fake *fakeAWS) CheckCredentials(context.Context) error {
	fake.credentialsCalls++
	return fake.credentialsErr
}

func (fake *fakeAWS) CheckModel(context.Context) error {
	fake.modelCalls++
	return fake.modelErr
}

func (fake *fakeAWS) InvocationLogging(context.Context) (InvocationLoggingState, error) {
	fake.loggingCalls++
	return fake.loggingState, fake.loggingErr
}

func (fake *fakeAWS) Invoke(context.Context) error {
	fake.invokeCalls++
	return nil
}

func (fake *fakeAWS) ConfigureLogging(context.Context) error {
	fake.configureLoggingCalls++
	return nil
}
