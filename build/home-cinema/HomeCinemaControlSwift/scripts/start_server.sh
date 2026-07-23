#!/bin/bash
set -euo pipefail

SERVER_PATH=${1:?server_path}
MEDIA_DIR=${2:?media_dir}
PORT=${3:?port}
STATE_DIR=${4:?state_dir}
PID_FILE="$STATE_DIR/server.pid"
LOG_FILE="$STATE_DIR/server.log"

mkdir -p "$STATE_DIR"
touch "$LOG_FILE"

if [[ -f "$PID_FILE" ]]; then
  OLD_PID=$(cat "$PID_FILE" 2>/dev/null || true)
  if [[ -n "${OLD_PID:-}" ]] && kill -0 "$OLD_PID" 2>/dev/null; then
    echo "running=1"
    echo "pid=$OLD_PID"
    echo "log_path=$LOG_FILE"
    exit 0
  fi
  rm -f "$PID_FILE"
fi

nohup "$SERVER_PATH" --media-dir "$MEDIA_DIR" --port "$PORT" --data-dir "$STATE_DIR" --tv-stream-first >>"$LOG_FILE" 2>&1 &
PID=$!
printf '%s\n' "$PID" > "$PID_FILE"

sleep 1
if kill -0 "$PID" 2>/dev/null; then
  echo "running=1"
  echo "pid=$PID"
  echo "log_path=$LOG_FILE"
  echo "media_dir=$MEDIA_DIR"
  exit 0
fi

rm -f "$PID_FILE"
echo "failed=1"
tail -n 20 "$LOG_FILE" 2>/dev/null || true
exit 1
