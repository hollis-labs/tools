# tools

Command-line tools and small utilities from Hollis Labs, in one public repository. Each tool lives in its own top-level directory `<name>/`. A Go tool may be its own Go module (`<name>/go.mod`, module path `github.com/hollis-labs/tools/<name>`, tags `<name>/vX.Y.Z`); a tool in any other language is a plain directory.

It is not: a place for libraries (see `libs`), for application projects, or for retired code.

## Start Here

- `README.md` - what this repository is and how to use it.
- `<name>/` - one top-level directory per imported repository. Code arrives through `scripts/import-repo`.
- `scripts/` - all tooling (`repo.conf` holds the per-repository settings).
- `scan-public.allow.d/` - owner-reviewed allowlists for the public-hygiene scan, one `<name>.allow` per imported repository.
- `.github/workflows/` - `guards.yml` runs layout and scan-public on every push and pull request; `modules.yml` plans the changed Go modules (`scripts/changed-modules`) and runs `go vet` and `go test` on each.

## Commands

```sh
scripts/check-layout                          # layout guard
scripts/scan-public                           # secrets / hosts / private paths, tree and history
scripts/scan-public --source <path-or-url>    # scan a repository before importing it
scripts/import-repo [--target DIR] <source> [<name>]
scripts/release [--apply] <name> <version>    # Go tools only; dry run by default; never pushes
scripts/changed-modules [--base REV] [--all]  # JSON list of Go modules a change touches
```

## Boundaries

- Bring code in with `scripts/import-repo <source> [<name>]`, not by copying files, so history survives. It lands at `<name>/` at the repository root (flat, no groups), keeping original commits, authors and dates, and merges with a merge commit that records the source name, its HEAD and its tags. Old tags are not carried; they are listed in the merge commit message. Work on a branch, not main; set `GIT_FILTER_REPO` if `git-filter-repo` is not on PATH.
- Stop conditions. `scripts/import-repo` refuses, before writing anything, a source whose history (any commit, any ref) holds a file over 10 MiB, a submodule (`.gitmodules` or a gitlink) or Git LFS. These need an owner decision; there is no override flag. Do not work around them (no squashing, no history rewrite of the source) without that decision.
- Clean break: no shims, redirects, aliases, deprecation stubs or transitional re-exports. The original repository is deleted by the owner after the import; nothing here points back at it.
- This repository is public. Nothing secret, no token, internal host name, internal address or personal path may enter a file, a commit message or CI config. `scripts/import-repo` scans the source (tip and every commit) with `scripts/scan-public` first, and a finding stops the import. Allowlist entries (`RULE-ID PATH-GLOB REASON`, in `scan-public.allow.d/<name>.allow`) are owner-reviewed; the reason says why, and never quotes a secret value or a literal address. `scripts/check-layout` rejects entries without a reason.
- Go modules: module path `github.com/hollis-labs/tools/<name>`, `go.mod` at `<name>/go.mod` and nowhere else (not at the root, never nested). No `go.work` (git-ignored), no `replace` directive in a committed `go.mod`. Guard: `scripts/check-layout`.
- Modules are versioned independently. Tag only `<name>/vX.Y.Z` (for example `folio/v0.1.0`). No bare `vX.Y.Z`, no repository-wide version. `scripts/release` never pushes; a tag push is the owner's decision.
- After an import of a Go tool, the module line and the old module path in its `.go` and `.md` files are rewritten to the new path, requirements are untouched, and its own `AGENTS.md`, `CLAUDE.md`, `LICENSE` and `lefthook.yml` are dropped (history keeps them). Add `<name>/CHANGELOG.md` before the first release.
- Non-Go tools are plain directories: no module or tag machinery.
- Documentation names, in prose, only things a stranger can see in this repository or on the public Hollis Labs organization.

## Do Not

- Add a `CLAUDE.md`; `AGENTS.md` is the single instruction file.
- Create the GitHub repository, push, or tag on your own; those are the owner's steps.
- Copy files in by hand instead of importing, or import a repository that tripped a stop condition.
