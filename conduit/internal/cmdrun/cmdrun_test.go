package cmdrun

import (
	"context"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

func run(t *testing.T, cmd string, timeout time.Duration) *Result {
	t.Helper()
	r, err := Run(context.Background(), Spec{Command: cmd, Timeout: timeout, Env: os.Environ()})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestCapturesStreamsSeparately(t *testing.T) {
	r := run(t, `echo out; echo err >&2; exit 3`, 0)
	if string(r.Stdout) != "out\n" || string(r.Stderr) != "err\n" || r.ExitCode != 3 {
		t.Fatalf("%+v", r)
	}
	if Classify(r) != ClassFailed {
		t.Errorf("class %s", Classify(r))
	}
}

func TestTimeoutKillsProcessGroup(t *testing.T) {
	start := time.Now()
	// a background child in the same group would survive a plain kill of bash
	r := run(t, `sleep 60 & sleep 60; wait`, 300*time.Millisecond)
	if !r.TimedOut || Classify(r) != ClassTimeout {
		t.Fatalf("%+v", r)
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("took %v", time.Since(start))
	}
}

func TestTimeoutEscalatesToKill(t *testing.T) {
	r := run(t, `trap '' TERM; sleep 60`, 200*time.Millisecond)
	if !r.TimedOut {
		t.Fatalf("%+v", r)
	}
}

func TestSignalPreserved(t *testing.T) {
	r := run(t, `kill -USR1 $$`, 0)
	if r.Signal == "" || r.ExitCode != 128+int(syscall.SIGUSR1) {
		t.Fatalf("%+v", r)
	}
	if Classify(r) != ClassSignal {
		t.Errorf("class %s", Classify(r))
	}
}

func TestTruncationCountsAllBytes(t *testing.T) {
	r, _ := Run(context.Background(), Spec{Command: `head -c 5000 /dev/zero | tr '\0' x`, MaxCapture: 100, Env: os.Environ()})
	if len(r.Stdout) != 100 || r.StdoutBytes != 5000 || !r.Truncated {
		t.Fatalf("kept=%d total=%d trunc=%v", len(r.Stdout), r.StdoutBytes, r.Truncated)
	}
}

func TestEnvScrubbed(t *testing.T) {
	env := []string{"PATH=/usr/bin:/bin", "HOME=/h", "MY_API_KEY=abc", "GITHUB_TOKEN=x", "AWS_SECRET_ACCESS_KEY=y", "DB_PASSWORD=z", "KEEPME_TOKEN=k", "LANG=C"}
	r, _ := Run(context.Background(), Spec{Command: `env`, Env: env, PassEnv: []string{"KEEPME_TOKEN"}})
	out := string(r.Stdout)
	for _, leaked := range []string{"MY_API_KEY", "GITHUB_TOKEN", "AWS_SECRET", "DB_PASSWORD"} {
		if strings.Contains(out, leaked) {
			t.Errorf("%s leaked", leaked)
		}
	}
	if !strings.Contains(out, "KEEPME_TOKEN=k") || !strings.Contains(out, "HOME=/h") {
		t.Errorf("pass-through lost: %s", out)
	}
	if len(r.ScrubbedEnv) != 4 {
		t.Errorf("scrubbed %v", r.ScrubbedEnv)
	}
}

func TestClassifyTailOnly(t *testing.T) {
	long := "this run talks about rate limit and 429 errors\n" + strings.Repeat("progress line\n", 400) + "boom\n"
	r := &Result{ExitCode: 1, Stderr: []byte(long)}
	if c := Classify(r); c != ClassFailed {
		t.Errorf("discussion of rate limits flagged as %s", c)
	}
	r = &Result{ExitCode: 1, Stderr: []byte("HTTP 429 Too Many Requests\n")}
	if c := Classify(r); c != ClassRateLimit {
		t.Errorf("got %s", c)
	}
	r = &Result{ExitCode: 127, Stderr: []byte("bash: x: command not found\n")}
	if c := Classify(r); c != ClassNotFound {
		t.Errorf("got %s", c)
	}
	r = &Result{ExitCode: 1, Stderr: []byte("dial tcp: connection refused\n")}
	if c := Classify(r); c != ClassTransient || !Retryable(c) {
		t.Errorf("got %s", c)
	}
	r = &Result{ExitCode: 2, Stderr: []byte("error: unknown flag: --nope\n")}
	if c := Classify(r); c != ClassUsage {
		t.Errorf("got %s", c)
	}
}

type panicWriter struct{}

func (panicWriter) Write([]byte) (int, error) { panic("live writer exploded") }

// os/exec calls Write from its own goroutine; an uncontained panic there would
// kill the whole process. The run must carry on and capture output regardless.
func TestPanickingLiveWriterDoesNotKillTheRun(t *testing.T) {
	r, err := Run(context.Background(), Spec{Command: `echo one; sleep 0.1; echo two`, Env: os.Environ(), Stdout: panicWriter{}})
	if err != nil || !r.OK() || string(r.Stdout) != "one\ntwo\n" {
		t.Fatalf("%v %+v", err, r)
	}
}

func TestOnStartReceivesGroupLeaderAndItsPanicIsContained(t *testing.T) {
	var got int
	r, err := Run(context.Background(), Spec{Command: `true`, Env: os.Environ(), OnStart: func(pid int) { got = pid }})
	if err != nil || !r.OK() || got <= 1 {
		t.Fatalf("pid %d, %v, %+v", got, err, r)
	}
	r, err = Run(context.Background(), Spec{Command: `echo still-ran`, Env: os.Environ(), OnStart: func(int) { panic("callback bug") }})
	if err != nil || string(r.Stdout) != "still-ran\n" {
		t.Fatalf("a panicking OnStart must not stop the command: %v %+v", err, r)
	}
}
