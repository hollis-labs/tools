package entry

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/hollis-labs/tools/localinvocation/invocation"
)

type Settings struct {
	Options invocation.Options
	Store   string
}

// ParseRun requires a real argv after the wrapper's flags. It never joins or
// re-parses the command arguments.
func ParseRun(args []string) (Settings, error) {
	var s Settings
	var attempt uint
	f := flag.NewFlagSet("run", flag.ContinueOnError)
	f.StringVar(&s.Options.CWD, "cwd", "", "working directory")
	f.StringVar(&s.Options.EnvironmentRef, "environment-ref", "", "host-issued environment reference")
	f.StringVar(&s.Options.SessionID, "session-id", "", "session identifier")
	f.StringVar(&s.Options.ProviderCallID, "provider-call-id", "", "provider call identifier")
	f.StringVar(&s.Options.CallID, "call-id", "", "logical call identifier (required for retry)")
	f.StringVar(&s.Store, "store", "", "local record journal")
	f.UintVar(&attempt, "attempt", 1, "logical call attempt number")
	if err := f.Parse(args); err != nil {
		return Settings{}, err
	}
	if attempt == 0 || attempt > uint(^uint32(0)) {
		return Settings{}, errors.New("attempt must be between 1 and 4294967295")
	}
	s.Options.Attempt = uint32(attempt)
	s.Options.Argv = f.Args()
	if len(s.Options.Argv) == 0 {
		return Settings{}, errors.New("expected command argv after --")
	}
	if s.Store == "" {
		var err error
		s.Store, err = DefaultStorePath()
		if err != nil {
			return Settings{}, err
		}
	}
	if !filepath.IsAbs(s.Store) {
		return Settings{}, errors.New("store path must be absolute")
	}
	return s, nil
}

func ParseHookArgs(args []string) (Settings, error) {
	var s Settings
	var attempt uint
	f := flag.NewFlagSet("hook", flag.ContinueOnError)
	f.StringVar(&s.Options.CWD, "cwd", "", "working directory")
	f.StringVar(&s.Options.EnvironmentRef, "environment-ref", "", "host-issued environment reference")
	f.StringVar(&s.Options.SessionID, "session-id", "", "session identifier")
	f.StringVar(&s.Options.ProviderCallID, "provider-call-id", "", "provider call identifier")
	f.StringVar(&s.Options.CallID, "call-id", "", "logical call identifier")
	f.StringVar(&s.Store, "store", "", "local record journal")
	f.UintVar(&attempt, "attempt", 1, "logical call attempt number")
	if err := f.Parse(args); err != nil {
		return Settings{}, err
	}
	if len(f.Args()) != 0 {
		return Settings{}, errors.New("hook takes JSON on stdin, not command arguments")
	}
	if attempt == 0 || attempt > uint(^uint32(0)) {
		return Settings{}, errors.New("attempt must be between 1 and 4294967295")
	}
	s.Options.Attempt = uint32(attempt)
	if s.Store == "" {
		var err error
		s.Store, err = DefaultStorePath()
		if err != nil {
			return Settings{}, err
		}
	}
	if !filepath.IsAbs(s.Store) {
		return Settings{}, errors.New("store path must be absolute")
	}
	return s, nil
}

func DefaultStorePath() (string, error) {
	if path := os.Getenv(invocation.StoreEnv); path != "" {
		if !filepath.IsAbs(path) {
			return "", fmt.Errorf("%s must be absolute", invocation.StoreEnv)
		}
		return path, nil
	}
	if state := os.Getenv("XDG_STATE_HOME"); state != "" {
		if !filepath.IsAbs(state) {
			return "", errors.New("XDG_STATE_HOME must be absolute")
		}
		return filepath.Join(state, "localinvocation", "records.jsonl"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "localinvocation", "records.jsonl"), nil
}
