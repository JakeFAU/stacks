package queryplan

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/JakeFAU/stacks/core/identity"
	"github.com/JakeFAU/stacks/core/timepoint"
	"stacks/internal/query"
)

const (
	minimumQuestionByteLimit = 1
	maximumQuestionByteLimit = 64 * 1024
)

// NormalizeInput validates private input before provider disclosure and returns
// a defensively copied, canonical form suitable for deterministic planning.
func NormalizeInput(input Input, limits query.Limits, maxQuestionBytes int) (Input, error) {
	if err := query.ValidateLimits(limits); err != nil {
		return Input{}, errors.New("query planner limits are invalid")
	}
	if maxQuestionBytes < minimumQuestionByteLimit ||
		maxQuestionBytes > maximumQuestionByteLimit ||
		len(input.Question) > maxQuestionBytes ||
		!utf8.ValidString(input.Question) ||
		strings.TrimSpace(input.Question) == "" {
		return Input{}, errors.New("query planner question is invalid")
	}
	if input.ReferenceTime.IsZero() {
		return Input{}, errors.New("query planner reference time is required")
	}
	referenceTime := timepoint.Normalize(input.ReferenceTime)
	if _, err := serializeReferenceTime(referenceTime); err != nil {
		return Input{}, errors.New("query planner reference time is invalid")
	}
	entityIDs, err := normalizeEntityIDs(input.EntityIDs, limits.MaxEntities)
	if err != nil {
		return Input{}, err
	}
	for _, entityID := range entityIDs {
		if strings.Contains(input.Question, string(entityID)) {
			return Input{}, errors.New("query planner question contains a canonical entity ID")
		}
	}
	return Input{
		Question:      input.Question,
		EntityIDs:     entityIDs,
		ReferenceTime: referenceTime,
	}, nil
}

func normalizeEntityIDs(values []identity.EntityID, maximum int) ([]identity.EntityID, error) {
	if len(values) == 0 || len(values) > maximum {
		return nil, errors.New("query planner entity IDs are invalid")
	}
	result := make([]identity.EntityID, len(values))
	seen := make(map[identity.EntityID]struct{}, len(values))
	for index, value := range values {
		value = identity.EntityID(strings.TrimSpace(string(value)))
		if value == "" {
			return nil, errors.New("query planner entity IDs are invalid")
		}
		if _, exists := seen[value]; exists {
			return nil, errors.New("query planner entity IDs are invalid")
		}
		seen[value] = struct{}{}
		result[index] = value
	}
	sort.Slice(result, func(left, right int) bool { return result[left] < result[right] })
	return result, nil
}

type privateModelInput struct {
	Question                 string             `json:"question"`
	ReferenceTime            string             `json:"reference_time"`
	EntityCount              int                `json:"entity_count"`
	EntityIDsAttachedLocally bool               `json:"entity_ids_attached_locally"`
	Limits                   privateModelLimits `json:"limits"`
}

type privateModelLimits struct {
	MaxEntities   int `json:"max_entities"`
	MaxPredicates int `json:"max_predicates"`
	MaxChronology int `json:"max_chronology"`
}

func modelRequestFor(input Input, limits query.Limits) (ModelRequest, error) {
	if err := query.ValidateLimits(limits); err != nil {
		return ModelRequest{}, errors.New("query planner limits are invalid")
	}
	contract, err := PromptContract(PromptVersion)
	if err != nil {
		return ModelRequest{}, errors.New("query planner contract is invalid")
	}
	referenceTime, err := serializeReferenceTime(input.ReferenceTime)
	if err != nil {
		return ModelRequest{}, errors.New("query planner reference time is invalid")
	}
	payload, err := json.Marshal(privateModelInput{
		Question:                 input.Question,
		ReferenceTime:            referenceTime,
		EntityCount:              len(input.EntityIDs),
		EntityIDsAttachedLocally: true,
		Limits: privateModelLimits{
			MaxEntities:   limits.MaxEntities,
			MaxPredicates: limits.MaxPredicates,
			MaxChronology: limits.MaxChronology,
		},
	})
	if err != nil {
		return ModelRequest{}, errors.New("query planner private input is invalid")
	}
	return ModelRequest{
		PromptVersion: contract.Version,
		SystemPrompt:  contract.SystemPrompt,
		Input:         string(payload),
		SchemaName:    contract.SchemaName,
		JSONSchema:    append([]byte(nil), contract.JSONSchema...),
	}, nil
}

func serializeReferenceTime(value time.Time) (string, error) {
	normalized := timepoint.Normalize(value)
	serialized := normalized.Format(time.RFC3339Nano)
	parsed, err := time.Parse(time.RFC3339Nano, serialized)
	if err != nil || !parsed.Equal(normalized) {
		return "", errors.New("reference time is invalid")
	}
	return serialized, nil
}
