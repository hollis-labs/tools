package policy

import (
	"path"
	"regexp"
	"strings"

	"github.com/hollis-labs/tools/conduit/internal/cmdparse"
)

// rootish paths: recursive removal or permission changes here are never run.
var rootish = map[string]bool{
	"/": true, "~": true, "$HOME": true, "/home": true, "/etc": true, "/usr": true, "/var": true,
	"/bin": true, "/sbin": true, "/lib": true, "/boot": true, "/root": true, "/opt": true,
}

var rootishPattern = regexp.MustCompile(`^/[/*{},.]*$`)

// isRootish normalises a path word the way a shell would reduce it (quotes,
// ${HOME}, //, /./, trailing slashes, a trailing glob or empty brace group) and
// reports whether it names a location whose recursive removal is never run.
func isRootish(a string) bool {
	a = strings.Trim(a, `"'`)
	a = strings.ReplaceAll(a, "${HOME}", "$HOME")
	if rootishPattern.MatchString(a) {
		return true
	}
	if strings.HasSuffix(a, "/*") {
		a = strings.TrimSuffix(a, "/*")
		if a == "" {
			return true
		}
	}
	if a != "~" && a != "$HOME" {
		a = path.Clean(a)
	}
	a = strings.TrimSuffix(a, "/")
	if a == "" {
		return true
	}
	return rootish[a] || rootish[strings.TrimSuffix(a, "/")]
}

var interpreters = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true, "python": true, "python3": true,
	"perl": true, "ruby": true, "node": true, "php": true,
}

// builtinDeny is the hard floor. It is deliberately short and structural: a
// reject-list of shell metacharacters is not a policy, and the sandbox, not
// this list, is the final boundary.
func builtinDeny(c *cmdparse.Parsed, i int, s *cmdparse.Segment) (Verdict, string, string, bool) {
	switch s.Bin {
	case "rm":
		if recursive(s) {
			for _, a := range s.Positional() {
				if isRootish(a) {
					return Deny, "deny-rm-root", "recursive remove of " + a, true
				}
			}
		}
	case "chmod", "chown", "chgrp":
		if recursive(s) {
			for _, a := range s.Positional() {
				if isRootish(a) {
					return Deny, "deny-chmod-root", "recursive permission change of " + a, true
				}
			}
		}
	case "find":
		deleting := false
		for _, f := range s.Flags {
			if f == "-delete" {
				deleting = true
			}
		}
		if deleting {
			for _, a := range s.Positional() {
				if isRootish(a) {
					return Deny, "deny-find-delete-root", "find -delete from " + a, true
				}
			}
		}
	case "dd":
		for _, a := range s.Argv[1:] {
			if strings.HasPrefix(a, "of=/dev/") {
				return Deny, "deny-dd-device", "dd to a block device: " + a, true
			}
		}
	case "shred", "wipefs", "fdisk", "parted":
		for _, a := range s.Positional() {
			if strings.HasPrefix(a, "/dev/") {
				return Deny, "deny-device-write", s.Bin + " on " + a, true
			}
		}
	}
	if strings.HasPrefix(s.Bin, "mkfs") {
		return Deny, "deny-mkfs", "creating a filesystem", true
	}
	for _, r := range s.Redirects {
		if !r.Dynamic && strings.HasPrefix(r.Target, "/dev/") &&
			!strings.HasPrefix(r.Target, "/dev/null") && !strings.HasPrefix(r.Target, "/dev/std") &&
			!strings.HasPrefix(r.Target, "/dev/fd") && !strings.HasPrefix(r.Target, "/dev/tty") &&
			(r.Op == ">" || r.Op == ">>" || r.Op == ">|") {
			return Deny, "deny-redirect-device", "redirect into " + r.Target, true
		}
	}
	// curl | sh: a network fetch piped straight into an interpreter.
	if i > 0 && s.Op == "|" && interpreters[s.Bin] && s.Depth == c.Segments[i-1].Depth {
		prev := &c.Segments[i-1]
		if prev.Effect == cmdparse.EffectNetwork && (prev.Bin == "curl" || prev.Bin == "wget" || prev.Bin == "nc") {
			return Deny, "deny-pipe-to-shell", "network download piped into " + s.Bin, true
		}
	}
	return "", "", "", false
}

func recursive(s *cmdparse.Segment) bool {
	for _, f := range s.Flags {
		if f == "--recursive" || f == "-R" || f == "-r" {
			return true
		}
		if len(f) > 2 && f[0] == '-' && f[1] != '-' && strings.ContainsAny(f[1:], "rR") {
			return true
		}
	}
	return false
}
