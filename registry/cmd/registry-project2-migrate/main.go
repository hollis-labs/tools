// Command registry-project2-migrate rewrites local authored JSON drafts into a
// new directory. It never opens Registry storage or sends network requests.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/hollis-labs/substrate/kinds"
	"github.com/hollis-labs/tools/registry/internal/projectmigration"
)

func main() {
	output := flag.String("output", "", "new output directory (must not exist)")
	flag.Parse()
	if err := migrate(*output, flag.Args()); err != nil {
		fmt.Fprintln(os.Stderr, "project2 migration:", err)
		os.Exit(1)
	}
}

func migrate(output string, paths []string) error {
	if output == "" || len(paths) == 0 {
		return fmt.Errorf("usage: registry-project2-migrate --output NEW_DIRECTORY DRAFT.json ...")
	}
	// Preflight the complete batch before creating the output directory.
	drafts := map[string][]byte{}
	for i, path := range paths {
		name := filepath.Base(path)
		if _, exists := drafts[name]; exists {
			return fmt.Errorf("input %d: duplicate output basename", i+1)
		}
		raw, err := os.ReadFile(path) // #nosec G304 -- explicit local operator input
		if err != nil {
			return fmt.Errorf("input %d: cannot read draft", i+1)
		}
		if err := kinds.ValidateJSONValue(raw); err != nil {
			return fmt.Errorf("input %d: invalid JSON value", i+1)
		}
		next, err := projectmigration.Rewrite(raw)
		if err != nil {
			return fmt.Errorf("input %d: %w", i+1, err)
		}
		payload, err := projectmigration.Payload(next)
		if err != nil {
			return fmt.Errorf("input %d: cannot extract payload", i+1)
		}
		if err := kinds.ValidateJSON("project/2", payload); err != nil {
			// Shared validators can include authored values in errors. Do not
			// print draft content, which may contain private operator metadata.
			return fmt.Errorf("input %d: shared project/2 validation failed; check schema support and authored values", i+1)
		}
		if err := kinds.ValidateNoSecretsJSON(next); err != nil {
			return fmt.Errorf("input %d: secret-like value refused", i+1)
		}
		drafts[name] = append(next, '\n')
	}
	if err := os.Mkdir(output, 0700); err != nil {
		return fmt.Errorf("cannot create new output directory")
	}
	for name, raw := range drafts {
		if err := os.WriteFile(filepath.Join(output, name), raw, 0600); err != nil { // #nosec G306 -- private authored drafts
			return fmt.Errorf("cannot write output draft; discard incomplete output directory")
		}
	}
	fmt.Printf("Rewrote and validated %d project drafts; source files unchanged.\n", len(drafts))
	return nil
}
