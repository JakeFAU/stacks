package config

import (
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv(HTTPHostEnvironmentVariable, "")
	t.Setenv(HTTPPortEnvironmentVariable, "")
	t.Setenv(ReadHeaderTimeoutEnvironmentVariable, "")

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
}

func TestLoadOverridesDefaults(t *testing.T) {
	t.Setenv(HTTPHostEnvironmentVariable, "0.0.0.0")
	t.Setenv(HTTPPortEnvironmentVariable, "9090")
	t.Setenv(ReadHeaderTimeoutEnvironmentVariable, "7")

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
