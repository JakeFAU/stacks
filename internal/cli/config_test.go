package cli

import (
	"context"
	"strings"
	"testing"
)

func TestConfigValidateCommandWritesBoundedTargetOutput(t *testing.T) {
	tests := []struct {
		name   string
		target ConfigValidationInput
		want   string
	}{
		{"serve", ConfigValidationInput{Command: CommandServe}, "configuration valid for serve\n"},
		{"doctor", ConfigValidationInput{Command: CommandDoctor}, "configuration valid for doctor\n"},
		{"sync", ConfigValidationInput{Command: CommandSync}, "configuration valid for sync\n"},
		{"entities", ConfigValidationInput{Command: CommandEntities}, "configuration valid for entities\n"},
		{"review", ConfigValidationInput{Command: CommandReview}, "configuration valid for review\n"},
		{"analyze", ConfigValidationInput{Command: CommandAnalyze}, "configuration valid for analyze\n"},
		{"db migrate", ConfigValidationInput{Command: CommandDBMigrate}, "configuration valid for db-migrate\n"},
		{"db status", ConfigValidationInput{Command: CommandDBStatus}, "configuration valid for db-status\n"},
		{"db reset", ConfigValidationInput{Command: CommandDBReset}, "configuration valid for db-reset\n"},
		{"google auth", ConfigValidationInput{Command: CommandAuth, Action: ActionAuthGoogle}, "configuration valid for auth google\n"},
		{"directory auth", ConfigValidationInput{Command: CommandAuth, Action: ActionAuthGoogleDirectory}, "configuration valid for auth google-directory\n"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			var output strings.Builder
			configFile := "/synthetic/private/stacks.yaml"
			err := (ConfigValidateCommand{Output: &output}).Run(context.Background(), Invocation{
				Command:          CommandConfig,
				Action:           ActionValidate,
				ConfigFile:       &configFile,
				ConfigValidation: &testCase.target,
			})
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if output.String() != testCase.want {
				t.Fatalf("output = %q, want %q", output.String(), testCase.want)
			}
			for _, privateInput := range []string{configFile, "synthetic-private-setting"} {
				if strings.Contains(output.String(), privateInput) {
					t.Fatalf("output exposed private input %q: %q", privateInput, output.String())
				}
			}
		})
	}
}

func TestConfigValidateCommandRejectsUnexpectedArgumentsWithoutWritingThem(t *testing.T) {
	const privateArgument = "synthetic-private-setting"
	var output strings.Builder
	err := (ConfigValidateCommand{Output: &output}).Run(context.Background(), Invocation{
		Command:   CommandConfig,
		Action:    ActionValidate,
		Arguments: []string{privateArgument},
		ConfigValidation: &ConfigValidationInput{
			Command: CommandServe,
		},
	})
	if err == nil {
		t.Fatal("Run() error = nil, want unexpected arguments rejection")
	}
	if strings.Contains(err.Error(), privateArgument) || strings.Contains(output.String(), privateArgument) {
		t.Fatalf("Run() exposed private argument: error=%q output=%q", err, output.String())
	}
}

func TestConfigValidateCommandRejectsMissingTarget(t *testing.T) {
	err := (ConfigValidateCommand{}).Run(context.Background(), Invocation{Command: CommandConfig, Action: ActionValidate})
	if err == nil {
		t.Fatal("Run() error = nil, want missing target rejection")
	}
}

func TestConfigValidateCommandRejectsInvalidAuthAction(t *testing.T) {
	var output strings.Builder
	err := (ConfigValidateCommand{Output: &output}).Run(context.Background(), Invocation{
		Command:          CommandConfig,
		Action:           ActionValidate,
		ConfigValidation: &ConfigValidationInput{Command: CommandAuth, Action: ActionList},
	})
	if err == nil {
		t.Fatal("Run() error = nil, want invalid auth action rejection")
	}
	if err.Error() != "configuration validation target is invalid" {
		t.Fatalf("Run() error = %q, want invalid auth target rejection", err)
	}
	if output.Len() != 0 {
		t.Fatalf("Run() output = %q, want none for invalid auth target", output.String())
	}
}
