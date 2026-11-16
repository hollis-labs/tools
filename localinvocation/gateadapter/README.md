# Optional Gatecrasher admission for local invocation

`Adapter` is off by default. When off, `Prepare` and `Start` make no GatePort
calls and discard caller-supplied Gate references. A host opts in explicitly
with a GatePort, AuthorityVerifier, RequestFactory and AdmissionPort. This
package does not enable approval in `entry` or the CLI by itself.

The request factory must bind a verified principal, current runtime binding,
environment, policy, native choices and the supplied digest. The digest covers
argv, source form, cwd, environment reference, invocation identity and runner
options. It excludes decision references, which arrive later. The host must
persist `Prepared` before returning a pending approval to a client, then
reload it on reconnect. `Reconcile` queries GatePort; display text cannot
grant admission. `Cancel` explicitly withdraws the interaction.

`Start` refuses any state except a currently valid native allow decision.
`AdmissionPort.Admit` must atomically claim and fence the attempt through the
actual `Starter.Start` callback. An uncertain claim remains consumed until
host reconciliation. The callback rehashes the prepared operation and
rechecks authority immediately before starting. The returned record contains
the decision and operation references known before execution; a later
execution receipt belongs in a separate sideband record keyed by attempt.
`ProjectReceipts` checks late delivery and execution receipt correlation and
returns that sideband shape with separate refs and an honest execution state.
The host must append it durably; the existing preparation journal is immutable.

The existing Gatecrasher HTTP API does not expose GatePort Request, Await or
Cancel. Use an injected full GatePort and the host's durable journal until
the Gatecrasher owner provides those routes; `ServiceHTTPPort` in the SDK
connector reports those operations as unsupported against the pinned API
contract. No fallback infers approval from a card or CLI text.
