package extract

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestSchemasCloseEveryObjectAndRequireCoreFields(t *testing.T) {
	for name, schema := range map[string][]byte{
		"extraction": ExtractionJSONSchema(),
		"analysis":   AnalysisJSONSchema(),
	} {
		t.Run(name, func(t *testing.T) {
			var document any
			if err := json.Unmarshal(schema, &document); err != nil {
				t.Fatalf("schema is invalid JSON: %v", err)
			}
			assertClosedObjects(t, document)
		})
	}
}

func TestPromptVersionsAreEmbeddedAndExplicit(t *testing.T) {
	for _, test := range []struct {
		version string
		want    string
	}{
		{version: ExtractionPromptVersion, want: "UTF-8 byte offsets"},
		{version: AnalysisPromptVersion, want: "private mental state"},
	} {
		prompt, err := Prompt(test.version)
		if err != nil {
			t.Fatalf("Prompt(%q) error = %v", test.version, err)
		}
		if !strings.Contains(prompt, test.want) {
			t.Fatalf("Prompt(%q) does not contain %q", test.version, test.want)
		}
	}
	if _, err := Prompt("unknown-v1"); err == nil {
		t.Fatal("Prompt(unknown) error = nil")
	}
}

func TestPromptContractReturnsIsolatedReviewedPairings(t *testing.T) {
	tests := []struct {
		version    string
		schemaName string
	}{
		{version: ExtractionPromptVersion, schemaName: ExtractionSchemaName},
		{version: AnalysisPromptVersion, schemaName: AnalysisSchemaName},
	}
	for _, test := range tests {
		first, err := PromptContract(test.version)
		if err != nil {
			t.Fatalf("PromptContract(%q) error = %v", test.version, err)
		}
		if first.Version != test.version || first.SystemPrompt == "" || first.SchemaName != test.schemaName || len(first.JSONSchema) == 0 {
			t.Fatalf("PromptContract(%q) = %+v", test.version, first)
		}
		first.SystemPrompt = "mutated"
		first.JSONSchema[0] = 'x'

		second, err := PromptContract(test.version)
		if err != nil {
			t.Fatalf("second PromptContract(%q) error = %v", test.version, err)
		}
		if second.SystemPrompt == "mutated" || second.JSONSchema[0] == 'x' {
			t.Fatalf("PromptContract(%q) returned mutable shared state", test.version)
		}
	}
	if _, err := PromptContract("unknown-v1"); err == nil {
		t.Fatal("PromptContract(unknown) error = nil")
	}
}

func TestDecodeAndValidateExtractionRejectsUnknownProperties(t *testing.T) {
	raw := []byte(`{"meeting_date":"","citations":[],"people":[],"statements":[],"signals":[],"private_payload":"must not pass"}`)
	_, err := DecodeAndValidateExtraction(validSubmittedText(), raw)
	if err == nil || !strings.Contains(err.Error(), "schema") || strings.Contains(err.Error(), "private_payload") {
		t.Fatalf("DecodeAndValidateExtraction() error = %v, want private-safe schema error", err)
	}
}

func TestDecodeAndValidateExtractionRejectsTrailingJSON(t *testing.T) {
	raw := []byte(`{"meeting_date":"","citations":[],"people":[],"statements":[],"signals":[]} {}`)
	if _, err := DecodeAndValidateExtraction(validSubmittedText(), raw); err == nil {
		t.Fatal("DecodeAndValidateExtraction() error = nil, want trailing JSON rejection")
	}
}

func TestDecodeAndValidateExtractionRejectsMissingRequiredFields(t *testing.T) {
	raw := []byte(`{
		"meeting_date":"2026-07-21",
		"citations":[{"id":"citation-1","tab_id":"transcript-tab","start_offset":0,"end_offset":18,"quote":"speaker-a assigned"}],
		"people":[{"id":"mention-a","surface":"speaker-a","role":"speaker","email":"","citation_ids":["citation-1"]}],
		"statements":[{"id":"statement-1","speaker_mention_id":"mention-a","subject_mention_id":"mention-a","predicate":"assigned","object_text":"follow-up","valid_date":"2026-07-21","citation_ids":["citation-1"]}],
		"signals":[{"id":"signal-1","subject_mention_id":"mention-a","object_mention_id":"mention-a","statement_ids":["statement-1"],"category":"future_responsibility","direction":"strengthening","rationale":"explicit assignment","confidence":0.8,"supporting_citation_ids":["citation-1"]}]
	}`)
	if _, err := DecodeAndValidateExtraction(validSubmittedText(), raw); err == nil || !strings.Contains(err.Error(), "schema") {
		t.Fatalf("DecodeAndValidateExtraction() error = %v, want missing required field rejection", err)
	}
}

func assertClosedObjects(t *testing.T, value any) {
	t.Helper()
	object, ok := value.(map[string]any)
	if ok {
		if object["type"] == "object" {
			closed, present := object["additionalProperties"]
			if !present || closed != false {
				encoded, _ := json.Marshal(object)
				t.Fatalf("object schema is not closed: %s", bytes.TrimSpace(encoded))
			}
			if _, present := object["required"]; !present {
				t.Fatal("object schema has no required fields")
			}
		}
	}
	for _, child := range children(value) {
		assertClosedObjects(t, child)
	}
}

func children(value any) []any {
	switch value := value.(type) {
	case map[string]any:
		children := make([]any, 0, len(value))
		for _, child := range value {
			children = append(children, child)
		}
		return children
	case []any:
		return value
	default:
		return nil
	}
}
