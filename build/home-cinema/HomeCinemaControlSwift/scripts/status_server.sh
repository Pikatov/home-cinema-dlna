#!/bin/bash
set -euo pipefail

STATE_DIR=${1:?state_dir}
PID_FILE="$STATE_DIR/server.pid"
LOG_FILE="$STATE_DIR/server.log"

echo "state_dir=$STATE_DIR"
echo "log_path=$LOG_FILE"

if [[ ! -f "$PID_FILE" ]]; then
  echo "running=0"
  exit 0
fi

PID=$(cat "$PID_FILE" 2>/dev/null || true)
if [[ -z "${PID:-}" ]]; then
  rm -f "$PID_FILE"
  echo "running=0"
  exit 0
fi

if kill -0 "$PID" 2>/dev/null; then
  echo "running=1"
  echo "pid=$PID"
else
  rm -f "$PID_FILE"
  echo "running=0"
fi
