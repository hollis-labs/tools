# MCP: `caster mcp`

Stdio MCP server with `caster_put`, `caster_get`, `caster_head`, `caster_snap`, `caster_ls`, `caster_log`, `caster_search`, `caster_scan`, `caster_expand`. Register it as `{"command":"caster","args":["mcp"]}`. Missing refs are outcomes (`state: not_found`), not errors; `caster_get` truncates at 64 KiB by default. On the home node a bare `caster mcp` runs as the local operator (all scopes, same trust as the CLI). To give agents less than that, run it as a client of the home node with a paired device token (`CASTER_REMOTE` + `CASTER_TOKEN_REF`): then the agent holds exactly the scopes it was paired with.

## Tesseract gateway

MCP-store refs go through a stdio MCP client (`internal/mcpclient`) that launches the server from your Tether catalog entry `~/.tether/catalog/mcp-servers/tesseract.yaml` (today: ssh to the hub), so caster uses the access you already have and holds no credentials. Override with `CASTER_TESSERACT_MCP="tesseract mcp"`. Env values that are secret references are not passed. One server process serves a whole invocation (~1 s ssh start).


## Scoped agent access (Tether catalog)

A dedicated device for agents, with read/write/resolve and nothing else:

```sh
caster pair -scope read,write,resolve -label agents -token-file ~/.config/caster/agent.token   # on the home node
CASTER_REMOTE=https://<home>:8443 CASTER_TOKEN_REF=file:///abs/agent.token caster whoami
```

`deploy/tether/mcp-server.caster.yaml` and `deploy/tether/agent.caster.yaml` are the catalog entries (the MCP server, and a `caster` agent persona that keeps notes and hands around refs). The Tether catalog and gateway live on the hub, so the hub needs the linux `caster` binary (`/var/lib/fabric/bin/caster`) and its own device token (`credentials/caster.token`, label `hub-agents`, so it can be revoked on its own), and the entry points `CASTER_REMOTE` at the home node's tailnet HTTPS URL. Catalog changes go through the plan/approve/apply flow (`tether_catalog_plan`, then `tether catalog approve <plan_id>` at a terminal), and the gateway lists the servers it exposes in its `--servers` argument, which must include `caster`. The device token lasts 30 days; renew it with `caster renew` using the same environment.
