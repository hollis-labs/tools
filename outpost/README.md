# Outpost

A shared per-machine plugin host built on `libs/app/app-host`. Tether provides
session execution and mail; Cerberus provides a thin native unit executor.
Other applications can install independent plugins using the same subprocess
contract. Outpost has no dependency on the Tether hub for Cerberus requests.

`make build` creates `bin/outpost`. The host reads an operator-owned JSON file:

```json
{
  "address": "127.0.0.1:8977",
  "state_dir": "/absolute/outpost/state",
  "credentials_file": "/absolute/outpost/pairings.json",
  "plugins": [
    {
      "id": "tether",
      "command": "/absolute/releases/tether-worker",
      "args": ["plugin"],
      "dir": "/absolute/worker",
      "env": ["HOME=/absolute/home", "PATH=/usr/local/bin:/usr/bin:/bin"],
      "config": {"worker_root": "/absolute/existing-worker-state"}
    },
    {
      "id": "cerberus",
      "command": "/absolute/releases/cerberus-outpost",
      "dir": "/absolute/home",
      "env": ["HOME=/absolute/home", "PATH=/usr/local/bin:/usr/bin:/bin"],
      "config": {"protected_units": "outpost.service"}
    }
  ]
}
```

Paths must be absolute. Each plugin gets exactly its declared environment,
configuration, data directory, and cache directory. The Tether plugin retains
the existing enrollment, device token, mail registrations, provider commands,
and shim state at `worker_root`.

The local operator pairs a controller to one plugin:

```sh
outpost --config /absolute/outpost.json --id controller \
  --plugin cerberus --output /private/controller.token pair
outpost --config /absolute/outpost.json run
outpost --config /absolute/outpost.json --id controller revoke
```

Only a credential hash is stored in `pairings.json`. Deliver the new private
token file to the controller over the existing authenticated deployment
channel. Revocation takes effect on the next request. Credentials are not
printed, placed in child environments, or forwarded to plugins.

Authenticated requests use `/api/plugins/{id}/…` and require scope
`outpost.{id}`. `/healthz` is an unauthenticated readiness endpoint. Plain HTTP
binds only to literal loopback or Tailscale addresses; use a TLS proxy for
public access. The plugin inventory starts completely before HTTP admission.
A failed plugin makes the host exit so its native supervisor can restart it.

The Tether worker binary supports `plugin` as well as its existing enrollment,
mail, shim, and relay commands. Cerberus's `cerberus-outpost` binary implements
the fixed `probe`, `units`, `logs`, `start`, `stop`, and `restart` protocol.
Cerberus performs planning, approval, auditing, and output redaction; its plugin
executes native user-service commands and refuses mutations of the control
plane carrying its request.

For deployment, stage immutable plugin binaries and switch the inventory to
their exact paths. Preserve the prior inventory and native service definition
for rollback. Stop the standalone worker before starting Outpost, since both
claim the same mail socket and enrollment. On Linux use `KillMode=process`;
on macOS avoid switching while sessions are live. SSH is the recovery and
self-upgrade route. The host can be supervised by an existing Cerberus resource
or by systemd/launchd on machines without a local Cerberus controller.
