package gateadapter_test

import (
	"testing"

	"github.com/hollis-labs/tools/localinvocation/gateadapter"
	"github.com/hollis-labs/tools/localinvocation/invocation"
)

func TestDigestBindsPreparedCommandAndAttempt(t *testing.T) {
	command, _, err := invocation.Prepare(invocation.Options{Argv: []string{"sh", "-c", "printf ok"}, CWD: "/tmp", EnvironmentRef: "env-1", Environ: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := gateadapter.Digest(command)
	if err != nil {
		t.Fatal(err)
	}
	if err := baseline.Validate(); err != nil {
		t.Fatal(err)
	}
	changed := []invocation.Command{}
	args := command
	args.Argv = append([]string(nil), command.Argv...)
	args.Argv[2] = "printf changed"
	changed = append(changed, args)
	cwd := command
	cwd.CWD = "/other"
	changed = append(changed, cwd)
	env := command
	env.EnvironmentRef = "env-2"
	changed = append(changed, env)
	attempt := command
	attempt.IDs.AttemptID = "new-attempt"
	changed = append(changed, attempt)
	for _, candidate := range changed {
		digest, err := gateadapter.Digest(candidate)
		if err != nil || digest == baseline {
			t.Fatalf("change did not alter digest: %v %v", candidate, err)
		}
	}
}

func TestDescribeRejectsUnpreparedCommand(t *testing.T) {
	if _, err := gateadapter.Describe(invocation.Command{}); err == nil {
		t.Fatal("accepted empty command")
	}
}
