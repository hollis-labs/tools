package gateadapter_test

import (
	"testing"

	"github.com/hollis-labs/tools/localinvocation/gateadapter"
	"github.com/hollis-labs/tools/localinvocation/invocation"
	"github.com/hollis-labs/tools/localinvocation/runner"
)

func TestDigestBindsPreparedCommandAndAttempt(t *testing.T) {
	command, _, err := invocation.Prepare(invocation.Options{Argv: []string{"sh", "-c", "printf ok"}, CWD: "/tmp", EnvironmentRef: "env-1", Environ: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := gateadapter.Digest(command, runner.Options{})
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
	form := command
	form.SourceForm = invocation.FormShellCommand
	changed = append(changed, form)
	for _, candidate := range changed {
		digest, err := gateadapter.Digest(candidate, runner.Options{})
		if err != nil || digest == baseline {
			t.Fatalf("change did not alter digest: %v %v", candidate, err)
		}
	}
	withPTY, err := gateadapter.Digest(command, runner.Options{PTY: true})
	if err != nil || withPTY == baseline {
		t.Fatalf("runner options did not alter digest: %v", err)
	}
}

func TestDescribeRejectsUnpreparedCommand(t *testing.T) {
	if _, err := gateadapter.Describe(invocation.Command{}, runner.Options{}); err == nil {
		t.Fatal("accepted empty command")
	}
}
