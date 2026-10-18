# Security

Conduit is pre-release software. Security fixes target the current main
branch; there are no supported release lines yet.

Report suspected vulnerabilities through [GitHub private vulnerability
reporting](https://github.com/hollis-labs/tools/security/advisories/new).
Include the affected tool, revision and a minimal reproduction with synthetic
data. Do not put credentials, private notes or real command output in a public issue.

Conduit parses commands and records policy decisions; parsing alone does
not isolate a process. Require a sandbox where confinement matters. Local
operators can approve commands, while MCP clients cannot approve their own
requests. Protect the state directory and use narrowly scoped API tokens.

Redaction reduces accidental disclosure; it cannot identify every possible
secret. Live terminal output is not redacted. Keep HTTP on loopback or behind
a trusted TLS proxy. See [the contributor guide](CONTRIBUTING.md) for the
authorization, sandbox and fail-closed invariants and their regression tests.
