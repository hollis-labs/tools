package main

import (
	"context"
	"testing"

	"github.com/hollis-labs/substrate/boot"
)

func TestRootHasCommands(t *testing.T) {
	ctx := context.Background()
	run, err := boot.Start(ctx, substrateApp, commandModules(nil), substrateConfigJSON)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = run.Stop(ctx) }()
	root := newRoot(ctx, run.App)
	for _, name := range []string{"version", "serve"} {
		if c, _, err := root.Find([]string{name}); err != nil || c == root {
			t.Errorf("command %q missing: %v", name, err)
		}
	}
}
