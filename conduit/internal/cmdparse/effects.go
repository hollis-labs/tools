package cmdparse

import "strings"

// arity lists, per binary, the global flags that consume a value; a binary
// present here has subcommands.
var arity = map[string]map[string]bool{
	"git":        {"-C": true, "-c": true, "--git-dir": true, "--work-tree": true, "--namespace": true},
	"docker":     {"-H": true, "--host": true, "-c": true, "--context": true, "--config": true, "-l": true},
	"kubectl":    {"-n": true, "--namespace": true, "--context": true, "--kubeconfig": true, "-s": true, "--server": true},
	"npm":        {"--prefix": true, "-C": true},
	"pnpm":       {"--dir": true, "-C": true},
	"yarn":       {"--cwd": true},
	"go":         {"-C": true},
	"cargo":      {"--manifest-path": true, "-C": true},
	"systemctl":  {"-H": true, "--host": true, "-M": true},
	"apt":        {},
	"apt-get":    {},
	"brew":       {},
	"gh":         {"-R": true, "--repo": true},
	"pip":        {},
	"pip3":       {},
	"make":       {},
	"tmux":       {"-L": true, "-S": true, "-f": true},
	"caster":     {},
	"conduit":    {},
	"journalctl": {"-u": true, "-n": true},
}

// binEffect is the default effect of a binary with no subcommand rule.
var binEffect = map[string]Effect{
	// read
	"ls": EffectRead, "cat": EffectRead, "head": EffectRead, "tail": EffectRead, "less": EffectRead,
	"more": EffectRead, "grep": EffectRead, "egrep": EffectRead, "fgrep": EffectRead, "rg": EffectRead,
	"ag": EffectRead, "fd": EffectRead, "wc": EffectRead, "sort": EffectRead, "uniq": EffectRead,
	"cut": EffectRead, "tr": EffectRead, "echo": EffectRead, "printf": EffectRead, "pwd": EffectRead,
	"date": EffectRead, "whoami": EffectRead, "id": EffectRead, "uname": EffectRead, "hostname": EffectRead,
	"stat": EffectRead, "file": EffectRead, "du": EffectRead, "df": EffectRead, "ps": EffectRead,
	"tree": EffectRead, "diff": EffectRead, "cmp": EffectRead, "which": EffectRead, "type": EffectRead,
	"basename": EffectRead, "dirname": EffectRead, "realpath": EffectRead, "readlink": EffectRead,
	"jq": EffectRead, "yq": EffectRead, "awk": EffectRead, "column": EffectRead, "nl": EffectRead,
	"sha256sum": EffectRead, "md5sum": EffectRead, "shasum": EffectRead, "seq": EffectRead,
	"true": EffectRead, "false": EffectRead, "test": EffectRead, "[": EffectRead, "sleep": EffectRead,
	"uptime": EffectRead, "free": EffectRead, "lsof": EffectRead, "env": EffectRead, "printenv": EffectRead,
	"tput": EffectRead, "nproc": EffectRead, "xxd": EffectRead, "od": EffectRead, "strings": EffectRead,
	"caster-ls": EffectRead, "dig": EffectNetwork, "nslookup": EffectNetwork,

	// write
	"cp": EffectWrite, "mv": EffectWrite, "mkdir": EffectWrite, "touch": EffectWrite, "tee": EffectWrite,
	"ln": EffectWrite, "chmod": EffectWrite, "chown": EffectWrite, "chgrp": EffectWrite, "install": EffectWrite,
	"tar": EffectWrite, "unzip": EffectWrite, "zip": EffectWrite, "gzip": EffectWrite, "gunzip": EffectWrite,
	"patch": EffectWrite, "sed": EffectRead, "cd": EffectNone, "export": EffectNone, "unset": EffectNone,
	"set": EffectNone, "alias": EffectNone, ":": EffectNone, "wait": EffectNone, "return": EffectNone, "exit": EffectNone, "read": EffectNone, "source": EffectExec, ".": EffectExec, "mktemp": EffectWrite,

	// network
	"curl": EffectNetwork, "wget": EffectNetwork, "ssh": EffectNetwork, "scp": EffectNetwork,
	"rsync": EffectNetwork, "nc": EffectNetwork, "ncat": EffectNetwork, "ping": EffectNetwork,
	"sftp": EffectNetwork, "telnet": EffectNetwork, "traceroute": EffectNetwork,

	// exec: runs arbitrary code
	"sh": EffectExec, "bash": EffectExec, "zsh": EffectExec, "dash": EffectExec, "ash": EffectExec, "ksh": EffectExec,
	"python": EffectExec, "python3": EffectExec, "node": EffectExec, "ruby": EffectExec, "perl": EffectExec,
	"php": EffectExec, "deno": EffectExec, "bun": EffectExec, "eval": EffectExec, "exec": EffectExec,
	"xargs": EffectExec, "make": EffectExec, "npx": EffectExec, "just": EffectExec, "watch": EffectExec,

	// destructive
	"rm": EffectDestructive, "rmdir": EffectWrite, "dd": EffectDestructive, "shred": EffectDestructive,
	"truncate": EffectDestructive, "mkfs": EffectDestructive, "wipefs": EffectDestructive,
	"kill": EffectDestructive, "pkill": EffectDestructive, "killall": EffectDestructive,
	"shutdown": EffectDestructive, "reboot": EffectDestructive, "halt": EffectDestructive,
	"fdisk": EffectDestructive, "parted": EffectDestructive, "mount": EffectDestructive, "umount": EffectDestructive,
}

// subEffect gives (bin, subcommand) -> effect. A missing subcommand falls back
// to the "*" entry, then to unknown.
var subEffect = map[string]map[string]Effect{
	"git": {
		"status": EffectRead, "log": EffectRead, "diff": EffectRead, "show": EffectRead, "blame": EffectRead,
		"rev-parse": EffectRead, "ls-files": EffectRead, "remote": EffectRead, "describe": EffectRead,
		"shortlog": EffectRead, "grep": EffectRead, "cat-file": EffectRead, "ls-tree": EffectRead,
		"rev-list": EffectRead, "reflog": EffectRead, "config": EffectRead, "branch": EffectRead,
		"tag": EffectRead, "stash": EffectWrite, "worktree": EffectWrite,
		"add": EffectWrite, "commit": EffectWrite, "checkout": EffectWrite, "switch": EffectWrite,
		"restore": EffectWrite, "merge": EffectWrite, "rebase": EffectWrite, "cherry-pick": EffectWrite,
		"revert": EffectWrite, "mv": EffectWrite, "rm": EffectWrite, "init": EffectWrite, "apply": EffectWrite,
		"am": EffectWrite, "submodule": EffectWrite,
		"fetch": EffectNetwork, "pull": EffectNetwork, "push": EffectNetwork, "clone": EffectNetwork,
		"reset": EffectWrite, "clean": EffectWrite,
	},
	"docker": {
		"ps": EffectRead, "images": EffectRead, "logs": EffectRead, "inspect": EffectRead, "version": EffectRead,
		"info": EffectRead, "stats": EffectRead, "top": EffectRead, "compose": EffectExec,
		"run": EffectExec, "exec": EffectExec, "build": EffectExec, "start": EffectWrite, "stop": EffectWrite,
		"restart": EffectWrite, "pull": EffectNetwork, "push": EffectNetwork,
		"rm": EffectDestructive, "rmi": EffectDestructive, "prune": EffectDestructive, "kill": EffectDestructive,
		"system": EffectDestructive, "volume": EffectDestructive, "network": EffectWrite,
	},
	"kubectl": {
		"get": EffectRead, "describe": EffectRead, "logs": EffectRead, "top": EffectRead, "version": EffectRead,
		"config": EffectRead, "api-resources": EffectRead, "explain": EffectRead,
		"apply": EffectWrite, "create": EffectWrite, "patch": EffectWrite, "edit": EffectWrite,
		"scale": EffectWrite, "rollout": EffectWrite, "label": EffectWrite, "annotate": EffectWrite,
		"exec": EffectExec, "port-forward": EffectNetwork, "cp": EffectWrite,
		"delete": EffectDestructive, "drain": EffectDestructive, "cordon": EffectWrite,
	},
	"npm": {
		"ls": EffectRead, "list": EffectRead, "view": EffectNetwork, "outdated": EffectNetwork, "audit": EffectNetwork, "search": EffectNetwork,
		"test": EffectExec, "run": EffectExec, "start": EffectExec, "build": EffectExec, "exec": EffectExec,
		"install": EffectNetwork, "i": EffectNetwork, "ci": EffectNetwork, "publish": EffectNetwork,
		"uninstall": EffectWrite, "init": EffectWrite,
	},
	"go": {
		"version": EffectRead, "env": EffectRead, "list": EffectRead, "vet": EffectRead, "doc": EffectRead,
		"fmt": EffectWrite, "mod": EffectNetwork, "get": EffectNetwork, "install": EffectNetwork,
		"build": EffectExec, "test": EffectExec, "run": EffectExec, "generate": EffectExec, "tool": EffectExec,
		"work": EffectWrite, "clean": EffectWrite,
	},
	"cargo": {
		"check": EffectExec, "build": EffectExec, "test": EffectExec, "run": EffectExec, "clippy": EffectExec,
		"fmt": EffectWrite, "metadata": EffectRead, "tree": EffectRead, "install": EffectNetwork,
		"add": EffectNetwork, "publish": EffectNetwork, "clean": EffectWrite, "doc": EffectExec,
	},
	"systemctl": {
		"status": EffectRead, "is-active": EffectRead, "is-enabled": EffectRead, "list-units": EffectRead,
		"list-unit-files": EffectRead, "show": EffectRead, "cat": EffectRead, "list-timers": EffectRead,
		"start": EffectWrite, "stop": EffectDestructive, "restart": EffectWrite, "reload": EffectWrite,
		"enable": EffectWrite, "disable": EffectWrite, "daemon-reload": EffectWrite, "mask": EffectWrite,
		"poweroff": EffectDestructive, "reboot": EffectDestructive,
	},
	"journalctl": {"*": EffectRead},
	"gh": {
		"pr": EffectNetwork, "issue": EffectNetwork, "repo": EffectNetwork, "run": EffectNetwork,
		"api": EffectNetwork, "release": EffectNetwork, "auth": EffectNetwork, "workflow": EffectNetwork,
	},
	"apt":     {"list": EffectRead, "show": EffectRead, "search": EffectRead, "update": EffectNetwork, "install": EffectNetwork, "upgrade": EffectNetwork, "remove": EffectDestructive, "purge": EffectDestructive},
	"apt-get": {"update": EffectNetwork, "install": EffectNetwork, "upgrade": EffectNetwork, "remove": EffectDestructive, "purge": EffectDestructive},
	"brew":    {"list": EffectRead, "info": EffectRead, "search": EffectRead, "update": EffectNetwork, "install": EffectNetwork, "upgrade": EffectNetwork, "uninstall": EffectDestructive},
	"tmux":    {"ls": EffectRead, "list-sessions": EffectRead, "list-windows": EffectRead, "capture-pane": EffectRead, "send-keys": EffectExec, "kill-session": EffectDestructive, "kill-server": EffectDestructive},
	"caster":  {"get": EffectRead, "resolve": EffectRead, "head": EffectRead, "ls": EffectRead, "log": EffectRead, "search": EffectRead, "scan": EffectRead, "expand": EffectRead, "paths": EffectRead, "put": EffectWrite, "snap": EffectWrite},
	"conduit": {"parse": EffectRead, "check": EffectRead, "ls": EffectRead, "show": EffectRead, "recall": EffectRead, "run": EffectExec},
	"pip":     {"list": EffectRead, "show": EffectRead, "install": EffectNetwork, "uninstall": EffectDestructive},
	"pip3":    {"list": EffectRead, "show": EffectRead, "install": EffectNetwork, "uninstall": EffectDestructive},
	"make":    {"*": EffectExec},
}

// classify rates a peeled, non-dynamic segment. Flags can raise the rating
// (git push --force, find -delete, sed -i); they never lower it.
func classify(s *Segment) Effect {
	bin := s.Bin
	e, known := binEffect[bin]
	if !known {
		e = EffectUnknown
	}
	if m, ok := subEffect[bin]; ok {
		if se, ok := m[s.Sub]; ok {
			e = se
		} else if se, ok := m["*"]; ok {
			e = se
		} else if s.Sub == "" && bin != "git" {
			// `docker` / `kubectl` with no subcommand (help, version flags)
			if !known {
				e = EffectRead
			}
		} else if !known || e == EffectNone {
			e = EffectUnknown
		}
	}
	// mkfs.ext4 and friends
	if strings.HasPrefix(bin, "mkfs") {
		e = EffectDestructive
	}

	has := func(flags ...string) bool {
		for _, f := range s.Flags {
			for _, w := range flags {
				if f == w {
					return true
				}
				// combined short flags: -rf, -fr, -xf ...
				if len(w) == 2 && len(f) > 2 && f[0] == '-' && f[1] != '-' && strings.Contains(f[1:], w[1:]) {
					return true
				}
			}
		}
		return false
	}

	switch bin {
	case "git":
		switch s.Sub {
		case "push":
			if has("--force", "-f", "--force-with-lease", "--delete", "-d", "--mirror", "--prune") {
				e = EffectDestructive
			}
		case "reset":
			if has("--hard", "--merge", "--keep") {
				e = EffectDestructive
			}
		case "clean":
			if has("-f", "--force", "-x", "-d") {
				e = EffectDestructive
			}
		case "checkout", "restore", "switch":
			if has("--force", "-f", "--discard-changes") || hasWord(s, ".") && s.Sub != "switch" {
				e = EffectDestructive
			}
		case "branch":
			if has("-D", "-d", "-m", "-M", "--delete", "--move", "-c", "-C", "--copy") {
				e = EffectWrite
			}
			if has("-D") {
				e = EffectDestructive
			}
		case "tag":
			if has("-d", "-a", "-s", "-f", "--delete") || len(positional(s)) > 0 {
				e = EffectWrite
			}
		case "config":
			if len(positional(s)) >= 2 || has("--unset", "--add", "--edit", "-e") {
				e = EffectWrite
			}
		case "stash":
			if hasWord(s, "drop") || hasWord(s, "clear") {
				e = EffectDestructive
			}
		case "remote":
			if hasWord(s, "add") || hasWord(s, "remove") || hasWord(s, "rm") || hasWord(s, "set-url") || hasWord(s, "rename") {
				e = EffectWrite
			}
		}
	case "find":
		e = EffectRead
		if has("-delete") {
			e = EffectDestructive
		}
		if has("-exec", "-execdir", "-ok", "-okdir") {
			e = MaxEffect(e, EffectExec)
		}
		if has("-fprint", "-fprintf", "-fls") {
			e = MaxEffect(e, EffectWrite)
		}
	case "sed":
		if has("-i", "--in-place") {
			e = EffectWrite
		}
	case "awk":
		// awk can shell out and write; keep it read unless system()/redirects appear in the program.
		for _, a := range s.Argv[1:] {
			if strings.Contains(a, "system(") || strings.Contains(a, "| \"") || strings.Contains(a, "> \"") {
				e = EffectExec
			}
		}
	case "sort":
		if has("-o") {
			e = EffectWrite
		}
	case "curl":
		e = EffectNetwork
	case "chmod", "chown", "chgrp":
		e = EffectWrite
	case "rm":
		e = EffectDestructive
	case "tar":
		e = EffectWrite
		if has("-t") || hasWord(s, "tf") || hasWord(s, "-tf") {
			e = EffectRead
		}
	case "docker":
		if s.Sub == "compose" {
			e = EffectExec
		}
	case "env", "printenv":
		e = EffectRead
	}
	e = refine(s, e)
	if e == EffectUnknown {
		s.risk("unknown-binary")
	}
	return e
}

func hasWord(s *Segment, w string) bool {
	for _, a := range s.Argv[1:] {
		if a == w {
			return true
		}
	}
	return false
}

// positional returns the non-flag words after bin and its subcommand.
func positional(s *Segment) []string {
	var out []string
	skippedSub := s.Sub == ""
	for _, a := range s.Argv[1:] {
		if strings.HasPrefix(a, "-") {
			continue
		}
		if !skippedSub && a == s.Sub {
			skippedSub = true
			continue
		}
		out = append(out, a)
	}
	return out
}

// Positional exposes the non-flag arguments of a segment (after any
// subcommand) to policy.
func (s *Segment) Positional() []string { return positional(s) }
