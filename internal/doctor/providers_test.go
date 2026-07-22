package doctor

import (
	"context"
	"errors"
	"strings"
	"testing"

	awsbedrock "github.com/aws/aws-sdk-go-v2/service/bedrock"
	bedrocktypes "github.com/aws/aws-sdk-go-v2/service/bedrock/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	"stacks/internal/source"
)

func TestPostgresProbeChecksRequiredMigrationWithoutApplyingIt(t *testing.T) {
	connection := &fakePostgresConnection{
		appliedMigrationVersions: []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
	}
	probe := newPostgresProbe("postgres://synthetic", func(context.Context, string) (postgresConnection, error) {
		return connection, nil
	})
	defer probe.Close()

	if err := probe.Ping(context.Background()); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
	current, err := probe.MigrationsCurrent(context.Background())
	if err != nil {
		t.Fatalf("MigrationsCurrent() error = %v", err)
	}
	if !current {
		t.Fatal("MigrationsCurrent() = false, want true")
	}
	if connection.pingCalls != 1 || connection.queryCalls != 1 || connection.mutationCalls != 0 {
		t.Fatalf("calls = ping:%d query:%d mutation:%d, want 1/1/0", connection.pingCalls, connection.queryCalls, connection.mutationCalls)
	}
	normalizedQuery := strings.ToLower(connection.lastQuery)
	if strings.Contains(normalizedQuery, "max(") || !strings.Contains(normalizedQuery, "unnest") || !strings.Contains(normalizedQuery, "distinct on") {
		t.Fatalf("migration query = %q, want required-set comparison against each version's latest state", connection.lastQuery)
	}
}

func TestPostgresProbeRejectsAppliedMigrationSetWithInteriorGap(t *testing.T) {
	connection := &fakePostgresConnection{
		appliedMigrationVersions: []int64{1, 2, 4, 5, 6},
	}
	probe := newPostgresProbe("postgres://synthetic", func(context.Context, string) (postgresConnection, error) {
		return connection, nil
	})
	defer probe.Close()

	current, err := probe.MigrationsCurrent(context.Background())
	if err != nil {
		t.Fatalf("MigrationsCurrent() error = %v", err)
	}
	if current {
		t.Fatal("MigrationsCurrent() = true, want false for missing applied migration 3")
	}
}

func TestPostgresProbeRequiresLegacyAdmissionMigration(t *testing.T) {
	connection := &fakePostgresConnection{appliedMigrationVersions: []int64{1, 2, 3, 4, 5}}
	probe := newPostgresProbe("postgres://synthetic", func(context.Context, string) (postgresConnection, error) {
		return connection, nil
	})
	defer probe.Close()

	current, err := probe.MigrationsCurrent(context.Background())
	if err != nil {
		t.Fatalf("MigrationsCurrent() error = %v", err)
	}
	if current {
		t.Fatal("MigrationsCurrent() = true, want migration 6 required")
	}
}

func TestPostgresProbeRequiresCompatibilityAdmissionMigration(t *testing.T) {
	connection := &fakePostgresConnection{appliedMigrationVersions: []int64{1, 2, 3, 4, 5, 6}}
	probe := newPostgresProbe("postgres://synthetic", func(context.Context, string) (postgresConnection, error) {
		return connection, nil
	})
	defer probe.Close()

	current, err := probe.MigrationsCurrent(context.Background())
	if err != nil {
		t.Fatalf("MigrationsCurrent() error = %v", err)
	}
	if current {
		t.Fatal("MigrationsCurrent() = true, want migration 7 required")
	}
}

func TestPostgresProbeRequiresSnapshotCoherenceAdmissionMigration(t *testing.T) {
	connection := &fakePostgresConnection{appliedMigrationVersions: []int64{1, 2, 3, 4, 5, 6, 7}}
	probe := newPostgresProbe("postgres://synthetic", func(context.Context, string) (postgresConnection, error) {
		return connection, nil
	})
	defer probe.Close()

	current, err := probe.MigrationsCurrent(context.Background())
	if err != nil {
		t.Fatalf("MigrationsCurrent() error = %v", err)
	}
	if current {
		t.Fatal("MigrationsCurrent() = true, want migration 8 required")
	}
}

func TestPostgresProbeRequiresDoctorInspectionMigration(t *testing.T) {
	connection := &fakePostgresConnection{appliedMigrationVersions: []int64{1, 2, 3, 4, 5, 6, 7, 8}}
	probe := newPostgresProbe("postgres://synthetic", func(context.Context, string) (postgresConnection, error) {
		return connection, nil
	})
	defer probe.Close()

	current, err := probe.MigrationsCurrent(context.Background())
	if err != nil {
		t.Fatalf("MigrationsCurrent() error = %v", err)
	}
	if current {
		t.Fatal("MigrationsCurrent() = true, want migration 9 required")
	}
}

func TestRequiredMigrationVersionsIncludesModelProviderProvenance(t *testing.T) {
	connection := &fakePostgresConnection{appliedMigrationVersions: []int64{1, 2, 3, 4, 5, 6, 7, 8, 9}}
	probe := newPostgresProbe("postgres://synthetic", func(context.Context, string) (postgresConnection, error) {
		return connection, nil
	})
	defer probe.Close()

	current, err := probe.MigrationsCurrent(context.Background())
	if err != nil {
		t.Fatalf("MigrationsCurrent() error = %v", err)
	}
	if current {
		t.Fatal("MigrationsCurrent() = true, want migration 10 required")
	}
}

func TestGoogleProbeChecksFolderBeforeUsingBoundedRepresentativeLookup(t *testing.T) {
	sourceBoundary := &fakeSource{documents: []source.Document{
		{ID: "document-1"},
		{ID: "document-2"},
	}, document: source.Document{ID: "document-1", Tabs: []source.Tab{{Role: source.TabRoleTranscript}}}}
	factoryCalls := 0
	probe := newGoogleProbe("folder", func(context.Context) (source.RepresentativeSource, error) {
		factoryCalls++
		return sourceBoundary, nil
	})

	if err := probe.CheckAuthorization(context.Background()); err != nil {
		t.Fatalf("CheckAuthorization() error = %v", err)
	}
	if err := probe.CheckFolder(context.Background()); err != nil {
		t.Fatalf("CheckFolder() error = %v", err)
	}
	document, found, err := probe.GetRepresentative(context.Background())
	if err != nil {
		t.Fatalf("GetRepresentative() error = %v", err)
	}
	if !found || document.ID != "document-1" {
		t.Fatalf("GetRepresentative() = (%#v, %t), want document-1, true", document, found)
	}
	if _, err := probe.GetDocument(context.Background(), document.ID); err != nil {
		t.Fatalf("GetDocument() error = %v", err)
	}
	if factoryCalls != 1 || sourceBoundary.collectionCalls != 1 || sourceBoundary.representativeCalls != 1 || sourceBoundary.listCalls != 0 || sourceBoundary.getCalls != 1 || sourceBoundary.mutationCalls != 0 {
		t.Fatalf("calls = factory:%d folder:%d representative:%d list:%d get:%d mutation:%d, want 1/1/1/0/1/0", factoryCalls, sourceBoundary.collectionCalls, sourceBoundary.representativeCalls, sourceBoundary.listCalls, sourceBoundary.getCalls, sourceBoundary.mutationCalls)
	}
}

func TestAWSProbeChecksFoundationModelOrInferenceProfileWithoutInvocation(t *testing.T) {
	tests := []struct {
		name           string
		modelID        string
		bedrock        *fakeBedrockControl
		wantFoundation int
		wantProfile    int
	}{
		{
			name:    "foundation model",
			modelID: "synthetic.foundation-model-v1",
			bedrock: &fakeBedrockControl{
				profileErr:       &bedrocktypes.ResourceNotFoundException{},
				foundationOutput: availableFoundationOutput(),
			},
			wantFoundation: 1,
			wantProfile:    1,
		},
		{
			name:        "cross-region inference profile",
			modelID:     "us.synthetic.foundation-model-v1",
			bedrock:     &fakeBedrockControl{profileOutput: &awsbedrock.GetInferenceProfileOutput{Status: bedrocktypes.InferenceProfileStatusActive}},
			wantProfile: 1,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			identity := &fakeSTS{}
			probe := newAWSProbe(testCase.modelID, identity, testCase.bedrock)
			if err := probe.CheckCredentials(context.Background()); err != nil {
				t.Fatalf("CheckCredentials() error = %v", err)
			}
			if err := probe.CheckModel(context.Background()); err != nil {
				t.Fatalf("CheckModel() error = %v", err)
			}
			if identity.calls != 1 || testCase.bedrock.foundationCalls != testCase.wantFoundation || testCase.bedrock.profileCalls != testCase.wantProfile || testCase.bedrock.invokeCalls != 0 {
				t.Fatalf("calls = credentials:%d foundation:%d profile:%d invoke:%d", identity.calls, testCase.bedrock.foundationCalls, testCase.bedrock.profileCalls, testCase.bedrock.invokeCalls)
			}
		})
	}
}

func TestAWSProbeRequiresAvailableFoundationAgreement(t *testing.T) {
	tests := []struct {
		name      string
		agreement *bedrocktypes.AgreementAvailability
		wantErr   bool
	}{
		{name: "available", agreement: &bedrocktypes.AgreementAvailability{Status: bedrocktypes.AgreementStatusAvailable}},
		{name: "missing", wantErr: true},
		{name: "pending", agreement: &bedrocktypes.AgreementAvailability{Status: bedrocktypes.AgreementStatusPending}, wantErr: true},
		{name: "not available", agreement: &bedrocktypes.AgreementAvailability{Status: bedrocktypes.AgreementStatusNotAvailable}, wantErr: true},
		{name: "error", agreement: &bedrocktypes.AgreementAvailability{Status: bedrocktypes.AgreementStatusError}, wantErr: true},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			output := availableFoundationOutput()
			output.AgreementAvailability = testCase.agreement
			control := &fakeBedrockControl{
				profileErr:       &bedrocktypes.ResourceNotFoundException{},
				foundationOutput: output,
			}

			err := newAWSProbe("synthetic.foundation-model-v1", &fakeSTS{}, control).CheckModel(context.Background())
			if (err != nil) != testCase.wantErr {
				t.Fatalf("CheckModel() error = %v, wantErr %t", err, testCase.wantErr)
			}
		})
	}
}

func TestAWSProbeFindsArbitraryAndRecognizableInferenceProfileIDs(t *testing.T) {
	for _, modelID := range []string{
		"application-profile-id-without-prefix",
		"us.synthetic.foundation-model-v1",
		"arn:aws:bedrock:us-east-1:000000000000:application-inference-profile/synthetic",
	} {
		t.Run(modelID, func(t *testing.T) {
			control := &fakeBedrockControl{profileOutput: &awsbedrock.GetInferenceProfileOutput{Status: bedrocktypes.InferenceProfileStatusActive}}

			if err := newAWSProbe(modelID, &fakeSTS{}, control).CheckModel(context.Background()); err != nil {
				t.Fatalf("CheckModel() error = %v", err)
			}
			if control.profileCalls != 1 || control.foundationCalls != 0 {
				t.Fatalf("calls = profile:%d foundation:%d, want 1/0", control.profileCalls, control.foundationCalls)
			}
		})
	}
}

func TestAWSProbeDoesNotFallbackFromProfileAccessDeniedOrCancellation(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "access denied", err: &bedrocktypes.AccessDeniedException{}},
		{name: "canceled", err: context.Canceled},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			control := &fakeBedrockControl{profileErr: testCase.err, foundationOutput: availableFoundationOutput()}

			err := newAWSProbe("application-profile-id", &fakeSTS{}, control).CheckModel(context.Background())
			if err == nil {
				t.Fatal("CheckModel() error = nil, want profile error")
			}
			if testCase.name == "canceled" && !errors.Is(err, context.Canceled) {
				t.Fatalf("CheckModel() error = %v, want context.Canceled", err)
			}
			if control.profileCalls != 1 || control.foundationCalls != 0 {
				t.Fatalf("calls = profile:%d foundation:%d, want 1/0", control.profileCalls, control.foundationCalls)
			}
		})
	}
}

func availableFoundationOutput() *awsbedrock.GetFoundationModelAvailabilityOutput {
	return &awsbedrock.GetFoundationModelAvailabilityOutput{
		AgreementAvailability:   &bedrocktypes.AgreementAvailability{Status: bedrocktypes.AgreementStatusAvailable},
		AuthorizationStatus:     bedrocktypes.AuthorizationStatusAuthorized,
		EntitlementAvailability: bedrocktypes.EntitlementAvailabilityAvailable,
		RegionAvailability:      bedrocktypes.RegionAvailabilityAvailable,
	}
}

func TestAWSProbeTreatsAnyLoggingConfigurationAsEnabled(t *testing.T) {
	tests := []struct {
		name   string
		output *awsbedrock.GetModelInvocationLoggingConfigurationOutput
		want   InvocationLoggingState
	}{
		{name: "no configuration", output: &awsbedrock.GetModelInvocationLoggingConfigurationOutput{}, want: InvocationLoggingDisabled},
		{name: "configured", output: &awsbedrock.GetModelInvocationLoggingConfigurationOutput{LoggingConfig: &bedrocktypes.LoggingConfig{}}, want: InvocationLoggingEnabled},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			control := &fakeBedrockControl{loggingOutput: testCase.output}
			state, err := newAWSProbe("synthetic-model", &fakeSTS{}, control).InvocationLogging(context.Background())
			if err != nil {
				t.Fatalf("InvocationLogging() error = %v", err)
			}
			if state != testCase.want {
				t.Fatalf("InvocationLogging() = %q, want %q", state, testCase.want)
			}
			if control.configureLoggingCalls != 0 {
				t.Fatalf("logging mutation calls = %d, want zero", control.configureLoggingCalls)
			}
		})
	}
}

type fakePostgresConnection struct {
	appliedMigrationVersions []int64
	pingCalls                int
	queryCalls               int
	mutationCalls            int
	lastQuery                string
}

func (fake *fakePostgresConnection) Ping(context.Context) error {
	fake.pingCalls++
	return nil
}

func (fake *fakePostgresConnection) QueryRow(_ context.Context, query string, arguments ...any) postgresRow {
	fake.queryCalls++
	fake.lastQuery = query
	if len(arguments) == 1 {
		requiredVersions, ok := arguments[0].([]int64)
		if !ok {
			return fakePostgresRow{err: errors.New("unexpected migration query argument")}
		}
		current := containsEveryMigration(requiredVersions, fake.appliedMigrationVersions)
		return fakePostgresRow{current: &current}
	}
	return fakePostgresRow{err: errors.New("required migration versions were not supplied")}
}

func (fake *fakePostgresConnection) Close() {}

func (fake *fakePostgresConnection) ApplyMigrations(context.Context) error {
	fake.mutationCalls++
	return nil
}

type fakePostgresRow struct {
	current *bool
	err     error
}

func (row fakePostgresRow) Scan(destinations ...any) error {
	if row.err != nil {
		return row.err
	}
	if len(destinations) != 1 {
		return errors.New("unexpected scan destinations")
	}
	switch value := destinations[0].(type) {
	case *bool:
		if row.current == nil {
			return errors.New("unexpected boolean scan destination")
		}
		*value = *row.current
	default:
		return errors.New("unexpected scan destination")
	}
	return nil
}

func containsEveryMigration(required, applied []int64) bool {
	appliedSet := make(map[int64]struct{}, len(applied))
	for _, version := range applied {
		appliedSet[version] = struct{}{}
	}
	for _, version := range required {
		if _, ok := appliedSet[version]; !ok {
			return false
		}
	}
	return true
}

type fakeSource struct {
	documents           []source.Document
	document            source.Document
	collectionCalls     int
	representativeCalls int
	listCalls           int
	getCalls            int
	mutationCalls       int
}

func (fake *fakeSource) CheckCollection(context.Context, string) error {
	fake.collectionCalls++
	return nil
}

func (fake *fakeSource) GetRepresentative(context.Context, string) (source.Document, bool, error) {
	fake.representativeCalls++
	if len(fake.documents) == 0 {
		return source.Document{}, false, nil
	}
	return fake.documents[0], true, nil
}

func (fake *fakeSource) List(context.Context, string) ([]source.Document, error) {
	fake.listCalls++
	return fake.documents, nil
}

func (fake *fakeSource) Get(context.Context, string) (source.Document, error) {
	fake.getCalls++
	return fake.document, nil
}

func (fake *fakeSource) Mutate(context.Context) error {
	fake.mutationCalls++
	return nil
}

type fakeSTS struct {
	calls int
}

func (fake *fakeSTS) GetCallerIdentity(context.Context, *sts.GetCallerIdentityInput, ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error) {
	fake.calls++
	return &sts.GetCallerIdentityOutput{}, nil
}

type fakeBedrockControl struct {
	foundationOutput      *awsbedrock.GetFoundationModelAvailabilityOutput
	foundationErr         error
	profileOutput         *awsbedrock.GetInferenceProfileOutput
	profileErr            error
	loggingOutput         *awsbedrock.GetModelInvocationLoggingConfigurationOutput
	foundationCalls       int
	profileCalls          int
	loggingCalls          int
	invokeCalls           int
	configureLoggingCalls int
}

func (fake *fakeBedrockControl) GetFoundationModelAvailability(context.Context, *awsbedrock.GetFoundationModelAvailabilityInput, ...func(*awsbedrock.Options)) (*awsbedrock.GetFoundationModelAvailabilityOutput, error) {
	fake.foundationCalls++
	return fake.foundationOutput, fake.foundationErr
}

func (fake *fakeBedrockControl) GetInferenceProfile(context.Context, *awsbedrock.GetInferenceProfileInput, ...func(*awsbedrock.Options)) (*awsbedrock.GetInferenceProfileOutput, error) {
	fake.profileCalls++
	return fake.profileOutput, fake.profileErr
}

func (fake *fakeBedrockControl) GetModelInvocationLoggingConfiguration(context.Context, *awsbedrock.GetModelInvocationLoggingConfigurationInput, ...func(*awsbedrock.Options)) (*awsbedrock.GetModelInvocationLoggingConfigurationOutput, error) {
	fake.loggingCalls++
	return fake.loggingOutput, nil
}

func (fake *fakeBedrockControl) InvokeModel(context.Context) error {
	fake.invokeCalls++
	return nil
}

func (fake *fakeBedrockControl) PutModelInvocationLoggingConfiguration(context.Context) error {
	fake.configureLoggingCalls++
	return nil
}
