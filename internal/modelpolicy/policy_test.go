package modelpolicy

import "testing"

func TestProviderValid(t *testing.T) {
	tests := []struct {
		provider Provider
		want     bool
	}{
		{ProviderBedrock, true},
		{ProviderOpenAI, true},
		{ProviderAnthropic, true},
		{"", false},
		{"other", false},
	}

	for _, test := range tests {
		t.Run(string(test.provider), func(t *testing.T) {
			if got := test.provider.Valid(); got != test.want {
				t.Fatalf("Provider(%q).Valid() = %t, want %t", test.provider, got, test.want)
			}
		})
	}
}

func TestDataModeValidForNewRun(t *testing.T) {
	tests := []struct {
		dataMode DataMode
		want     bool
	}{
		{DataModePersonal, true},
		{DataModeRestricted, true},
		{DataModeLegacy, false},
		{"", false},
		{"other", false},
	}

	for _, test := range tests {
		t.Run(string(test.dataMode), func(t *testing.T) {
			if got := test.dataMode.ValidForNewRun(); got != test.want {
				t.Fatalf("DataMode(%q).ValidForNewRun() = %t, want %t", test.dataMode, got, test.want)
			}
		})
	}
}

func TestInvocationValidatePolicyMatrix(t *testing.T) {
	tests := []struct {
		name       string
		invocation Invocation
		wantErr    bool
	}{
		{"personal bedrock", Invocation{Provider: ProviderBedrock, DataMode: DataModePersonal, Region: "us-east-1"}, false},
		{"personal openai", Invocation{Provider: ProviderOpenAI, DataMode: DataModePersonal}, false},
		{"personal anthropic", Invocation{Provider: ProviderAnthropic, DataMode: DataModePersonal}, false},
		{"restricted bedrock", Invocation{Provider: ProviderBedrock, DataMode: DataModeRestricted, Region: "us-east-1"}, false},
		{"restricted openai", Invocation{Provider: ProviderOpenAI, DataMode: DataModeRestricted}, true},
		{"restricted anthropic", Invocation{Provider: ProviderAnthropic, DataMode: DataModeRestricted}, true},
		{"legacy bedrock", Invocation{Provider: ProviderBedrock, DataMode: DataModeLegacy, Region: "us-east-1"}, true},
		{"invalid provider", Invocation{Provider: "other", DataMode: DataModePersonal}, true},
		{"invalid data mode", Invocation{Provider: ProviderOpenAI, DataMode: "other"}, true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gotErr := test.invocation.Validate() != nil
			if gotErr != test.wantErr {
				t.Fatalf("Invocation%+v.Validate() error = %t, want %t", test.invocation, gotErr, test.wantErr)
			}
		})
	}
}

func TestInvocationValidateRequiresBedrockRegionOnly(t *testing.T) {
	tests := []struct {
		name       string
		invocation Invocation
		wantErr    bool
	}{
		{"bedrock missing region", Invocation{Provider: ProviderBedrock, DataMode: DataModePersonal}, true},
		{"bedrock whitespace region", Invocation{Provider: ProviderBedrock, DataMode: DataModePersonal, Region: " \t"}, true},
		{"bedrock padded region", Invocation{Provider: ProviderBedrock, DataMode: DataModePersonal, Region: " us-east-1 "}, true},
		{"bedrock trimmed region", Invocation{Provider: ProviderBedrock, DataMode: DataModePersonal, Region: "us-east-1"}, false},
		{"openai region", Invocation{Provider: ProviderOpenAI, DataMode: DataModePersonal, Region: "us-east-1"}, true},
		{"anthropic region", Invocation{Provider: ProviderAnthropic, DataMode: DataModePersonal, Region: "us-east-1"}, true},
		{"openai no region", Invocation{Provider: ProviderOpenAI, DataMode: DataModePersonal}, false},
		{"anthropic no region", Invocation{Provider: ProviderAnthropic, DataMode: DataModePersonal}, false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gotErr := test.invocation.Validate() != nil
			if gotErr != test.wantErr {
				t.Fatalf("Invocation%+v.Validate() error = %t, want %t", test.invocation, gotErr, test.wantErr)
			}
		})
	}
}
