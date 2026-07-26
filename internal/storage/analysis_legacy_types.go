package storage

// These private types preserve the pre-canonical analysis cache implementation
// until its storage-owned deletion. They must not re-enter the analysis service
// contract, which now reads canonical evidence on demand.

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"hash"
	"strings"

	"github.com/google/uuid"

	analysisdomain "stacks/internal/analysis"
	"stacks/internal/modelpolicy"
)

const (
	legacyTemporalDigestScope = "stacks.temporal-pair-analysis.v1"
	legacyProviderDigestScope = "stacks.pair-analysis-input.v2.provider"
)

var errLegacyStaleAnalysisInput = fmt.Errorf(
	"analysis inputs are stale; retry with a fresh snapshot",
)

type legacyInputKind string

const (
	legacyInputDocumentVersion    legacyInputKind = "document_version"
	legacyInputSourceDocument     legacyInputKind = "source_document"
	legacyInputDocumentTab        legacyInputKind = "document_tab"
	legacyInputObservation        legacyInputKind = "observation"
	legacyInputSignal             legacyInputKind = "signal"
	legacyInputResolutionDecision legacyInputKind = "resolution_decision"
)

type legacyInputReference struct {
	Kind   legacyInputKind
	ID     string
	Digest [sha256.Size]byte
}

type legacyPairSnapshot struct {
	Accepted bool
	Inputs   []legacyInputReference
	Signals  []analysisdomain.Signal
}

type legacyAnalysisIdentity struct {
	EmployeeEntityID string
	ManagerEntityID  string
	PromptVersion    string
	PolicyVersion    string
	Provider         modelpolicy.Provider
	Region           string
	ModelID          string
	MaxTokens        int
	Inputs           []legacyInputReference
	InputDigest      [sha256.Size]byte
}

type legacyReport struct {
	analysisdomain.Report
	ID          string
	InputDigest [sha256.Size]byte
}

type legacyCompletion struct {
	Identity legacyAnalysisIdentity
	Report   legacyReport
	DataMode modelpolicy.DataMode
}

func computeLegacyInputDigest(
	identity legacyAnalysisIdentity,
) ([sha256.Size]byte, error) {
	if strings.TrimSpace(identity.EmployeeEntityID) == "" ||
		strings.TrimSpace(identity.ManagerEntityID) == "" ||
		strings.TrimSpace(identity.PromptVersion) == "" ||
		strings.TrimSpace(identity.PolicyVersion) == "" ||
		strings.TrimSpace(identity.ModelID) == "" ||
		identity.MaxTokens <= 0 {
		return [sha256.Size]byte{}, fmt.Errorf(
			"analysis identity fields are required",
		)
	}
	if err := (modelpolicy.Invocation{
		Provider: identity.Provider,
		DataMode: modelpolicy.DataModePersonal,
		Region:   identity.Region,
	}).Validate(); err != nil {
		return [sha256.Size]byte{}, fmt.Errorf(
			"analysis identity provider policy is invalid",
		)
	}
	employeeID, err := uuid.Parse(strings.TrimSpace(identity.EmployeeEntityID))
	if err != nil {
		return [sha256.Size]byte{}, fmt.Errorf(
			"analysis employee entity ID is invalid",
		)
	}
	managerID, err := uuid.Parse(strings.TrimSpace(identity.ManagerEntityID))
	if err != nil || employeeID == managerID {
		return [sha256.Size]byte{}, fmt.Errorf(
			"analysis manager entity ID is invalid",
		)
	}
	hasher := sha256.New()
	digestScope := legacyProviderDigestScope
	if identity.Provider == modelpolicy.ProviderBedrock {
		digestScope = legacyTemporalDigestScope
	}
	writeLegacyDigestString(hasher, digestScope)
	writeLegacyDigestString(hasher, employeeID.String())
	writeLegacyDigestString(hasher, managerID.String())
	writeLegacyDigestString(hasher, strings.TrimSpace(identity.PromptVersion))
	writeLegacyDigestString(hasher, strings.TrimSpace(identity.PolicyVersion))
	writeLegacyDigestString(hasher, strings.TrimSpace(identity.Region))
	if identity.Provider != modelpolicy.ProviderBedrock {
		writeLegacyDigestString(hasher, string(identity.Provider))
	}
	writeLegacyDigestString(hasher, strings.TrimSpace(identity.ModelID))
	writeLegacyDigestLength(hasher, uint64(identity.MaxTokens))
	writeLegacyDigestLength(hasher, uint64(len(identity.Inputs)))
	seenInputs := make(map[string]struct{}, len(identity.Inputs))
	for index, input := range identity.Inputs {
		if !validLegacyInputKind(input.Kind) ||
			strings.TrimSpace(input.ID) == "" ||
			input.Digest == ([sha256.Size]byte{}) {
			return [sha256.Size]byte{}, fmt.Errorf(
				"analysis identity input %d is invalid",
				index,
			)
		}
		inputID := strings.TrimSpace(input.ID)
		if parsed, parseErr := uuid.Parse(inputID); parseErr == nil {
			inputID = parsed.String()
		}
		inputKey := string(input.Kind) + "\x00" + inputID
		if _, exists := seenInputs[inputKey]; exists {
			return [sha256.Size]byte{}, fmt.Errorf(
				"analysis identity input %d is repeated",
				index,
			)
		}
		seenInputs[inputKey] = struct{}{}
		writeLegacyDigestString(hasher, string(input.Kind))
		writeLegacyDigestString(hasher, inputID)
		writeLegacyDigestBytes(hasher, input.Digest[:])
	}
	var digest [sha256.Size]byte
	copy(digest[:], hasher.Sum(nil))
	return digest, nil
}

func validLegacyInputKind(kind legacyInputKind) bool {
	switch kind {
	case legacyInputDocumentVersion,
		legacyInputSourceDocument,
		legacyInputDocumentTab,
		legacyInputObservation,
		legacyInputSignal,
		legacyInputResolutionDecision:
		return true
	default:
		return false
	}
}

func writeLegacyDigestString(hasher hash.Hash, value string) {
	writeLegacyDigestBytes(hasher, []byte(value))
}

func writeLegacyDigestBytes(hasher hash.Hash, value []byte) {
	writeLegacyDigestLength(hasher, uint64(len(value)))
	_, _ = hasher.Write(value)
}

func writeLegacyDigestLength(hasher hash.Hash, length uint64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], length)
	_, _ = hasher.Write(encoded[:])
}
