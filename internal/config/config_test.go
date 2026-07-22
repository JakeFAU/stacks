package config

import (
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv(HTTPHostEnvironmentVariable, "")
	t.Setenv(HTTPPortEnvironmentVariable, "")
	t.Setenv(ReadHeaderTimeoutEnvironmentVariable, "")
	clearObservabilityEnvironment(t)

	settings, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if settings.HTTPAddress != "127.0.0.1:8080" {
		t.Errorf("HTTPAddress = %q, want %q", settings.HTTPAddress, "127.0.0.1:8080")
	}
	if settings.ReadHeaderTimeout != 5*time.Second {
		t.Errorf("ReadHeaderTimeout = %v, want %v", settings.ReadHeaderTimeout, 5*time.Second)
	}
	if settings.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want %q", settings.LogLevel, "info")
	}
	if settings.Telemetry.Enabled {
		t.Error("Telemetry.Enabled = true, want false")
	}
	if settings.Telemetry.Endpoint != "127.0.0.1:4317" {
		t.Errorf("Telemetry.Endpoint = %q, want %q", settings.Telemetry.Endpoint, "127.0.0.1:4317")
	}
	if !settings.Telemetry.Insecure {
		t.Error("Telemetry.Insecure = false, want true")
	}
	if settings.Telemetry.MetricExportInterval != 10*time.Second {
		t.Errorf("Telemetry.MetricExportInterval = %v, want %v", settings.Telemetry.MetricExportInterval, 10*time.Second)
	}
	if settings.Telemetry.ServiceName != "stacks" {
		t.Errorf("Telemetry.ServiceName = %q, want %q", settings.Telemetry.ServiceName, "stacks")
	}
	if settings.Telemetry.TraceSampleRatio != 1 {
		t.Errorf("Telemetry.TraceSampleRatio = %v, want 1", settings.Telemetry.TraceSampleRatio)
	}
}

func TestLoadOverridesDefaults(t *testing.T) {
	t.Setenv(HTTPHostEnvironmentVariable, "0.0.0.0")
	t.Setenv(HTTPPortEnvironmentVariable, "9090")
	t.Setenv(ReadHeaderTimeoutEnvironmentVariable, "7")
	t.Setenv(LogLevelEnvironmentVariable, "debug")
	t.Setenv(OTelEnabledEnvironmentVariable, "true")
	t.Setenv(OTelEndpointEnvironmentVariable, "collector:4317")
	t.Setenv(OTelInsecureEnvironmentVariable, "false")
	t.Setenv(OTelMetricIntervalEnvironmentVariable, "3s")
	t.Setenv(OTelServiceNameEnvironmentVariable, "stacks-test")
	t.Setenv(OTelTraceSampleRatioEnvironmentVariable, "0.25")

	settings, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if settings.HTTPAddress != "0.0.0.0:9090" {
		t.Errorf("HTTPAddress = %q, want %q", settings.HTTPAddress, "0.0.0.0:9090")
	}
	if settings.ReadHeaderTimeout != 7*time.Second {
		t.Errorf("ReadHeaderTimeout = %v, want %v", settings.ReadHeaderTimeout, 7*time.Second)
	}
	if settings.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, want %q", settings.LogLevel, "debug")
	}
	if !settings.Telemetry.Enabled {
		t.Error("Telemetry.Enabled = false, want true")
	}
	if settings.Telemetry.Endpoint != "collector:4317" {
		t.Errorf("Telemetry.Endpoint = %q, want %q", settings.Telemetry.Endpoint, "collector:4317")
	}
	if settings.Telemetry.Insecure {
		t.Error("Telemetry.Insecure = true, want false")
	}
	if settings.Telemetry.MetricExportInterval != 3*time.Second {
		t.Errorf("Telemetry.MetricExportInterval = %v, want %v", settings.Telemetry.MetricExportInterval, 3*time.Second)
	}
	if settings.Telemetry.ServiceName != "stacks-test" {
		t.Errorf("Telemetry.ServiceName = %q, want %q", settings.Telemetry.ServiceName, "stacks-test")
	}
	if settings.Telemetry.TraceSampleRatio != 0.25 {
		t.Errorf("Telemetry.TraceSampleRatio = %v, want 0.25", settings.Telemetry.TraceSampleRatio)
	}
}

func TestLoadRejectsInvalidPort(t *testing.T) {
	t.Setenv(HTTPPortEnvironmentVariable, "70000")

	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want invalid port error")
	}
}

func TestLoadRejectsInvalidTimeout(t *testing.T) {
	t.Setenv(ReadHeaderTimeoutEnvironmentVariable, "never")

	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want invalid timeout error")
	}
}

func TestLoadRejectsInvalidObservabilitySettings(t *testing.T) {
	tests := []struct {
		name     string
		variable string
		value    string
	}{
		{name: "log level", variable: LogLevelEnvironmentVariable, value: "verbose"},
		{name: "enabled", variable: OTelEnabledEnvironmentVariable, value: "sometimes"},
		{name: "insecure", variable: OTelInsecureEnvironmentVariable, value: "perhaps"},
		{name: "metric interval", variable: OTelMetricIntervalEnvironmentVariable, value: "soon"},
		{name: "negative metric interval", variable: OTelMetricIntervalEnvironmentVariable, value: "-1s"},
		{name: "sample ratio", variable: OTelTraceSampleRatioEnvironmentVariable, value: "1.1"},
		{name: "NaN sample ratio", variable: OTelTraceSampleRatioEnvironmentVariable, value: "NaN"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clearObservabilityEnvironment(t)
			t.Setenv(test.variable, test.value)
			if _, err := Load(); err == nil {
				t.Fatal("Load() error = nil, want validation error")
			}
		})
	}
}

func clearObservabilityEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		LogLevelEnvironmentVariable,
		OTelEnabledEnvironmentVariable,
		OTelEndpointEnvironmentVariable,
		OTelInsecureEnvironmentVariable,
		OTelMetricIntervalEnvironmentVariable,
		OTelServiceNameEnvironmentVariable,
		OTelTraceSampleRatioEnvironmentVariable,
	} {
		t.Setenv(name, "")
	}
}
