# Shadow mode: `caster shadow`

Measures how noisy detection is before any card is shown. A UI or ingester hands each message to `observe`; caster runs every extractor (including the low-confidence ones: bare ULIDs → `tesseract://item/…`, bare 64-hex → `cas://…`), validates each candidate with its resolver, applies the card/quiet policy as a **dry run**, and records the outcome. It returns counts only, so nothing can leak into the UI, and it never stores message text (only ref, extractor, state, verdict).

```sh
caster shadow observe -source chat:<session>/<msg> < message.txt   # shadow: 6 candidates, 3 resolved, 2 would show
caster shadow report [-since 24h] [-json]    # per extractor: candidates, resolve rate, would-show, distinct refs, precision
caster shadow label <ref> true|false         # human verdict on a detected ref -> precision
```

Verdicts: `card` (resolved, high confidence), `quiet_index` (resolved, low confidence: never shown), `ignore` (did not resolve), `error` (resolver could not answer), `skipped` (over the 50-resolves-per-text budget). Identical text at the same extractor generation (`ref.Generation`, bump it when an extractor changes) is recorded once; later sightings count as repeats. HTTP: `POST /v1/observe`, `GET /v1/report?since=24h`, `POST /v1/label`; MCP: `caster_observe`, `caster_shadow_report`, `caster_shadow_label`.

**Feeding it from a chat UI.** Ops Chat (`apps/ops-chat`) sends finished messages through an observe-only dev-server endpoint that keeps a paired `caster.observe`-only token in its Node process (see its README, `VITE_CASTER_SHADOW=1`). Any other browser client should do the same: the token must not reach the browser, and a generic pass-through proxy would let any web page drive the whole API.

Known false-positive classes (from the first run): a task id inside a URL path matches `torque-id`. Not extracted yet: git SHAs, `org/repo#N`, bare file paths.
