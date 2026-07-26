package doctor

import (
	"context"
	"errors"
	"strings"
	"testing"

	awsbedrock "github.com/aws/aws-sdk-go-v2/service/bedrock"
	bedrocktypes "github.com/aws/aws-sdk-go-v2/service/bedrock/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/JakeFAU/stacks/adapters/postgres/migration"

	"stacks/internal/modelpolicy"
	"stacks/internal/source"
)

func TestPostgresProbeUsesOneCallerOwnedQueryBoundary(t *testing.T) {
	database := &recordingPostgresDatabase{}
	probe := NewPostgresProbeWithScopes(database, []migration.Scope{"core"})

	if err := probe.Ping(context.Background()); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
	statuses, err := probe.MigrationStatus(context.Background())
	if err != nil {
		t.Fatalf("MigrationStatus() error = %v", err)
	}
	if database.pings != 1 || database.inspections != 1 {
		t.Fatalf(
			"caller-owned query boundary calls = ping:%d status:%d, want 1/1",
			database.pings,
			database.inspections,
		)
	}
	if len(database.manifests) != 2 || len(database.configured) != 1 ||
		database.configured[0] != "core" || len(statuses) != 2 {
		t.Fatalf(
			"migration inspection = manifests:%d configured:%v statuses:%d, want 2/[core]/2",
			len(database.manifests),
			database.configured,
			len(statuses),
		)
	}
}

func TestRequireRestrictedDisclosureSkipsPersonalMode(t *testing.T) {
	probe := &fakeDisclosureProbe{state: InvocationLoggingEnabled}
	err := RequireRestrictedDisclosure(context.Background(), modelpolicy.Invocation{
		Provider: modelpolicy.ProviderOpenAI,
		DataMode: modelpolicy.DataModePersonal,
	}, probe)
	if err != nil {
		t.Fatalf("RequireRestrictedDisclosure() error = %v", err)
	}
	if probe.calls != 0 {
		t.Fatalf("InvocationLogging() calls = %d, want zero", probe.calls)
	}
}

type recordingPostgresDatabase struct {
	pings       int
	inspections int
	manifests   []migration.Manifest
	configured  []migration.Scope
}

func (database *recordingPostgresDatabase) Ping(context.Context) error {
	database.pings++
	return nil
}

func (database *recordingPostgresDatabase) InspectMigrationStatus(
	_ context.Context,
	manifests []migration.Manifest,
	configured []migration.Scope,
) ([]migration.ScopeStatus, error) {
	database.inspections++
	database.manifests = append([]migration.Manifest(nil), manifests...)
	database.configured = append([]migration.Scope(nil), configured...)
	return []migration.ScopeStatus{
		{Scope: "core", State: migration.StateCurrent, Configured: true},
		{Scope: "directory", State: migration.StateAbsent, Configured: false},
	}, nil
}

func TestRequireRestrictedDisclosureRejectsDirectProvidersWithoutInspection(t *testing.T) {
	for _, provider := range []modelpolicy.Provider{modelpolicy.ProviderOpenAI, modelpolicy.ProviderAnthropic} {
		t.Run(string(provider), func(t *testing.T) {
			probe := &fakeDisclosureProbe{state: InvocationLoggingDisabled}
			err := RequireRestrictedDisclosure(context.Background(), modelpolicy.Invocation{
				Provider: provider,
				DataMode: modelpolicy.DataModeRestricted,
			}, probe)
			if !errors.Is(err, ErrDisclosureNotConfirmed) {
				t.Fatalf("RequireRestrictedDisclosure() error = %v, want ErrDisclosureNotConfirmed", err)
			}
			if probe.calls != 0 {
				t.Fatalf("InvocationLogging() calls = %d, want zero", probe.calls)
			}
		})
	}
}

func TestRequireRestrictedDisclosureFailsClosed(t *testing.T) {
	privateError := errors.New("AccessDeniedException private-request-id")
	tests := []struct {
		name    string
		state   InvocationLoggingState
		err     error
		wantErr error
	}{
		{name: "disabled", state: InvocationLoggingDisabled},
		{name: "enabled", state: InvocationLoggingEnabled, wantErr: ErrDisclosureNotConfirmed},
		{name: "unknown", state: InvocationLoggingUnknown, wantErr: ErrDisclosureNotConfirmed},
		{name: "access denied", err: privateError, wantErr: ErrDisclosureNotConfirmed},
		{name: "timeout", err: context.DeadlineExceeded, wantErr: context.DeadlineExceeded},
		{name: "cancellation", err: context.Canceled, wantErr: context.Canceled},
		{name: "missing probe", wantErr: ErrDisclosureNotConfirmed},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			var probe DisclosureProbe
			fake := &fakeDisclosureProbe{state: testCase.state, err: testCase.err}
			if testCase.name != "missing probe" {
				probe = fake
			}
			err := RequireRestrictedDisclosure(context.Background(), modelpolicy.Invocation{
				Provider: modelpolicy.ProviderBedrock,
				DataMode: modelpolicy.DataModeRestricted,
				Region:   "us-east-1",
			}, probe)
			if testCase.wantErr == nil {
				if err != nil {
					t.Fatalf("RequireRestrictedDisclosure() error = %v", err)
				}
				return
			}
			if !errors.Is(err, ErrDisclosureNotConfirmed) || !errors.Is(err, testCase.wantErr) {
				t.Fatalf("RequireRestrictedDisclosure() error = %v, want disclosure and %v", err, testCase.wantErr)
			}
			if strings.Contains(err.Error(), "private") || len(err.Error()) > 160 {
				t.Fatalf("RequireRestrictedDisclosure() leaked or returned unbounded error: %q", err)
			}
		})
	}
}

func TestRequireRestrictedDisclosureRejectsCancellationAfterDisabledResult(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	probe := &fakeDisclosureProbe{logging: func(context.Context) (InvocationLoggingState, error) {
		cancel()
		return InvocationLoggingDisabled, nil
	}}

	err := RequireRestrictedDisclosure(ctx, modelpolicy.Invocation{
		Provider: modelpolicy.ProviderBedrock,
		DataMode: modelpolicy.DataModeRestricted,
		Region:   "us-east-1",
	}, probe)

	if !errors.Is(err, ErrDisclosureNotConfirmed) || !errors.Is(err, context.Canceled) || err != nil && len(err.Error()) > 160 {
		t.Fatalf("RequireRestrictedDisclosure() error = %v, want bounded disclosure failure and canonical context.Canceled", err)
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

type fakeDisclosureProbe struct {
	state   InvocationLoggingState
	err     error
	logging func(context.Context) (InvocationLoggingState, error)
	calls   int
}

func (fake *fakeDisclosureProbe) InvocationLogging(ctx context.Context) (InvocationLoggingState, error) {
	fake.calls++
	if fake.logging != nil {
		return fake.logging(ctx)
	}
	return fake.state, fake.err
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
