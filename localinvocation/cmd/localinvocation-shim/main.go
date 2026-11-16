package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/hollis-labs/tools/localinvocation/entry"
	"github.com/hollis-labs/tools/localinvocation/internal/cli"
	"github.com/hollis-labs/tools/localinvocation/invocation"
)

func main() { os.Exit(run(os.Args[0], os.Args[1:])) }

func run(invoked string, args []string) int {
	name, commandArgs, err := entry.ShimName(invoked, args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	executable, err := entry.ResolveShimTarget(name, self, os.Getenv("PATH"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	store, err := entry.DefaultStorePath()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	command, record, err := entry.Shim(executable, commandArgs, invocation.Options{})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	code, err := cli.Execute(ctx, command, record, store, os.Stdin, os.Stdout, os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
	return code
}
