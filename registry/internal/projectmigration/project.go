// Package projectmigration rewrites authored project drafts once. It is not
// used by Registry's request path and does not provide compatibility aliases.
package projectmigration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/hollis-labs/tools/registry/internal/catalog"
)

// Rewrite accepts a project descriptor, an authored Registry Input, or a
// record_create/record_migrate argument envelope. It preserves unrelated fields.
// The caller must validate the result with the current contracts project schema
// before writing anything. Missing decisions are errors, never inferred tiers
// or lifecycle-to-disposition mappings.
func Rewrite(raw []byte) ([]byte, error) {
	if err := validateShape(raw); err != nil {
		return nil, err
	}
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

func validateShape(raw []byte) error {
	var shape map[string]json.RawMessage
	if err := json.Unmarshal(raw, &shape); err != nil || shape == nil {
		return fmt.Errorf("expected a JSON object")
	}
	var input *catalog.Input
	if _, exists := shape["record"]; exists {
		var args catalog.Args
		if err := catalog.Decode(raw, &args); err != nil || args.Record == nil {
			return fmt.Errorf("invalid Registry argument envelope")
		}
		if args.ID != "" || args.Version != "" || args.Reason != "" {
			if strings.TrimSpace(args.ID) == "" || strings.TrimSpace(args.Version) == "" || strings.TrimSpace(args.Reason) == "" {
				return fmt.Errorf("migration envelope requires id, version and reason")
			}
		}
		if args.Publication != "" && args.Publication != "draft" {
			return fmt.Errorf("only draft argument envelopes may be rewritten")
		}
		input = args.Record
	} else if _, exists := shape["payload"]; exists {
		var value catalog.Input
		if err := catalog.Decode(raw, &value); err != nil {
			return fmt.Errorf("invalid Registry authored Input")
		}
		input = &value
	} else {
		// Standalone descriptors have their own contracts schema; wrapper-only
		// fields are never interpreted here.
		return nil
	}
	if strings.TrimSpace(input.Key) == "" {
		return fmt.Errorf("record.key must be a nonempty string")
	}
	return nil
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
