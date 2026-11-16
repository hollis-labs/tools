#!/usr/bin/env bash
set -euo pipefail

# Compare the runner snapshot to the approved immutable libs commit.
# Usage: check-resultenv-source.sh /path/to/hollis-labs/libs
if [[ $# -ne 1 ]]; then
  echo 'usage: check-resultenv-source.sh /path/to/hollis-labs/libs' >&2
  exit 2
fi

source_repo=$1
source_commit=6fe38f6b77bad3d63a206b882b0a99c4e0a2a9dc
snapshot_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)

check() {
  local source_path=$1 copy_name=$2 expected_hash=$3 source_hash copy_hash
  source_hash=$(git -C "$source_repo" show "$source_commit:$source_path" | sha256sum)
  source_hash=${source_hash%% *}
  copy_hash=$(sha256sum "$snapshot_dir/$copy_name")
  copy_hash=${copy_hash%% *}
  if [[ "$source_hash" != "$expected_hash" || "$copy_hash" != "$expected_hash" ]]; then
    echo "resultenv drift: $copy_name source=$source_hash copy=$copy_hash expected=$expected_hash" >&2
    exit 1
  fi
  git -C "$source_repo" show "$source_commit:$source_path" | cmp - "$snapshot_dir/$copy_name"
  echo "ok: $copy_name matches $source_commit ($expected_hash)"
}

check agent/resultenv/schema/resultenv.schema.json resultenv.schema.json e0bd6eb5dde103b3895fb1673514c814d0b161f73c2a901470e39a384e8980e0
check agent/resultenv/testdata/fixtures.json resultenv-fixtures.json ee4cc5aecf87d3d9c8770ac4cbfc387763d042e9ba8cd4769949dbd7db040739
