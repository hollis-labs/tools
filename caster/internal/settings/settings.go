// Package settings reads the app's own "caster" block from the manifest config. The shell modules
// read theirs; this block holds what only Caster has.
//
//	caster:
//	  torque_url: ""        # base URL of Torque, for torque:// refs; empty = torque:// reports unavailable
//	  tesseract_mcp: ""     # command line of a Tesseract MCP server, e.g. "tesseract mcp"; empty = the Tether catalog entry
//	  tesseract_bin: ""     # a Tesseract CLI binary for tesseract:// refs; empty = the MCP gateway only
//	  allowed_hosts: []     # extra Host names a local TLS proxy forwards (loopback names are always served)
package settings

import (
	"fmt"
	"sort"
	"strings"
)

// Key is the name of the config block.
const Key = "caster"

// Settings are the parsed block.
type Settings struct {
	TorqueURL    string
	TesseractMCP string
	TesseractBin string
	AllowedHosts []string
}

// Parse reads the block (nil for a missing one). An unknown key or a bad value is an error naming
// the key, so a typo is found at startup instead of silently turning a protection off.
func Parse(raw any) (Settings, error) {
	var s Settings
	if raw == nil {
		return s, nil
	}
	block, ok := raw.(map[string]any)
	if !ok {
		return s, fmt.Errorf("caster: the config block must be a map")
	}
	known := map[string]bool{"torque_url": true, "tesseract_mcp": true, "tesseract_bin": true, "allowed_hosts": true}
	var unknown []string
	for k := range block {
		if !known[k] {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return s, fmt.Errorf("caster: unknown config key(s): %s", strings.Join(unknown, ", "))
	}
	str := func(k string, dst *string) error {
		v, present := block[k]
		if !present || v == nil {
			return nil
		}
		t, ok := v.(string)
		if !ok {
			return fmt.Errorf("caster.%s must be a string", k)
		}
		*dst = strings.TrimSpace(t)
		return nil
	}
	for _, step := range []error{
		str("torque_url", &s.TorqueURL),
		str("tesseract_mcp", &s.TesseractMCP),
		str("tesseract_bin", &s.TesseractBin),
	} {
		if step != nil {
			return s, step
		}
	}
	if v, present := block["allowed_hosts"]; present && v != nil {
		items, ok := v.([]any)
		if !ok {
			return s, fmt.Errorf("caster.allowed_hosts must be a list of strings")
		}
		for _, it := range items {
			t, ok := it.(string)
			if !ok || strings.TrimSpace(t) == "" {
				return s, fmt.Errorf("caster.allowed_hosts must be a list of non-empty strings")
			}
			s.AllowedHosts = append(s.AllowedHosts, strings.TrimSpace(t))
		}
	}
	return s, nil
}
