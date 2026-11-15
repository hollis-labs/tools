# Citadel Operative

Experimental command-line consumer of the Go Agent SDK. It validates authored
agent definitions and runs the actual SDK loop against an explicitly selected
synthetic provider, one in-memory tool, HITL approvals and a closed fixture host.

Build and install with Go 1.26.11 or later:

```sh
go build ./cmd/citadel-operative
go install ./cmd/citadel-operative
```

Once this tool's commit is available remotely, install the exact reviewed version:

```sh
go install github.com/hollis-labs/tools/citadel-operative/cmd/citadel-operative@<commit-or-tag>
```

The module consumes the merged SDK dependency
`github.com/hollis-labs/libs/agent/sdk v0.0.0-20261115211534-c76ea978573d`.
It uses no local replacement or workspace overlay.

From this module directory:

```sh
citadel-operative validate --definition testdata/operative.md
citadel-operative run --fixture allow --definition testdata/operative.md \
  --run-id sample-input --input 'Read the synthetic value.'
```

`validate` checks agentdef v2 structure and reports its semantic digest. It does
not resolve resource pins, authenticate a caller or certify that a host supports
the definition. Definitions are bounded to 1 MiB. `run` also checks the SDK's
supported features and the fixture's permission/resource bindings before effects.

| Explicit fixture | Behavior | Exit |
| --- | --- | --- |
| `allow` | HITL allow-once, one synthetic read, second model turn reports 42 | 0 |
| `deny` | HITL deny, zero tool effects | 3 |
| `pending` | Unresolved HITL ticket, zero tool effects; reports pending ticket presence | 4 |
| `cancel` | Provider simulates cancellation, zero tool effects | 130 |

Caller cancellation (interrupt, termination or a cancelled injected context) also
exits 130. Other execution/validation failures exit 1; argument or missing-host
configuration errors exit 2. Runs print a JSON result including fixture, status,
model calls and synthetic effect count. No prompt, tool arguments or credentials
are emitted as event payloads.

There is no default fixture or production host. `run` without `--fixture` refuses
because production configuration is absent. Endpoint flags, credentials, real
provider configuration and network/filesystem tools are unsupported. The fixture
accepts only an ephemeral definition with permission profile `fixture` and the
`read_value` tool. Its labels and references cannot grant authority outside the
in-memory fixture universe; the scripted model does not interpret endpoint names
or execute definition instructions against external resources.

The host atomically reserves owner/operative/conversation/run ID before model or
tool activity, rechecks the retained immutable reservation at admission, resolves
the real HITL snapshot/outcome, rehashes the operation, and fences current binding
through single-use claim and effect completion. Claims remain consumed after
uncertainty. Separate SDK/command instances sharing one fixture ledger cannot
repeat a run ID, even with changed input, a fresh provider call ID or a replacement
attempt. Distinct logical run IDs remain distinct work, even with equal arguments.

The ledger is deliberately **process-local**. Each independent CLI invocation
starts a new synthetic universe. This is not durable replay protection across
process restarts or a production identity/admission implementation. An installed
production runner still needs authenticated durable run/effect admission,
environment verification and lifecycle ownership supplied by its host. Pending
fixture tickets cannot resume in another invocation.

This is a class 2 local CLI, with an authored `project.yaml`. Current Folio
`substrate-cli` scaffolding adds a Substrate runtime/deployment layer outside this
bounded fixture consumer. No Substrate runtime, deployment descriptor, generated
Folio breadcrumb, authentication provisioning or live application integration is
claimed here.

Checks from this module directory:

```sh
go mod tidy -diff
go mod verify
go vet ./...
go build ./...
go test -race -count=1 ./...
go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...
```

Repository checks run from the tools root: `scripts/check-layout` and
`scripts/scan-public --ref HEAD`. Tests exercise real SDK allow/deny/pending and
cancellation paths, malformed/unsupported definitions, changed digest/resource/
policy, stale binding, uncertain replay across instances/generations and concurrent
run reservations. They establish synthetic consumer behavior, not live deployment
or production authority.
