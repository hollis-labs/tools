# The home node

## Running it

For a Cerberus-managed deployment, adapt the checkout path in `caster.cerberus.yaml` before importing it. The example `caster.cerberus.yaml` defines `caster-uat`, an `os_service` (systemd user unit via `supervisor: auto`, lingering on, so it survives logout and reboot) running the repo-built artifact `./bin/caster serve -addr 127.0.0.1:8991`. Check it with `cerberus resource status caster-uat`; after changing source run `cerberus resource deploy caster-uat --ack` (a plain `go install` does not update the service). The service and the `caster` CLI on that machine share one store under `~/.local/share/caster`, so `caster put` there is immediately visible to remote clients.

## Using a home node from other machines

One machine is the **home node**: it owns the store, resolves Torque and Tesseract, and runs `caster serve` (bound to loopback). Every other machine, human or agent, is a client of it: the CLI and `caster mcp` call the home node's HTTP API through the same service contract, with no local store.

```sh
# on the home node, once per client
caster serve                                  # loopback; reach it over tailnet HTTPS (below) or an SSH forward
caster pair -scope read,resolve,write -label work-laptop     # prints a one-use code (5 min)

# on the client (the URL is what the client can reach: https://<name>.<tailnet>.ts.net, or http://localhost:8991 through `ssh -L 8991:localhost:8991 <home>`)
caster login https://home-node.<tailnet>.ts.net:8443 <code>
caster whoami                                 # who the home node takes you for, your scopes, expiry
caster put -n notes/today < today.md          # now stored on the home node
caster remote                                 # mode, reachability, scopes, days left;   caster renew   extends 30 days
```

`login` redeems the code and stores the device token with the **secrets lib** (OS keychain, else its managed 0600 file store on headless Linux; `-ref` picks another reference such as `op://…`). The saved profile (`~/.config/caster/remote.json`) holds only the URL and that reference, never the token. After login every command on that machine uses the home node, including `caster mcp`, so an agent platform only needs `{"command": "caster", "args": ["mcp"]}` (give the absolute path where `PATH` is minimal). `CASTER_LOCAL=1` forces a local store (use it on the home node itself); `CASTER_REMOTE` + `CASTER_TOKEN_REF` configure a machine without a profile. `caster logout` forgets the home node locally; end the device itself with `caster revoke <dev_…>` there.

The client refuses plain http except to loopback or a tailnet address (`-allow-http` / `CASTER_ALLOW_HTTP=1` to accept it), never follows redirects, and says what to do when its token is expired or revoked. Commands that only make sense on the home node (`serve`, `token`, `pair`, `paths`) say so instead of acting.


## HTTPS on the tailnet

Caster speaks plain HTTP on loopback. TLS is terminated in front of it by Tailscale Serve, which uses the node's tailnet certificate (HTTPS Certificates must be enabled for the tailnet in the admin console; the hostname ends up in public certificate-transparency logs, as any tailnet HTTPS name does).

```sh
deploy/tailscale-serve.sh        # tailscale serve --bg --https=8443 http://127.0.0.1:8991, then prints the status
tailscale serve --https=8443 off # undo
```

The script defaults to port 8443; set `CASTER_TS_HTTPS_PORT` to choose another port. The URL is then `https://home-node.<tailnet>.ts.net:8443`: tailnet-only, never the public internet (that would be Funnel, which is not used). The browser UI, the CLI (`caster login`), `caster mcp` and curl all use it; every request still needs a device token, and the UI shell is the only unauthenticated path. Serve config lives in `tailscaled`, not in Cerberus, so re-run the script after a node rebuild; a Cerberus-managed step is still to do.
