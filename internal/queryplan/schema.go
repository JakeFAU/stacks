package queryplan

import _ "embed"

//go:embed prompts/query-plan-v2.txt
var queryPlanPrompt string

var queryPlanSchema = []byte(`{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "required": [
    "status",
    "reason",
    "intent",
    "entity_match",
    "predicates",
    "selections",
    "knowledge_scope",
    "chronology_limit"
  ],
  "properties": {
    "status": {"type": "string", "enum": ["executable", "cannot-plan"]},
    "reason": {
      "type": "string",
      "enum": [
        "none",
        "ambiguous-question",
        "unsupported-question",
        "insufficient-temporal-detail"
      ]
    },
    "intent": {
      "type": "string",
      "enum": ["", "point-in-time", "trend-comparison", "trajectory", "causal-chain"]
    },
    "entity_match": {"type": "string", "enum": ["", "all", "any"]},
    "predicates": {
      "type": "array",
      "maxItems": 256,
      "items": {"type": "string", "minLength": 1}
    },
    "selections": {
      "type": "array",
      "maxItems": 2,
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["kind", "label", "at", "start", "end"],
        "properties": {
          "kind": {"type": "string", "enum": ["point", "window"]},
          "label": {"type": "string", "enum": ["point", "before", "after", "between"]},
          "at": {"type": "string"},
          "start": {"type": "string"},
          "end": {"type": "string"}
        }
      }
    },
    "knowledge_scope": {
      "type": "object",
      "additionalProperties": false,
      "required": ["kind", "as_of"],
      "properties": {
        "kind": {"type": "string", "enum": ["", "current", "as-of"]},
        "as_of": {"type": "string"}
      }
    },
    "chronology_limit": {"type": "integer", "minimum": 0, "maximum": 10000}
  }
}
`)
