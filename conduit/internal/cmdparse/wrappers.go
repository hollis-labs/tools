package cmdparse

import (
	"path"
	"regexp"
	"strings"
)

// peel strips env-style assignments and process wrappers from the front of an
// argv, recording what it removed. sudo and doas are peeled for classification
// but kept as a risk flag: the wrapped command runs with more authority.
func (w *walker) peel(seg *Segment, argv []string, dyn []bool) ([]string, []bool) {
	for n := 0; len(argv) > 0 && !dyn[0]; n++ {
		if n >= MaxWrappers {
			seg.Opaque = true
			seg.risk("wrapper-depth")
			return argv, dyn
		}
		bin := path.Base(argv[0])
		skip, ok := wrapperSkip(bin, argv, dyn)
		if !ok {
			return argv, dyn
		}
		if skip < 0 { // unparseable wrapper arguments
			seg.Opaque = true
			seg.risk("wrapper-args")
			return argv, dyn
		}
		if skip >= len(argv) {
			// Nothing follows the wrapper's own options: it is not wrapping anything, it
			// is the command (`env` prints the environment, `nice` prints the niceness).
			return argv, dyn
		}
		for _, a := range argv[1:skip] {
			if bin == "env" && isAssignment(a) {
				name := a[:strings.IndexByte(a, '=')]
				seg.Env = append(seg.Env, name)
				if envSensitive(name) {
					seg.risk("env-sensitive")
				}
			}
		}
		seg.Wrappers = append(seg.Wrappers, bin)
		if bin == "sudo" || bin == "doas" || bin == "pkexec" {
			seg.risk("sudo")
		}
		argv, dyn = argv[skip:], dyn[skip:]
	}
	return argv, dyn
}

// wspec describes the options a wrapper accepts. Anything outside it is not
// skipped: the segment becomes opaque, because guessing which word is the wrapped
// command is exactly how a classifier ends up rating the wrong program.
type wspec struct {
	value   map[string]bool // consume the next word (or a joined value for short options)
	boolean map[string]bool
}

func set(names ...string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

var wrapperSpecs = map[string]wspec{
	"env":      {value: set("-u", "--unset", "-C", "--chdir", "-a", "--argv0", "-P"), boolean: set("-i", "--ignore-environment", "-0", "--null", "-v", "--debug")},
	"timeout":  {value: set("-k", "--kill-after", "-s", "--signal"), boolean: set("--foreground", "--preserve-status", "-v", "--verbose")},
	"nice":     {value: set("-n", "--adjustment")},
	"ionice":   {value: set("-c", "--class", "-n", "--classdata"), boolean: set("-t", "--ignore")},
	"stdbuf":   {value: set("-i", "--input", "-o", "--output", "-e", "--error")},
	"nohup":    {},
	"setsid":   {boolean: set("-c", "--ctty", "-f", "--fork", "-w", "--wait")},
	"command":  {boolean: set("-p")},
	"builtin":  {},
	"time":     {boolean: set("-p")},
	"sudo":     {value: set("-u", "-g", "-h", "-p", "-C", "-D", "-r", "-t", "-U", "--user", "--group"), boolean: set("-n", "-E", "-H", "-S", "-A", "-b", "-P", "--non-interactive", "--preserve-env")},
	"doas":     {value: set("-u", "-C"), boolean: set("-n")},
	"unbuffer": {},
}

var legacyNice = regexp.MustCompile(`^-\d+$`)

// parseFlags consumes the options of a wrapper starting at argv[i]. ok is false
// for an option the spec does not know, or a dynamic word.
func parseFlags(bin string, argv []string, dyn []bool, i int, sp wspec) (next int, ok bool) {
	for i < len(argv) {
		a := argv[i]
		if dyn[i] {
			return i, false
		}
		if a == "--" {
			return i + 1, true
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			return i, true
		}
		if bin == "nice" && legacyNice.MatchString(a) {
			i++
			continue
		}
		if strings.HasPrefix(a, "--") {
			name, _, hasEq := strings.Cut(a, "=")
			switch {
			case sp.value[name] && hasEq, sp.boolean[name] && !hasEq, sp.boolean[name] && name == "--preserve-env":
				i++
			case sp.value[name]:
				i += 2
			default:
				return i, false
			}
			continue
		}
		switch {
		case sp.value[a]:
			i += 2
		case sp.boolean[a]:
			i++
		case len(a) > 2 && sp.value[a[:2]] && !sp.boolean[a[:2]]: // -n5, -oL: a joined value
			i++
		default:
			return i, false // a cluster or an option we do not know
		}
	}
	return i, true
}

// wrapperSkip returns how many leading words of argv belong to the wrapper
// (so the wrapped command starts at argv[skip]). ok is false when argv[0] is
// not a wrapper. skip < 0 means it is one but its arguments cannot be read.
func wrapperSkip(bin string, argv []string, dyn []bool) (skip int, ok bool) {
	sp, isWrapper := wrapperSpecs[bin]
	if !isWrapper {
		return 0, false
	}
	i, good := parseFlags(bin, argv, dyn, 1, sp)
	if !good || i > len(argv) {
		return -1, true
	}
	switch bin {
	case "env":
		for i < len(argv) && !dyn[i] && isAssignment(argv[i]) {
			i++
		}
	case "timeout":
		if i >= len(argv) || dyn[i] {
			return -1, true
		}
		i++ // DURATION
	}
	return min(i, len(argv)), true
}

// isAssignment reports NAME=value with a valid shell name.
func isAssignment(s string) bool {
	name, _, ok := strings.Cut(s, "=")
	if !ok || name == "" || name[0] >= '0' && name[0] <= '9' {
		return false
	}
	for _, r := range name {
		if r != '_' && (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

var shells = map[string]bool{"sh": true, "bash": true, "zsh": true, "dash": true, "ash": true, "ksh": true}

// IsShell reports whether bin is a shell that takes -c.
func IsShell(bin string) bool { return shells[bin] }

// unwrapInner parses the command carried by sh -c, ssh and xargs one level
// down and appends its segments after seg (depth+1), up to MaxDepth.
func (w *walker) unwrapInner(seg *Segment) {
	var inner string
	var via string
	var xargsArgv []string

	switch {
	case shells[seg.Bin]:
		s, ok, dynamic := shellDashC(seg)
		if !ok {
			return // script file or interactive: classify() already rates it exec
		}
		if dynamic {
			seg.Opaque = true
			seg.risk("dynamic-shell-c")
			return
		}
		inner, via = s, seg.Bin+" -c"
	case seg.Bin == "ssh":
		s, interactive := sshRemote(seg)
		if interactive {
			seg.risk("interactive")
			return
		}
		inner, via = s, "ssh"
	case seg.Bin == "xargs":
		var okArgs bool
		xargsArgv, okArgs = xargsCommand(seg)
		via = "xargs"
		seg.risk("xargs")
		if !okArgs {
			seg.Opaque = true
			seg.risk("wrapper-args")
			return
		}
		if len(xargsArgv) == 0 {
			return // xargs echo
		}
	default:
		return
	}

	if w.depth+1 > MaxDepth {
		seg.Opaque = true
		seg.risk("depth-cap")
		return
	}

	if xargsArgv != nil {
		sub := &walker{src: strings.Join(xargsArgv, " "), depth: w.depth + 1}
		dyn := make([]bool, len(xargsArgv))
		inSeg := Segment{Op: ";", Via: via, Depth: w.depth + 1, Raw: sub.src, Effect: EffectNone}
		argv, dyn := sub.peel(&inSeg, xargsArgv, dyn)
		if len(argv) == 0 {
			inSeg.Opaque, inSeg.Effect = true, EffectUnknown
		} else {
			inSeg.Argv, inSeg.DynArg = argv, dyn
			inSeg.Bin = path.Base(argv[0])
			inSeg.Sub = subcommand(inSeg.Bin, argv[1:])
			for _, a := range argv[1:] {
				if strings.HasPrefix(a, "-") && a != "-" {
					inSeg.Flags = append(inSeg.Flags, a)
				}
			}
			inSeg.Effect = classify(&inSeg)
		}
		w.pendingInner = append(w.pendingInner, inSeg)
		return
	}

	segs, err := parseAt(inner, w.depth+1, via)
	if err != nil {
		seg.Opaque = true
		seg.risk("inner-parse-error")
		return
	}
	for i := range segs {
		if segs[i].Op == "" {
			segs[i].Op = ";"
		}
	}
	w.pendingInner = append(w.pendingInner, segs...)
}

// shellDashC finds the script string of `sh -c script`. ok is false when the
// invocation has no -c (a script file or an interactive shell).
func shellDashC(seg *Segment) (script string, ok, dynamic bool) {
	args := seg.Argv[1:]
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") || strings.HasPrefix(a, "--") {
			if a == "--" {
				return "", false, false
			}
			continue
		}
		if strings.Contains(a[1:], "c") {
			if i+1 >= len(args) {
				return "", false, false
			}
			return args[i+1], true, seg.DynArg[1+i+1]
		}
	}
	return "", false, false
}

var sshValueFlags = map[string]bool{
	"-b": true, "-c": true, "-D": true, "-E": true, "-e": true, "-F": true, "-I": true, "-i": true,
	"-J": true, "-L": true, "-l": true, "-m": true, "-O": true, "-o": true, "-p": true, "-Q": true,
	"-R": true, "-S": true, "-W": true, "-w": true,
}

// sshRemote returns the remote command of `ssh [opts] host cmd...`.
func sshRemote(seg *Segment) (cmd string, interactive bool) {
	args := seg.Argv[1:]
	i := 0
	for i < len(args) {
		a := args[i]
		if a == "--" {
			i++
			break
		}
		if !strings.HasPrefix(a, "-") {
			break
		}
		if sshValueFlags[a] {
			i += 2
			continue
		}
		i++
	}
	i++ // the host
	if i >= len(args) {
		return "", true
	}
	return strings.Join(args[i:], " "), false
}

var xargsValueFlags = set("-a", "-d", "-E", "-e", "-I", "-i", "-L", "-l", "-n", "-P", "-s",
	"--arg-file", "--delimiter", "--max-args", "--max-procs", "--max-chars", "--max-lines", "--replace", "--eof")
var xargsBoolFlags = set("-0", "-r", "-t", "-p", "-x", "-o", "--null", "--no-run-if-empty", "--verbose", "--interactive", "--exit", "--open-tty")

// xargsCommand returns the command xargs will run. ok is false when an option is
// not one we know (the command word cannot then be located reliably).
func xargsCommand(seg *Segment) (cmd []string, ok bool) {
	args := seg.Argv[1:]
	i := 0
	for i < len(args) {
		a := args[i]
		if seg.DynArg[1+i] {
			return nil, false
		}
		if a == "--" {
			i++
			break
		}
		if !strings.HasPrefix(a, "-") {
			break
		}
		name, _, hasEq := strings.Cut(a, "=")
		switch {
		case strings.HasPrefix(a, "--") && xargsValueFlags[name] && hasEq, strings.HasPrefix(a, "--") && xargsBoolFlags[name] && !hasEq:
			i++
		case xargsValueFlags[a]:
			i += 2
		case xargsBoolFlags[a]:
			i++
		case len(a) > 2 && !strings.HasPrefix(a, "--") && xargsValueFlags[a[:2]]: // -n1, -I{}
			i++
		default:
			return nil, false
		}
	}
	if i >= len(args) {
		return nil, true
	}
	return args[i:], true
}

// subcommand returns the subcommand word by the arity table, skipping the
// global flags of bin that take a value (git -C path log -> log).
func subcommand(bin string, args []string) string {
	spec, ok := arity[bin]
	if !ok {
		return ""
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			return ""
		}
		if strings.HasPrefix(a, "-") {
			if spec[a] {
				i++
			}
			continue
		}
		return a
	}
	return ""
}
