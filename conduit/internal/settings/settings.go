// Package settings reads the app's own "conduit" block from the config. The shell modules read theirs; this block holds
// what only Conduit has. The defaults live in cmd/conduit/substrate.yaml; operators override any key in the YAML file
// named by SUBSTRATE_CONFIG. This package only decodes and validates.
//
//	conduit:
//	  allowed_hosts: []     # extra Host values (besides loopback) served behind a local TLS proxy
//	  allow_dirs: []        # roots a caller's working directory must sit under (serve; default: the data directory's exec/ is NOT implied, one is required)
//	  policy_file: ""       # the command policy YAML (default: <config dir>/policy.yaml)
//	  runbooks_file: ""     # the runbook YAML (default: <config dir>/runbooks.yaml)
//	  blobs: ""             # output store: "" (caster when on PATH, else local), "local" or "caster"
//	  rate_per_minute: 240  # requests per minute per caller
package settings

import (
	"fmt"
	"sort"
	"strings"
)

// Key is the name of the config block.
const Key = "conduit"

// Settings are the parsed block.
type Settings struct {
	AllowedHosts  []string
	AllowDirs     []string
	PolicyFile    string
	RunbooksFile  string
	Blobs         string
	RatePerMinute int
}

// Parse reads the block (nil for a missing one). An unknown key or a wrong type is an error that names the key.
func Parse(raw any) (Settings, error) {
	s := Settings{RatePerMinute: 240}
	if raw == nil {
		return s, nil
	}
	block, ok := raw.(map[string]any)
	if !ok {
		return s, fmt.Errorf("conduit: the config block must be a map")
	}
	known := []string{"allowed_hosts", "allow_dirs", "policy_file", "runbooks_file", "blobs", "rate_per_minute"}
	for k := range block {
		found := false
		for _, n := range known {
			found = found || n == k
		}
		if !found {
			sort.Strings(known)
			return s, fmt.Errorf("conduit.%s is not a known key (known: %s)", k, strings.Join(known, ", "))
		}
	}
	var err error
	if s.AllowedHosts, err = list(block, "allowed_hosts"); err != nil {
		return s, err
	}
	if s.AllowDirs, err = list(block, "allow_dirs"); err != nil {
		return s, err
	}
	for key, dst := range map[string]*string{"policy_file": &s.PolicyFile, "runbooks_file": &s.RunbooksFile, "blobs": &s.Blobs} {
		v, present := block[key]
		if !present || v == nil {
			continue
		}
		t, ok := v.(string)
		if !ok {
			return s, fmt.Errorf("conduit.%s must be a string", key)
		}
		*dst = strings.TrimSpace(t)
	}
	if s.Blobs != "" && s.Blobs != "local" && s.Blobs != "caster" {
		return s, fmt.Errorf("conduit.blobs must be empty, local or caster, got %q", s.Blobs)
	}
	if v, present := block["rate_per_minute"]; present && v != nil {
		switch n := v.(type) {
		case int:
			s.RatePerMinute = n
		case float64:
			s.RatePerMinute = int(n)
		default:
			return s, fmt.Errorf("conduit.rate_per_minute must be a number")
		}
		if s.RatePerMinute < 1 {
			return s, fmt.Errorf("conduit.rate_per_minute must be at least 1")
		}
	}
	return s, nil
}

func list(m map[string]any, key string) ([]string, error) {
	v, present := m[key]
	if !present || v == nil {
		return nil, nil
	}
	items, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("conduit.%s must be a list of strings", key)
	}
	var out []string
	for _, it := range items {
		t, ok := it.(string)
		if !ok || strings.TrimSpace(t) == "" {
			return nil, fmt.Errorf("conduit.%s must be a list of non-empty strings", key)
		}
		out = append(out, strings.TrimSpace(t))
	}
	return out, nil
}
