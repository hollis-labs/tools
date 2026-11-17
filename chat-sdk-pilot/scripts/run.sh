#!/usr/bin/env bash
set -euo pipefail
pilot_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cache=$("$pilot_dir/scripts/prepare.sh")
exec "$cache/bin/pilot" -state "${PILOT_STATE_DIR:-$cache/state}" -agent "$pilot_dir/agents/pilot" -web "$pilot_dir/web/dist" "$@"
