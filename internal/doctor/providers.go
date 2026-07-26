package doctor

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	awsbedrock "github.com/aws/aws-sdk-go-v2/service/bedrock"
	bedrocktypes "github.com/aws/aws-sdk-go-v2/service/bedrock/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/smithy-go"
	"golang.org/x/oauth2"

	"github.com/JakeFAU/stacks/adapters/postgres/coremigrations"
	"github.com/JakeFAU/stacks/adapters/postgres/directorymigrations"
	"github.com/JakeFAU/stacks/adapters/postgres/migration"

	"stacks/internal/modelpolicy"
	"stacks/internal/source"
	"stacks/internal/source/drive"
)

var (
	ErrDisclosureNotConfirmed = errors.New("restricted model disclosure is not confirmed")
	ErrModelAuthentication    = errors.New("model credentials are invalid")
	ErrModelAuthorization     = errors.New("model metadata access is denied")
	ErrModelNotFound          = errors.New("configured model was not found")
	ErrModelUnavailable       = errors.New("model metadata is unavailable")
	ErrModelInspection        = errors.New("model metadata could not be inspected")
)

// RequireRestrictedDisclosure enforces the pre-source restricted-data gate.
// Personal mode performs no logging inspection. Restricted direct providers
// and every Bedrock state other than confirmed disabled fail closed.
func RequireRestrictedDisclosure(ctx context.Context, invocation modelpolicy.Invocation, probe DisclosureProbe) error {
	if invocation.DataMode == modelpolicy.DataModePersonal {
		return nil
	}
	if invocation.DataMode != modelpolicy.DataModeRestricted || invocation.Provider != modelpolicy.ProviderBedrock || probe == nil {
		return ErrDisclosureNotConfirmed
	}
	state, err := probe.InvocationLogging(ctx)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return errors.Join(ErrDisclosureNotConfirmed, ctxErr)
	}
	if err == nil && state == InvocationLoggingDisabled {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return errors.Join(ErrDisclosureNotConfirmed, context.Canceled)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return errors.Join(ErrDisclosureNotConfirmed, context.DeadlineExceeded)
	}
	return ErrDisclosureNotConfirmed
}

type postgresQueryBoundary interface {
	Ping(context.Context) error
	InspectMigrationStatus(
		context.Context,
		[]migration.Manifest,
		[]migration.Scope,
	) ([]migration.ScopeStatus, error)
}

// PostgresProbe performs read-only checks through one caller-owned canonical
// PostgreSQL query boundary.
type PostgresProbe struct {
	database    postgresQueryBoundary
	manifests   []migration.Manifest
	configured  []migration.Scope
	manifestErr error
}

// NewPostgresProbe constructs a read-only core-scope probe.
func NewPostgresProbe(database postgresQueryBoundary) *PostgresProbe {
	return NewPostgresProbeWithScopes(database, []migration.Scope{"core"})
}

// NewPostgresProbeWithScopes constructs a read-only status probe over the
// caller-owned database and an explicit configured-scope selection.
func NewPostgresProbeWithScopes(
	database postgresQueryBoundary,
	configured []migration.Scope,
) *PostgresProbe {
	probe := &PostgresProbe{
		database:   database,
		configured: append([]migration.Scope(nil), configured...),
	}
	core, err := coremigrations.Manifest()
	if err != nil {
		probe.manifestErr = err
		return probe
	}
	directory, err := directorymigrations.Manifest()
	if err != nil {
		probe.manifestErr = err
		return probe
	}
	probe.manifests = []migration.Manifest{core, directory}
	return probe
}

// Ping verifies the caller-owned PostgreSQL connection.
func (probe *PostgresProbe) Ping(ctx context.Context) error {
	if probe == nil || probe.database == nil {
		return fmt.Errorf("PostgreSQL doctor database is not configured")
	}
	if err := probe.database.Ping(ctx); err != nil {
		return fmt.Errorf("ping PostgreSQL for doctor: %w", err)
	}
	return nil
}

// MigrationStatus inspects both known scoped manifests without writing.
func (probe *PostgresProbe) MigrationStatus(
	ctx context.Context,
) ([]migration.ScopeStatus, error) {
	if probe.manifestErr != nil {
		return nil, fmt.Errorf("load PostgreSQL migration manifests: %w", probe.manifestErr)
	}
	if probe == nil || probe.database == nil {
		return nil, fmt.Errorf("PostgreSQL migration inspector is not configured")
	}
	return probe.database.InspectMigrationStatus(ctx, probe.manifests, probe.configured)
}

type googleSourceFactory func(context.Context) (source.RepresentativeSource, error)

// GoogleProbe lazily validates the OAuth material and inspects one in-scope
// document. It does not run the authorization flow or synchronize content.
type GoogleProbe struct {
	folderID string
	open     googleSourceFactory
	source   source.RepresentativeSource
}

// NewGoogleProbe constructs a Google probe without reading credentials or
// contacting Google.
func NewGoogleProbe(clientFile, tokenFile, folderID string, classifier drive.TabClassifier) *GoogleProbe {
	return newGoogleProbe(folderID, func(ctx context.Context) (source.RepresentativeSource, error) {
		httpClient, err := drive.NewAuthorizedHTTPClient(ctx, clientFile, tokenFile)
		if err != nil {
			return nil, err
		}
		if err := validateGoogleToken(ctx, httpClient); err != nil {
			return nil, err
		}
		return drive.NewClient(ctx, httpClient, classifier)
	})
}

func newGoogleProbe(folderID string, open googleSourceFactory) *GoogleProbe {
	return &GoogleProbe{folderID: folderID, open: open}
}

// CheckAuthorization reads the configured files and retrieves a valid token.
// A refresh, when necessary, is held in memory and never written by doctor.
func (probe *GoogleProbe) CheckAuthorization(ctx context.Context) error {
	if probe.source != nil {
		return nil
	}
	sourceBoundary, err := probe.open(ctx)
	if err != nil {
		return fmt.Errorf("validate Google authorization: %w", err)
	}
	probe.source = sourceBoundary
	return nil
}

// CheckFolder verifies the configured folder through a minimal metadata read.
func (probe *GoogleProbe) CheckFolder(ctx context.Context) error {
	if probe.source == nil {
		return errors.New("Google authorization has not been checked")
	}
	return probe.source.CheckCollection(ctx, probe.folderID)
}

// GetRepresentative returns at most one direct Google Doc for the subsequent
// all-tabs check.
func (probe *GoogleProbe) GetRepresentative(ctx context.Context) (source.Document, bool, error) {
	if probe.source == nil {
		return source.Document{}, false, errors.New("Google authorization has not been checked")
	}
	return probe.source.GetRepresentative(ctx, probe.folderID)
}

// GetDocument retrieves the representative document through the source's
// include-all-tabs contract.
func (probe *GoogleProbe) GetDocument(ctx context.Context, documentID string) (source.Document, error) {
	if probe.source == nil {
		return source.Document{}, errors.New("Google authorization has not been checked")
	}
	return probe.source.Get(ctx, documentID)
}

func validateGoogleToken(ctx context.Context, client *http.Client) error {
	transport, ok := client.Transport.(*oauth2.Transport)
	if !ok || transport.Source == nil {
		return errors.New("Google OAuth token source is unavailable")
	}
	if _, err := transport.Source.Token(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return errors.New("Google OAuth token is unavailable or expired")
	}
	return nil
}

type stsAPI interface {
	GetCallerIdentity(context.Context, *sts.GetCallerIdentityInput, ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error)
}

type bedrockControlAPI interface {
	GetFoundationModelAvailability(context.Context, *awsbedrock.GetFoundationModelAvailabilityInput, ...func(*awsbedrock.Options)) (*awsbedrock.GetFoundationModelAvailabilityOutput, error)
	GetInferenceProfile(context.Context, *awsbedrock.GetInferenceProfileInput, ...func(*awsbedrock.Options)) (*awsbedrock.GetInferenceProfileOutput, error)
	GetModelInvocationLoggingConfiguration(context.Context, *awsbedrock.GetModelInvocationLoggingConfigurationInput, ...func(*awsbedrock.Options)) (*awsbedrock.GetModelInvocationLoggingConfigurationOutput, error)
}

type awsClientsFactory func(context.Context) (stsAPI, bedrockControlAPI, error)

// AWSProbe lazily loads the configured credential chain and creates only STS
// and Bedrock control-plane clients. It has no runtime invocation client.
type AWSProbe struct {
	modelID string
	open    awsClientsFactory
	sts     stsAPI
	bedrock bedrockControlAPI
}

// NewAWSProbe constructs an AWS probe without loading or retrieving
// credentials. An empty profile uses the normal default credential chain.
func NewAWSProbe(profile, region, modelID string) *AWSProbe {
	return &AWSProbe{
		modelID: modelID,
		open: func(ctx context.Context) (stsAPI, bedrockControlAPI, error) {
			options := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(region)}
			if strings.TrimSpace(profile) != "" {
				options = append(options, awsconfig.WithSharedConfigProfile(profile))
			}
			configuration, err := awsconfig.LoadDefaultConfig(ctx, options...)
			if err != nil {
				return nil, nil, err
			}
			return sts.NewFromConfig(configuration), awsbedrock.NewFromConfig(configuration), nil
		},
	}
}

func newAWSProbe(modelID string, identity stsAPI, control bedrockControlAPI) *AWSProbe {
	return &AWSProbe{modelID: modelID, sts: identity, bedrock: control}
}

// CheckCredentials asks STS to validate the configured caller without exposing
// the account, user ID, or ARN.
func (probe *AWSProbe) CheckCredentials(ctx context.Context) error {
	identity, _, err := probe.clients(ctx)
	if err != nil {
		return fmt.Errorf("load AWS doctor clients: %w", err)
	}
	if _, err := identity.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{}); err != nil {
		return fmt.Errorf("validate AWS credentials: %w", err)
	}
	return nil
}

// CheckModel inspects a configured foundation model or inference profile. This
// is a control-plane availability check and deliberately performs no inference.
func (probe *AWSProbe) CheckModel(ctx context.Context) error {
	_, control, err := probe.clients(ctx)
	if err != nil {
		return fmt.Errorf("load AWS doctor clients: %w", err)
	}
	output, profileErr := control.GetInferenceProfile(ctx, &awsbedrock.GetInferenceProfileInput{
		InferenceProfileIdentifier: awssdk.String(probe.modelID),
	})
	if profileErr == nil {
		if output == nil || output.Status != bedrocktypes.InferenceProfileStatusActive {
			return errors.New("bedrock inference profile is not active")
		}
		return nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("inspect Bedrock inference profile: %w", ctxErr)
	}
	if isInferenceProfileID(probe.modelID) || !foundationFallbackAllowed(profileErr) {
		return fmt.Errorf("inspect Bedrock inference profile: %w", profileErr)
	}

	foundationOutput, err := control.GetFoundationModelAvailability(ctx, &awsbedrock.GetFoundationModelAvailabilityInput{
		ModelId: awssdk.String(probe.modelID),
	})
	if err != nil {
		return fmt.Errorf("inspect Bedrock foundation model availability: %w", err)
	}
	if foundationOutput == nil || foundationOutput.AgreementAvailability == nil ||
		foundationOutput.AgreementAvailability.Status != bedrocktypes.AgreementStatusAvailable ||
		foundationOutput.AuthorizationStatus != bedrocktypes.AuthorizationStatusAuthorized ||
		foundationOutput.EntitlementAvailability != bedrocktypes.EntitlementAvailabilityAvailable ||
		foundationOutput.RegionAvailability != bedrocktypes.RegionAvailabilityAvailable {
		return errors.New("bedrock foundation model is not available")
	}
	return nil
}

func foundationFallbackAllowed(err error) bool {
	var apiError smithy.APIError
	if !errors.As(err, &apiError) {
		return false
	}
	switch apiError.ErrorCode() {
	case "ResourceNotFoundException", "ValidationException":
		return true
	default:
		return false
	}
}

// InvocationLogging inspects account-level configuration. Any non-nil
// configuration is conservatively reported as enabled.
func (probe *AWSProbe) InvocationLogging(ctx context.Context) (InvocationLoggingState, error) {
	_, control, err := probe.clients(ctx)
	if err != nil {
		return InvocationLoggingUnknown, fmt.Errorf("load AWS doctor clients: %w", err)
	}
	output, err := control.GetModelInvocationLoggingConfiguration(ctx, &awsbedrock.GetModelInvocationLoggingConfigurationInput{})
	if err != nil {
		return InvocationLoggingUnknown, fmt.Errorf("inspect Bedrock invocation logging: %w", err)
	}
	if output == nil {
		return InvocationLoggingUnknown, errors.New("bedrock invocation logging response is missing")
	}
	if output.LoggingConfig == nil {
		return InvocationLoggingDisabled, nil
	}
	return InvocationLoggingEnabled, nil
}

func (probe *AWSProbe) clients(ctx context.Context) (stsAPI, bedrockControlAPI, error) {
	if probe.sts != nil && probe.bedrock != nil {
		return probe.sts, probe.bedrock, nil
	}
	identity, control, err := probe.open(ctx)
	if err != nil {
		return nil, nil, err
	}
	probe.sts = identity
	probe.bedrock = control
	return identity, control, nil
}

func isInferenceProfileID(modelID string) bool {
	if strings.Contains(modelID, ":inference-profile/") || strings.Contains(modelID, ":application-inference-profile/") {
		return true
	}
	for _, prefix := range []string{"us.", "eu.", "apac.", "global."} {
		if strings.HasPrefix(modelID, prefix) {
			return true
		}
	}
	return false
}
