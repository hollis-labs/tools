# tools

Go tools and other command-line utilities from Hollis Labs. One public repository, flat layout: every tool is a top-level directory.

## Caster and Conduit

- [Caster](caster/) stores content-addressed notes and drafts, resolves references,
  and exposes one service through CLI, browser, MCP and HTTP.
- [Conduit](conduit/) parses commands, applies policy, records outcomes and lets
  operators approve work submitted by agents.

> **Pre-release.** Both are new tools being built in the open. Their documentation
> describes what exists today; interfaces and behavior can change without notice,
> and there are no compatibility guarantees or tagged releases yet.

```sh
go install github.com/hollis-labs/tools/caster/cmd/caster@latest
go install github.com/hollis-labs/tools/conduit/cmd/conduit@latest
```

Both require Go 1.26.9 or later. Caster and Conduit are independent modules;
Conduit can optionally use the Caster binary to store command output.

## Layout

```
<name>/            one directory per tool
<name>/go.mod      only for Go tools that are modules: github.com/hollis-labs/tools/<name>
scripts/           import-repo, scan-public, check-layout, release, changed-modules
```

A Go module is released on its own with the tag `<name>/vX.Y.Z`; install with
`go install github.com/hollis-labs/tools/<name>@vX.Y.Z` once it is tagged. There is no `go.work` and no `replace`
directive. Non-Go tools are plain directories.

Releasing: write `<name>/CHANGELOG.md` with a `## vX.Y.Z` section, then `scripts/release <name> vX.Y.Z` (dry run) and
`--apply` to create the local tag. The script never pushes.

## Importing a repository

```sh
git switch -c import-<name>
export GIT_FILTER_REPO=/path/to/git-filter-repo   # if not on PATH
scripts/import-repo <path-or-url-of-source> [<name>]
scripts/check-layout
```

The import keeps the original commits, authors and dates (via `git filter-repo`) and merges them with a merge commit that
records the source name, its HEAD and its tags. Old tags are not carried; they are listed in the merge commit message.
It stops, writing nothing, when the source history holds a file over 10 MiB, a submodule or Git LFS, and when the full-history
`scripts/scan-public` scan finds anything that must not be public. Both need an owner decision. Clean break: no shims,
redirects or stubs; the original repository is deleted by its owner afterwards.

## Public hygiene

`scripts/scan-public` scans the tree and every commit for secrets, internal hosts and addresses, and personal paths.
Allowlisted findings are still printed. Entries live in `scan-public.allow.d/<name>.allow` as `RULE-ID PATH-GLOB REASON`;
a reason describes why and never quotes a value or an address.

## License

MIT. See [LICENSE](LICENSE).
