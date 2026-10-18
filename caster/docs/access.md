# Access and security

Authorization lives in the service layer, so the CLI, MCP and HTTP are held to the same rules by the same code; a transport only stamps who is calling. The CLI and stdio MCP stamp the local operator (the process owner, all scopes); HTTP stamps whoever its bearer token resolves to. No identity is refused.

| Scope | Allows |
|---|---|
| `caster.read` | `get`/`list`/`log`/`search`/`scan`/`expand`/`actions` over the local store (`cas://`, `caster://`) |
| `caster.resolve` | the same for refs backed by another system (`torque://`, `tesseract://`), read **as caster**. Separate so a read-only holder cannot read everything the home node can reach |
| `caster.write` | `put`, `snap` |
| `caster.act` | actions with effects on other systems (`save-handoff`) |
| `caster.observe` | the shadow-mode feed (records refs and verdicts, returns counts; it validates candidates, so it can learn whether an id exists) |
| `caster.admin` | shadow reports and labels, listing and revoking devices |

`scan`/`expand` treat a ref the caller lacks the scope for as unresolved, never as forbidden.

**Pairing** (the Tether Outpost shape): at the local terminal, `caster pair -scope read,resolve -label laptop` prints a one-use code (5 min by default, 1 h max). The client redeems it once (`caster login <url> <code>`, which calls `POST /v1/pair/exchange`) for a `cst_` device token (30 days; only the SHA-256 of codes and tokens is stored; every failure answers the same 401; the route is rate-limited and takes at most 4 KiB). Scopes can only be narrowed at exchange and renewal, `POST /v1/renew` extends a device, `caster devices` lists, `caster revoke <dev_…>` ends one on its next request. Pairing grants can only be created at the home node's own terminal, never over HTTP, so a leaked token cannot enroll anything. `caster pair -scope observe -token-file PATH` redeems locally and writes the token (0600), for a client on the same machine such as the Ops Chat feed.

**Identity sources** (`caster serve`, tried in order): device tokens; Tether-issued JWTs when `CASTER_TETHER_URL` is set (`aud=caster`, scopes `caster.<verb>`, verified offline against Tether's JWKS with go-auth; `CASTER_JWT_ISSUER` pins `iss`, `CASTER_JWT_BOOTSTRAP_KEY` is a PEM or JWKS file used before the first fetch); and the local operator's static key (the token file `caster token` prints, `caster.*`). Tether cannot yet mint `caster.*` scopes for paired devices or launch grants, which is why caster has its own pairing for now. Issued-token revocation relies on the token's short lifetime: Tether requires a credential to read its deny-list and the poller is not wired.

**Known limits.** One shared owner, no per-namespace permissions inside caster. `caster.resolve` reads Tesseract/Torque with the home node's access, so grant it deliberately. Same-uid processes can read the store and token files directly.
