package kinds

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/hollis-labs/libs/security/redact"
)

// ErrSecretValue contains no rejected content. Callers must not log input when
// admission fails; values are refused, never silently scrubbed or persisted.
var ErrSecretValue = errors.New("secret-like value refused; use a reference")

// ValidateNoSecretsJSON checks decoded strings and nested object keys with the
// shared credential detector. It performs no resolution, network or storage IO.
// The five Registry content kinds run this before structural validation, so a
// schema error cannot echo a detected credential. Services may also apply it to
// envelopes and older kinds. This is conservative detection, not proof that an
// arbitrary opaque string contains no secret; upstream admission still matters.
func ValidateNoSecretsJSON(data []byte) error {
	if err := ValidateJSONValue(data); err != nil {
		return fmt.Errorf("invalid JSON value")
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("invalid JSON value")
	}
	return refuseSecrets(value)
}

func refuseSecrets(value any) error {
	slots := map[string]bool{}
	correlationJTIPath := ""
	if root, ok := value.(map[string]any); ok {
		schema, _ := root["schema"].(string)
		prefix := ""
		if _, envelope := root["payload"]; envelope {
			prefix = "/payload"
		}
		// Only a direct correlation payload or its matching Registry Input
		// envelope has this typed identifier slot. An outer schema label must
		// not lend an exception to a different or untyped payload kind.
		if schema == CorrelationSchema {
			if prefix == "" {
				correlationJTIPath = "/metadata/token_jti"
			} else if payload, ok := root["payload"].(map[string]any); ok && payload["schema"] == CorrelationSchema {
				correlationJTIPath = "/payload/metadata/token_jti"
			}
		}
		switch schema {
		case ConfigSchema:
			slots[prefix+"/settings_schema"] = true
		case ToolSchema:
			slots[prefix+"/definition/input_schema"] = true
			slots[prefix+"/definition/output_schema"] = true
		case SkillSchema:
			slots[prefix+"/input_schema"] = true
			slots[prefix+"/output_schema"] = true
		}
	}
	return refuseInstance(value, "", slots, correlationJTIPath)
}

func pointerKey(key string) string {
	return strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1")
}

func refuseInstance(value any, path string, schemaSlots map[string]bool, correlationJTIPath string) error {
	if schemaSlots[path] {
		return refuseSchema(value, false)
	}

	switch v := value.(type) {
	case string:
		if len(redact.Default().Scan(v)) > 0 {
			return ErrSecretValue
		}
	case []any:
		for _, item := range v {
			if err := refuseInstance(item, path+"/[]", schemaSlots, correlationJTIPath); err != nil {
				return err
			}
		}
	case map[string]any:
		for key, item := range v {
			// Sensitive settings must use a typed reference, rather than a literal,
			// placeholder or nested value that could be mistaken for a credential.
			itemPath := path + "/" + pointerKey(key)
			if itemPath == correlationJTIPath {
				// A JTI is an identifier, not token material. Only this exact
				// schema-qualified path admits a nonempty string. Recursion
				// below still scans the actual value for credentials.
				jti, ok := item.(string)
				if !ok || strings.TrimSpace(jti) == "" {
					return ErrSecretValue
				}
			} else if redact.SensitiveName(key) && !secretReference(item) {
				return ErrSecretValue
			}
			if len(redact.Default().Scan(key)) > 0 {
				return ErrSecretValue
			}
			if err := refuseInstance(item, itemPath, schemaSlots, correlationJTIPath); err != nil {
				return err
			}
		}
	}
	return nil
}

// Schema property/definition names declare fields rather than hold instances.
// Only known schema-valued kind slots enter this path; similarly named metadata
// stays instance data. Literal annotations are always checked as instances.
func refuseSchema(value any, sensitive bool) error {
	object, ok := value.(map[string]any)
	if !ok {
		return refuseInstance(value, "", nil, "")
	}
	for key, item := range object {
		if len(redact.Default().Scan(key)) > 0 {
			return ErrSecretValue
		}
		switch key {
		case "properties", "patternProperties", "$defs", "definitions", "dependentSchemas":
			declarations, ok := item.(map[string]any)
			if !ok {
				return ErrSecretValue
			}
			for name, subschema := range declarations {
				if len(redact.Default().Scan(name)) > 0 {
					return ErrSecretValue
				}
				if err := refuseSchema(subschema, redact.SensitiveName(name)); err != nil {
					return err
				}
			}
		case "items", "additionalProperties", "unevaluatedProperties", "unevaluatedItems", "contains", "propertyNames", "not", "if", "then", "else", "contentSchema":
			if err := refuseSchema(item, sensitive); err != nil {
				return err
			}
		case "allOf", "anyOf", "oneOf", "prefixItems":
			members, ok := item.([]any)
			if !ok {
				return ErrSecretValue
			}
			for _, member := range members {
				if err := refuseSchema(member, sensitive); err != nil {
					return err
				}
			}
		case "default", "const":
			if sensitive && !secretReference(item) {
				return ErrSecretValue
			}
			if err := refuseInstance(item, "", nil, ""); err != nil {
				return err
			}
		case "enum", "examples":
			members, ok := item.([]any)
			if !ok {
				return ErrSecretValue
			}
			for _, member := range members {
				if sensitive && !secretReference(member) {
					return ErrSecretValue
				}
				if err := refuseInstance(member, "", nil, ""); err != nil {
					return err
				}
			}
		default:
			// Includes decoded documentation strings and unsupported extension values.
			if redact.SensitiveName(key) && !secretReference(item) {
				return ErrSecretValue
			}
			if err := refuseInstance(item, "", nil, ""); err != nil {
				return err
			}
		}
	}
	return nil
}

func secretReference(value any) bool {
	ref, ok := value.(map[string]any)
	if !ok || (len(ref) != 3 && len(ref) != 4) {
		return false
	}
	if len(ref) == 4 {
		uri, ok := ref["uri"].(string)
		if !ok || uri == "" {
			return false
		}
	}
	authority, a := ref["authority"].(string)
	kind, k := ref["kind"].(string)
	id, i := ref["id"].(string)
	return a && k && i && authority != "" && kind == "secret" && id != ""
}
