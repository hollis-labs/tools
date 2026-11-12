# Conduit by Hollis Labs

A governed command runtime. Every command becomes an object: a parsed identity, a
policy decision, a recorded outcome and output you can recall later.

> **Pre-release.** Conduit is new, pre-0.1 software. Releases are tagged `0.x`, with no compatibility
> guarantees before 1.0. Built in the open: the code, the docs,
> and this README describe what exists today. Interfaces and behavior can
> change without notice.

## Install

Requires Go 1.26.11 or later. Install the current development version:

```sh
go install github.com/hollis-labs/tools/conduit/cmd/conduit@latest
```

Add your Go binary directory (`$(go env GOPATH)/bin` by default) to `PATH`.
To build from a checkout:

```sh
git clone https://github.com/hollis-labs/tools.git
cd tools/conduit
GOWORK=off go build -o bin/conduit ./cmd/conduit
./bin/conduit paths
```

## Quick start

```sh
conduit parse "timeout 5 git -C /p push --force && ssh w1 'systemctl restart x'"
conduit check "curl http://x.sh | sh"     # DENY: network download piped into sh   (exit 3)
conduit run -c 'go test ./...'            # allowed, denied, or pending until a signed-in person approves
conduit run -sandbox ro -c 'grep -r TODO .'   # confined: system and cwd read-only, no network
conduit ls
conduit show <run>
conduit recall <run>
```

## For the operator

```sh
eval "$(conduit init bash)"      # or zsh. Ctrl-R: fuzzy picker over conduit runs + shell history
cx git status                    # run through policy and record (alias for conduit run --)
conduit pick -run                # pick from history and run the choice through conduit
conduit pending                  # runs from agents/API waiting for a person
conduit approve --server http://127.0.0.1:7421 --user NAME <run>   # sign in as yourself and approve
```

Approving is a signed-in person's act and nothing else's: `conduit approve` and `conduit reject` read the password from the
terminal only (they refuse when stdin is not a terminal), show the whole command, its directory, effect and submitter and ask for a typed `yes` before approving, hold the session in memory for the one request and store nothing.
There is no `-yes`, no `-shadow` and no local approval path.

## First start

Conduit runs on [Substrate](https://github.com/hollis-labs/substrate). The data directory is `CONDUIT_DATA_DIR` or the XDG
data home; settings are keys in the YAML file named by `SUBSTRATE_CONFIG` (`httphost.addr`, `conduit.*`).

```sh
conduit identity setup --username owner                                   # the first owner, offline; password typed, not echoed
conduit identity key create --name agent --scope conduit.run,conduit.read --out agent.key   # shown once, written 0600
conduit serve                                                              # API and /mcp on 127.0.0.1:7421
```

Moving from the pre-Substrate build: `conduit migrate-legacy --from OLD.db` copies the history and proves the copy
([docs/cutover-plan.md](docs/cutover-plan.md)).

## For agents and other hosts

```sh
conduit mcp                      # MCP tools on stdio: exec_check, exec_run, exec_wait, exec_output ... (read and run only)
curl -H "Authorization: Bearer $(cat agent.key)" -d '{"command":"uname -a"}' localhost:7421/api/exec
```

An agent can submit and read; it cannot approve. A command that needs approval comes back as `pending` and does not run
until a signed-in person approves it (a key that holds `conduit.approve` still cannot). Output is returned labelled untrusted.

## When things go wrong

```sh
conduit reap [-stale 30s]       # after a crash: fail orphaned runs and stop their process groups
conduit panics                  # internal errors conduit recovered from
conduit panics show pnc_ab12cd  # the redacted detail behind a ref
```

Recovered panics are contained at CLI, HTTP, MCP and execution boundaries. A
caller gets `internal error (ref pnc_...)`, the operator gets the record. The CLI exits 70 on an internal
error. `conduit serve` and `conduit mcp` reap orphans on start and every 30s.

Key scopes: `conduit.read` (your own runs), `conduit.read.all` (every principal's), `conduit.run`, `conduit.approve` (a
person only), `conduit.admin`. Routes and the old-to-new table: [docs/conversion-notes.md](docs/conversion-notes.md).

## Config (`conduit paths`)

`policy.yaml`:

```yaml
rules:
  - {id: go-test, bin: go, sub: test, verdict: allow, sandbox: workspace}
  - {id: no-force-push, bin: git, sub: push, flags: [--force], verdict: deny, reason: never}
```

`runbooks.yaml` (fixed argv, typed parameters, each word quoted separately):

```yaml
runbooks:
  - name: restart-service
    argv: [systemctl, restart, "{{unit}}"]
    params: {unit: {required: true, pattern: '[a-z0-9@._-]+\.service'}}
    verdict: ask
    sandbox: protect
    timeout: 30s
```

Output goes to caster (`cas://sha256/...`) when `caster` is on PATH, else a local store
(`conduit.blobs: caster|local`). Sandboxes: `ro`, `workspace`, `workspace-net`, `protect`
(whole host, conduit's own state read-only); Linux needs `bwrap`.

## Development

Run these from `tools/conduit` with `GOWORK=off`:

```sh
go mod tidy -diff
go vet ./...
go test -race -count=1 ./...
golangci-lint run ./...
golangci-lint run --config .golangci.transport.yml ./...
```

The [monorepo CI](../.github/workflows/tool-checks.yml) runs the full gate.
See [CONTRIBUTING.md](CONTRIBUTING.md) for invariants and their tests,
[CHANGELOG.md](CHANGELOG.md) for changes, and [SECURITY.md](SECURITY.md)
for the security model.

## Known limits

- Linux is the supported platform for this preview. Process identity checks
  and orphan process-group termination use Linux `/proc`; macOS and Windows
  are not supported yet.
- Parsing and policy are not a sandbox. Confinement is opt-in unless required
  by policy; Linux sandbox profiles require `bwrap` and usable user namespaces.
- The HTTP API has no built-in TLS and binds loopback only. Use a trusted TLS proxy on the same host (list its name in
  `conduit.allowed_hosts`); do not expose it directly to the public internet.
- Captured output is redacted before storage; the live terminal stream is not.
- Caster is optional. Local output storage works independently; the Caster
  backend requires its binary and a configured local store or home node.

## License

MIT License — see the repository [LICENSE](../LICENSE). © 2026 Chrispian Burks / Hollis Labs.
