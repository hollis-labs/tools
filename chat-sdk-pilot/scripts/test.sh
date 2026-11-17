#!/usr/bin/env bash
set -euo pipefail
pilot_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cache=$("$pilot_dir/scripts/prepare.sh")
(cd "$pilot_dir" && GOWORK="$cache/go.work" go test ./... -count=1)
test_state=$(mktemp -d "$cache/e2e-XXXXXXXX")
echo "Playwright state: $test_state"
(cd "$pilot_dir/web" && PILOT_BIN="$cache/bin/pilot" PILOT_AGENT="$pilot_dir/agents/pilot" PILOT_WEB="$pilot_dir/web/dist" PILOT_TEST_STATE="$test_state" node node_modules/@playwright/test/cli.js test)
