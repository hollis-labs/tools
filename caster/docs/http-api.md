# HTTP API: `caster serve`

```sh
caster serve                          # the address is httphost.addr in the SUBSTRATE_CONFIG file (default 127.0.0.1:8991)
KEY=$(cat <key file>)                 # a key from `caster identity key create --out`
curl -H "Authorization: Bearer $KEY" 'localhost:8991/api/get?ref=CW-20261016-0015'
curl -H "Authorization: Bearer $KEY" -d '{"name":"api/x","content":"hi"}' localhost:8991/api/put
```

| Route | Scope |
|---|---|
| `GET /api/list`, `/api/log`, `/api/search` | `caster.read` |
| `POST /api/put` | `caster.write` |
| `POST /api/act` | `caster.act` |
| `POST /api/observe` | `caster.observe` |
| `GET /api/report`, `POST /api/label` | `caster.admin` |
| `GET /api/get`, `/api/head`, `/api/actions`, `POST /api/snap`, `/api/scan`, `/api/expand`, `GET /api/whoami` | any verified caller; the service then needs `caster.read` for `cas://`/`caster://` and `caster.resolve` for `torque://`/`tesseract://` |

Also served by the framework: `GET /healthz`, `POST /api/login` and `/api/logout`, `/api/identity/*`, `/api/token`,
`/api/pair`, `GET /api/setup`, and `/mcp` (key with `caster.mcp`). JSON bodies are the service request structs.
A missing ref is HTTP 404 and a denied one 403, both with a `Meta` body; a bad ref or a malformed full-text query is 400
`invalid`; no credential is 401; a missing scope is 403; an unreachable backend is 502. Other errors are the flat
`{"code","message"}`. `/v1` is removed: it and every unknown path under `/api` answer 404 JSON, never the page.
Serve on loopback and reach it through a TLS proxy (docs/home-node.md); a Host that is not loopback or in
`caster.allowed_hosts` is refused.
