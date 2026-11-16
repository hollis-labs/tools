// Package invocation defines the local command identity and its durable record.
// It does not execute commands or retain environment values.
package invocation

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"time"
)

const Version = 1

// The propagation variables contain identifiers only. They must never contain
// arguments, command output, or environment values.
const (
	RootEnv    = "LOCALINVOCATION_ROOT_ID"
	ParentEnv  = "LOCALINVOCATION_PARENT_CALL_ID"
	SessionEnv = "LOCALINVOCATION_SESSION_ID"
	StoreEnv   = "LOCALINVOCATION_STORE"
)

type IDs struct {
	RootCallID     string `json:"root_call_id"`
	ParentCallID   string `json:"parent_call_id,omitempty"`
	CallID         string `json:"call_id"`
	AttemptID      string `json:"attempt_id"`
	Attempt        uint32 `json:"attempt"`
	SessionID      string `json:"session_id,omitempty"`
	ProviderCallID string `json:"provider_call_id,omitempty"`
}

// Command is the runner input. Argv is always a list, including for shell
// scripts (for example ["sh", "-c", script]); no shell splitting occurs here.
type Command struct {
	Argv           []string `json:"argv"`
	CWD            string   `json:"cwd"`
	EnvironmentRef string   `json:"environment_ref,omitempty"`
	IDs            IDs      `json:"ids"`
}

// Record is the immutable preparation receipt. It intentionally has no env
// map; the environment reference is a host-issued locator, not its contents.
type Record struct {
	Version        int      `json:"version"`
	Argv           []string `json:"argv"`
	CWD            string   `json:"cwd"`
	EnvironmentRef string   `json:"environment_ref,omitempty"`
	IDs            IDs      `json:"ids"`
	PreparedAt     string   `json:"prepared_at"`
}

type Options struct {
	Argv           []string
	CWD            string
	EnvironmentRef string
	CallID         string // set with Attempt > 1 to retry the same logical call
	Attempt        uint32
	SessionID      string
	ProviderCallID string
	Parent         *IDs     // explicit parent, otherwise inherited from propagation env
	Environ        []string // optional environment snapshot for identity propagation only
	Now            time.Time
}

func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func envValue(environ []string, name string) string {
	for _, pair := range environ {
		k, v, ok := strings.Cut(pair, "=")
		if ok && k == name {
			return v
		}
	}
	return ""
}

func Prepare(o Options) (Command, Record, error) {
	if len(o.Argv) == 0 || o.Argv[0] == "" {
		return Command{}, Record{}, errors.New("argv must name an executable")
	}
	cwd := o.CWD
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return Command{}, Record{}, err
		}
	}
	if !strings.HasPrefix(cwd, "/") {
		return Command{}, Record{}, errors.New("cwd must be absolute")
	}
	if o.Attempt == 0 {
		o.Attempt = 1
	}
	if o.Attempt > 1 && o.CallID == "" {
		return Command{}, Record{}, errors.New("retry requires the original call id")
	}
	if o.Environ == nil {
		o.Environ = os.Environ()
	}
	ids := IDs{CallID: o.CallID, Attempt: o.Attempt, SessionID: o.SessionID, ProviderCallID: o.ProviderCallID}
	if ids.CallID == "" {
		var err error
		ids.CallID, err = newID()
		if err != nil {
			return Command{}, Record{}, err
		}
	}
	var err error
	ids.AttemptID, err = newID()
	if err != nil {
		return Command{}, Record{}, err
	}
	if o.Parent != nil {
		ids.RootCallID = o.Parent.RootCallID
		ids.ParentCallID = o.Parent.CallID
		if ids.SessionID == "" {
			ids.SessionID = o.Parent.SessionID
		}
	} else {
		ids.RootCallID = envValue(o.Environ, RootEnv)
		ids.ParentCallID = envValue(o.Environ, ParentEnv)
		if ids.SessionID == "" {
			ids.SessionID = envValue(o.Environ, SessionEnv)
		}
	}
	if ids.RootCallID == "" {
		if ids.ParentCallID != "" {
			return Command{}, Record{}, errors.New("inherited parent requires root id")
		}
		ids.RootCallID = ids.CallID
	}
	if ids.SessionID == "" {
		ids.SessionID, err = newID()
		if err != nil {
			return Command{}, Record{}, err
		}
	}
	if ids.ParentCallID == ids.CallID {
		return Command{}, Record{}, errors.New("call cannot parent itself")
	}
	now := o.Now
	if now.IsZero() {
		now = time.Now()
	}
	argv := append([]string(nil), o.Argv...)
	cmd := Command{Argv: argv, CWD: cwd, EnvironmentRef: o.EnvironmentRef, IDs: ids}
	rec := Record{Version: Version, Argv: append([]string(nil), argv...), CWD: cwd, EnvironmentRef: o.EnvironmentRef, IDs: ids, PreparedAt: now.UTC().Format(time.RFC3339Nano)}
	return cmd, rec, nil
}

// ChildEnv returns the identifiers a runner must add or replace in a child's
// process environment. Other environment entries remain the runner's concern.
func ChildEnv(ids IDs) []string {
	return []string{RootEnv + "=" + ids.RootCallID, ParentEnv + "=" + ids.CallID, SessionEnv + "=" + ids.SessionID}
}
