package config

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"time"
)

const (
	defaultHTTPHost                      = "127.0.0.1"
	defaultHTTPPort                      = 8080
	defaultReadHeaderTimeoutSeconds      = 5
	HTTPHostEnvironmentVariable          = "STACKS_HTTP_HOST"
	HTTPPortEnvironmentVariable          = "STACKS_HTTP_PORT"
	ReadHeaderTimeoutEnvironmentVariable = "STACKS_READ_HEADER_TIMEOUT_SECONDS"
)

// Settings contains validated runtime configuration.
type Settings struct {
	HTTPAddress       string
	ReadHeaderTimeout time.Duration
}

// Load reads and validates settings from the environment.
func Load() (Settings, error) {
	host := environmentOrDefault(HTTPHostEnvironmentVariable, defaultHTTPHost)
	port, err := positiveIntegerEnvironment(HTTPPortEnvironmentVariable, defaultHTTPPort)
	if err != nil {
		return Settings{}, err
	}
	if port > 65535 {
		return Settings{}, fmt.Errorf("%s must be at most 65535", HTTPPortEnvironmentVariable)
	}

	readHeaderTimeoutSeconds, err := positiveIntegerEnvironment(
		ReadHeaderTimeoutEnvironmentVariable,
		defaultReadHeaderTimeoutSeconds,
	)
	if err != nil {
		return Settings{}, err
	}

	return Settings{
		HTTPAddress:       net.JoinHostPort(host, strconv.Itoa(port)),
		ReadHeaderTimeout: time.Duration(readHeaderTimeoutSeconds) * time.Second,
	}, nil
}

func environmentOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func positiveIntegerEnvironment(name string, fallback int) (int, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}

	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return parsed, nil
}
