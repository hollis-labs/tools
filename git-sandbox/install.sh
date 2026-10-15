#!/usr/bin/env bash
set -euo pipefail

TARGET="${TARGET:-/usr/local/bin/git-sandbox}"
RAW_URL="${URL:-https://raw.githubusercontent.com/hollis-labs/git-sandbox/main/git-sandbox}"

echo "Installing git-sandbox to $TARGET"
curl -fsSL "$RAW_URL" -o "$TARGET"
chmod +x "$TARGET"

echo "Installed. Version:"
"$TARGET" -v || true
echo "Try: git sandbox -h"

