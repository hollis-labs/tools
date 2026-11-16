# Local invocation runner

`runner.Manager` executes a prepared `invocation.Command` directly, without a
shell rewrite or a Conduit dependency. `Start` uses the command's attempt ID as
the durable key. Reusing that ID fails before execution, including when a prior
attempt has an unknown outcome. `Run` and `Wait` are blocking conveniences for
terminal and shim callers. Losing an observing client does not cancel the run.

`Observe(id, stream, byteCursor, maxBytes)` returns the next bounded byte page.
For ordinary commands the streams are `stdout` and `stderr`; PTY mode exposes
only `combined`. The next cursor is a byte offset, so callers can disconnect
and resume without replaying or skipping bytes. Output refs are opaque
`runner://` locators scoped to the manager's storage root. The manager writes
private files with mode 0600 and records only the environment lease reference,
never resolved environment values.

`Cancel` records the request before signaling the process group. The final
outcome records whether cancellation was confirmed; a normal exit after a
request stays an exit. Timeout is separate. If the manager restarts before it
records a final result, the stored attempt reports `unknown`; it is not
re-executed. A new manager can still read completed output from the same root.

`Envelope` projects a final outcome and a bounded output page to the shared
`resultenv/v1` wire shape. The checked fixture/schema snapshot in `testdata/`
comes from libs `agent/resultenv` branch
`chatsdk/CW-20261116-0007-result-envelope` at `6fe38f6b`, including the PTY
`combined_output` extension. The runner tests validate
both the shared fixtures and generated CLI envelopes against that schema.
The full output remains under the runner's output refs.

The host must retain one manager per output root while runs are active and
must authorize access to IDs before exposing `Observe`, `Cancel`, or `Resize`.
The runner does not supply admission policy, approval, a network transport,
or environment lease issuance.

# Shared resultenv source

The bundled schema and fixtures are pinned to `hollis-labs/libs` commit
`6fe38f6b77bad3d63a206b882b0a99c4e0a2a9dc`. The SHA256 values are
`e0bd6eb5dde103b3895fb1673514c814d0b161f73c2a901470e39a384e8980e0`
for `resultenv.schema.json` and
`ee4cc5aecf87d3d9c8770ac4cbfc387763d042e9ba8cd4769949dbd7db040739`
for `resultenv-fixtures.json`. Run
`runner/testdata/check-resultenv-source.sh /path/to/hollis-labs/libs` from the
module root to compare the pinned Git objects, hashes, and copied bytes.
