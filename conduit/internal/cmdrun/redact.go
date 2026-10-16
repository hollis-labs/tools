package cmdrun

import "github.com/hollis-labs/conduit/internal/redact"

// Redact replaces secrets in b with a marker and reports how many it replaced.
func Redact(b []byte) ([]byte, int) { return redact.Redact(b) }
