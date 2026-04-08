#!/bin/bash
set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
APP_DIR="$SCRIPT_DIR/HomeCinemaControlSwift/Home Cinema.app"

if [[ "${1:-}" == "--build" || ! -d "$APP_DIR" ]]; then
  "$SCRIPT_DIR/build_app.sh"
fi

open "$APP_DIR"
