package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestLoadConfigDocumentNilPathReturnsZeroDocument(t *testing.T) {
	document, err := loadConfigDocument(nil)
	if err != nil {
		t.Fatalf("loadConfigDocument(nil) error = %v", err)
	}
	if document.Format != "" || document.Data != nil {
		t.Fatalf("loadConfigDocument(nil) = %#v, want zero document", document)
	}
}

func TestLoadConfigDocumentSelectsExplicitFormats(t *testing.T) {
	for _, extension := range []string{".yaml", ".YML", ".json"} {
		t.Run(extension, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "stacks"+extension)
			if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
				t.Fatal(err)
			}

			document, err := loadConfigDocument(&path)
			if err != nil {
				t.Fatalf("loadConfigDocument() error = %v", err)
			}
			if len(document.Data) == 0 {
				t.Fatal("loadConfigDocument() returned no bytes")
			}
			wantFormat := "yaml"
			if strings.EqualFold(extension, ".json") {
				wantFormat = "json"
			}
			if document.Format != wantFormat {
				t.Fatalf("format = %q, want %q", document.Format, wantFormat)
			}
		})
	}
}

func TestLoadConfigDocumentRejectsExplicitBlankAndUnsupportedPaths(t *testing.T) {
	for _, value := range []string{"", "   ", "stacks.toml", "stacks"} {
		t.Run(strconv.Quote(value), func(t *testing.T) {
			_, err := loadConfigDocument(&value)
			if err == nil {
				t.Fatal("loadConfigDocument() error = nil")
			}
		})
	}
}

func TestLoadConfigDocumentRejectsMissingDirectoryAndUnreadablePaths(t *testing.T) {
	privateContent := "private-config-content"
	missingPath := filepath.Join(t.TempDir(), "missing.yaml")
	directoryPath := filepath.Join(t.TempDir(), "directory.yaml")
	if err := os.Mkdir(directoryPath, 0o700); err != nil {
		t.Fatal(err)
	}
	unreadablePath := filepath.Join(t.TempDir(), "unreadable.yaml")
	if err := os.WriteFile(unreadablePath, []byte(privateContent), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(unreadablePath, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(unreadablePath, 0o600); err != nil {
			t.Errorf("restore unreadable fixture mode: %v", err)
		}
	})

	for _, testCase := range []struct {
		name string
		path string
	}{
		{name: "missing", path: missingPath},
		{name: "directory", path: directoryPath},
		{name: "unreadable", path: unreadablePath},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := loadConfigDocument(&testCase.path)
			if err == nil {
				t.Fatal("loadConfigDocument() error = nil")
			}
			if !strings.Contains(err.Error(), "read configuration file") {
				t.Fatalf("loadConfigDocument() error = %v, want bounded read operation", err)
			}
			if strings.Contains(err.Error(), privateContent) {
				t.Fatalf("loadConfigDocument() error exposed file contents: %v", err)
			}
		})
	}
}

func TestValidateConfigDocumentAcceptsEverySchemaShape(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		format string
		body   string
	}{
		{
			name:   "YAML",
			format: "yaml",
			body: `http:
  host: 127.0.0.1
  port: 8080
  read_header_timeout_seconds: 5
log:
  level: info
telemetry:
  enabled: true
  endpoint: collector:4317
  insecure: false
  metric_export_interval: 10s
  service_name: stacks
  trace_sample_ratio: 0.5
database:
  scopes: [core, directory]
  application_role: stacks_app
google:
  folder_id: synthetic-folder
  oauth_client_file: /synthetic/client.json
  oauth_token_file: /synthetic/token.json
  transcript_titles: [Transcripts]
  notes_titles: [Notes]
directory:
  enabled: false
  oauth_client_file: /synthetic/directory-client.json
  oauth_token_file: /synthetic/directory-token.json
  email_domains: [example.test]
  freshness: 24h
  retry_after: 15m
  max_attempts: 3
model:
  data_mode: remote
  provider: openai
  id: synthetic-model
  max_output_tokens: 256
  max_attempts: 2
  aws_profile: synthetic
  aws_region: us-east-1
ingestion:
  lease_duration: 5m
  attempt_timeout: 4m
extraction:
  prompt_version: v1
analysis:
  prompt_version: v1
`,
		},
		{
			name:   "JSON",
			format: "json",
			body:   `{"http":{"host":"127.0.0.1","port":8080,"read_header_timeout_seconds":5},"log":{"level":"info"},"telemetry":{"enabled":true,"endpoint":"collector:4317","insecure":false,"metric_export_interval":"10s","service_name":"stacks","trace_sample_ratio":0.5},"database":{"scopes":["core","directory"],"application_role":"stacks_app"},"google":{"folder_id":"synthetic-folder","oauth_client_file":"/synthetic/client.json","oauth_token_file":"/synthetic/token.json","transcript_titles":["Transcripts"],"notes_titles":["Notes"]},"directory":{"enabled":false,"oauth_client_file":"/synthetic/directory-client.json","oauth_token_file":"/synthetic/directory-token.json","email_domains":["example.test"],"freshness":"24h","retry_after":"15m","max_attempts":3},"model":{"data_mode":"remote","provider":"openai","id":"synthetic-model","max_output_tokens":256,"max_attempts":2,"aws_profile":"synthetic","aws_region":"us-east-1"},"ingestion":{"lease_duration":"5m","attempt_timeout":"4m"},"extraction":{"prompt_version":"v1"},"analysis":{"prompt_version":"v1"}}`,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if err := validateConfigDocument(testCase.format, []byte(testCase.body)); err != nil {
				t.Fatalf("validateConfigDocument() error = %v", err)
			}
		})
	}
}

func TestValidateConfigDocumentRejectsUnknownDuplicateNullAndWrongTypes(t *testing.T) {
	tests := []struct {
		name   string
		format string
		body   string
	}{
		{"non-object YAML root", "yaml", "true\n"},
		{"non-object JSON root", "json", `true`},
		{"unknown YAML root key", "yaml", "mystery: true\n"},
		{"unknown YAML nested key", "yaml", "http:\n  mystery: true\n"},
		{"unknown JSON root key", "json", `{"mystery":true}`},
		{"unknown JSON nested key", "json", `{"http":{"mystery":true}}`},
		{"duplicate YAML root key", "yaml", "http: {}\nhttp: {}\n"},
		{"duplicate YAML nested key", "yaml", "http:\n  port: 8080\n  port: 9090\n"},
		{"duplicate JSON root key", "json", `{"http":{},"http":{}}`},
		{"duplicate JSON nested key", "json", `{"http":{"port":8080,"port":9090}}`},
		{"null YAML value", "yaml", "http:\n  host: null\n"},
		{"null JSON value", "json", `{"http":{"host":null}}`},
		{"wrong YAML integer", "yaml", "http:\n  port: \"8080\"\n"},
		{"wrong YAML number", "yaml", "telemetry:\n  trace_sample_ratio: \"0.5\"\n"},
		{"wrong YAML boolean", "yaml", "telemetry:\n  enabled: \"true\"\n"},
		{"wrong YAML object", "yaml", "http: 8080\n"},
		{"wrong YAML list", "yaml", "google:\n  notes_titles: Notes\n"},
		{"wrong JSON integer", "json", `{"http":{"port":"8080"}}`},
		{"wrong JSON number", "json", `{"telemetry":{"trace_sample_ratio":"0.5"}}`},
		{"wrong JSON boolean", "json", `{"telemetry":{"enabled":"true"}}`},
		{"wrong JSON object", "json", `{"http":8080}`},
		{"wrong JSON list", "json", `{"google":{"notes_titles":"Notes"}}`},
		{"wrong YAML list member", "yaml", "google:\n  notes_titles: [Notes, 7]\n"},
		{"wrong JSON list member", "json", `{"google":{"notes_titles":["Notes",7]}}`},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			err := validateConfigDocument(testCase.format, []byte(testCase.body))
			if err == nil {
				t.Fatal("validateConfigDocument() error = nil")
			}
			if strings.Contains(err.Error(), testCase.body) {
				t.Fatalf("error exposed document body: %v", err)
			}
		})
	}
}

func TestValidateConfigDocumentNamesNestedJSONDuplicateKey(t *testing.T) {
	err := validateConfigDocument("json", []byte(`{"http":{"port":8080,"port":9090}}`))
	if err == nil {
		t.Fatal("validateConfigDocument() error = nil")
	}
	if !strings.Contains(err.Error(), "http.port") {
		t.Fatalf("validateConfigDocument() error = %v, want dotted key http.port", err)
	}
}

func TestValidateConfigDocumentReportsFirstJSONInvalidKeyInDocumentOrder(t *testing.T) {
	const document = `{"http":{"mystery":true},"log":{"mystery":true}}`
	for attempt := 0; attempt < 50; attempt++ {
		err := validateConfigDocument("json", []byte(document))
		if err == nil {
			t.Fatal("validateConfigDocument() error = nil")
		}
		if !strings.Contains(err.Error(), "http.mystery") {
			t.Fatalf("validateConfigDocument() error = %v, want first invalid key http.mystery", err)
		}
	}
}

func TestValidateConfigDocumentRejectsYAMLFeaturesAndAdditionalDocuments(t *testing.T) {
	for _, testCase := range []struct {
		name string
		body string
	}{
		{name: "anchor", body: "http: &http\n  host: 127.0.0.1\n"},
		{name: "alias", body: "http: &http\n  host: 127.0.0.1\nlog: *http\n"},
		{name: "merge key", body: "http:\n  <<: &defaults\n    host: 127.0.0.1\n"},
		{name: "additional document", body: "http: {}\n---\nlog: {}\n"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			err := validateConfigDocument("yaml", []byte(testCase.body))
			if err == nil {
				t.Fatal("validateConfigDocument() error = nil")
			}
			if testCase.name == "merge key" && !strings.Contains(err.Error(), "merge") {
				t.Fatalf("validateConfigDocument() error = %v, want merge-key rejection", err)
			}
		})
	}
}

func TestValidateConfigDocumentRejectsOutOfRangeNumbersAndTrailingJSON(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		format string
		body   string
	}{
		{name: "YAML integer above int", format: "yaml", body: "http:\n  port: 999999999999999999999999999999\n"},
		{name: "JSON integer above int", format: "json", body: `{"http":{"port":999999999999999999999999999999}}`},
		{name: "JSON non-finite number", format: "json", body: `{"telemetry":{"trace_sample_ratio":1e10000}}`},
		{name: "second JSON value", format: "json", body: `{} {}`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if err := validateConfigDocument(testCase.format, []byte(testCase.body)); err == nil {
				t.Fatal("validateConfigDocument() error = nil")
			}
		})
	}
}

func TestValidateConfigDocumentDoesNotExposeValuesUnderRejectedKeys(t *testing.T) {
	const databaseURL = "postgres://private-user:private-password@example.test/private-database"
	const modelAPIKey = "private-openai-api-key"
	body := "database:\n  url: " + databaseURL + "\nmodel:\n  openai_api_key: " + modelAPIKey + "\n"

	err := validateConfigDocument("yaml", []byte(body))
	if err == nil {
		t.Fatal("validateConfigDocument() error = nil")
	}
	if strings.Contains(err.Error(), databaseURL) || strings.Contains(err.Error(), modelAPIKey) {
		t.Fatalf("validateConfigDocument() error exposed synthetic credential-like value: %v", err)
	}
}
