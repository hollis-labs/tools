# Conversion notes

Old-to-new map and gaps for the Substrate port. Filled in per slice; the old-to-new tables for HTTP,
CLI and MCP arrive with the slices that change them.

## Dropped

| What | Why |
|---|---|
| OpenTelemetry (`CASTER_OTEL_ENABLED`, `CASTER_ENV`, go-otel) | Opt-in, off on the live instance. Not part of the framework shell; follow-up if wanted. |

## Environment and paths (so far)

| Old | New |
|---|---|
| `CASTER_TORQUE_URL` | `caster.torque_url` |
| `CASTER_TESSERACT_MCP` | `caster.tesseract_mcp` |
| `CASTER_TESSERACT_BIN` | `caster.tesseract_bin` |
| `HOLLIS_AUTH`, `HOLLIS_AUTH_CASTER` | `auth.mode` (default `enforce`) |
| `-addr` flag | `httphost.addr` |
| `CASTER_DB_PATH`, `CASTER_WORKSPACE` | unchanged: the data-directory override (apppaths) |
| `workspaces/default/main.db` | `workspaces/default/caster.db` beside it |
| `workspaces/default/blobs/` | `workspaces/default/cas/` |

## Gaps

None open. Observation for the framework: `internal/hostguard` is an app-local copy of Loom's package.
