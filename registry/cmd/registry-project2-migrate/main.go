// Command registry-project2-migrate rewrites local authored JSON drafts into a
// new directory. It never opens Registry storage or sends network requests.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/hollis-labs/tools/registry/contracts"
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
			// Contracts validators can include authored values in errors. Do not
			// print draft content, which may contain private operator metadata.
			return fmt.Errorf("input %d: contracts project/2 validation failed; check schema support and authored values", i+1)
		}
		if err := kinds.ValidateNoSecretsJSON(next); err != nil {
			return fmt.Errorf("input %d: secret-like value refused", i+1)
		}
		drafts[name] = append(next, '\n')
	}
	if err := publishDrafts(output, drafts, os.WriteFile); err != nil {
		return err
	}
	fmt.Printf("Rewrote and validated %d project drafts; source files unchanged.\n", len(drafts))
	return nil
}

func publishDrafts(output string, drafts map[string][]byte, write func(string, []byte, os.FileMode) error) error {
	if _, err := os.Lstat(output); !os.IsNotExist(err) {
		return fmt.Errorf("output must not exist")
	}
	stage, err := os.MkdirTemp(filepath.Dir(output), ".project2-staging-*")
	if err != nil {
		return fmt.Errorf("cannot create private sibling staging directory")
	}
	defer func() { _ = os.RemoveAll(stage) }()
	names := make([]string, 0, len(drafts))
	for name := range drafts {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := write(filepath.Join(stage, name), drafts[name], 0600); err != nil {
			return fmt.Errorf("cannot write staged draft; output not published")
		}
	}
	if err := publishDirectory(stage, output); err != nil {
		return fmt.Errorf("cannot publish output without replacing a destination")
	}
	return nil
}
