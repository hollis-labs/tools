package cmdrun

import (
	"regexp"
	"strings"
)

// secretName matches variable names that usually carry credentials.
var secretName = regexp.MustCompile(`(?i)(^|_)(api_?key|key|token|secret|password|passwd|credential|credentials|auth|private_?key|session)($|_)|^(aws_|gh_|github_|anthropic_|openai_)|_(key|token|secret|pat)$`)

// ScrubEnv removes credential-looking variables from env, except names in
// pass. It returns the cleaned environment and the names it removed.
func ScrubEnv(env, pass []string) (clean, removed []string) {
	keep := map[string]bool{}
	for _, p := range pass {
		keep[p] = true
	}
	for _, kv := range env {
		name, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if !keep[name] && secretName.MatchString(name) {
			removed = append(removed, name)
			continue
		}
		clean = append(clean, kv)
	}
	return clean, removed
}
