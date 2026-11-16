package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/hollis-labs/tools/localinvocation/entry"
	"github.com/hollis-labs/tools/localinvocation/internal/cli"
)

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: localinvocation run [flags] -- command [args...] | hook [flags] < event.json")
		return 2
	}
	switch args[0] {
	case "hook":
		settings, err := entry.ParseHookArgs(args[1:])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		input, err := io.ReadAll(io.LimitReader(os.Stdin, (4<<20)+1))
		if err == nil && len(input) > 4<<20 {
			err = fmt.Errorf("hook event exceeds 4 MiB")
		}
		if err == nil {
			err = cli.RecordHook(input, settings)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return 0
	case "run":
		settings, err := entry.ParseRun(args[1:])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		command, record, err := entry.Terminal(settings.Options.Argv, settings.Options)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		code, err := cli.Execute(ctx, command, record, settings.Store, os.Stdout, os.Stderr)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
		return code
	default:
		fmt.Fprintln(os.Stderr, "unknown subcommand:", args[0])
		return 2
	}
}
