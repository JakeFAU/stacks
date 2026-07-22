package extract

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var (
	// ErrAuthentication is a provider-neutral bounded model credential failure.
	ErrAuthentication = errors.New("model authentication failed; configure valid provider credentials")
	// ErrAuthorization is a provider-neutral bounded model permission failure.
	ErrAuthorization = errors.New("model authorization failed; grant invocation access to the configured model")
)

const (
	ExtractionPromptVersion = "extract-v1"
	AnalysisPromptVersion   = "analyze-v1"

	ExtractionSchemaName = "meeting_extraction"
	AnalysisSchemaName   = "pair_analysis"
)

//go:embed prompts/extract-v1.txt
var extractionPrompt string

//go:embed prompts/analyze-v1.txt
var analysisPrompt string

// Model is the provider-neutral structured generation boundary.
type Model interface {
	Generate(context.Context, Request) (Response, error)
}

// Contract is one reviewed prompt and schema pairing. JSONSchema is an
// isolated copy so callers cannot alter the embedded contract.
type Contract struct {
	Version      string
	SystemPrompt string
	SchemaName   string
	JSONSchema   []byte
}

// Request contains one private model input and its public, versioned contract.
// Callers must not place Input or SystemPrompt in telemetry or errors.
type Request struct {
	PromptVersion string
	SystemPrompt  string
	Input         string
	SchemaName    string
	JSONSchema    []byte
}

// Usage reports the bounded token accounting returned by a model provider.
type Usage struct {
	InputTokens  int64
	OutputTokens int64
	TotalTokens  int64
}

// Response contains untrusted JSON and bounded invocation metadata. Output
// must pass decoding and deterministic validation before domain conversion.
type Response struct {
	Output        json.RawMessage
	Usage         Usage
	Latency       time.Duration
	ModelID       string
	PromptVersion string
	Outcome       string
}

// Prompt returns the embedded, reviewable prompt for an exact version.
func Prompt(version string) (string, error) {
	contract, err := PromptContract(version)
	if err != nil {
		return "", err
	}
	return contract.SystemPrompt, nil
}

// PromptContract returns the exact reviewed prompt and schema pairing for a
// supported version.
func PromptContract(version string) (Contract, error) {
	switch version {
	case ExtractionPromptVersion:
		return Contract{
			Version: version, SystemPrompt: extractionPrompt,
			SchemaName: ExtractionSchemaName, JSONSchema: ExtractionJSONSchema(),
		}, nil
	case AnalysisPromptVersion:
		return Contract{
			Version: version, SystemPrompt: analysisPrompt,
			SchemaName: AnalysisSchemaName, JSONSchema: AnalysisJSONSchema(),
		}, nil
	default:
		return Contract{}, fmt.Errorf("prompt version is not supported")
	}
}

// ExtractionJSONSchema returns an isolated copy of the extraction schema.
func ExtractionJSONSchema() []byte {
	return append([]byte(nil), extractionJSONSchema...)
}

// AnalysisJSONSchema returns an isolated copy of the analysis schema.
func AnalysisJSONSchema() []byte {
	return append([]byte(nil), analysisJSONSchema...)
}

var extractionJSONSchema = []byte(`{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "required": ["meeting_date", "citations", "people", "statements", "signals"],
  "properties": {
    "meeting_date": {"type": "string"},
    "citations": {
      "type": "array",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["id", "tab_id", "start_offset", "end_offset", "quote"],
        "properties": {
          "id": {"type": "string", "minLength": 1, "pattern": "^\\S(?:.*\\S)?$"},
          "tab_id": {"type": "string", "minLength": 1, "pattern": "^\\S(?:.*\\S)?$"},
          "start_offset": {"type": "integer", "minimum": 0},
          "end_offset": {"type": "integer", "minimum": 1},
          "quote": {"type": "string", "minLength": 1}
        }
      }
    },
    "people": {
      "type": "array",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["id", "surface", "role", "email", "citation_ids"],
        "properties": {
          "id": {"type": "string", "minLength": 1, "pattern": "^\\S(?:.*\\S)?$"},
          "surface": {"type": "string", "minLength": 1},
          "role": {"type": "string", "enum": ["speaker", "reference"]},
          "email": {"type": "string"},
          "citation_ids": {"type": "array", "minItems": 1, "items": {"type": "string", "minLength": 1, "pattern": "^\\S(?:.*\\S)?$"}}
        }
      }
    },
    "statements": {
      "type": "array",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["id", "speaker_mention_id", "subject_mention_id", "predicate", "object_text", "valid_date", "citation_ids"],
        "properties": {
          "id": {"type": "string", "minLength": 1, "pattern": "^\\S(?:.*\\S)?$"},
          "speaker_mention_id": {"type": "string", "minLength": 1, "pattern": "^\\S(?:.*\\S)?$"},
          "subject_mention_id": {"type": "string", "minLength": 1, "pattern": "^\\S(?:.*\\S)?$"},
          "predicate": {"type": "string", "minLength": 1},
          "object_text": {"type": "string", "minLength": 1},
          "valid_date": {"type": "string"},
          "citation_ids": {"type": "array", "minItems": 1, "items": {"type": "string", "minLength": 1, "pattern": "^\\S(?:.*\\S)?$"}}
        }
      }
    },
    "signals": {
      "type": "array",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["id", "subject_mention_id", "object_mention_id", "statement_ids", "category", "direction", "rationale", "confidence", "supporting_citation_ids", "contradicting_citation_ids"],
        "properties": {
          "id": {"type": "string", "minLength": 1, "pattern": "^\\S(?:.*\\S)?$"},
          "subject_mention_id": {"type": "string", "minLength": 1, "pattern": "^\\S(?:.*\\S)?$"},
          "object_mention_id": {"type": "string", "minLength": 1, "pattern": "^\\S(?:.*\\S)?$"},
          "statement_ids": {"type": "array", "minItems": 1, "items": {"type": "string", "minLength": 1, "pattern": "^\\S(?:.*\\S)?$"}},
          "category": {"type": "string", "enum": ["delegation_autonomy", "scrutiny_correction", "endorsement_trust", "support_advocacy", "future_responsibility"]},
          "direction": {"type": "string", "enum": ["strengthening", "weakening", "mixed", "unclear"]},
          "rationale": {"type": "string", "minLength": 1},
          "confidence": {"type": "number", "minimum": 0, "maximum": 1},
          "supporting_citation_ids": {"type": "array", "minItems": 1, "items": {"type": "string", "minLength": 1, "pattern": "^\\S(?:.*\\S)?$"}},
          "contradicting_citation_ids": {"type": "array", "items": {"type": "string", "minLength": 1, "pattern": "^\\S(?:.*\\S)?$"}}
        }
      }
    }
  }
}`)

var analysisJSONSchema = []byte(`{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "required": ["conclusion", "rationale", "supporting_signal_ids", "contradicting_signal_ids", "gaps"],
  "properties": {
    "conclusion": {"type": "string", "enum": ["insufficient evidence", "no material directional change detected", "mixed or conflicting signals", "possible declining-confidence signal"]},
    "rationale": {"type": "string", "minLength": 1},
    "supporting_signal_ids": {"type": "array", "items": {"type": "string", "minLength": 1, "pattern": "^\\S(?:.*\\S)?$"}},
    "contradicting_signal_ids": {"type": "array", "items": {"type": "string", "minLength": 1, "pattern": "^\\S(?:.*\\S)?$"}},
    "gaps": {"type": "array", "items": {"type": "string"}}
  }
}`)
