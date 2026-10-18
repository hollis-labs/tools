# Actions: `caster actions`, `caster act`

An action is a registered operation on a ref whose parameters bind from ambient context instead of being typed. One exists so far:

```sh
export CASTER_NAMESPACE=project/<id>/workspace/handoffs CASTER_SESSION_ID=<session> CASTER_AGENT_ID=<agent>
caster actions caster://handoffs/today          # what applies, and what context it needs
caster act save-handoff caster://handoffs/today -dry-run
caster act save-handoff caster://handoffs/today  # -> tesseract://item/<id>
```

`save-handoff` pins the source in CAS, then creates a Tesseract **workspace draft** (replaceable, promoted only after review) with provenance (`source_ref`, `snapshot`, `sha256`) in the item's `data`. It is a remote write, reported as such. The idempotency key is content hash + session, so a retry or double click replays the original item instead of creating another; the first run is recorded in the local `action_log`. Namespaces must look like `{scope}/{id}/workspace/{purpose}` and may not be in `user/`. A source that does not resolve causes no write. Over MCP the same thing is `caster_actions` / `caster_act` (with `dry_run`); over HTTP `GET /v1/actions`, `POST /v1/act` (scope `caster.write`).
