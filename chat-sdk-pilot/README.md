# Chat SDK standalone pilot

This is a local integration fixture, not a hosted service. It loads an authored
stable agent directory through `agentdir.Host` and the SDK loop, registers that
identity with `chathost`, keeps one durable conversation, and writes
Claude-shaped JSONL with `transcript`. The SDK model is deterministic by
default. `/tool nonzero` and `/tool lost` use
`localinvocation` and show their `resultenv/v1` receipts in kit-cards.
`/gate` is off by default; `-gate` enables a fake GatePort that records a
decision but never executes an effect.

## Run

From this directory:

```sh
./scripts/run.sh
```

Open `http://127.0.0.1:7088/` for side-by-side controller and viewer panes.
The wrapper accepts pilot flags, for example
`./scripts/run.sh -addr 127.0.0.1:7092 -gate`.
`PILOT_STATE_DIR` selects the persistent state directory. The default is in
the external pilot cache. `PILOT_CACHE_DIR` selects that cache; the default is
`/tmp/chat-sdk-pilot-$UID`. The compiled binary runs from the cache.

## Test

```sh
./scripts/test.sh
```

The command builds the pinned dependencies and web page, runs Go tests, then
starts compiled local binaries for one headless Playwright scenario. It prints
the temporary state path. `PILOT_CHROME` can select an existing Chrome binary;
the default is `/usr/bin/google-chrome`.

The test checks cursor replay, viewer input refusal, controller takeover,
detach and reattach, distinct nonzero and unknown tool outcomes, the
invocation root/child IDs against Claude `tool_use`/`tool_result`, zero gate
calls while off, and a fake decision ID in the transcript while on.
The Go suite also checks the SDK directory host reloads the authored definition
per turn and that the SSE event stream replays after a cursor and ends on detach.

## Dependency and storage boundary

`scripts/prepare.sh` pins exact merged commits for libs, tools and
frontend-support, plus a temporary kit-chat attach-contract candidate. It
makes disposable aggregate checkouts outside this repo. `go.work` is generated
in the external cache; no `replace` or nested module is committed here. The
script does not push or edit dependency repositories.

`chathost` owns attachment and writer admission. Tool and approval cards are
stored as immutable JSON files; chathost commits their content-addressed
references, so either view can recover them from its cursor using `/api/card`.
`agentdir.Host` reloads the authored definition each turn, and its fake model
runs through the SDK loop. The transcript owns native Claude-shaped rows. The
invocation journal records preparation, while runner receipts record observed
execution outcome. A lost observer shows `unknown`;
it does not silently retry. The fake GatePort only demonstrates the connector
and presentation path; its decision is not execution authority. The local
HTTP server binds to loopback by default and has no external authentication.

This pilot uses a deterministic fake model and does not configure a real model.
The SDK directory host shares the fixed conversation ID with `chathost`, while
`chathost` retains the durable conversation and transcript. The local SDK
admission ledger is a fixture, not a production persistence implementation.
The SDK host emits model deltas through `TurnStream`; the pilot verifies they
match the final answer before committing that answer to `chathost`. The browser
projects committed chathost events through kit-chat's chatstream binding;
`/api/events/stream` replays them over SSE. Direct live model delta forwarding
into that durable stream remains an integration seam. These gaps are tracked
on the owning tasks.
