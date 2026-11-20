// Package projectmigration rewrites authored project drafts once. It is not
// used by Registry's request path and does not provide compatibility aliases.
package projectmigration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
)

// Rewrite accepts a project descriptor, an authored Registry Input, or a
// record_create/record_migrate argument envelope. It preserves unrelated fields.
// The caller must validate the result with the current shared project schema
// before writing anything. Missing decisions are errors, never inferred tiers
// or lifecycle-to-disposition mappings.
func Rewrite(raw []byte) ([]byte, error) {
	var doc map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&doc); err != nil || doc == nil {
		return nil, fmt.Errorf("expected a JSON object")
	}
	input := doc
	if record, ok := doc["record"]; ok {
		var valid bool
		input, valid = record.(map[string]any)
		if !valid {
			return nil, fmt.Errorf("record must be an object")
		}
	}
	payload := input
	if p, ok := input["payload"]; ok {
		var valid bool
		payload, valid = p.(map[string]any)
		if !valid {
			return nil, fmt.Errorf("payload must be an object")
		}
		if input["schema"] != "project/1" {
			return nil, fmt.Errorf("record.schema must be project/1")
		}
	}
	if payload["schema"] != "project/1" {
		return nil, fmt.Errorf("payload.schema must be project/1; drafts are rewritten once")
	}
	for _, source := range []map[string]any{payload, input} {
		m, exists := source["metadata"]
		if !exists {
			continue
		}
		metadata, ok := m.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("metadata must be an object")
		}
		for _, field := range []string{"disposition", "type"} {
			if value, ok := metadata[field]; ok {
				if current, exists := payload[field]; exists && !reflect.DeepEqual(current, value) {
					return nil, fmt.Errorf("conflicting %s values", field)
				}
				payload[field] = value
				delete(metadata, field)
			}
		}
		if len(metadata) == 0 {
			delete(source, "metadata")
		}
	}
	if class, _ := payload["class"].(string); class == "lib" {
		if typ, exists := payload["type"]; exists {
			switch typ {
			case "framework", "monorepo", "lib":
			default:
				return nil, fmt.Errorf("class lib conflicts with authored type")
			}
		} else {
			payload["type"] = "lib"
		}
		delete(payload, "class")
	}
	for _, field := range []string{"disposition", "type"} {
		if value, ok := payload[field].(string); !ok || value == "" {
			return nil, fmt.Errorf("authored %s is required", field)
		}
	}
	input["schema"] = "project/2"
	payload["schema"] = "project/2"
	return json.MarshalIndent(doc, "", "  ")
}

// Payload extracts the schema document from any accepted authored shape.
func Payload(raw []byte) (json.RawMessage, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	if record, ok := doc["record"]; ok {
		if err := json.Unmarshal(record, &doc); err != nil {
			return nil, err
		}
	}
	if payload, ok := doc["payload"]; ok {
		return payload, nil
	}
	return json.RawMessage(raw), nil
}
