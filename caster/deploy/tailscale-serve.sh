#!/bin/sh
# Put the home node behind tailnet-only HTTPS. caster itself stays plain HTTP
# on loopback; Tailscale terminates TLS with the node's tailnet certificate.
# Needs HTTPS Certificates enabled for the tailnet (admin console > DNS).
# Port 8443 because 443 on this node's tailnet address is taken. Idempotent.
set -eu
PORT="${CASTER_TS_HTTPS_PORT:-8443}"
UPSTREAM="${CASTER_UPSTREAM:-http://127.0.0.1:8991}"
tailscale serve --bg --https="$PORT" "$UPSTREAM"
tailscale serve status
# Undo: tailscale serve --https=$PORT off
