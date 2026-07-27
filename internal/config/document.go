package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

const (
	configurationYAMLExtension  = ".yaml"
	configurationYMLExtension   = ".yml"
	configurationJSONExtension  = ".json"
	configurationYAMLFormat     = "yaml"
	configurationJSONFormat     = "json"
	configurationRootName       = "configuration root"
	configurationExpectedObject = "object"
)

type configDocument struct {
	Format string
	Data   []byte
}

type configSchemaKind uint8

const (
	configSchemaObject configSchemaKind = iota
	configSchemaString
	configSchemaInteger
	configSchemaNumber
	configSchemaBoolean
	configSchemaDurationString
	configSchemaStringArray
)

type configSchemaNode struct {
	kind     configSchemaKind
	children map[string]configSchemaNode
}

var configurationSchema = configObjectSchema(map[string]configSchemaNode{
	"http": configObjectSchema(map[string]configSchemaNode{
		"host":                        configLeafSchema(configSchemaString),
		"port":                        configLeafSchema(configSchemaInteger),
		"read_header_timeout_seconds": configLeafSchema(configSchemaInteger),
	}),
	"log": configObjectSchema(map[string]configSchemaNode{
		"level": configLeafSchema(configSchemaString),
	}),
	"telemetry": configObjectSchema(map[string]configSchemaNode{
		"enabled":                configLeafSchema(configSchemaBoolean),
		"endpoint":               configLeafSchema(configSchemaString),
		"insecure":               configLeafSchema(configSchemaBoolean),
		"metric_export_interval": configLeafSchema(configSchemaDurationString),
		"service_name":           configLeafSchema(configSchemaString),
		"trace_sample_ratio":     configLeafSchema(configSchemaNumber),
	}),
	"database": configObjectSchema(map[string]configSchemaNode{
		"scopes":           configLeafSchema(configSchemaStringArray),
		"application_role": configLeafSchema(configSchemaString),
	}),
	"google": configObjectSchema(map[string]configSchemaNode{
		"folder_id":         configLeafSchema(configSchemaString),
		"oauth_client_file": configLeafSchema(configSchemaString),
		"oauth_token_file":  configLeafSchema(configSchemaString),
		"transcript_titles": configLeafSchema(configSchemaStringArray),
		"notes_titles":      configLeafSchema(configSchemaStringArray),
	}),
	"directory": configObjectSchema(map[string]configSchemaNode{
		"enabled":           configLeafSchema(configSchemaBoolean),
		"oauth_client_file": configLeafSchema(configSchemaString),
		"oauth_token_file":  configLeafSchema(configSchemaString),
		"email_domains":     configLeafSchema(configSchemaStringArray),
		"freshness":         configLeafSchema(configSchemaDurationString),
		"retry_after":       configLeafSchema(configSchemaDurationString),
		"max_attempts":      configLeafSchema(configSchemaInteger),
	}),
	"model": configObjectSchema(map[string]configSchemaNode{
		"data_mode":         configLeafSchema(configSchemaString),
		"provider":          configLeafSchema(configSchemaString),
		"id":                configLeafSchema(configSchemaString),
		"max_output_tokens": configLeafSchema(configSchemaInteger),
		"max_attempts":      configLeafSchema(configSchemaInteger),
		"aws_profile":       configLeafSchema(configSchemaString),
		"aws_region":        configLeafSchema(configSchemaString),
	}),
	"ingestion": configObjectSchema(map[string]configSchemaNode{
		"lease_duration":  configLeafSchema(configSchemaDurationString),
		"attempt_timeout": configLeafSchema(configSchemaDurationString),
	}),
	"extraction": configObjectSchema(map[string]configSchemaNode{
		"prompt_version": configLeafSchema(configSchemaString),
	}),
	"query": configObjectSchema(map[string]configSchemaNode{
		"max_entities":   configLeafSchema(configSchemaInteger),
		"max_predicates": configLeafSchema(configSchemaInteger),
		"max_chronology": configLeafSchema(configSchemaInteger),
	}),
})

func configObjectSchema(children map[string]configSchemaNode) configSchemaNode {
	return configSchemaNode{kind: configSchemaObject, children: children}
}

func configLeafSchema(kind configSchemaKind) configSchemaNode {
	return configSchemaNode{kind: kind}
}

func loadConfigDocument(path *string) (configDocument, error) {
	if path == nil {
		return configDocument{}, nil
	}
	if strings.TrimSpace(*path) == "" {
		return configDocument{}, errors.New("--config requires a nonblank path")
	}
	format, err := configFormat(filepath.Ext(*path))
	if err != nil {
		return configDocument{}, err
	}
	data, err := os.ReadFile(*path)
	if err != nil {
		return configDocument{}, fmt.Errorf("read configuration file: %w", err)
	}
	if err := validateConfigDocument(format, data); err != nil {
		return configDocument{}, fmt.Errorf("validate configuration file: %w", err)
	}
	return configDocument{Format: format, Data: data}, nil
}

func configFormat(extension string) (string, error) {
	switch strings.ToLower(extension) {
	case configurationYAMLExtension, configurationYMLExtension:
		return configurationYAMLFormat, nil
	case configurationJSONExtension:
		return configurationJSONFormat, nil
	default:
		return "", fmt.Errorf("unsupported configuration file extension %q", extension)
	}
}

func validateConfigDocument(format string, data []byte) error {
	switch format {
	case configurationYAMLFormat:
		return validateYAMLConfigDocument(data)
	case configurationJSONFormat:
		return validateJSONConfigDocument(data)
	default:
		return fmt.Errorf("unsupported configuration document format %q", format)
	}
}

func validateYAMLConfigDocument(data []byte) error {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		if errors.Is(err, io.EOF) {
			return configTypeError(configurationRootName, configurationExpectedObject)
		}
		return errors.New("parse YAML configuration document")
	}
	var extraDocument yaml.Node
	if err := decoder.Decode(&extraDocument); err != nil && !errors.Is(err, io.EOF) {
		return errors.New("parse YAML configuration document")
	} else if err == nil {
		return errors.New("YAML configuration must contain one document")
	}
	if document.Kind != yaml.DocumentNode || len(document.Content) != 1 {
		return configTypeError(configurationRootName, configurationExpectedObject)
	}
	return validateYAMLNode(document.Content[0], configurationSchema, "")
}

func validateYAMLNode(node *yaml.Node, schema configSchemaNode, path string) error {
	if node.Alias != nil || node.Kind == yaml.AliasNode {
		return configFeatureError(configKeyName(path), "aliases are not allowed")
	}
	if node.Anchor != "" {
		return configFeatureError(configKeyName(path), "anchors are not allowed")
	}
	if node.Tag == "!!null" {
		return configTypeError(configKeyName(path), configExpectedType(schema.kind))
	}

	switch schema.kind {
	case configSchemaObject:
		if node.Kind != yaml.MappingNode {
			return configTypeError(configKeyName(path), configurationExpectedObject)
		}
		return validateYAMLObject(node, schema, path)
	case configSchemaString, configSchemaDurationString:
		if node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
			return configTypeError(configKeyName(path), configExpectedType(schema.kind))
		}
		return nil
	case configSchemaInteger:
		if node.Kind != yaml.ScalarNode || node.Tag != "!!int" {
			return configTypeError(configKeyName(path), configExpectedType(schema.kind))
		}
		if _, err := strconv.ParseInt(node.Value, 0, 0); err != nil {
			return configTypeError(configKeyName(path), configExpectedType(schema.kind))
		}
		return nil
	case configSchemaNumber:
		if node.Kind != yaml.ScalarNode || (node.Tag != "!!int" && node.Tag != "!!float") {
			return configTypeError(configKeyName(path), configExpectedType(schema.kind))
		}
		if !finiteNumber(node.Value) {
			return configTypeError(configKeyName(path), configExpectedType(schema.kind))
		}
		return nil
	case configSchemaBoolean:
		if node.Kind != yaml.ScalarNode || node.Tag != "!!bool" {
			return configTypeError(configKeyName(path), configExpectedType(schema.kind))
		}
		return nil
	case configSchemaStringArray:
		if node.Kind != yaml.SequenceNode {
			return configTypeError(configKeyName(path), configExpectedType(schema.kind))
		}
		for _, item := range node.Content {
			if item.Alias != nil || item.Kind == yaml.AliasNode || item.Anchor != "" || item.Tag != "!!str" || item.Kind != yaml.ScalarNode {
				return configTypeError(configKeyName(path), configExpectedType(schema.kind))
			}
		}
		return nil
	default:
		return errors.New("invalid configuration schema")
	}
}

func validateYAMLObject(node *yaml.Node, schema configSchemaNode, path string) error {
	seenKeys := make(map[string]struct{}, len(node.Content)/2)
	for index := 0; index < len(node.Content); index += 2 {
		keyNode := node.Content[index]
		valueNode := node.Content[index+1]
		if keyNode.Alias != nil || keyNode.Kind == yaml.AliasNode || keyNode.Anchor != "" || keyNode.Kind != yaml.ScalarNode {
			return configFeatureError(configKeyName(path), "mapping keys must be plain strings")
		}
		key := keyNode.Value
		keyPath := configChildPath(path, key)
		if key == "<<" || keyNode.Tag == "!!merge" {
			return configFeatureError(keyPath, "merge keys are not allowed")
		}
		if keyNode.Tag != "!!str" {
			return configFeatureError(configKeyName(path), "mapping keys must be plain strings")
		}
		if _, exists := seenKeys[key]; exists {
			return configDuplicateKeyError(keyPath)
		}
		seenKeys[key] = struct{}{}
		childSchema, exists := schema.children[key]
		if !exists {
			return configUnknownKeyError(keyPath)
		}
		if err := validateYAMLNode(valueNode, childSchema, keyPath); err != nil {
			return err
		}
	}
	return nil
}

type configJSONValueKind uint8

const (
	configJSONNull configJSONValueKind = iota
	configJSONString
	configJSONNumber
	configJSONBoolean
	configJSONObject
	configJSONArray
)

type configJSONValue struct {
	kind       configJSONValueKind
	number     json.Number
	object     map[string]configJSONValue
	objectKeys []string
	array      []configJSONValue
}

func validateJSONConfigDocument(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	value, err := decodeJSONConfigValue(decoder, "")
	if err != nil {
		return err
	}
	if _, err := decoder.Token(); err != nil {
		if !errors.Is(err, io.EOF) {
			return errors.New("parse JSON configuration document")
		}
	} else {
		return errors.New("JSON configuration must contain one value")
	}
	return validateJSONConfigValue(value, configurationSchema, "")
}

func decodeJSONConfigValue(decoder *json.Decoder, path string) (configJSONValue, error) {
	token, err := decoder.Token()
	if err != nil {
		return configJSONValue{}, errors.New("parse JSON configuration document")
	}
	switch value := token.(type) {
	case nil:
		return configJSONValue{kind: configJSONNull}, nil
	case string:
		return configJSONValue{kind: configJSONString}, nil
	case bool:
		return configJSONValue{kind: configJSONBoolean}, nil
	case json.Number:
		return configJSONValue{kind: configJSONNumber, number: value}, nil
	case json.Delim:
		switch value {
		case '{':
			return decodeJSONObject(decoder, path)
		case '[':
			return decodeJSONArray(decoder, path)
		default:
			return configJSONValue{}, errors.New("parse JSON configuration document")
		}
	default:
		return configJSONValue{}, errors.New("parse JSON configuration document")
	}
}

func decodeJSONObject(decoder *json.Decoder, path string) (configJSONValue, error) {
	object := make(map[string]configJSONValue)
	objectKeys := make([]string, 0)
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return configJSONValue{}, errors.New("parse JSON configuration document")
		}
		key, ok := keyToken.(string)
		if !ok {
			return configJSONValue{}, errors.New("parse JSON configuration document")
		}
		keyPath := configChildPath(path, key)
		if _, exists := object[key]; exists {
			return configJSONValue{}, configDuplicateKeyError(keyPath)
		}
		value, err := decodeJSONConfigValue(decoder, keyPath)
		if err != nil {
			return configJSONValue{}, err
		}
		object[key] = value
		objectKeys = append(objectKeys, key)
	}
	endToken, err := decoder.Token()
	if err != nil {
		return configJSONValue{}, errors.New("parse JSON configuration document")
	}
	if end, ok := endToken.(json.Delim); !ok || end != '}' {
		return configJSONValue{}, errors.New("parse JSON configuration document")
	}
	return configJSONValue{kind: configJSONObject, object: object, objectKeys: objectKeys}, nil
}

func decodeJSONArray(decoder *json.Decoder, path string) (configJSONValue, error) {
	array := make([]configJSONValue, 0)
	for decoder.More() {
		value, err := decodeJSONConfigValue(decoder, path)
		if err != nil {
			return configJSONValue{}, err
		}
		array = append(array, value)
	}
	endToken, err := decoder.Token()
	if err != nil {
		return configJSONValue{}, errors.New("parse JSON configuration document")
	}
	if end, ok := endToken.(json.Delim); !ok || end != ']' {
		return configJSONValue{}, errors.New("parse JSON configuration document")
	}
	return configJSONValue{kind: configJSONArray, array: array}, nil
}

func validateJSONConfigValue(value configJSONValue, schema configSchemaNode, path string) error {
	if value.kind == configJSONNull {
		return configTypeError(configKeyName(path), configExpectedType(schema.kind))
	}

	switch schema.kind {
	case configSchemaObject:
		if value.kind != configJSONObject {
			return configTypeError(configKeyName(path), configurationExpectedObject)
		}
		for _, key := range value.objectKeys {
			childValue := value.object[key]
			childSchema, exists := schema.children[key]
			keyPath := configChildPath(path, key)
			if !exists {
				return configUnknownKeyError(keyPath)
			}
			if err := validateJSONConfigValue(childValue, childSchema, keyPath); err != nil {
				return err
			}
		}
		return nil
	case configSchemaString, configSchemaDurationString:
		if value.kind != configJSONString {
			return configTypeError(configKeyName(path), configExpectedType(schema.kind))
		}
		return nil
	case configSchemaInteger:
		if value.kind != configJSONNumber {
			return configTypeError(configKeyName(path), configExpectedType(schema.kind))
		}
		if _, err := strconv.ParseInt(value.number.String(), 10, 0); err != nil {
			return configTypeError(configKeyName(path), configExpectedType(schema.kind))
		}
		return nil
	case configSchemaNumber:
		if value.kind != configJSONNumber || !finiteNumber(value.number.String()) {
			return configTypeError(configKeyName(path), configExpectedType(schema.kind))
		}
		return nil
	case configSchemaBoolean:
		if value.kind != configJSONBoolean {
			return configTypeError(configKeyName(path), configExpectedType(schema.kind))
		}
		return nil
	case configSchemaStringArray:
		if value.kind != configJSONArray {
			return configTypeError(configKeyName(path), configExpectedType(schema.kind))
		}
		for _, item := range value.array {
			if item.kind != configJSONString {
				return configTypeError(configKeyName(path), configExpectedType(schema.kind))
			}
		}
		return nil
	default:
		return errors.New("invalid configuration schema")
	}
}

func finiteNumber(value string) bool {
	number, err := strconv.ParseFloat(value, 64)
	return err == nil && !math.IsInf(number, 0) && !math.IsNaN(number)
}

func configExpectedType(kind configSchemaKind) string {
	switch kind {
	case configSchemaObject:
		return configurationExpectedObject
	case configSchemaString:
		return "string"
	case configSchemaInteger:
		return "integer"
	case configSchemaNumber:
		return "number"
	case configSchemaBoolean:
		return "boolean"
	case configSchemaDurationString:
		return "duration string"
	case configSchemaStringArray:
		return "array of strings"
	default:
		return "valid configuration value"
	}
}

func configChildPath(parent, key string) string {
	if parent == "" {
		return key
	}
	return parent + "." + key
}

func configKeyName(path string) string {
	if path == "" {
		return configurationRootName
	}
	return path
}

func configTypeError(path, expected string) error {
	return fmt.Errorf("configuration key %q must be a %s", path, expected)
}

func configUnknownKeyError(path string) error {
	return fmt.Errorf("unknown configuration key %q", path)
}

func configDuplicateKeyError(path string) error {
	return fmt.Errorf("duplicate configuration key %q", path)
}

func configFeatureError(path, feature string) error {
	return fmt.Errorf("configuration key %q: %s", path, feature)
}
