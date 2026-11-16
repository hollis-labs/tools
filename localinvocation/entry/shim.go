package entry

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// ResolveShimTarget searches PATH for a command while skipping the shim binary
// itself. The resolved executable becomes argv[0], so a PATH alias cannot
// recurse into itself and the recorded command names what actually runs.
func ResolveShimTarget(name, shimPath, pathEnv string) (string, error) {
	if name == "" || filepath.Base(name) != name || name == "." || name == ".." {
		return "", errors.New("shim command must be a bare executable name")
	}
	shimInfo, err := os.Stat(shimPath)
	if err != nil {
		return "", err
	}
	for _, dir := range filepath.SplitList(pathEnv) {
		if dir == "" {
			dir = "."
		}
		candidate := filepath.Join(dir, name)
		info, err := os.Stat(candidate)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 || os.SameFile(info, shimInfo) {
			continue
		}
		resolved, err := filepath.Abs(candidate)
		if err == nil {
			return resolved, nil
		}
	}
	return "", errors.New("executable not found outside shim")
}

// ShimName uses the invoked basename as the target for a symlinked PATH shim.
// A direct call to the shim binary takes its target as the first argument.
func ShimName(invoked string, args []string) (string, []string, error) {
	name := filepath.Base(invoked)
	if name == "localinvocation-shim" {
		if len(args) == 0 {
			return "", nil, errors.New("shim target is required")
		}
		name, args = args[0], args[1:]
	}
	if strings.ContainsAny(name, `/\`) || name == "" {
		return "", nil, errors.New("shim target must be a bare name")
	}
	return name, args, nil
}
