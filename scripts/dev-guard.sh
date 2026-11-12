#!/bin/bash
# dev-guard.sh — keep the local demo stack alive across crashes and reboots.
#
#   ./scripts/dev-guard.sh start     # start (or restart) API + web, then watch
#   ./scripts/dev-guard.sh stop      # stop the watcher and both services
#   ./scripts/dev-guard.sh status    # show what is up
#   ./scripts/dev-guard.sh restart   # stop + start
#
# The watcher checks every 15s and relaunches whichever side died. It is safe to
# run from a terminal, from `nohup`, or from a launchd agent.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
API_PORT="${API_PORT:-8080}"
WEB_PORT="${WEB_PORT:-5173}"
STATE_DIR="${TMPDIR:-/tmp}/dtp-guard"
API_LOG="$STATE_DIR/api.log"
WEB_LOG="$STATE_DIR/web.log"
GUARD_LOG="$STATE_DIR/guard.log"
API_PID_FILE="$STATE_DIR/api.pid"
WEB_PID_FILE="$STATE_DIR/web.pid"
GUARD_PID_FILE="$STATE_DIR/guard.pid"
mkdir -p "$STATE_DIR"

log() { printf '%s %s\n' "$(date '+%H:%M:%S')" "$*" | tee -a "$GUARD_LOG" >&2; }

port_up() { lsof -nP -iTCP:"$1" -sTCP:LISTEN >/dev/null 2>&1; }

health_ok() { curl -sf --max-time 3 "http://127.0.0.1:$API_PORT/api/health" 2>/dev/null | grep -q '"status":"ok"'; }

start_api() {
  if health_ok; then return 0; fi
  log "starting API on :$API_PORT"
  (
    cd "$ROOT/services/api" || exit 1
    PORT="$API_PORT" AUTH_MODE=on JWT_SECRET="${JWT_SECRET:-dev-secret-change-me}" \
      BUS_DRIVER=jsonl BUS_DIR=data/bus \
      nohup ./bin/api >>"$API_LOG" 2>&1 &
    echo $! >"$API_PID_FILE"
  )
  for _ in $(seq 1 30); do health_ok && return 0; sleep 1; done
  log "API did not become healthy; see $API_LOG"
  return 1
}

start_web() {
  if port_up "$WEB_PORT"; then return 0; fi
  log "starting web on :$WEB_PORT"
  (
    cd "$ROOT/apps/web" || exit 1
    nohup pnpm dev --port "$WEB_PORT" >>"$WEB_LOG" 2>&1 &
    echo $! >"$WEB_PID_FILE"
  )
  for _ in $(seq 1 40); do port_up "$WEB_PORT" && return 0; sleep 1; done
  log "web did not come up; see $WEB_LOG"
  return 1
}

ensure() {
  health_ok || start_api
  port_up "$WEB_PORT" || start_web
}

stop_pid_file() {
  local file="$1"
  [ -f "$file" ] || return 0
  local pid
  pid="$(cat "$file" 2>/dev/null)"
  if [ -n "${pid:-}" ] && kill -0 "$pid" 2>/dev/null; then kill "$pid" 2>/dev/null; fi
  rm -f "$file"
}

stop_all() {
  stop_pid_file "$GUARD_PID_FILE"
  stop_pid_file "$API_PID_FILE"
  stop_pid_file "$WEB_PID_FILE"
  lsof -ti tcp:"$API_PORT" 2>/dev/null | xargs -r kill 2>/dev/null
  lsof -ti tcp:"$WEB_PORT" 2>/dev/null | xargs -r kill 2>/dev/null
  log "stopped"
}

watch() {
  echo $$ >"$GUARD_PID_FILE"
  log "watcher started (api :$API_PORT, web :$WEB_PORT, interval 15s)"
  trap 'log "watcher exiting"; rm -f "$GUARD_PID_FILE"; exit 0' INT TERM
  while true; do
    ensure
    sleep 15
  done
}

status() {
  if health_ok; then echo "API   : up   http://127.0.0.1:$API_PORT"; else echo "API   : DOWN"; fi
  if port_up "$WEB_PORT"; then echo "Web   : up   http://127.0.0.1:$WEB_PORT"; else echo "Web   : DOWN"; fi
  if [ -f "$GUARD_PID_FILE" ] && kill -0 "$(cat "$GUARD_PID_FILE")" 2>/dev/null; then
    echo "Guard : running (pid $(cat "$GUARD_PID_FILE"))"
  else
    echo "Guard : not running"
  fi
}

case "${1:-start}" in
  start)
    ensure
    status
    if [ -f "$GUARD_PID_FILE" ] && kill -0 "$(cat "$GUARD_PID_FILE")" 2>/dev/null; then
      log "watcher already running (pid $(cat "$GUARD_PID_FILE"))"
      exit 0
    fi
    if [ "${FOREGROUND:-0}" = "1" ]; then
      watch
    else
      nohup "$0" __watch >>"$GUARD_LOG" 2>&1 &
      sleep 1
      status
    fi
    ;;
  __watch) watch ;;
  stop) stop_all ;;
  restart) stop_all; sleep 1; "$0" start ;;
  status) status ;;
  *) echo "usage: $0 {start|stop|restart|status}" >&2; exit 2 ;;
esac