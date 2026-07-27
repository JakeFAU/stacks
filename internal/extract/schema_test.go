package extract

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestExtractV2SchemaAndPromptRemainByteStable(t *testing.T) {
	contract, err := PromptContract(ExtractionPromptVersion)
	if err != nil {
		t.Fatalf("PromptContract(%q) error = %v", ExtractionPromptVersion, err)
	}
	const (
		wantPromptDigest = "88e19f093fb72f5b948caafd8bdc57d52e95273c5e84e465445157d05c84aedc"
		wantSchemaDigest = "26d5a016ac6d529b51685da2927d5fe1368fbd0dd270f05ed14a9707a427b6ea"
	)
	if got := fmt.Sprintf("%x", sha256.Sum256([]byte(contract.SystemPrompt))); got != wantPromptDigest {
		t.Fatalf("extract-v2 prompt SHA-256 = %s, want %s", got, wantPromptDigest)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(contract.JSONSchema)); got != wantSchemaDigest {
		t.Fatalf("extract-v2 schema SHA-256 = %s, want %s", got, wantSchemaDigest)
	}
}

func TestPromptContractSupportsExtractionOnly(t *testing.T) {
	contract, err := PromptContract(ExtractionPromptVersion)
	if err != nil {
		t.Fatalf("PromptContract(%q) error = %v", ExtractionPromptVersion, err)
	}
	if contract.Version != ExtractionPromptVersion ||
		contract.SchemaName != ExtractionSchemaName ||
		contract.SystemPrompt == "" ||
		len(contract.JSONSchema) == 0 {
		t.Fatalf("PromptContract(%q) = %+v, want complete extraction contract", ExtractionPromptVersion, contract)
	}
	retiredPromptVersion := strings.ReplaceAll("analyXze-v1", "X", "")
	if _, err := PromptContract(retiredPromptVersion); err == nil {
		t.Fatalf("PromptContract(%q) error = nil, want retired prompt rejection", retiredPromptVersion)
	}
}

func TestSchemasCloseEveryObjectAndRequireCoreFields(t *testing.T) {
	for name, schema := range map[string][]byte{
		"extraction": ExtractionJSONSchema(),
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

func TestSchemasRejectWhitespacePaddedModelLocalIdentifiers(t *testing.T) {
	for name, testCase := range map[string]struct {
		schema []byte
		count  int
	}{
		"extraction": {schema: ExtractionJSONSchema(), count: 14},
	} {
		t.Run(name, func(t *testing.T) {
			var document any
			if err := json.Unmarshal(testCase.schema, &document); err != nil {
				t.Fatalf("schema is invalid JSON: %v", err)
			}
			if count := assertLocalIDPatterns(t, document); count != testCase.count {
				t.Fatalf("local identifier field count = %d, want %d", count, testCase.count)
			}
		})
	}
}

func TestPromptVersionsAreEmbeddedAndExplicit(t *testing.T) {
	for _, test := range []struct {
		version string
		want    string
	}{
		{version: ExtractionPromptVersion, want: "UTF-8 byte offsets"},
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

func TestExtractionContractAdvancesPastSupersededIdentityAssociationSemantics(t *testing.T) {
	if ExtractionPromptVersion != "extract-v2" {
		t.Fatalf("ExtractionPromptVersion = %q, want cache-invalidating extract-v2", ExtractionPromptVersion)
	}
	if ExtractionSchemaName != "meeting_extraction_v2" {
		t.Fatalf("ExtractionSchemaName = %q, want versioned schema identity", ExtractionSchemaName)
	}
	contract, err := PromptContract(ExtractionPromptVersion)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(contract.JSONSchema, []byte(`"$id": "https://stacks.invalid/schemas/meeting-extraction-v2"`)) {
		t.Fatal("extraction schema does not carry its v2 identity")
	}
	if !strings.Contains(contract.SystemPrompt, "email is only an untrusted proposal") {
		t.Fatal("extraction prompt does not state the model-email trust boundary")
	}
}

func TestPromptContractReturnsIsolatedReviewedPairing(t *testing.T) {
	first, err := PromptContract(ExtractionPromptVersion)
	if err != nil {
		t.Fatalf("PromptContract(%q) error = %v", ExtractionPromptVersion, err)
	}
	if first.Version != ExtractionPromptVersion || first.SystemPrompt == "" ||
		first.SchemaName != ExtractionSchemaName || len(first.JSONSchema) == 0 {
		t.Fatalf("PromptContract(%q) = %+v", ExtractionPromptVersion, first)
	}
	first.SystemPrompt = "mutated"
	first.JSONSchema[0] = 'x'

	second, err := PromptContract(ExtractionPromptVersion)
	if err != nil {
		t.Fatalf("second PromptContract(%q) error = %v", ExtractionPromptVersion, err)
	}
	if second.SystemPrompt == "mutated" || second.JSONSchema[0] == 'x' {
		t.Fatalf("PromptContract(%q) returned mutable shared state", ExtractionPromptVersion)
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

func assertLocalIDPatterns(t *testing.T, value any) int {
	t.Helper()
	object, ok := value.(map[string]any)
	if !ok {
		return 0
	}
	count := 0
	if properties, ok := object["properties"].(map[string]any); ok {
		for name, rawProperty := range properties {
			property, ok := rawProperty.(map[string]any)
			if !ok {
				continue
			}
			var identifierSchema map[string]any
			switch {
			case name == "id" || strings.HasSuffix(name, "_id"):
				identifierSchema = property
			case strings.HasSuffix(name, "_ids"):
				identifierSchema, _ = property["items"].(map[string]any)
			}
			if identifierSchema != nil {
				count++
				if identifierSchema["pattern"] != `^\S(?:.*\S)?$` {
					t.Errorf("property %q does not reject padded identifiers", name)
				}
			}
		}
	}
	for _, child := range object {
		switch typed := child.(type) {
		case map[string]any:
			count += assertLocalIDPatterns(t, typed)
		case []any:
			for _, item := range typed {
				count += assertLocalIDPatterns(t, item)
			}
		}
	}
	return count
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
