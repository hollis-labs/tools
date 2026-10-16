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
