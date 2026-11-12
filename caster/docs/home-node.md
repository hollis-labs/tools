# The home node

## Running it

For a Cerberus-managed deployment, adapt the checkout path in `caster.cerberus.yaml` before importing it. It defines
`caster-uat`, an `os_service` (systemd user unit via `supervisor: auto`, lingering on) running the repo-built artifact
`./bin/caster serve`. The listen address and every other setting are keys in the YAML file named by `SUBSTRATE_CONFIG`
(`httphost.addr`, `caster.torque_url`, `caster.tesseract_mcp`, `caster.tesseract_bin`, `caster.allowed_hosts`); `deploy.yaml`
is the `fleet-install` descriptor (binary, unit drop-in, backup preflight). Data lives in the XDG data directory
(`caster paths`): `caster.db` and `cas/`. Moving from the pre-Substrate service is docs/cutover-plan.md.

First run, offline: `caster identity setup --username <owner>`, then one `caster identity key create ... --out <file>`
per machine caller, then `caster serve`. `GET /api/setup` answers `setup_required: false` once the owner exists.

## Using a home node from other machines

One machine is the **home node**: it owns the store, resolves Torque and Tesseract, and runs `caster serve` (bound to
loopback). Every other machine, human or agent, is a client of it: the CLI and `caster mcp` call the home node's HTTP API
with an identity key, with no local store.

```sh
# on the client; <code> is a pairing code an administrator minted at the home node (POST /api/identity/pairing-codes)
caster login https://home-node.<tailnet>.ts.net:8443 <code>
caster whoami                                 # who the home node takes you for, and the scopes
caster put -n notes/today < today.md          # now stored on the home node
caster remote                                 # mode, reachability, scopes
```

`login` redeems the code and stores the key with the **secrets lib** (OS keychain, else its managed 0600 file store on
headless Linux; `-ref` picks another reference such as `op://...`, `-name` names the credential). The saved profile (`remote.json` in the config
directory) holds only the URL and that reference, never the key. After login every command on that machine uses the
home node, including `caster mcp`. The client refuses plain http except to loopback or a tailnet address
(`CASTER_ALLOW_HTTP=1` accepts it) and never follows redirects. Commands that only make sense on the home node
(`serve`, `migrate`, `identity ...`) are not forwarded.

## HTTPS on the tailnet

Caster speaks plain HTTP on loopback. TLS is terminated in front of it by Tailscale Serve, which uses the node's tailnet
certificate (HTTPS Certificates must be enabled for the tailnet in the admin console; the hostname ends up in public
certificate-transparency logs, as any tailnet HTTPS name does).

```sh
deploy/tailscale-serve.sh        # tailscale serve --bg --https=8443 http://127.0.0.1:8991, then prints the status
tailscale serve --https=8443 off # undo
```

The URL is then `https://home-node.<tailnet>.ts.net:8443`: tailnet-only, never Funnel. Add that name to
`caster.allowed_hosts`: a name outside loopback is served only from a loopback peer (the proxy) carrying exactly one
`X-Forwarded-Proto: https`. Every request still needs a key or the owner's session; the page shell is the only
unauthenticated path. Callers and their Host names: docs/callers.md.
