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

The host must retain one manager per output root while runs are active and
must authorize access to IDs before exposing `Observe`, `Cancel`, or `Resize`.
The runner does not supply admission policy, approval, a network transport,
or environment lease issuance.
