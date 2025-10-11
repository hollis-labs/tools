# git-sandbox

Safe, throwaway Git **worktrees** to experiment (delete files, refactor, spike) without polluting your main working tree or history. Non-interactive by default so scripts/agents can use it reliably; `-i/--interactive` adds prompts.

> Version: **1.3.1** · Updated: **2025-10-11**

---

## Features

- Creates a sandbox branch & worktree: `sandbox/<timestamp>-<phrase>`
- Enters the sandbox (subshell or `--tmux`) and **auto-cleans on exit** (default)
- Interactive mode (`-i`) adds a pause before entering and a cleanup prompt
- Themes: `green` (default), `blue`, `synth`, `sunset`, `mono`
- Pretty banners with width-locked alignment across pre-banner, subshell, and tmux
- Optional `--show-commits` (only when `-i`)
- Session lock prevents piling up stale sandboxes

---

## Install

### Homebrew (recommended)

```bash
brew tap hollis-labs/tap
brew install git-sandbox
# later:
brew upgrade git-sandbox
```

### Quick (manual)

```bash
curl -fsSL https://raw.githubusercontent.com/hollis-labs/git-sandbox/main/git-sandbox   -o /usr/local/bin/git-sandbox && chmod +x /usr/local/bin/git-sandbox
```

> On Apple Silicon you may prefer `/opt/homebrew/bin`. Ensure the directory is on your `PATH`.

### From a release asset

Download the `git-sandbox` file from **Releases**, place it at `/usr/local/bin/git-sandbox`, and `chmod +x` it.

### Verify

```bash
git sandbox -v
git sandbox -h
```

---

## Usage

```bash
git sandbox "remove old registry"          # non-interactive; enter; auto-clean on exit
git sandbox -i "try deletes"               # interactive; pause + prompt to cleanup
git sandbox --tmux "experiment"            # tmux pane with borders/status
git sandbox --theme=synth "retro vibes"    # pick a theme
git sandbox --end                          # end/cleanup active sandbox
git sandbox --end -k                       # keep branch & worktree
git sandbox -v                             # version
```

### Flags

- `-i, --interactive` — prompts before entering; ask to cleanup  
- `--tmux` — launch inside tmux with colored borders  
- `--theme=NAME` — `green` | `blue` | `synth` | `sunset` | `mono`  
- `--allow-dirty` — allow uncommitted changes (non-interactive refuses by default)  
- `--show-commits` — show last 5 commits during interactive pause  
- `-n, --no-switch` — create sandbox but don’t enter  
- `-e, --end` — end active sandbox  
- `-k, --keep` — keep branch/worktree on end  
- `--no-color` — disable ANSI color  
- `-v, --version` — print version  
- `-h, --help` — help  
- `--readme` — extended docs in terminal

---

## Example Workflows

### “Delete five files and see what breaks”

```bash
git sandbox "try removing legacy"
rm path/to/a path/to/b path/to/c path/to/d path/to/e
# run tests…
exit   # auto-cleans branch/worktree by default
```

### Sandbox for agents (AI/automation)

Give your agent a simple, safe recipe:

1. `git sandbox --allow-dirty "agent-$(date +%H%M)"`  
2. Work inside the **worktree path** printed by the tool  
3. Exit to auto-cleanup (or `git sandbox --end -k` to keep results)

### Persistent sandbox

```bash
git sandbox "long-running spike"
# do work
exit
git sandbox --end -k   # keep worktree + branch for later
```

---

## How It Works

- Verifies repo, creates `sandbox/<timestamp>-<phrase>`
- Adds a worktree at `../_sandboxes/<id>`
- Records a session lock at `.git/.sandbox-session` to avoid pile-ups
- **Non-interactive**: straight in; auto-clean on exit  
- **Interactive**: pause before entering; optional last-5; confirm cleanup
- `--tmux` mode opens a framed pane with a welcome banner (no pager “flash”)

---

## Troubleshooting

- **“git: 'sandbox' is not a git command”** — Ensure the file is named `git-sandbox`, executable, and on your `PATH`.
- **Permission errors on macOS** — Use `/usr/local/bin` (SIP blocks `/usr/bin`). Remove quarantine if needed:  
  `xattr -d com.apple.quarantine /usr/local/bin/git-sandbox`
- **Shebang issues** — First line must be `#!/usr/bin/env bash` (no leading BOM/whitespace).
- **tmux not found** — Install `tmux` or omit `--tmux`.
- **Org/SSO issues with `gh`** — Approve token for the org:  
  `gh auth refresh -h github.com -s repo,read:org` and authorize SSO in the browser.

---

## Contributing

PRs and issues welcome. Please run `shellcheck` and test on macOS + Linux.

---

## License

This project is public domain via **The Unlicense**. See [LICENSE](LICENSE).

