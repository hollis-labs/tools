# Chat SDK standalone pilot

This is a local integration fixture, not a hosted service. It loads an authored
stable agent directory, registers that identity with `chathost`, keeps one durable
conversation, and writes Claude-shaped JSONL with `transcript`. A deterministic
fake turn is the default. `/tool nonzero` and `/tool lost` use
`localinvocation` and show their `resultenv/v1` receipts in kit-cards.
`/gate` is off by default; `-gate` enables a fake GatePort that records a
decision but never executes an effect.

## Run

From this directory:

```sh
./scripts/run.sh
```

Open `http://127.0.0.1:7088/` for a controller and
`http://127.0.0.1:7088/?role=viewer` for a viewer. The wrapper accepts pilot
flags, for example `./scripts/run.sh -addr 127.0.0.1:7092 -gate`.
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

## Dependency and storage boundary

`scripts/prepare.sh` pins exact commits for the unmerged SDK, wrapper, chat,
and card branches. It makes disposable aggregate checkouts outside this repo.
The frontend Gate card branch contains an older copy of several tool-card
files; for those merge conflicts, the aggregate keeps the newer tool-card
version and adds the Gate card files. `go.work` is generated in the external
cache; no `replace` or nested module is committed here. The script does not
push or edit dependency repositories.

`chathost` owns attachment and writer admission. The transcript owns native
Claude-shaped rows. The invocation journal records preparation, while runner
receipts record observed execution outcome. A lost observer shows `unknown`;
it does not silently retry. The fake GatePort only demonstrates the connector
and presentation path; its decision is not execution authority. The local
HTTP server binds to loopback by default and has no external authentication.

This pilot uses a deterministic fake reply and does not configure a real model.
The agent directory is reloaded at startup; its current SDK host layer has no
durable store adapter to `chathost`, so this pilot loads the definition and
registers the durable binding explicitly. The browser projects chathost output
through kit-chat's chatstream binding; a direct server-side chatstream adapter
is still an integration seam. These gaps are tracked on the owning tasks.
