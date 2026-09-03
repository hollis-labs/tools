# Provider Capture and Background Transfer Execution Plan

**Date:** 2026-09-03

**Project:** fe-clipper (`PRJ-20260903-0001`)

**Torque plan:** `CW-20260903-0030`

**Specification:**
[`2026-09-03-provider-capture-background-transfer.md`](../specs/2026-09-03-provider-capture-background-transfer.md)

**Canonical architecture:**
`/Users/chrispian/dev/hollis-labs/apps/fragments-engine/docs/browser-capture-reader-architecture.md`

## Execution model

Torque is the live execution graph. Every child task is manual until the
orchestrator deliberately promotes a dependency-ready task. Execute by dependency
eligibility. Waves are integration gates; independent adapter tasks inside a wave
should use isolated worktrees and can proceed concurrently when their shared
contracts are integrated.

The clipper integration checkout is
`/Users/chrispian/dev/projects/fe-clipper`. FE is a parallel, separately owned
repository. Do not edit it from clipper tasks.

The parallel FE plan is `CW-20260903-0029` in project
`PRJ-20260514-0004`. Cross-project dependencies are limited to FE contract,
capture-service, provider-completion, and final conformance handoffs.

## Wave 1 — Capture client foundation

Gate: the extension has a provider-adapter registry, generated/validated FE
client, and durable manifest-first capture jobs while preserving current generic
capture behavior.

| Task | Purpose | Depends on |
|---|---|---|
| `CW-20260903-0032` | Introduce PageCaptureAdapter registry and generic fallback | — |
| `CW-20260903-0048` | Consume pinned generated FE types/validators and capability discovery | FE `0031`, clipper `0032` |
| `CW-20260903-0049` | Persist and submit resumable manifest-first capture jobs | FE `0036`, clipper `0048` |

`CW-20260903-0032` can start in parallel with FE's contract task. Once FE
`0031` lands, `0048` is the shared schema handoff. Provider tasks can begin after
`0032` and `0048` without waiting for the complete FE backend.

## Wave 2 — Provider extraction

Gate: all adapters emit stable identity, attributed document data, ordered media,
variants, annotations, capability coverage, and warnings through the shared
contract.

| Task | Purpose | Depends on |
|---|---|---|
| `CW-20260903-0051` | Capture Pinterest identity, metadata, and original/thumbnail image variants | `0032`, `0048` |
| `CW-20260903-0050` | Capture Instagram images, reels/video, and ordered mixed galleries | `0032`, `0048` |
| `CW-20260903-0053` | Capture YouTube identity, metadata, posters, and transcript candidates | `0032`, `0048` |
| `CW-20260903-0052` | Extend generic articles with meaningful hero/inline media | `0032`, `0048` |

These four tasks are intended to run in parallel after the shared adapter and
contract layers are integrated. Each owns provider-specific files and fixtures;
shared registry/type changes remain orchestrator-owned integration work.

## Wave 3 — Background media transfer

Gate: per-variant browser acquisition and status survive MV3 lifecycle, exclude
credentials, and support optimistic partial/retry behavior.

| Task | Purpose | Depends on |
|---|---|---|
| `CW-20260903-0054` | Stream browser-preferred variants safely and resumably | `0049`, `0050`, `0051`, `0052`, `0053` |
| `CW-20260903-0055` | Add accepted/pending/partial/failure/retry UX | `0049`, `0054` |

## Wave 4 — FE conformance and acceptance

Gate: the pinned contracts and real-page scenarios pass against FE, and the
extension branch is reviewable with complete verification evidence.

| Task | Purpose | Depends on |
|---|---|---|
| `CW-20260903-0056` | Verify contract/provider/partial/idempotency/security scenarios | FE `0038`, `0039`, `0040`; clipper `0050`-`0055` as declared in Torque |
| `CW-20260903-0057` | Final review, repository gates, documentation, and release/PR handoff | `0056` |

FE cross-project acceptance task `CW-20260903-0058` waits on clipper `0056`, not
on the clipper's final documentation/review task. Both repositories can therefore
finish their final review work independently after shared conformance is green.

## Cross-project coordination

- FE owns schema source and generated artifact publication. Never patch FE from a
  clipper task or copy-edit generated types by hand.
- A contract ambiguity is reported on FE task `0031` or as a new FE-owned task;
  do not resolve it through a private extension payload.
- Keep provider fixtures sanitized and deterministic. Live-page tests are manual
  smoke evidence, not a brittle network-dependent CI requirement.
- Clipper owns browser DOM/page-state extraction bugs. FE owns canonical identity
  resolution policy, provider completion, persistence, blob custody, and Reader
  behavior.
- Coordinate schema version changes before regenerating the client or merging
  dependent adapter branches.

## Task completion protocol

For each Torque task:

1. Confirm all FE and clipper dependencies are integrated and green.
2. Read the full task, required subtodos, specification, and applicable
   architecture sections.
3. Inspect current extension code/tests before choosing implementation details.
4. Use an isolated task worktree/branch when another write task is active.
5. Add deterministic fixtures and focused tests with behavior changes.
6. Run task-specific gates and affected extension regression tests.
7. Review the full diff and rerun key checks from the integration checkout.
8. Integrate shared registry/contracts before provider branches, then integrate
   providers before background transfer.
9. Attach commit, commands/results, permissions, generated artifacts, warnings,
   risks, and skipped checks to Torque; close required subtodos only with evidence.
10. Run each complete wave gate before promoting dependent work.

## Stop and escalation conditions

Continue through all dependency-ready work. When an FE handoff is pending,
continue disjoint clipper tasks rather than stopping the orchestrator.

Escalate when evidence would require a competing FE contract, a new permission or
credential boundary, provider-specific transport, automatic video custody,
cross-repository writes, weakened exit criteria, or destructive handling of
existing capture behavior.

New in-scope work becomes an explicit plan child with dependencies. Useful
out-of-scope findings become a separate task in the owning project.
