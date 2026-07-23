// Package modelpolicy defines the bounded provider and disclosure policy for
// model invocations that may receive private source content.
package modelpolicy

import (
	"fmt"
	"strings"
)

// Provider identifies a supported model invocation boundary.
type Provider string

const (
	ProviderBedrock   Provider = "bedrock"
	ProviderOpenAI    Provider = "openai"
	ProviderAnthropic Provider = "anthropic"
)

// Valid reports whether provider is one of the supported invocation boundaries.
func (provider Provider) Valid() bool {
	switch provider {
	case ProviderBedrock, ProviderOpenAI, ProviderAnthropic:
		return true
	default:
		return false
	}
}

// DataMode identifies the disclosure policy associated with a model run.
type DataMode string

const (
	DataModePersonal   DataMode = "personal"
	DataModeRestricted DataMode = "restricted"
	DataModeLegacy     DataMode = "legacy"
)

// ValidForNewRun reports whether dataMode may authorize a new model invocation.
// Legacy identifies historical provenance for rows created before disclosure
// policy enforcement and must never authorize new disclosure.
func (dataMode DataMode) ValidForNewRun() bool {
	switch dataMode {
	case DataModePersonal, DataModeRestricted:
		return true
	default:
		return false
	}
}

// Invocation is the provider and disclosure policy selected for a new model run.
type Invocation struct {
	Provider Provider
	DataMode DataMode
	Region   string
}

// Validate rejects invalid provider, disclosure-mode, and provider-region
// combinations before a model invocation boundary is constructed.
func (invocation Invocation) Validate() error {
	if !invocation.Provider.Valid() {
		return fmt.Errorf("model provider is invalid")
	}
	if !invocation.DataMode.ValidForNewRun() {
		return fmt.Errorf("data mode is invalid for a new model run")
	}
	if invocation.DataMode == DataModeRestricted && invocation.Provider != ProviderBedrock {
		return fmt.Errorf("restricted data mode requires the Bedrock provider")
	}

	region := strings.TrimSpace(invocation.Region)
	if invocation.Provider == ProviderBedrock {
		if region == "" || region != invocation.Region {
			return fmt.Errorf("bedrock provider requires a region")
		}
		return nil
	}
	if invocation.Region != "" {
		return fmt.Errorf("direct model providers do not accept a region")
	}
	return nil
}
