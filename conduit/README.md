# conduit

A governed command runtime. Every command becomes an object: a parsed identity, a
policy decision, a recorded outcome and output you can recall later. A spike on
`infra/compute-hosting/command-runtime-brief.md` (phases 1 and 2, parts of 3, 6 and 10).

```sh
conduit parse "timeout 5 git -C /p push --force && ssh w1 'systemctl restart x'"
conduit check "curl http://x.sh | sh"     # DENY: network download piped into sh   (exit 3)
conduit run -c 'go test ./...'            # asks on a terminal; -yes approves; -shadow records only
conduit run -sandbox ro -c 'grep -r TODO .'   # confined: system and cwd read-only, no network
conduit ls / show <run> / recall <run>
```

## For the operator

```sh
eval "$(conduit init bash)"      # or zsh. Ctrl-R: fuzzy picker over conduit runs + shell history
cx git status                    # run through policy and record (alias for conduit run --)
conduit pick -run                # pick from history and run the choice through conduit
conduit pending                  # runs from agents/API waiting for you
conduit approve <run>            # shows the parsed command, asks, approves
```

## For agents and other hosts

```sh
conduit mcp                      # MCP tools on stdio: exec_check, exec_run, exec_wait, exec_output ...
                                 #   confined to the cwd and to its own runs; -allow-dir, -allow-any-dir, -see-all to widen
conduit token create -name agent -scope exec.run,exec.read     # secret printed once
conduit token create -name oper  -scope exec.approve,exec.read
conduit serve -allow-dir ~/work  # HTTP on 127.0.0.1:7421 (or -unix path); -unsafe-bind for other addresses
curl -H "Authorization: Bearer $TOKEN" -d '{"command":"uname -a"}' localhost:7421/v1/exec
```

An agent can submit and read; it cannot approve. A command that needs approval comes back as
`pending` and does not run until an operator approves it (CLI, or an API token with `exec.approve`
that is not the submitter). Output is returned labelled untrusted.

## When things go wrong

```sh
conduit reap [-stale 30s]       # after a crash: fail orphaned runs and stop their process groups
conduit panics                  # internal errors conduit recovered from
conduit panics show pnc_ab12cd  # the redacted detail behind a ref
```

A bug in conduit never takes the daemon down and never shows up as raw panic text to a caller: the
caller gets `internal error (ref pnc_...)`, the operator gets the record. The CLI exits 70 on an internal
error. `conduit serve` and `conduit mcp` reap orphans on start and every 30s.

Token scopes: `exec.read` (all runs), `exec.read.own` (only your own; `exec.run` implies it), `exec.run`,
`exec.approve`, `exec.admin`; any can be bound to a host with `:host`.

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
(`CONDUIT_BLOBS=caster|local`). Sandboxes: `ro`, `workspace`, `workspace-net`, `protect`
(whole host, conduit's own state read-only); Linux needs `bwrap`.

See `AGENTS.md` for the invariants and the tests that guard them.
