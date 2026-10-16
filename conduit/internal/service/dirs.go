package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ResolveRoots canonicalises allowed working-directory roots (symlinks resolved,
// absolute), so a later check compares like with like.
func ResolveRoots(dirs []string) ([]string, error) {
	roots := make([]string, 0, len(dirs))
	for _, d := range dirs {
		r, err := filepath.EvalSymlinks(d)
		if err != nil {
			return nil, fmt.Errorf("allowed directory %q: %w", d, err)
		}
		abs, err := filepath.Abs(r)
		if err != nil {
			return nil, err
		}
		roots = append(roots, abs)
	}
	return roots, nil
}

// ResolveDir validates a caller-supplied working directory against roots (from
// ResolveRoots). An empty dir resolves to the first root. The path is resolved
// through symlinks first, so a link inside a root that points outside it is not
// a way out.
func ResolveDir(roots []string, d string) (string, error) {
	if len(roots) == 0 {
		return "", fmt.Errorf("%w: no allowed directories configured", ErrBadRequest)
	}
	if d == "" {
		return roots[0], nil
	}
	resolved, err := filepath.EvalSymlinks(d)
	if err != nil {
		return "", fmt.Errorf("%w: dir %q: %w", ErrBadRequest, d, err)
	}
	if !filepath.IsAbs(resolved) {
		return "", fmt.Errorf("%w: dir must be absolute", ErrBadRequest)
	}
	for _, root := range roots {
		if resolved == root || strings.HasPrefix(resolved, root+string(os.PathSeparator)) {
			return resolved, nil
		}
	}
	return "", fmt.Errorf("%w: dir %q is outside the allowed roots", ErrBadRequest, d)
}
