package cmdrun

import (
	"strings"

	"github.com/hollis-labs/libs/security/redact"
)

// credentialFamilies are variable-name prefixes whose members are credentials or sit
// beside one (AWS_ACCESS_KEY_ID without AWS_SECRET_ACCESS_KEY is half a credential).
var credentialFamilies = []string{"AWS_", "GH_", "GITHUB_", "ANTHROPIC_", "OPENAI_"}

// scrubbed reports whether a variable is removed from a command's environment.
// The shared predicate (redact.SensitiveName) says which names hold a secret. This
// is stricter on purpose: removing a variable is cheap and a missed secret is not,
// so anything ending in _KEY, and the credential families above, go too. Redacting
// text cannot make that trade (it would cut ordinary words), which is why the
// shared predicate does not include a bare "key".
func scrubbed(name string) bool {
	if redact.SensitiveName(name) {
		return true
	}
	up := strings.ToUpper(name)
	if strings.HasSuffix(up, "_KEY") || strings.HasSuffix(up, "_PAT") {
		return true
	}
	for _, p := range credentialFamilies {
		if strings.HasPrefix(up, p) {
			return true
		}
	}
	return false
}

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
		if !keep[name] && scrubbed(name) {
			removed = append(removed, name)
			continue
		}
		clean = append(clean, kv)
	}
	return clean, removed
}
