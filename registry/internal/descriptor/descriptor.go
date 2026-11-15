// Package descriptor delegates project structure to the shared FC-8 contract.
// Service deployment/auth conformance remains in the local descriptor/runtime
// checks; parsing alone does not certify a project's operational class bar.
package descriptor

import (
	"github.com/hollis-labs/substrate/kinds"
	"os"
)

func Load(path string) (*kinds.Project, error) {
	b, err := os.ReadFile(path) // #nosec G304 -- caller-selected local descriptor
	if err != nil {
		return nil, err
	}
	return Parse(b)
}
func Parse(b []byte) (*kinds.Project, error) { return kinds.ParseProjectYAML(b) }
