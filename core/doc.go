// Package core documents the experimental dependency boundary shared by
// Stacks' public evidence, identity, observation, and temporal packages.
//
// It preserves the domain contracts needed to retain provenance, uncertainty,
// and temporal meaning without treating a provider, persistence layer, model,
// or application workflow as part of that contract.
//
// Core performs no provider I/O, persistence, configuration loading, logging,
// telemetry initialization, model invocation, or application-specific policy.
package core
