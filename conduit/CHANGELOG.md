# Changelog

All notable changes to conduit are documented here. The format
follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Write the entry for a release here BEFORE cutting its tag: the release workflow
refuses a tag whose CHANGELOG has no heading for it.

## Unreleased

### Added

- Initial scaffold.
- `cmdparse`: shell command parsing into segments, wrappers, effect classes; `sh -c`, `ssh`, `xargs` unwrapped to a depth cap.
- `policy`: Deny > Ask > Allow over parsed segments, fail closed; rules file with sandbox obligations.
- `cmdrun`: process-group exec, timeout and tree kill, credential-scrubbed env, redaction, error classes, bubblewrap sandbox profiles (`ro`, `workspace`, `workspace-net`, `protect`).
- Output stored through caster (local fallback); CommandRun and attempts in SQLite.
- CLI: `parse`, `check`, `run`, `ls`, `show`, `recall`, `pick`, `init bash|zsh`, `pending`, `approve`, `reject`, `cancel`, `runbooks`, `token`, `mcp`, `serve`.
- Fuzzy history picker and embedded shell integration.
- Approval flow with expiry and self-approval refusal; named runbooks with validated, per-word-quoted parameters.
- MCP stdio server (no approval tool) and a scoped, token-authenticated HTTP API.
- Terminal-safe rendering of everything submitted text can reach (approval preview, listings, picker, recall).
- `internal/safe`: panics contained at every boundary (goroutines, HTTP, MCP, CLI, parser, policy, executor) with refs, persisted redacted records, `conduit panics`, exit 70; parser and policy fail closed on a panic.
- Run liveness (heartbeat, owner and process-group identity by pid + start time), `conduit reap`, daemon reaper, graceful shutdown drain.
- `exec.read.own` scope; own-only visibility for MCP agents; secrets redacted in command text in views.
- MCP: working directory confined (`-allow-dir`, `-allow-any-dir`), `-see-all` opt-out for own-only visibility, concurrent tool calls; runbook timeouts are ceilings; shared directory resolver with the HTTP API.
- Classifier hardened after independent review: compound redirects, path-qualified commands, env prefixes, strict wrapper options, flag-driven exec; `FuzzParse` and `FuzzDecide`.
