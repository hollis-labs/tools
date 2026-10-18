# HTTP API: `caster serve`

```sh
caster serve [-addr 127.0.0.1:8991]
TOKEN=$(cat "$(caster token)")                       # the local operator's key (all scopes); other clients pair, see below
curl -H "Authorization: Bearer $TOKEN" 'localhost:8991/v1/get?ref=CW-20261016-0015'
curl -H "Authorization: Bearer $TOKEN" -d '{"name":"api/x","content":"hi"}' localhost:8991/v1/put
```

Routes: `GET /v1/head|get|list|log|search|actions|devices|report`, `POST /v1/put|snap|scan|expand|act|observe|label|renew|devices/revoke|pair/exchange`, `GET /health` (JSON bodies are the service request structs). A missing ref is HTTP 404 with a Meta body; a bad ref is 400; no identity is 401; a missing scope is 403; an unreachable backend is 502. Serve on loopback and reach it over tailnet HTTPS (Tailscale Serve, see `home-node.md`; the ADR 0062 pattern) or an SSH forward. `GET /` and `/ui/*` serve the browser UI and are the only unauthenticated routes besides `/health` and `/v1/pair/exchange`; `-addr` on another interface is allowed only in enforce mode.
