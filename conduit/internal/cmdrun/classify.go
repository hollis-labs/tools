package cmdrun

import (
	"regexp"
	"strings"
)

// Error classes drive retry and the hint shown after a failure.
const (
	ClassOK         = "ok"
	ClassTimeout    = "timeout"
	ClassSignal     = "signal"
	ClassStartFail  = "start_failed"
	ClassSandbox    = "sandbox_failed"
	ClassNotFound   = "not_found"
	ClassPermission = "permission_denied"
	ClassUsage      = "usage"
	ClassRateLimit  = "rate_limit"
	ClassTransient  = "transient"
	ClassFailed     = "failed"
)

var (
	reNotFound   = regexp.MustCompile(`(?i)command not found|no such file or directory|not found$`)
	rePermission = regexp.MustCompile(`(?i)permission denied|operation not permitted|access denied`)
	reUsage      = regexp.MustCompile(`(?i)unknown (flag|option|shorthand)|unrecognized (option|argument)|invalid (option|flag)|illegal option|usage:`)
	reRate       = regexp.MustCompile(`(?i)rate limit|too many requests|\b429\b|quota exceeded`)
	reTransient  = regexp.MustCompile(`(?i)connection (refused|reset|timed out)|temporary failure|i/o timeout|try again|service unavailable|\b50[234]\b|network is unreachable|tls handshake`)
)

// tailBytes is how much of stderr is examined: only the tail, so a run that
// merely discusses rate limits in its output is not mistaken for one.
const tailBytes = 2048

// Classify names the outcome of an attempt. It reads the stderr tail only.
func Classify(r *Result) string {
	switch {
	case r.SandboxErr != "":
		return ClassSandbox
	case r.StartErr != "":
		return ClassStartFail
	case r.TimedOut:
		return ClassTimeout
	case r.Signal != "":
		return ClassSignal
	case r.ExitCode == 0:
		return ClassOK
	}
	tail := r.Stderr
	if len(tail) > tailBytes {
		tail = tail[len(tail)-tailBytes:]
	}
	t := lastLines(strings.TrimSpace(string(tail)), 5)
	switch {
	case r.ExitCode == 127 || reNotFound.MatchString(t):
		return ClassNotFound
	case r.ExitCode == 126 || rePermission.MatchString(t):
		return ClassPermission
	case reRate.MatchString(t):
		return ClassRateLimit
	case reTransient.MatchString(t):
		return ClassTransient
	case reUsage.MatchString(t):
		return ClassUsage
	}
	return ClassFailed
}

// Retryable reports whether an attempt of this class may be retried, given
// that the command is idempotent. Rate limits are not retried here: relay's
// rule is that a limit becomes a linked task with a reset hint, not a retry.
func Retryable(class string) bool { return class == ClassTransient }

func lastLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
