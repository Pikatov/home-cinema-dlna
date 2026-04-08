#!/bin/bash
set -euo pipefail

STATE_DIR=${1:?state_dir}
PID_FILE="$STATE_DIR/server.pid"

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
  kill "$PID" 2>/dev/null || true
  for _ in $(seq 1 20); do
    if ! kill -0 "$PID" 2>/dev/null; then
      rm -f "$PID_FILE"
      echo "running=0"
      exit 0
    fi
    sleep 0.25
  done
  echo "Server did not stop in time."
  exit 1
fi

rm -f "$PID_FILE"
echo "running=0"
