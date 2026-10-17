# Outpost

Outpost is the per-machine application host. It uses `libs/app/app-host` for
plugin processes and HTTP routing, and `libs/security/auth` for verification.
Tether and Cerberus are peer plugins. The host imports neither application.

Run `go test -race ./...` and `go vet ./...`. `make build` builds `bin/outpost`.

Machine inventories and pairing credentials live outside the repository.
Plugin environments are exact and do not inherit the host environment.
Pairings authorize one plugin; child processes never receive controller bearer
credentials. Cerberus owns approval and policy for its operations.

Changing the host must preserve Tether shim processes. Native supervision uses
process-only termination; plugin shutdown drains its control loops, leaving
shim-hosts in their independent sessions. SSH remains the deployment and
recovery path when Outpost itself is unavailable.
