#!/usr/bin/env bash
set -euo pipefail

pilot_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cache_root=${PILOT_CACHE_DIR:-/tmp/chat-sdk-pilot-${UID}}
cache="$cache_root/pinned-20261117-v3"
mkdir -p "$cache"

# Exact source commits. All are branch heads observed for this pilot; moving a
# branch does not silently change the fixture. Go and frontend overlays live
# only in the cache. No dependency repository is modified or pushed.
libs_base=7333675be9ad2d4e7737e819712bd5603ac7b142
libs_chathost=190764babe23ece7b912fa0342a65f22ded4f309
libs_gate=6bd314e26db110922db544b17f24c948af10a20d
tools_base=7bfc1d9ebf957492b97d3d73667f4ffa1d058f15
frontend_base=7c5d8d35867d7a93b36077452e45c9f904df9dee
frontend_gate=219f23c5012f8082706d26ad461f201558c29e78

git_identity=(-c user.name='Chat SDK pilot aggregate' -c user.email='pilot@example.invalid')

if [[ ! -f "$cache/libs.ready" ]]; then
  git clone --filter=blob:none --no-checkout https://github.com/hollis-labs/libs.git "$cache/libs" >&2
  git -C "$cache/libs" fetch origin "$libs_base" "$libs_chathost" "$libs_gate" >&2
  git -C "$cache/libs" checkout --detach "$libs_base" >&2
  for sha in "$libs_chathost" "$libs_gate"; do
    git -C "$cache/libs" "${git_identity[@]}" merge --no-edit "$sha" >&2
  done
  printf '%s\n' "$libs_base" "$libs_chathost" "$libs_gate" > "$cache/libs.ready"
fi

if [[ ! -f "$cache/tools.ready" ]]; then
  git clone --filter=blob:none --no-checkout https://github.com/hollis-labs/tools.git "$cache/tools" >&2
  git -C "$cache/tools" fetch origin "$tools_base" >&2
  git -C "$cache/tools" checkout --detach "$tools_base" >&2
  printf '%s\n' "$tools_base" > "$cache/tools.ready"
fi

if [[ ! -f "$cache/frontend.ready" ]]; then
  git clone --filter=blob:none --no-checkout https://github.com/hollis-labs/frontend-support.git "$cache/frontend" >&2
  git -C "$cache/frontend" fetch origin "$frontend_base" "$frontend_gate" >&2
  git -C "$cache/frontend" checkout --detach "$frontend_base" >&2
  if ! git -C "$cache/frontend" "${git_identity[@]}" merge --no-edit "$frontend_gate" >&2; then
    # Gate card was based on an older tool-card commit. Keep the newer tool
    # contract for conflicting files and retain GateApprovalCard's new files.
    git -C "$cache/frontend" diff --name-only --diff-filter=U -z | xargs -0 -r git -C "$cache/frontend" checkout --ours --
    git -C "$cache/frontend" add -A
    git -C "$cache/frontend" "${git_identity[@]}" commit --no-edit >&2
  fi
  printf '%s\n' "$frontend_base" "$frontend_gate" > "$cache/frontend.ready"
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
