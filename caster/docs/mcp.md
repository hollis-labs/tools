# MCP: `caster mcp`

Stdio MCP server with `caster_put`, `caster_get`, `caster_head`, `caster_snap`, `caster_ls`, `caster_log`, `caster_search`, `caster_scan`, `caster_expand`. Register it as `{"command":"caster","args":["mcp"]}`. Missing refs are outcomes (`state: not_found`), not errors; `caster_get` truncates at 64 KiB by default. On the home node a bare `caster mcp` runs as the local operator (all scopes, same trust as the CLI). To give agents less than that, run it as a client of the home node with a paired device token (`CASTER_REMOTE` + `CASTER_TOKEN_REF`): then the agent holds exactly the scopes it was paired with.

## Tesseract gateway

MCP-store refs go through a stdio MCP client (`internal/mcpclient`) that launches the server from your Tether catalog entry `~/.tether/catalog/mcp-servers/tesseract.yaml`, so caster uses the access you already have and holds no credentials. Override with `CASTER_TESSERACT_MCP="tesseract mcp"`. Env values that are secret references are not passed. One server process serves a whole invocation.


## Scoped agent access (Tether catalog)

A dedicated device for agents, with read/write/resolve and nothing else:

```sh
caster pair -scope read,write,resolve -label agents -token-file ~/.config/caster/agent.token   # on the home node
CASTER_REMOTE=https://<home>:8443 CASTER_TOKEN_REF=file:///abs/agent.token caster whoami
```

`deploy/tether/mcp-server.caster.yaml` and `deploy/tether/agent.caster.yaml` are the catalog entries (the MCP server, and a `caster` agent persona that keeps notes and hands around refs). Install Caster on the machine running the Tether gateway, set the example entry's command to that binary, and store its paired device token outside the repository in a mode-0600 file. Set `CASTER_REMOTE` to your home node's HTTPS URL and `CASTER_TOKEN_REF` to that token file. Give the device a distinct label so it can be revoked independently. Catalog changes go through the plan/approve/apply flow (`tether_catalog_plan`, then `tether catalog approve <plan_id>` at a terminal), and the gateway lists the servers it exposes in its `--servers` argument, which must include `caster`. Tether resolves secret references in a server's `env` before launching it, so `CASTER_TOKEN_REF: file://…` reaches caster as the token itself; caster accepts that (`CASTER_TOKEN`, or a `cst_…` value in `CASTER_TOKEN_REF`) and never saves or prints it. The `tags` argument may be a string or a list. The device token lasts 30 days; renew it with `caster renew` using the same environment.
