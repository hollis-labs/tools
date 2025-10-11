# Changelog

All notable changes to **git-sandbox** will be documented in this file.

## [1.3.1] - 2025-10-11
### Added
- `-i/--interactive` restored and honored across subshell and `--tmux` modes.
- `-v/--version` flag.
- `--show-commits` (only used during interactive pause).

### Changed
- Default behavior is **non-interactive**: enter immediately and **auto-cleanup** on exit.
- Pre-banner now width-locked to subshell/tmux banner.

### Fixed
- Banner right-edge alignment (ASCII-only text inside box; colored borders).
- Reliable welcome banner inside **tmux** (no pager/flash).

## [1.3.0] - 2025-10-11
### Added
- Theme system (`green`, `blue`, `synth`, `sunset`, `mono`) + `--theme=` flag.
- Optional `--tmux` mode with colored borders and status bar.
- Session lock to avoid multiple concurrent sandboxes.

### Changed
- Made the tool safe for automation: non-interactive by default, auto-cleanup.

### Removed
- Automatic printing of “last 5 commits” (now opt-in via `--show-commits`).

## [1.2.3] - 2025-10-11
### Fixed
- Pre-banner referencing `$ID` before assignment under `set -u` (moved after sandbox creation).
- Spacing bug in `[[ "$THEME" == "sunset" ]]` test.
- Removed `git` pager during setup; no more pre-tmux “flash”.

### Added
- Welcome banner shown inside tmux on entry.

## [1.2.0] - 2025-10-11
### Added
- Embedded docs blocks (`USAGE`, `README`) rendered from within the script.
- Styled sections, rules, and notes for a minimal CLI/TUI feel.

## [1.1.0] - 2025-10-11
### Added
- Robust `-h/--help` extraction from marked doc blocks (no brittle line ranges).
- Safety checks and session cleanups.

## [1.0.0] - 2025-10-11
### Added
- First working prototype: create sandbox branch & worktree, enter subshell, cleanup.

