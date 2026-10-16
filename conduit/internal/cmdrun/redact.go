package cmdrun

import "regexp"

// redactions are applied to output before it is stored. Best effort: patterns
// extend by appending, and a pattern that fails to compile panics at start-up
// rather than silently dropping out (fail closed).
var redactions = []struct {
	name string
	re   *regexp.Regexp
}{
	{"private-key", regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`)},
	{"aws-access-key", regexp.MustCompile(`\b(AKIA|ASIA)[0-9A-Z]{16}\b`)},
	{"github-token", regexp.MustCompile(`\b(ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{30,}\b|\bgithub_pat_[A-Za-z0-9_]{40,}\b`)},
	{"api-key", regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}\b`)},
	{"bearer", regexp.MustCompile(`(?i)\b(bearer)\s+[A-Za-z0-9._~+/=-]{16,}`)},
	{"jwt", regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`)},
	{"assignment", regexp.MustCompile(`(?i)\b([A-Z0-9_]*(?:SECRET|TOKEN|PASSWORD|PASSWD|API_?KEY|PRIVATE_?KEY)[A-Z0-9_]*)\s*[=:]\s*['"]?[^\s'"]{6,}`)},
}

// Redact replaces secrets in b with a marker and reports how many it replaced.
func Redact(b []byte) ([]byte, int) {
	n := 0
	for _, r := range redactions {
		name := r.name
		b = r.re.ReplaceAllFunc(b, func(m []byte) []byte {
			n++
			if name == "assignment" || name == "bearer" {
				// keep the name, drop the value
				for i, c := range m {
					if c == '=' || c == ':' || c == ' ' {
						return append(append([]byte{}, m[:i+1]...), []byte("[REDACTED:"+name+"]")...)
					}
				}
			}
			return []byte("[REDACTED:" + name + "]")
		})
	}
	return b, n
}
