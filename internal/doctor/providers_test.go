package doctor

import (
	"context"
	"errors"
	"testing"

	awsbedrock "github.com/aws/aws-sdk-go-v2/service/bedrock"
	bedrocktypes "github.com/aws/aws-sdk-go-v2/service/bedrock/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	"stacks/internal/source"
)

func TestPostgresProbeChecksRequiredMigrationWithoutApplyingIt(t *testing.T) {
	connection := &fakePostgresConnection{migrationVersion: requiredMigrationVersion}
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
}

func TestGoogleProbeLoadsAuthorizationOnceAndReturnsOneRepresentativeDocument(t *testing.T) {
	sourceBoundary := &fakeSource{documents: []source.Document{
		{ID: "document-1"},
		{ID: "document-2"},
	}, document: source.Document{ID: "document-1", Tabs: []source.Tab{{Role: source.TabRoleTranscript}}}}
	factoryCalls := 0
	probe := newGoogleProbe("folder", func(context.Context) (source.Source, error) {
		factoryCalls++
		return sourceBoundary, nil
	})

	if err := probe.CheckAuthorization(context.Background()); err != nil {
		t.Fatalf("CheckAuthorization() error = %v", err)
	}
	documents, err := probe.ListFolder(context.Background())
	if err != nil {
		t.Fatalf("ListFolder() error = %v", err)
	}
	if len(documents) != 1 || documents[0].ID != "document-1" {
		t.Fatalf("ListFolder() = %#v, want first representative only", documents)
	}
	if _, err := probe.GetDocument(context.Background(), documents[0].ID); err != nil {
		t.Fatalf("GetDocument() error = %v", err)
	}
	if factoryCalls != 1 || sourceBoundary.listCalls != 1 || sourceBoundary.getCalls != 1 || sourceBoundary.mutationCalls != 0 {
		t.Fatalf("calls = factory:%d list:%d get:%d mutation:%d, want 1/1/1/0", factoryCalls, sourceBoundary.listCalls, sourceBoundary.getCalls, sourceBoundary.mutationCalls)
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
			bedrock: &fakeBedrockControl{foundationOutput: &awsbedrock.GetFoundationModelAvailabilityOutput{
				AuthorizationStatus:     bedrocktypes.AuthorizationStatusAuthorized,
				EntitlementAvailability: bedrocktypes.EntitlementAvailabilityAvailable,
				RegionAvailability:      bedrocktypes.RegionAvailabilityAvailable,
			}},
			wantFoundation: 1,
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
	migrationVersion int64
	pingCalls        int
	queryCalls       int
	mutationCalls    int
}

func (fake *fakePostgresConnection) Ping(context.Context) error {
	fake.pingCalls++
	return nil
}

func (fake *fakePostgresConnection) QueryRow(context.Context, string, ...any) postgresRow {
	fake.queryCalls++
	return fakePostgresRow{version: fake.migrationVersion}
}

func (fake *fakePostgresConnection) Close() {}

func (fake *fakePostgresConnection) ApplyMigrations(context.Context) error {
	fake.mutationCalls++
	return nil
}

type fakePostgresRow struct {
	version int64
}

func (row fakePostgresRow) Scan(destinations ...any) error {
	if len(destinations) != 1 {
		return errors.New("unexpected scan destinations")
	}
	value, ok := destinations[0].(*int64)
	if !ok {
		return errors.New("unexpected scan destination")
	}
	*value = row.version
	return nil
}

type fakeSource struct {
	documents     []source.Document
	document      source.Document
	listCalls     int
	getCalls      int
	mutationCalls int
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
	profileOutput         *awsbedrock.GetInferenceProfileOutput
	loggingOutput         *awsbedrock.GetModelInvocationLoggingConfigurationOutput
	foundationCalls       int
	profileCalls          int
	loggingCalls          int
	invokeCalls           int
	configureLoggingCalls int
}

func (fake *fakeBedrockControl) GetFoundationModelAvailability(context.Context, *awsbedrock.GetFoundationModelAvailabilityInput, ...func(*awsbedrock.Options)) (*awsbedrock.GetFoundationModelAvailabilityOutput, error) {
	fake.foundationCalls++
	return fake.foundationOutput, nil
}

func (fake *fakeBedrockControl) GetInferenceProfile(context.Context, *awsbedrock.GetInferenceProfileInput, ...func(*awsbedrock.Options)) (*awsbedrock.GetInferenceProfileOutput, error) {
	fake.profileCalls++
	return fake.profileOutput, nil
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
