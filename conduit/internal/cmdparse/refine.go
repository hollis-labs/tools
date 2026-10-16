package cmdparse

import (
	"regexp"
	"strings"
)

// refine inspects flags and scripts that make a command in the "read" tables do
// more than read. It only ever raises an effect. Where an option runs another
// program it also records the "flag-exec" risk, which keeps user allow rules
// from matching the segment as written.
func refine(s *Segment, e Effect) Effect {
	raise := func(to Effect, flagExec bool) {
		e = MaxEffect(e, to)
		if flagExec {
			s.risk("flag-exec")
		}
	}
	prefix := func(ps ...string) bool {
		for _, f := range s.Flags {
			for _, p := range ps {
				if strings.HasPrefix(f, p) {
					return true
				}
			}
		}
		return false
	}
	exact := func(fs ...string) bool {
		for _, f := range s.Flags {
			for _, w := range fs {
				if f == w {
					return true
				}
			}
		}
		return false
	}
	pos := positional(s)

	switch s.Bin {
	case "set":
		if len(s.Argv) > 1 {
			raise(EffectExec, true) // set -x with PS4, set -o ..., set -- args
		}
	case "sed":
		if !safeSed(s) {
			raise(EffectWrite, false)
		}
	case "sort":
		if prefix("--o", "--compress") || hasShortO(s.Flags) {
			raise(EffectWrite, false)
		}
	case "find":
		if exact("-delete") {
			raise(EffectDestructive, false)
		}
		if exact("-fprint", "-fprint0", "-fprintf", "-fls") {
			raise(EffectWrite, false)
		}
		if exact("-exec", "-execdir", "-ok", "-okdir") {
			raise(EffectExec, true)
		}
	case "fd", "fdfind":
		if exact("-x", "-X") || prefix("--exec") {
			raise(EffectExec, true)
		}
	case "rg", "ag":
		if prefix("--pre", "--hostname-bin", "--pager") {
			raise(EffectExec, true)
		}
	case "awk", "gawk", "mawk", "nawk":
		if !safeAwk(s) {
			raise(EffectExec, true)
		}
	case "uniq":
		if len(pos) >= 2 {
			raise(EffectWrite, false)
		}
	case "xxd":
		if exact("-r") || len(pos) >= 2 {
			raise(EffectWrite, false)
		}
	case "tree":
		if exact("-o") || prefix("--o") {
			raise(EffectWrite, false)
		}
	case "yq":
		if exact("-i", "-s") || prefix("--inplace", "--split-exp") {
			raise(EffectWrite, false)
		}
	case "date":
		if exact("-s") || prefix("--set") || (len(pos) > 0 && !strings.HasPrefix(pos[0], "+")) {
			raise(EffectWrite, false)
		}
	case "hostname":
		if len(pos) > 0 || exact("-F", "-b") {
			raise(EffectWrite, false)
		}
	case "tar":
		if prefix("--checkpoint-action", "--to-command", "--use-compress-program", "--rsh-command", "--info-script", "--new-volume-script") || exact("-I") {
			raise(EffectExec, true)
		}
	case "journalctl":
		if prefix("--vacuum", "--rotate", "--flush", "--sync", "--relinquish", "--smart-relinquish") {
			raise(EffectWrite, false)
		}
	case "kubectl":
		if s.Sub == "config" {
			if len(pos) == 0 || !readConfigSubs[pos[0]] {
				raise(EffectWrite, false)
			}
		}
	case "pip", "pip3":
		if s.Sub == "list" && (exact("-o") || prefix("--outdated")) {
			raise(EffectNetwork, false)
		}
	case "go":
		if prefix("-vettool", "-toolexec", "-exec") {
			raise(EffectExec, true)
		}
		if s.Sub == "env" && exact("-w", "-u") {
			raise(EffectWrite, false)
		}
	case "git":
		refineGit(s, pos, raise, prefix, exact)
	}
	return e
}

var readConfigSubs = map[string]bool{"view": true, "get-contexts": true, "current-context": true, "get-clusters": true, "get-users": true}

func hasShortO(flags []string) bool {
	for _, f := range flags {
		if strings.HasPrefix(f, "-") && !strings.HasPrefix(f, "--") && strings.Contains(f[1:], "o") {
			return true
		}
	}
	return false
}

func refineGit(s *Segment, pos []string, raise func(Effect, bool), prefix func(...string) bool, exact func(...string) bool) {
	// options that make git run another program
	if exact("-c") || prefix("--config-env", "--exec-path", "--upload-pack", "--receive-pack", "--open-files-in-pager", "-O") ||
		exact("--exec") {
		raise(EffectExec, true)
	}
	if prefix("--output") {
		raise(EffectWrite, false)
	}
	switch s.Sub {
	case "branch":
		listing := exact("--list", "-l", "--contains", "--no-contains", "--merged", "--no-merged", "--points-at", "--show-current")
		if len(pos) > 0 && !listing {
			raise(EffectWrite, false)
		}
		if prefix("--set-upstream", "--unset-upstream", "--edit-description") || exact("-u", "-f", "--force") {
			raise(EffectWrite, false)
		}
	case "config":
		switch {
		case prefix("--unset", "--add", "--replace", "--remove", "--rename", "--edit") || exact("-e"):
			raise(EffectWrite, false)
		case exact("--get", "--get-all", "--get-regexp", "--list", "-l"):
		case len(pos) > 1:
			raise(EffectWrite, false)
		}
	case "reflog":
		if len(pos) > 0 && (pos[0] == "expire" || pos[0] == "delete") {
			raise(EffectWrite, false)
		}
	case "remote":
		if len(pos) > 0 && pos[0] != "show" && pos[0] != "get-url" {
			raise(EffectWrite, false)
		}
	}
}

// --- sed ---

var sedAddr = `(?:\d+|\$|/(?:[^/\\]|\\.)*/)`
var sedSimple = regexp.MustCompile(`^\s*(?:` + sedAddr + `(?:,` + sedAddr + `)?!?)?\s*[pPdq=]\s*(?:;\s*(?:` + sedAddr + `(?:,` + sedAddr + `)?!?)?\s*[pPdq=]\s*)*$`)
var sedSubs = func() []*regexp.Regexp {
	var out []*regexp.Regexp
	for _, d := range []string{"/", "|", ",", "#", ":", "@"} {
		q := regexp.QuoteMeta(d)
		out = append(out, regexp.MustCompile(`^\s*(?:`+sedAddr+`(?:,`+sedAddr+`)?)?s`+q+`(?:[^`+q+`\\]|\\.)*`+q+`(?:[^`+q+`\\]|\\.)*`+q+`[gpiIm0-9]*\s*$`))
	}
	return out
}()

// safeSed reports a sed invocation that can only print, delete or substitute:
// no in-place edit, no script file, and no w, W, e or r commands or flags.
func safeSed(s *Segment) bool {
	script := ""
	for i, a := range s.Argv[1:] {
		if strings.HasPrefix(a, "-") && a != "-" {
			switch a {
			case "-n", "-E", "-r", "--quiet", "--silent", "--regexp-extended", "--posix":
				continue
			}
			return false
		}
		if s.DynArg[1+i] {
			return false
		}
		script = a
		break
	}
	if script == "" {
		return false
	}
	if sedSimple.MatchString(script) {
		return true
	}
	for _, re := range sedSubs {
		if re.MatchString(script) {
			return true
		}
	}
	return false
}

// --- awk ---

var awkSafe = regexp.MustCompile(`^[A-Za-z0-9_$ ,.{}()+\-*/%"'=<!~:;\[\]]*$`)

// safeAwk allows the common report one-liner and nothing that can write, pipe
// or call out: no output redirection, no system, getline or close, no -f file.
func safeAwk(s *Segment) bool {
	args := s.Argv[1:]
	for i := 0; i < len(args); i++ {
		a := args[i]
		if s.DynArg[1+i] {
			return false
		}
		if strings.HasPrefix(a, "-") && a != "-" {
			switch {
			case a == "-F" || a == "-v":
				i++ // value
				continue
			case strings.HasPrefix(a, "-F") && !strings.HasPrefix(a, "--"):
				continue
			case strings.HasPrefix(a, "-v") && len(a) > 2:
				continue
			}
			return false
		}
		low := strings.ToLower(a)
		if !awkSafe.MatchString(a) || strings.Contains(low, "system") || strings.Contains(low, "getline") ||
			strings.Contains(low, "close") || strings.Contains(low, "fflush") {
			return false
		}
		return true
	}
	return false
}

// FlagSafeBin reports binaries whose arguments cannot turn them into something
// more than they are: a dynamic argument (cat $f) cannot smuggle a dangerous
// flag into them. For everything else a dynamic argument needs a look.
func FlagSafeBin(bin string) bool { return flagSafe[bin] }

var flagSafe = set("echo", "cat", "ls", "head", "tail", "wc", "grep", "egrep", "fgrep", "cut", "tr", "pwd",
	"basename", "dirname", "true", "false", "test", "[", "sleep", "seq", "stat", "file", "du", "df", "diff", "cmp",
	"nl", "column", "sha256sum", "md5sum", "shasum", "realpath", "readlink", "which", "type", "id", "whoami", "uname", "printf")
