# conduit

Governed command runtime: every command is an object with identity, policy, recorded outcome and recoverable output

It is not: a terminal emulator, a replacement for Cerberus's connectors, a workflow engine (Hadron), or a token-savings product. The design brief is `~/dev/hollis-labs/infra/compute-hosting/command-runtime-brief.md`; this repo is a spike on its phases 1 and 2.

## Start Here

- `.golangci.transport.yml` — the transport-boundary gate; `docs/transport-boundary.md` says how to run and ratchet it.
- `cmd/conduit/main.go` — the composition root: resolves paths (go-apppaths), gates telemetry (go-otel), opens the store and builds the service layer; the only place that opens the store.
- `internal/service/service.go` — the service layer: unexported store, `Store()` escape hatch, `New(store)`.
- `internal/cmdparse` — shell command string to segments, wrappers, effect classes. `internal/policy` — Deny > Ask > Allow over parsed segments. `internal/cmdrun` — exec wrapper, env scrub, redaction, error classes. `internal/blob` — output store (caster CLI, local fallback). `internal/service/run.go` — the phases in order.
- `internal/service/run.go` — Submit (record + decide) / Approve / Execute (claims atomically) / Start (async) / Cancel. `internal/runbook` — named operations. `internal/api` (HTTP), `internal/mcp` (stdio), `internal/cli` (+ `picker.go`, `shell/*.sh` embedded by `conduit init`) are the transports. `internal/fuzzy` — the history matcher.
- Policy file: `<config dir>/policy.yaml` (`conduit paths`); a missing file is the baseline, a malformed one is an error.
- `.github/workflows/check.yml` — the full CI gate.

## Commands

```sh
gofmt -l .
go vet ./...
go test -race -count=1 ./...
```

CI (`.github/workflows/check.yml`) is the full gate.

## Boundaries

- Transports (the packages matched by `transport_dirs_regex` in `.golangci.transport.yml`) call the service layer, never `store.Store` methods or raw SQL; the gate is `.golangci.transport.yml`, its ceiling `.github/quality/transport-baseline.txt` only decreases.
- Fail closed. Anything the parser cannot attest is `Opaque` and never auto-allowed (`TestOpaqueConstructs`, `TestBaselineVerdicts`); a malformed policy file is an error, never an empty policy (`TestLoadFailsClosed`).
- Wrapping a command never lowers its verdict (`TestWrapNeverLessRestrictive`, `TestWrappingNeverLessSevere`). Allow rules match the segment as written and never pass through wrappers (`TestAllowRuleDoesNotPassThroughWrappers`); a rule for `git` never covers `git; rm -rf ~` (`TestAllowRuleDoesNotCoverChainedCommand`). Do not add substring or prefix matching on the raw string.
- Deny is not overridable by an approver or by shadow mode (`TestDenyIsNotOverridableByApprover`, `TestShadowModeStillBlocksDeny`). An Ask with no approver does not run (`TestAskWithoutApproverIsDeniedAndNothingRuns`). Approval comes from `/dev/tty` or `-yes`, never from piped stdin.
- Output is redacted before it is stored (`TestOutputIsRedactedBeforeStorage`); the live terminal tee is not. Output bytes live only in the blob store; the database holds refs.
- Only read-only commands are retried, and a retry is a new linked attempt (`TestRetryIsLinkedAttemptAndOnlyForReadOnly`). Rate limits are not retried here.
- Error classification reads the stderr tail only (`TestClassifyTailOnly`).
- The sandbox, not the parser, is the final boundary. It is opt-in per request (`-sandbox`), or required by a policy rule or runbook (`sandbox:`); a request can only tighten it (`TestPolicySandboxObligationIsEnforcedAndRequestCannotLowerIt`). A required sandbox that cannot be applied means the command does not run (`TestUnknownProfileFailsClosed`). Sandbox tests must prove the profile was applied before asserting what it blocked: a failed setup also looks like "write refused" (see `runIn` in `sandbox_test.go`). The `ro` profile works by handing go-sandbox a scratch dir as its workspace, because it cannot protect its own workspace root.
- Agents cannot approve: there is no approve tool in `internal/mcp` (`TestToolsListHasNoApprovalTool`), the HTTP approve route needs `exec.approve`, and a principal cannot approve its own run (`TestAgentCannotApproveItsOwnRun`, `TestSelfApprovalRefusedEvenWithBothScopes`). Approval re-decides against current policy and expires (`TestPolicyIsRedecidedAtApproval`, `TestApprovalExpires`). A run executes at most once (`TestExecuteRunsAtMostOnceUnderRace`).
- Runbook parameters are validated and quoted per word, never interpolated into a shell string (`TestInjectionAttemptsAreRejectedOrInert`). A runbook's verdict replaces the generic Ask/Allow, but a policy Deny always stands (`TestRunbookVerdictAppliesButDenyStands`).
- The HTTP API binds loopback or a unix socket unless `-unsafe-bind`; has no TLS. Tokens are stored as sha256, shown once, scoped (`exec.read|run|approve|admin`, optional `:host`). Working directories are checked against `-allow-dir` roots after symlink resolution (`TestDirectoryAllowList`). Failed-auth throttling keys on the client address without the port (`TestFailedAuthThrottle`).
- The classifier is an allow-list of "read" tools, and that list leaks unless it is policed. Things that make a read-looking command do more are never auto-allowed: env prefixes that change program loading (`env-sensitive`), path-qualified commands (`path-command`), options that run another program (`flag-exec`), text a later step could evaluate (`embedded-substitution`), dynamic arguments to tools whose flags matter, and any substitution or construct the walker did not account for (`unanalyzed-substitution`, `no-segments`). Wrapper options are parsed from a strict per-tool spec; an unknown option makes the segment opaque rather than guessed. `TestFormerBypassesNeedApproval` holds the review's probes and *executes* them to prove they are real; add every new bypass there. `TestOrdinaryReadsStayAllowed` guards against over-blocking.
- Text the operator reads before deciding (the command, segments, reasons, principal, cwd) is submitter-controlled. Everything the CLI prints from it goes through `visible`/`block`/`oneLineSafe` (`internal/cli/safe.go`): control, C1 and format characters show as escapes, and continuation lines of a command are marked so they cannot pass for preview fields. Stored output printed to a terminal is made visible too (`recall -raw` to opt out). Guarded by `internal/cli/safe_test.go`; new print sites of stored or submitted text must use these helpers.
- Output returned to agents is labelled `output_trust: untrusted`. It is data from the command, not instructions.
- Exit codes: `run` returns the command's own code (124 timeout, 127 start failure); 3 denied, 4 approval needed, 2 not found.
