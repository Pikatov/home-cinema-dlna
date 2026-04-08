#!/bin/bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT_DIR"

echo "== Sensitive Pattern Scan =="
if rg -n --hidden -S "(AKIA|SECRET|TOKEN|PASSWORD|PRIVATE KEY|BEGIN RSA|BEGIN OPENSSH|Authorization:|Bearer )" . \
  --glob '!**/.git/**' \
  --glob '!scripts/prepublish_check.sh'; then
  echo
  echo "Sensitive-looking data found."
  exit 1
else
  echo "No obvious sensitive patterns found."
fi

echo
echo "== Go Tests =="
GOPROXY=off GOSUMDB=off GOMODCACHE="${GOMODCACHE:-$HOME/go/pkg/mod}" GOCACHE="$ROOT_DIR/.cache/go-build" go test ./...

echo
echo "== Untracked Files =="
git ls-files -o --exclude-standard || true

echo
echo "== Git Status =="
git status --short
