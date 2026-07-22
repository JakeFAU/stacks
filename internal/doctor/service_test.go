package doctor

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stacks/internal/modelpolicy"
	"stacks/internal/source"
)

func TestDoctorReportsProviderNeutralModelChecksWithoutRuntimeInvocation(t *testing.T) {
	model := &fakeModelProbe{}
	disclosure := &fakeDisclosureProbe{state: InvocationLoggingEnabled}
	report := (Service{
		Database:   &fakeDatabase{migrationsCurrent: true},
		Google:     healthyGoogle(),
		Invocation: modelpolicy.Invocation{Provider: modelpolicy.ProviderOpenAI, DataMode: modelpolicy.DataModePersonal},
		Model:      model,
		Disclosure: disclosure,
	}).Check(context.Background())

	assertCheck(t, report, CheckModelCredentials, StatusOK, "openai credentials are valid")
	assertCheck(t, report, CheckModelAvailability, StatusOK, "configured openai model is available")
	assertCheck(t, report, CheckModelDisclosure, StatusOK, "personal data mode selected; provider logging inspection is not required")
	if model.credentialsCalls != 1 || model.modelCalls != 1 || model.invokeCalls != 0 || disclosure.calls != 0 {
		t.Fatalf("calls = credentials:%d model:%d invoke:%d disclosure:%d, want 1/1/0/0", model.credentialsCalls, model.modelCalls, model.invokeCalls, disclosure.calls)
	}
}

func TestDoctorRestrictedDisclosureFailsUnlessDisabledIsConfirmed(t *testing.T) {
	tests := []struct {
		name   string
		state  InvocationLoggingState
		err    error
		status Status
	}{
		{name: "disabled", state: InvocationLoggingDisabled, status: StatusOK},
		{name: "enabled", state: InvocationLoggingEnabled, status: StatusFailed},
		{name: "unknown", state: InvocationLoggingUnknown, status: StatusFailed},
		{name: "inspection error", err: errors.New("private provider body request-id"), status: StatusFailed},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			disclosure := &fakeDisclosureProbe{state: testCase.state, err: testCase.err}
			report := (Service{
				Database:   &fakeDatabase{migrationsCurrent: true},
				Google:     healthyGoogle(),
				Invocation: modelpolicy.Invocation{Provider: modelpolicy.ProviderBedrock, DataMode: modelpolicy.DataModeRestricted, Region: "us-east-1"},
				Model:      &fakeModelProbe{},
				Disclosure: disclosure,
			}).Check(context.Background())
			check := findCheck(t, report, CheckModelDisclosure)
			if check.Status != testCase.status || !strings.Contains(check.Message, "restricted data mode") {
				t.Fatalf("model disclosure check = %#v", check)
			}
			if strings.Contains(check.Message, "private") || len(check.Message) > 160 {
				t.Fatalf("model disclosure message leaked or is unbounded: %q", check.Message)
			}
		})
	}
}

func healthyGoogle() *fakeGoogle {
	return &fakeGoogle{
		representative:      source.Document{ID: "synthetic-document"},
		representativeFound: true,
		document:            source.Document{Tabs: []source.Tab{{Role: source.TabRoleTranscript}}},
	}
}

func findCheck(t *testing.T, report Report, name CheckName) Check {
	t.Helper()
	for _, check := range report.Checks {
		if check.Name == name {
			return check
		}
	}
	t.Fatalf("check %s is missing", name)
	return Check{}
}

type fakeModelProbe struct {
	credentialsErr   error
	modelErr         error
	credentialsCalls int
	modelCalls       int
	invokeCalls      int
}

func (fake *fakeModelProbe) CheckCredentials(context.Context) error {
	fake.credentialsCalls++
	return fake.credentialsErr
}

func (fake *fakeModelProbe) CheckModel(context.Context) error {
	fake.modelCalls++
	return fake.modelErr
}

func (fake *fakeModelProbe) Invoke(context.Context) error {
	fake.invokeCalls++
	return nil
}

func TestDoctorChecksEveryReadOnlyDependencyWithoutMutation(t *testing.T) {
	database := &fakeDatabase{migrationsCurrent: true}
	google := &fakeGoogle{
		representative:      source.Document{ID: "synthetic-document"},
		representativeFound: true,
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
	assertCheck(t, report, CheckModelCredentials, StatusOK, "bedrock credentials are valid")
	assertCheck(t, report, CheckModelAvailability, StatusOK, "configured bedrock model is available")
	assertCheck(t, report, CheckModelDisclosure, StatusOK, "personal data mode selected; provider logging inspection is not required")
	if !report.Healthy() {
		t.Fatal("Report.Healthy() = false, want true")
	}
	if database.applyMigrationsCalls != 0 || google.authorizeCalls != 0 || google.syncCalls != 0 || aws.invokeCalls != 0 || aws.configureLoggingCalls != 0 {
		t.Fatalf("mutation calls = database:%d authorize:%d sync:%d invoke:%d logging:%d, want all zero",
			database.applyMigrationsCalls, google.authorizeCalls, google.syncCalls, aws.invokeCalls, aws.configureLoggingCalls)
	}
	if google.folderCalls != 1 || google.representativeCalls != 1 || google.getCalls != 1 {
		t.Fatalf("Google read calls = folder:%d representative:%d document:%d, want 1/1/1", google.folderCalls, google.representativeCalls, google.getCalls)
	}
}

func TestDoctorReportsRestrictedInvocationLoggingEnabledOrUnknownAsFailed(t *testing.T) {
	tests := []struct {
		name    string
		state   InvocationLoggingState
		err     error
		status  Status
		message string
	}{
		{name: "enabled", state: InvocationLoggingEnabled, status: StatusFailed, message: "restricted data mode selected; model disclosure safety is not confirmed"},
		{name: "access denied", err: errors.New("AccessDeniedException: private account detail"), status: StatusFailed, message: "restricted data mode selected; model disclosure safety is not confirmed"},
		{name: "other inspection error", err: errors.New("request ID private-request"), status: StatusFailed, message: "restricted data mode selected; model disclosure safety is not confirmed"},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			report := healthyService(&fakeAWS{loggingState: testCase.state, loggingErr: testCase.err}).Check(context.Background())
			check := assertCheck(t, report, CheckModelDisclosure, testCase.status, testCase.message)
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
	if google.folderCalls != 0 || google.representativeCalls != 0 || google.getCalls != 0 || strings.Contains(check.Message, "private") {
		t.Fatalf("dependent calls/leak = folder:%d representative:%d get:%d message:%q", google.folderCalls, google.representativeCalls, google.getCalls, check.Message)
	}
}

func TestDoctorBoundsFolderFailureAndSkipsRepresentativeLookup(t *testing.T) {
	google := &fakeGoogle{folderErr: errors.New("private folder ID and provider detail")}
	report := (Service{
		Database: &fakeDatabase{migrationsCurrent: true},
		Google:   google,
		AWS:      &fakeAWS{loggingState: InvocationLoggingDisabled},
	}).Check(context.Background())

	check := assertCheck(t, report, CheckGoogleFolder, StatusFailed, "configured Google Drive folder is unavailable")
	if check.Remediation != "verify folder access and STACKS_GOOGLE_FOLDER_ID" {
		t.Fatalf("folder remediation = %q, want bounded configuration guidance", check.Remediation)
	}
	assertCheck(t, report, CheckGoogleTabs, StatusFailed, "not checked because the Google Drive folder is unavailable")
	if google.folderCalls != 1 || google.representativeCalls != 0 || google.getCalls != 0 {
		t.Fatalf("Google calls = folder:%d representative:%d document:%d, want 1/0/0", google.folderCalls, google.representativeCalls, google.getCalls)
	}
	if strings.Contains(check.Message, "private") || strings.Contains(check.Remediation, "private") {
		t.Fatalf("folder check disclosed provider detail: %#v", check)
	}
}

func TestDoctorCredentialRemediationSupportsDefaultChainOrConfiguredProfile(t *testing.T) {
	report := healthyService(&fakeAWS{credentialsErr: errors.New("private AWS identity")}).Check(context.Background())

	check := assertCheck(t, report, CheckModelCredentials, StatusFailed, "bedrock credentials are unavailable or invalid")
	if check.Remediation != "refresh AWS credentials or the configured profile" {
		t.Fatalf("AWS remediation = %q, want credential-chain-neutral guidance", check.Remediation)
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

func TestDoctorStopsAfterSuccessfulCallThatCancelsContext(t *testing.T) {
	tests := []struct {
		name    string
		service func(context.CancelFunc) (Service, func(*testing.T))
	}{
		{
			name: "database ping",
			service: func(cancel context.CancelFunc) (Service, func(*testing.T)) {
				database := &fakeDatabase{ping: func(context.Context) error { cancel(); return nil }, migrationsCurrent: true}
				google := &fakeGoogle{}
				aws := &fakeAWS{}
				return Service{Database: database, Google: google, AWS: aws}, func(t *testing.T) {
					if database.migrationsCalls != 0 || google.authorizationCalls != 0 || aws.credentialsCalls != 0 {
						t.Fatalf("downstream calls = migrations:%d google:%d aws:%d, want zero", database.migrationsCalls, google.authorizationCalls, aws.credentialsCalls)
					}
				}
			},
		},
		{
			name: "database migrations",
			service: func(cancel context.CancelFunc) (Service, func(*testing.T)) {
				database := &fakeDatabase{migrations: func(context.Context) (bool, error) { cancel(); return true, nil }}
				google := &fakeGoogle{}
				aws := &fakeAWS{}
				return Service{Database: database, Google: google, AWS: aws}, func(t *testing.T) {
					if google.authorizationCalls != 0 || aws.credentialsCalls != 0 {
						t.Fatalf("downstream calls = google:%d aws:%d, want zero", google.authorizationCalls, aws.credentialsCalls)
					}
				}
			},
		},
		{
			name: "Google authorization",
			service: func(cancel context.CancelFunc) (Service, func(*testing.T)) {
				google := &fakeGoogle{authorization: func(context.Context) error { cancel(); return nil }}
				aws := &fakeAWS{}
				return Service{Database: &fakeDatabase{migrationsCurrent: true}, Google: google, AWS: aws}, func(t *testing.T) {
					if google.folderCalls != 0 || google.representativeCalls != 0 || google.getCalls != 0 || aws.credentialsCalls != 0 {
						t.Fatalf("downstream calls = folder:%d representative:%d document:%d aws:%d, want zero", google.folderCalls, google.representativeCalls, google.getCalls, aws.credentialsCalls)
					}
				}
			},
		},
		{
			name: "Google folder metadata",
			service: func(cancel context.CancelFunc) (Service, func(*testing.T)) {
				google := &fakeGoogle{folder: func(context.Context) error { cancel(); return nil }}
				aws := &fakeAWS{}
				return Service{Database: &fakeDatabase{migrationsCurrent: true}, Google: google, AWS: aws}, func(t *testing.T) {
					if google.representativeCalls != 0 || google.getCalls != 0 || aws.credentialsCalls != 0 {
						t.Fatalf("downstream calls = representative:%d document:%d aws:%d, want zero", google.representativeCalls, google.getCalls, aws.credentialsCalls)
					}
				}
			},
		},
		{
			name: "Google representative",
			service: func(cancel context.CancelFunc) (Service, func(*testing.T)) {
				google := &fakeGoogle{representativeLookup: func(context.Context) (source.Document, bool, error) {
					cancel()
					return source.Document{ID: "synthetic-document"}, true, nil
				}}
				aws := &fakeAWS{}
				return Service{Database: &fakeDatabase{migrationsCurrent: true}, Google: google, AWS: aws}, func(t *testing.T) {
					if google.getCalls != 0 || aws.credentialsCalls != 0 {
						t.Fatalf("downstream calls = document:%d aws:%d, want zero", google.getCalls, aws.credentialsCalls)
					}
				}
			},
		},
		{
			name: "Google document",
			service: func(cancel context.CancelFunc) (Service, func(*testing.T)) {
				google := &fakeGoogle{
					representative:      source.Document{ID: "synthetic-document"},
					representativeFound: true,
					get: func(context.Context, string) (source.Document, error) {
						cancel()
						return source.Document{Tabs: []source.Tab{{Role: source.TabRoleTranscript}}}, nil
					},
				}
				aws := &fakeAWS{}
				return Service{Database: &fakeDatabase{migrationsCurrent: true}, Google: google, AWS: aws}, func(t *testing.T) {
					if aws.credentialsCalls != 0 {
						t.Fatalf("AWS credential calls = %d, want zero", aws.credentialsCalls)
					}
				}
			},
		},
		{
			name: "AWS credentials",
			service: func(cancel context.CancelFunc) (Service, func(*testing.T)) {
				aws := &fakeAWS{credentials: func(context.Context) error { cancel(); return nil }}
				return healthyService(aws), func(t *testing.T) {
					if aws.modelCalls != 0 || aws.loggingCalls != 0 {
						t.Fatalf("downstream calls = model:%d logging:%d, want zero", aws.modelCalls, aws.loggingCalls)
					}
				}
			},
		},
		{
			name: "Bedrock model",
			service: func(cancel context.CancelFunc) (Service, func(*testing.T)) {
				aws := &fakeAWS{model: func(context.Context) error { cancel(); return nil }}
				return healthyService(aws), func(t *testing.T) {
					if aws.loggingCalls != 0 {
						t.Fatalf("logging calls = %d, want zero", aws.loggingCalls)
					}
				}
			},
		},
		{
			name: "invocation logging",
			service: func(cancel context.CancelFunc) (Service, func(*testing.T)) {
				aws := &fakeAWS{logging: func(context.Context) (InvocationLoggingState, error) {
					cancel()
					return InvocationLoggingDisabled, nil
				}}
				return healthyService(aws), func(*testing.T) {}
			},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			service, assertNoDownstreamCalls := testCase.service(cancel)

			report := service.Check(ctx)

			if !errors.Is(report.Err, context.Canceled) || report.Err != context.Canceled {
				t.Fatalf("Report.Err = %v, want canonical context.Canceled", report.Err)
			}
			assertNoDownstreamCalls(t)
		})
	}
}

func healthyService(aws AWS) Service {
	return Service{
		Database:   &fakeDatabase{migrationsCurrent: true},
		Invocation: modelpolicy.Invocation{Provider: modelpolicy.ProviderBedrock, DataMode: modelpolicy.DataModeRestricted, Region: "us-east-1"},
		Google: &fakeGoogle{
			representative:      source.Document{ID: "synthetic-document"},
			representativeFound: true,
			document:            source.Document{Tabs: []source.Tab{{Role: source.TabRoleTranscript}}},
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
	migrations           func(context.Context) (bool, error)
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

func (fake *fakeDatabase) MigrationsCurrent(ctx context.Context) (bool, error) {
	fake.migrationsCalls++
	if fake.migrations != nil {
		return fake.migrations(ctx)
	}
	return fake.migrationsCurrent, fake.migrationsErr
}

func (fake *fakeDatabase) ApplyMigrations(context.Context) error {
	fake.applyMigrationsCalls++
	return nil
}

type fakeGoogle struct {
	authorization        func(context.Context) error
	folder               func(context.Context) error
	representativeLookup func(context.Context) (source.Document, bool, error)
	get                  func(context.Context, string) (source.Document, error)
	authorizationErr     error
	folderErr            error
	representativeErr    error
	getErr               error
	representative       source.Document
	representativeFound  bool
	document             source.Document
	authorizationCalls   int
	folderCalls          int
	representativeCalls  int
	getCalls             int
	authorizeCalls       int
	syncCalls            int
}

func (fake *fakeGoogle) CheckAuthorization(ctx context.Context) error {
	fake.authorizationCalls++
	if fake.authorization != nil {
		return fake.authorization(ctx)
	}
	return fake.authorizationErr
}

func (fake *fakeGoogle) CheckFolder(ctx context.Context) error {
	fake.folderCalls++
	if fake.folder != nil {
		return fake.folder(ctx)
	}
	return fake.folderErr
}

func (fake *fakeGoogle) GetRepresentative(ctx context.Context) (source.Document, bool, error) {
	fake.representativeCalls++
	if fake.representativeLookup != nil {
		return fake.representativeLookup(ctx)
	}
	return fake.representative, fake.representativeFound, fake.representativeErr
}

func (fake *fakeGoogle) GetDocument(ctx context.Context, documentID string) (source.Document, error) {
	fake.getCalls++
	if fake.get != nil {
		return fake.get(ctx, documentID)
	}
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
	credentials           func(context.Context) error
	model                 func(context.Context) error
	logging               func(context.Context) (InvocationLoggingState, error)
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

func (fake *fakeAWS) CheckCredentials(ctx context.Context) error {
	fake.credentialsCalls++
	if fake.credentials != nil {
		return fake.credentials(ctx)
	}
	return fake.credentialsErr
}

func (fake *fakeAWS) CheckModel(ctx context.Context) error {
	fake.modelCalls++
	if fake.model != nil {
		return fake.model(ctx)
	}
	return fake.modelErr
}

func (fake *fakeAWS) InvocationLogging(ctx context.Context) (InvocationLoggingState, error) {
	fake.loggingCalls++
	if fake.logging != nil {
		return fake.logging(ctx)
	}
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
