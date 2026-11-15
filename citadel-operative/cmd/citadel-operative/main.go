package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/hollis-labs/tools/citadel-operative/internal/command"
	"github.com/hollis-labs/tools/citadel-operative/internal/fixturehost"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	// The executable owns one process-local synthetic universe. Runner instances
	// must reuse it; no constructor silently substitutes new admission state.
	fixtures := fixturehost.NewLedger()
	code := command.New(fixtures).Execute(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}
