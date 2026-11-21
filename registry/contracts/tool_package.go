package kinds

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ToolPackage pins an agent-authored package and its Caster archive. Neither
// publisher field proves the caller's identity; artifact bytes are verified by
// the installer, not fetched during Registry admission.
type ToolPackage struct {
	Schema     string                `json:"schema"`
	ID         string                `json:"id"`
	Publisher  string                `json:"publisher"`
	Name       string                `json:"name"`
	Version    string                `json:"version"`
	Digest     string                `json:"digest"`
	Artifact   string                `json:"artifact"`
	Entry      string                `json:"entry"`
	Execution  ToolPackageExecution  `json:"execution"`
	Needs      []ToolPackageNeed     `json:"needs"`
	Env        []string              `json:"env"`
	Provenance ToolPackageProvenance `json:"provenance"`
	Summary    string                `json:"summary,omitempty"`
	Owner      string                `json:"owner,omitempty"`
}
type ToolPackageExecution struct {
	Kind string `json:"kind"`
}
type ToolPackageNeed struct {
	Audience string   `json:"audience"`
	Scopes   []string `json:"scopes"`
	Env      []string `json:"env"`
}
type ToolPackageProvenance struct {
	Publisher string `json:"publisher"`
	SourceRef string `json:"source_ref,omitempty"`
}

func (v ToolPackage) Validate() error { return validateValue(ToolPackageSchema, v) }
func validateToolPackage(data []byte) error {
	var v ToolPackage
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	if v.ID != v.Publisher+"/"+v.Name+"@"+v.Version {
		return fmt.Errorf("tool-package/1: id must equal publisher/name@version")
	}
	if !strings.HasSuffix(v.Provenance.Publisher, "/"+v.Publisher) {
		return fmt.Errorf("tool-package/1: provenance.publisher must identify publisher")
	}
	// Entries are package-relative regular files. Never clean a dangerous path
	// into an accepted one; the installer also validates every tar member.
	if strings.ContainsAny(v.Entry, "\\\x00\r\n:") {
		return fmt.Errorf("tool-package/1: invalid entry")
	}
	for _, part := range strings.Split(v.Entry, "/") {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("tool-package/1: entry must be a relative file path without traversal")
		}
	}
	return nil
}
