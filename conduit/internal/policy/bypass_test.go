package policy

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/conduit/internal/cmdparse"
)

// Each of these ran something beyond a read when executed by bash, and each was
// once classified read/allow by the baseline policy (found by an independent
// review). They must all need approval now. The corpus is executed in a temp
// dir to prove the side effect is real, so the test cannot pass vacuously.
var formerBypasses = []string{
	// redirects on compound commands
	`{ echo hi; } > f`, `(echo hi) > f`, `if true; then echo hi; fi > f`, `while false; do :; done > f`,
	// path-qualified commands are not the tool their basename names
	`./ls`, `./git status`, `/tmp/evil/cat f`,
	// environment prefixes
	`PATH=.:$PATH ls`, `LD_PRELOAD=/tmp/x.so cat f`, `GIT_EXTERNAL_DIFF=./x git diff`, `PATH=/tmp/x; ls`, `env PATH=. ls`,
	// set -x with PS4 command substitution
	`set -x; PS4='$(touch f)'; ls`,
	// substitutions in unusual AST positions
	`case $(touch f) in a) ;; esac`, `x=($(touch f))`, `x[$(touch f)]=1`,
	// runtime re-evaluation of data as code
	`a='x[$(touch f)]'; echo $((a))`, `x='$(touch f)'; echo ${x@P}`, `printf -v 'a[$(touch f)]' x`,
	`read 'a[$(touch f)]' <<< x`, `a=(1); test -v 'a[$(touch f)]'`,
	// dynamic arguments that carry flags
	`X=-fprint0; find . $X f`, `find . $(echo -fprint0) f`,
	// wrapper options parsed differently from the real tools
	`env -a cat touch f`, `env -iu cat touch f`,
	// "read" tools that are not
	`sed 'w f' /dev/null`, `sed -n 's/a/b/w f' /dev/null`, `echo x | sed 's/.*/touch f/e'`, `sed --in-place=.b s/a/b/ f`,
	`sort --output=f /dev/null`, `sort --out=f /dev/null`, `find . -fprint0 f`, `uniq /dev/null f`, `xxd -r /dev/null f`,
	`tree -o f`, `history -w f`,
	`echo x | awk '{ print $1 > "f" }'`, `echo x | awk 'BEGIN { "touch f" | getline }'`, `awk 'BEGIN { system ("touch f") }'`,
	`tar --checkpoint=1 --checkpoint-action=exec='touch f' -tf /dev/null`,
	// git options that run programs or write files
	`git -c core.fsmonitor='touch f' status`, `git -c diff.external='touch f' diff HEAD~0 HEAD`,
	`git log --output=f`, `git branch newbranch-conduit-test`, `git config --unset-all conduit.nothing`,
	`git reflog expire --all`, `git remote prune origin`,
}

func TestFormerBypassesNeedApproval(t *testing.T) {
	executed := 0
	for _, cmd := range formerBypasses {
		d := Baseline().Decide(cmdparse.Parse(cmd))
		if d.Verdict == Allow {
			t.Errorf("%q is still allowed (effect %s)", cmd, d.Effect)
		}
		// prove the probe is a real side effect, in a scratch dir (touch/echo only)
		dir := t.TempDir()
		c := exec.Command("bash", "-c", cmd) //nolint:gosec // inert probes in a temp dir, from this file's own corpus
		c.Dir = dir
		_ = c.Run()
		if _, err := os.Stat(filepath.Join(dir, "f")); err == nil {
			executed++
		}
	}
	if executed < 25 {
		t.Errorf("only %d of %d probes produced their side effect here; the corpus is no longer proving anything", executed, len(formerBypasses))
	}
}

// Over-blocking is a failure too: ordinary read-only work must stay frictionless.
func TestOrdinaryReadsStayAllowed(t *testing.T) {
	for _, cmd := range []string{
		`env`, `env | grep PATH`, `ls -la`, `git status`, `git log --oneline -5`, `git diff`, `git -C /tmp status`, `git branch --list`, `git branch -a`,
		`git config user.name`, `git config --get user.email`, `git remote -v`, `git remote show origin`, `git reflog`,
		`cat f | grep x | wc -l`, `sed -n '1,10p' f`, `sed 's/a/b/' f`, `sed -n '/foo/p' f`, `sed '$d' f`,
		`awk '{print $1}' f`, `awk -F, '{print $2, $3}' f`, `find . -name '*.go'`, `find . -type f -mtime -1`,
		`echo $HOME`, `cat $F`, `ls $DIR`, `grep -rn todo .`, `date +%F`, `sort -u f`, `sort -n f | uniq -c`,
		`tar -tf x.tar`, `kubectl get pods`, `kubectl config view`, `rg foo`, `head -n 5 f`, `wc -l f`, `pwd`,
		`go version`, `go vet ./...` /* exec: asks, so excluded below */, `uname -a`, `df -h`, `ps aux`, `stat f`,
		`echo "hi" && echo there`, `ls | head`, `docker ps`, `journalctl -u nginx -n 50`,
	} {
		d := Baseline().Decide(cmdparse.Parse(cmd))
		want := Allow
		if cmd == `go vet ./...` {
			continue // vet builds and runs analyzers: exec, asks
		}
		if d.Verdict != want {
			t.Errorf("%q: got %s (%+v), want allow", cmd, d.Verdict, d.Reasons)
		}
	}
}

// The deny floor survives quoting, braces, doubled slashes and ${HOME}.
func TestDenyFloorNormalisation(t *testing.T) {
	for _, cmd := range []string{
		`rm -rf "$HOME"`, `rm -rf ${HOME}`, `rm -rf /{,}`, `rm -rf //`, `rm -rf /./`, `rm -rf /*`, `rm -rf '/'`, `rm -rf ~/`,
		`find / -delete`, `find ~ -delete`, `chmod -R 777 /`,
	} {
		if v := Baseline().Decide(cmdparse.Parse(cmd)).Verdict; v != Deny {
			t.Errorf("%q: got %s want deny", cmd, v)
		}
	}
	for _, cmd := range []string{`rm -rf ./build`, `rm -rf /tmp/x`, `rm -rf build/`, `find ./src -delete`} {
		if v := Baseline().Decide(cmdparse.Parse(cmd)).Verdict; v == Deny {
			t.Errorf("%q: denied but is not a root-ish target", cmd)
		}
	}
}

// A user allow rule for a tool must not cover the options that make it run a program.
func TestAllowRuleDoesNotCoverProgramRunningOptions(t *testing.T) {
	p := &Policy{Rules: []Rule{{ID: "git-status", Bin: "git", Sub: "status", Verdict: Allow}, {ID: "git-diff", Bin: "git", Sub: "diff", Verdict: Allow}}}
	for _, cmd := range []string{
		`git -c core.fsmonitor=x status`, `GIT_EXTERNAL_DIFF=./x git diff`, `./git status`, `PATH=/tmp/x git status`,
	} {
		if v := p.Decide(cmdparse.Parse(cmd)).Verdict; v == Allow {
			t.Errorf("%q covered by an allow rule written for plain git", cmd)
		}
	}
	if v := p.Decide(cmdparse.Parse(`git status`)).Verdict; v != Allow {
		t.Errorf("plain git status should match its rule: %s", v)
	}
}
