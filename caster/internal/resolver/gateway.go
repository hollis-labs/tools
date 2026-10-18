package resolver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/hollis-labs/tools/caster/internal/mcpclient"
)

// ToolCaller calls an MCP tool and returns its structured result.
type ToolCaller interface {
	Call(ctx context.Context, tool string, args map[string]any) (json.RawMessage, error)
	Close() error
}

type catalogEntry struct {
	Command string            `yaml:"command"`
	Args    []string          `yaml:"args"`
	Env     map[string]string `yaml:"env"`
	Enabled *bool             `yaml:"enabled"`
}

// CatalogServer builds an MCP client from a Tether catalog mcp-servers entry
// (~/.tether/catalog/mcp-servers/<id>.yaml), so caster reaches a service through
// exactly the launch command the operator already configured. Environment
// values that are secret references (scheme://…) are not passed.
func CatalogServer(id string) (ToolCaller, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	if id == "" || filepath.Base(id) != id || id == "." || id == ".." {
		return nil, fmt.Errorf("catalog entry %q: not a server name", id)
	}
	b, err := os.ReadFile(filepath.Join(home, ".tether", "catalog", "mcp-servers", id+".yaml")) //nolint:gosec // id is validated above as a bare server name
	if err != nil {
		return nil, fmt.Errorf("catalog entry for %q: %w", id, err)
	}
	var e catalogEntry
	if err := yaml.Unmarshal(b, &e); err != nil {
		return nil, err
	}
	if e.Command == "" || (e.Enabled != nil && !*e.Enabled) {
		return nil, fmt.Errorf("catalog entry %q has no usable command", id)
	}
	var env []string
	for k, v := range e.Env {
		if !strings.Contains(v, "://") {
			env = append(env, k+"="+v)
		}
	}
	return mcpclient.New(e.Command, e.Args, env), nil
}

// CommandServer builds a client from a whitespace-separated command line
// (CASTER_TESSERACT_MCP), e.g. "tesseract mcp".
func CommandServer(cmdline string) (ToolCaller, error) {
	f := strings.Fields(cmdline)
	if len(f) == 0 {
		return nil, fmt.Errorf("empty MCP command")
	}
	return mcpclient.New(f[0], f[1:], nil), nil
}
