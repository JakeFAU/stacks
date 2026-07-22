package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/smithy-go"
	"go.opentelemetry.io/otel/metric/noop"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"stacks/internal/bedrock"
	"stacks/internal/config"
	"stacks/internal/extract"
	"stacks/internal/observability"
)

func TestAWSLoadOptionsUseDefaultCredentialChainWhenProfileIsAbsent(t *testing.T) {
	options := awsLoadOptions("", "us-east-1")
	loaded := awsconfig.LoadOptions{}
	for _, option := range options {
		if err := option(&loaded); err != nil {
			t.Fatalf("apply AWS load option: %v", err)
		}
	}
	if loaded.Region != "us-east-1" || loaded.SharedConfigProfile != "" {
		t.Fatalf("AWS load options = %#v, want region plus default credential chain", loaded)
	}
}

func TestValidateAWSConfigurationCredentialsReturnsBoundedAuthenticationFailure(t *testing.T) {
	const privateProviderDetail = "synthetic private credential-provider detail"
	configuration := aws.Config{Credentials: aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
		return aws.Credentials{}, errors.New(privateProviderDetail)
	})}

	err := validateAWSConfigurationCredentials(context.Background(), configuration)
	if !errors.Is(err, extract.ErrAuthentication) {
		t.Fatalf("validateAWSConfigurationCredentials() error = %v, want bounded authentication failure", err)
	}
	if strings.Contains(err.Error(), privateProviderDetail) {
		t.Fatalf("validateAWSConfigurationCredentials() exposed provider detail: %v", err)
	}
}

func TestValidateAWSConfigurationCredentialsReturnsBoundedAuthorizationFailure(t *testing.T) {
	configuration := aws.Config{Credentials: aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
		return aws.Credentials{}, &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "synthetic private authorization detail"}
	})}

	err := validateAWSConfigurationCredentials(context.Background(), configuration)
	if !errors.Is(err, extract.ErrAuthorization) {
		t.Fatalf("validateAWSConfigurationCredentials() error = %v, want bounded authorization failure", err)
	}
}

func TestValidateAWSConfigurationCredentialsPreservesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	configuration := aws.Config{Credentials: aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
		return aws.Credentials{}, errors.New("synthetic canceled credential retrieval")
	})}

	err := validateAWSConfigurationCredentials(ctx, configuration)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("validateAWSConfigurationCredentials() error = %v, want context cancellation", err)
	}
}

func TestValidateAWSConfigurationCredentialsAcceptsRetrievedSigningKeys(t *testing.T) {
	configuration := aws.Config{Credentials: aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
		return aws.Credentials{AccessKeyID: "synthetic-access-key", SecretAccessKey: "synthetic-secret-key"}, nil
	})}

	if err := validateAWSConfigurationCredentials(context.Background(), configuration); err != nil {
		t.Fatalf("validateAWSConfigurationCredentials() error = %v", err)
	}
}

func TestPoCCommandProviderRegistersDoctorSyncAndAnalyzeWithoutConstructingLiveDependencies(t *testing.T) {
	recorder, err := observability.NewDecisionRecorder(noop.NewMeterProvider().Meter("synthetic"))
	if err != nil {
		t.Fatalf("create decision recorder: %v", err)
	}
	invocations, err := bedrock.NewMetricsInvocationRecorder(noop.NewMeterProvider().Meter("synthetic"))
	if err != nil {
		t.Fatalf("create invocation recorder: %v", err)
	}
	commands, err := pocCommandProvider(
		context.Background(), config.Settings{}, io.Discard, io.Discard,
		tracenoop.NewTracerProvider().Tracer("synthetic"), recorder, invocations,
	)
	if err != nil {
		t.Fatalf("pocCommandProvider() error = %v", err)
	}
	if commands[string(config.CommandSync)] == nil {
		t.Fatal("sync command is not registered")
	}
	if commands[string(config.CommandDoctor)] == nil {
		t.Fatal("doctor command is not registered")
	}
	if commands[string(config.CommandAnalyze)] == nil {
		t.Fatal("analyze command is not registered")
	}
}
