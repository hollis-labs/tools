# Access and security

Authorization lives in the service layer, so the CLI, MCP and HTTP are held to the same rules by the same code;
a transport only stamps who is calling. Identity is the Substrate **identity module's**: an owner account (browser
sign-in) and API keys (machines), created **offline** with the binary. There is no open setup form and no
pairing of Caster's own.

| Scope | Allows |
|---|---|
| `caster.read` | `get`/`list`/`log`/`search`/`scan`/`expand`/`actions` over the local store (`cas://`, `caster://`) |
| `caster.resolve` | the same for refs backed by another system (`torque://`, `tesseract://`), read **as caster**. Separate so a read-only holder cannot read everything the home node can reach |
| `caster.write` | `put`, `snap` |
| `caster.act` | actions with effects on other systems (`save-handoff`) |
| `caster.observe` | the shadow-mode feed (records refs and verdicts, returns counts; it validates candidates, so it can learn whether an id exists) |
| `caster.admin` | shadow reports and labels |
| `caster.mcp` | the `/mcp` endpoint on the service (then the per-tool scope above) |

`caster.*` and `*` match all. `scan`/`expand` treat a ref the caller lacks the scope for as unresolved, never as forbidden.

**The local principal.** A CLI command or `caster mcp` on the home node, with no key, runs as `local:operator`:
`caster.read`, `resolve`, `write`, `observe` and **never** `act` or `admin` (the trust of anyone who can run the
binary as that user, agents included). `act`, `shadow report` and `shadow label` need a key: run them as a client.

**Credentials.**

```sh
caster identity setup --username <owner>                    # password on stdin; once, offline
caster identity key create --name <n> --scope caster.read,caster.write --out <file>   # 0600 file, machine caller
caster login <url> <pairing-code>                           # a machine joins a home node; the key goes to the secrets store
```

A pairing code is minted by an administrator over HTTP (`POST /api/identity/pairing-codes`, answered with `pair_...`) and
redeemed once at `POST /api/pair` for an API key. The old `cst_` tokens, pairing grants and device tokens are
**not credentials**: the tables survive as `legacy_grants` and `legacy_devices` (history, hashes included) and
nothing reads them (`TestNoLegacyGrantOrDeviceTokenIsAccepted`).

**Known limits.** One shared owner, no per-namespace permissions inside Caster. `caster.resolve` reads Tesseract
and Torque with the home node's own access, so grant it deliberately. Same-uid processes can read the store and
key files directly.
