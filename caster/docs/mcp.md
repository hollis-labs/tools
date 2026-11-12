# MCP: `caster mcp` and `/mcp`

Fifteen tools: `caster_put`, `caster_get`, `caster_head`, `caster_snap`, `caster_ls`, `caster_log`, `caster_search`,
`caster_scan`, `caster_expand`, `caster_actions`, `caster_act`, `caster_observe`, `caster_whoami`,
`caster_shadow_report`, `caster_shadow_label`. Register `{"command":"caster","args":["mcp"]}` for stdio (the framework's
server); the service also serves them at `/mcp` for a key that holds `caster.mcp` plus the tool's own scope. Missing
refs are outcomes (`state: not_found`), not errors; `caster_get` truncates at 64 KiB by default. A refused call is a
tool error with `{"code":"denied"}`. On the home node a bare `caster mcp` runs as the local principal
(`caster.read/resolve/write/observe`, never `act` or `admin`); as a client of a home node (`CASTER_REMOTE` and a key, or a
saved `caster login`) every call is an HTTP call that carries the key, so the service decides.

## Tesseract gateway

Refs backed by Tesseract go through a stdio MCP client (`internal/mcpclient`) that launches the server from your
Tether catalog entry `~/.tether/catalog/mcp-servers/tesseract.yaml`, so Caster uses the access you already have and
holds no credentials. Override with `caster.tesseract_mcp` in the config file. Env values that are secret references are not passed.

## Scoped agent access (Tether catalog)

A dedicated key for agents, with read/write/resolve and nothing else:

```sh
caster identity key create --name agents --scope caster.read,caster.write,caster.resolve,caster.mcp --out <file>   # on the home node
CASTER_REMOTE=https://<home> CASTER_TOKEN_REF=file://<abs path of the file> caster whoami
```

`deploy/tether/mcp-server.caster.yaml` and `deploy/tether/agent.caster.yaml` are catalog entries (the MCP server, and a
`caster` agent persona that keeps notes and hands around refs). On the machine running the Tether gateway the child
reaches the service on loopback (`http://127.0.0.1:8991`); keep the key file outside the repository, mode `0600`.
Prefer `CASTER_TOKEN_REF`; `CASTER_TOKEN` is for a gateway child that can pass nothing else. See docs/callers.md.
