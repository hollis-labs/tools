#!/usr/bin/env bash
set -euo pipefail

pilot_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cache_root=${PILOT_CACHE_DIR:-/tmp/chat-sdk-pilot-${UID}}
cache="$cache_root/pinned-20261117-v8"
mkdir -p "$cache"

# Exact source commits: merged mains plus the temporary kit-chat attach
# candidate. Moving a branch does not silently change the fixture. No
# dependency repository is modified or pushed.
libs_base=0d6f272e48bf9ef607e4f4609593643be7d3c464
tools_base=4155006d7c83334e2dd3741ca9758f465f540ce1
frontend_base=f101f7c0c35fc238c1062dcd4fffd0e0cc230881
frontend_attach=b200a676b30abb051547548d689d9a0e53c57e8f

if [[ ! -f "$cache/libs.ready" ]]; then
  git clone --filter=blob:none --no-checkout https://github.com/hollis-labs/libs.git "$cache/libs" >&2
  git -C "$cache/libs" fetch origin "$libs_base" >&2
  git -C "$cache/libs" checkout --detach "$libs_base" >&2
  printf '%s\n' "$libs_base" > "$cache/libs.ready"
fi

if [[ ! -f "$cache/tools.ready" ]]; then
  git clone --filter=blob:none --no-checkout https://github.com/hollis-labs/tools.git "$cache/tools" >&2
  git -C "$cache/tools" fetch origin "$tools_base" >&2
  git -C "$cache/tools" checkout --detach "$tools_base" >&2
  printf '%s\n' "$tools_base" > "$cache/tools.ready"
fi

if [[ ! -f "$cache/frontend.ready" ]]; then
  git clone --filter=blob:none --no-checkout https://github.com/hollis-labs/frontend-support.git "$cache/frontend" >&2
  git -C "$cache/frontend" fetch origin "$frontend_base" "$frontend_attach" >&2
  git -C "$cache/frontend" checkout --detach "$frontend_base" >&2
  git -C "$cache/frontend" -c user.name='Chat SDK pilot aggregate' -c user.email='pilot@example.invalid' merge --no-edit "$frontend_attach" >&2
  printf '%s\n' "$frontend_base" "$frontend_attach" > "$cache/frontend.ready"
fi

if [[ ! -f "$cache/go.work" ]]; then
  (cd "$cache" && go work init "$pilot_dir" "$cache/libs/agent/sdk" "$cache/libs/agent/resultenv" "$cache/libs/mesh/core" "$cache/tools/localinvocation")
fi

if [[ -e "$pilot_dir/web/node_modules" && ! -L "$pilot_dir/web/node_modules" ]]; then
  echo 'web/node_modules exists as a real directory; move it before running the pilot' >&2
  exit 1
fi
if [[ ! -d "$cache/frontend/ts/design-kit/node_modules" ]]; then
  (cd "$cache/frontend/ts/design-kit" && npm ci --ignore-scripts) > "$cache/npm-install.log" 2>&1 || { tail -80 "$cache/npm-install.log" >&2; exit 1; }
fi
ln -sfn "$cache/frontend/ts/design-kit/node_modules" "$pilot_dir/web/node_modules"

if [[ ! -f "$cache/frontend-built.ready" ]]; then
  (cd "$cache/frontend/ts/design-kit" &&
    npm run build -w @hollis-labs/design-tokens &&
    npm run build -w @hollis-labs/design-components &&
    npm run build -w @hollis-labs/kit-cards &&
    npm run build -w @hollis-labs/kit-chat) > "$cache/frontend-build.log" 2>&1 || { tail -80 "$cache/frontend-build.log" >&2; exit 1; }
  touch "$cache/frontend-built.ready"
fi

(cd "$pilot_dir/web" && PILOT_FRONTEND_AGGREGATE="$cache/frontend" npm run build) > "$cache/web-build.log" 2>&1 || { tail -80 "$cache/web-build.log" >&2; exit 1; }
mkdir -p "$cache/bin"
(cd "$pilot_dir" && GOWORK="$cache/go.work" go build -o "$cache/bin/pilot" .) >&2
printf '%s\n' "$cache"
