#!/usr/bin/env bash
# ---------------------------------------------------------------------------
# P0 + P1.0 + P2.0 end-to-end smoke test for the digital-twin API.
#
# P0 chain (docs/API.md, docs/P0_SCOPE.md §4.6):
#   health -> camera inventory (6 seeded cameras) -> ingest a high-confidence
#   person detection on CAM-001 -> event appears via GET /api/events ->
#   ack the event -> /metrics exposes the required counters.
#
# P1.0 additions (docs/API_P1.md):
#   calibration solve -> PUT calibration -> GET camera shows calibration;
#   ingest a detection carrying track_id -> GET /api/tracks returns it; event
#   workflow: GET detail (empty actions) -> dispatch -> close -> detail shows
#   the actions; GET /api/audit non-empty; GET /api/stats/summary totals > 0;
#   GET /api/stats/events.csv with the documented header.
#
# P2.0 additions (docs/API_P2.md, backend expected in AUTH_MODE=on):
#   no token -> 401; login (admin/operator/viewer seeded users) -> token and
#   GET /api/auth/me; viewer read-only (GET 200, ack 403); operator ack 200,
#   order create 201 -> processing -> done (closed_at) -> GET order; admin
#   per-camera rules (confidence_threshold 0.99 suppresses a 0.95 detection,
#   then defaults restored); operator camera PATCH outside its areas -> 403
#   out_of_scope, inside -> 200; audit contains order.create/order.update/
#   camera.rules.put.  Ingest calls stay token-free (machine entry) unless the
#   API sets INGEST_TOKEN, in which case export INGEST_TOKEN=<token>.
#
# P3.0 additions (docs/API_P3.md):
#   data-level filtering: operator only sees CAM-001/005/006 (areas gate +
#   parking); a fresh CAM-002 event is visible to admin but not to the
#   operator (list + detail 403 out_of_scope); operator GET /api/audit and
#   /api/notifications -> 403 forbidden;
#   escalation settings PUT/GET; a stale warn event (ts=now-10s) gets
#   escalated_at within ~25 s; notifications settings: invalid webhook URL ->
#   400, valid PUT, a new warn event is delivered to a local
#   scripts/webhook_receiver.py (ok=true + JSONL log contains the event id);
#   escalation/notification settings are restored to defaults at the end.
#
# P4.0 additions (docs/API_P4.md):
#   duty groups CRUD + /api/duty/current resolution + RBAC (operator POST 403,
#   logged-in GET allowed); auto-dispatch: escalation settings auto_dispatch
#   with after_seconds=1 make a stale warn event on CAM-001 (area_gate) produce
#   an order assigned to the duty group with a note containing 自动派单
#   (audit order.auto_dispatch); notification retry: receiver --fail-first 2 +
#   max_attempts=3/backoff_seconds=1 -> ok=true with attempts=3 and 3 POSTs in
#   the receiver log; silence: silence_seconds=60 suppresses the second
#   identical event (dsh_twin_notifications_suppressed_total > 0); P4 metrics;
#   cleanup deletes the duty group and restores settings defaults.
#
# P5.0 additions (docs/API_P5.md):
#   admin GET /api/bus/status shape (driver/published/next_offset/subscribers),
#   non-admin status/replay -> 403; a token-free ingest advances
#   published/next_offset; admin replay of the freshly published offset returns
#   ok=true with replayed>=1 and increases status.replayed; an invalid range
#   (from_offset > to_offset) -> 400 bad_request; /metrics exposes
#   dsh_twin_bus_published_total and dsh_twin_bus_replayed_total.
#
# P6.0 additions (docs/API_P6.md):
#   admin GET /api/site -> 200 with a non-empty name, a numeric ENU extent
#   (min_x/max_x/min_y/max_y), at least one model entry carrying
#   id/format(gltf|glb)/url, and map.style; viewer GET /api/site -> 200
#   (authenticated read); no-token GET /api/site -> 401 unauthorized.
#   Total: 97 checks (21 P0/P1.0 + 18 P2.0 + 21 P3.0 + 25 P4.0 + 9 P5.0 + 3 P6.0).
#
# Dependencies: bash, curl, python3 only.
#
# Usage:
#   ./scripts/smoke.sh                          # default http://127.0.0.1:8080
#   API_BASE=http://127.0.0.1:8080 ./scripts/smoke.sh
#   INGEST_TOKEN=secret ./scripts/smoke.sh      # only if the API sets INGEST_TOKEN
#
# Exit codes: 0 = all checks passed, 1 = at least one check failed.
# ---------------------------------------------------------------------------
set -uo pipefail

API_BASE="${API_BASE:-http://127.0.0.1:8080}"
API_BASE="${API_BASE%/}"
INGEST_TOKEN="${INGEST_TOKEN:-}"

TMP="$(mktemp -d "${TMPDIR:-/tmp}/dsh-smoke.XXXXXX")" || {
  printf 'FATAL: could not create a temp dir\n' >&2
  exit 1
}
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

WS_RECEIVER_PID=""
WS_RECEIVER_PORT=""
WS_RECEIVER_LOG="$TMP/webhook.log"
DUTY_GROUP_ID=""

cleanup() {
  if [ -n "${WS_RECEIVER_PID:-}" ]; then
    kill -TERM "$WS_RECEIVER_PID" 2>/dev/null || true
    wait "$WS_RECEIVER_PID" 2>/dev/null || true
    WS_RECEIVER_PID=""
  fi
  # Best effort: restore P3/P4 settings defaults even if a check failed mid-way.
  if [ -n "${admin_token:-}" ]; then
    curl -s -o /dev/null -m 3 -X PUT -H 'Content-Type: application/json' \
      -H "Authorization: Bearer $admin_token" \
      --data '{"enabled":true,"after_seconds":300,"severities":["warn","critical"],"auto_dispatch":true}' \
      "$API_BASE/api/settings/escalation" 2>/dev/null || true
    curl -s -o /dev/null -m 3 -X PUT -H 'Content-Type: application/json' \
      -H "Authorization: Bearer $admin_token" \
      --data '{"enabled":false,"webhook_url":"","format":"generic","min_severity":"warn","timeout_seconds":5,"max_attempts":3,"backoff_seconds":2,"silence_seconds":30}' \
      "$API_BASE/api/settings/notifications" 2>/dev/null || true
    # Camera rules may be temporarily relaxed (dedupe_seconds=0) by the
    # escalation/silence steps; restore the P2 defaults as a safety net.
    for iso_file in "$TMP"/iso-rules-*.json; do
      [ -f "$iso_file" ] || continue
      iso_id="${iso_file##*/iso-rules-}"
      iso_id="${iso_id%.json}"
      curl -s -o /dev/null -m 3 -X PUT -H 'Content-Type: application/json' \
        -H "Authorization: Bearer $admin_token" \
        --data '{"confidence_threshold":0.6,"dedupe_seconds":5,"enabled_classes":["person","vehicle","intrusion","smoke","no_helmet"],"roi":null}' \
        "$API_BASE/api/cameras/$iso_id/rules" 2>/dev/null || true
    done
    cleanup_cam_ids="$(python3 -c 'import json, sys
try:
    items = json.load(open(sys.argv[1])).get("items") or []
except Exception:
    items = []
print(" ".join(str(cam.get("id")) for cam in items if isinstance(cam, dict) and cam.get("id")))' "$TMP/cameras.json" 2>/dev/null || true)"
    [ -n "$cleanup_cam_ids" ] || cleanup_cam_ids="cam_001 cam_005 cam_006"
    for cam_cleanup in $cleanup_cam_ids; do
      curl -s -o /dev/null -m 3 -X PUT -H 'Content-Type: application/json' \
        -H "Authorization: Bearer $admin_token" \
        --data '{"confidence_threshold":0.6,"dedupe_seconds":5,"enabled_classes":["person","vehicle","intrusion","smoke","no_helmet"],"roi":null}' \
        "$API_BASE/api/cameras/$cam_cleanup/rules" 2>/dev/null || true
    done
    if [ -n "${DUTY_GROUP_ID:-}" ]; then
      curl -s -o /dev/null -m 3 -X DELETE -H "Authorization: Bearer $admin_token" \
        "$API_BASE/api/duty/groups/$DUTY_GROUP_ID" 2>/dev/null || true
    fi
  fi
  rm -rf "$TMP"
}
trap cleanup EXIT

PASS_COUNT=0
FAIL_COUNT=0

pass() {
  PASS_COUNT=$((PASS_COUNT + 1))
  printf 'PASS  %s\n' "$1"
}

fail() {
  FAIL_COUNT=$((FAIL_COUNT + 1))
  printf 'FAIL  %s\n' "$1" >&2
}

step() {
  printf '\n== %s ==\n' "$1"
}

summary() {
  printf '\n==============================\n'
  if [ "$FAIL_COUNT" -eq 0 ]; then
    printf 'smoke: PASS (%d checks passed)\n' "$PASS_COUNT"
  else
    printf 'smoke: FAIL (%d passed, %d failed)\n' "$PASS_COUNT" "$FAIL_COUNT" >&2
  fi
}

for bin in curl python3; do
  if ! command -v "$bin" >/dev/null 2>&1; then
    printf 'FATAL: %s is required but was not found in PATH\n' "$bin" >&2
    exit 1
  fi
done

# ---------------------------------------------------------------------------
# HTTP helpers: body -> $TMP/body.json, echo = status code
#   http_do METHOD PATH [BODY] [BEARER_TOKEN]
# Ingest (POST /api/ingest/detections) additionally sends X-Ingest-Token when
# the INGEST_TOKEN env var is set.
# ---------------------------------------------------------------------------
http_do() {
  local method="$1"
  local path="$2"
  local body="${3:-}"
  local token="${4:-}"
  local args
  args=(-s -o "$TMP/body.json" -w '%{http_code}' -m 5 -X "$method" -H 'Accept: application/json')
  if [ -n "$token" ]; then
    args+=(-H "Authorization: Bearer $token")
  fi
  if [ -n "$body" ]; then
    args+=(-H 'Content-Type: application/json' --data "$body")
  fi
  if [ -n "$INGEST_TOKEN" ] && [ "$path" = "/api/ingest/detections" ]; then
    args+=(-H "X-Ingest-Token: $INGEST_TOKEN")
  fi
  curl "${args[@]}" "$API_BASE$path" 2>/dev/null || true
}

http_get() { http_do GET "$1" "" "${2:-}"; }
http_post_json() { http_do POST "$1" "$2" "${3:-}"; }
http_put_json() { http_do PUT "$1" "$2" "${3:-}"; }
http_patch_json() { http_do PATCH "$1" "$2" "${3:-}"; }

utc_now() {
  python3 -c 'from datetime import datetime, timezone; print(datetime.now(timezone.utc).isoformat(timespec="milliseconds").replace("+00:00", "Z"))'
}

# login_user USER PASSWORD -> "<status> <token>" (token empty on failure)
login_user() {
  local code token
  code="$(curl -s -o "$TMP/login.json" -w '%{http_code}' -m 5 -X POST \
    -H 'Content-Type: application/json' \
    --data "{\"username\":\"$1\",\"password\":\"$2\"}" \
    "$API_BASE/api/auth/login" 2>/dev/null || true)"
  token="$(python3 - "$TMP/login.json" <<'PY'
import json, sys
try:
    data = json.load(open(sys.argv[1]))
except Exception:
    print("")
    sys.exit(0)
if not isinstance(data, dict):
    print("")
    sys.exit(0)
print(data.get("token") or "")
PY
)"
  printf '%s %s\n' "$code" "$token"
}

# first_event_id -> first id in an ingest 202 response ($TMP/body.json)
first_event_id() {
  python3 - "$TMP/body.json" <<'PY'
import json, sys
try:
    data = json.load(open(sys.argv[1]))
except Exception:
    print("")
    sys.exit(0)
events = data.get("events") if isinstance(data, dict) else None
if events and isinstance(events[0], dict):
    print(events[0].get("id") or "")
else:
    print("")
PY
}

# camera_id_for CODE -> id from $TMP/cameras.json
camera_id_for() {
  python3 - "$TMP/cameras.json" "$1" <<'PY'
import json, sys
try:
    items = json.load(open(sys.argv[1])).get("items") or []
except Exception:
    items = []
for cam in items:
    if isinstance(cam, dict) and cam.get("code") == sys.argv[2]:
        print(cam.get("id") or "")
        break
PY
}

# camera_world_ahead CODE DISTANCE -> JSON world point on the camera axis
camera_world_ahead() {
  python3 - "$TMP/cameras.json" "$1" "$2" <<'PY'
import json, math, sys
try:
    items = json.load(open(sys.argv[1])).get("items") or []
except Exception:
    items = []
cam = None
for item in items:
    if isinstance(item, dict) and item.get("code") == sys.argv[2]:
        cam = item
        break
if cam is None:
    print("")
    sys.exit(0)
heading = math.radians(float(cam.get("heading") or 0.0))
distance = float(sys.argv[3])
print(json.dumps({
    "x": round(float(cam.get("x") or 0.0) + distance * math.sin(heading), 3),
    "y": round(float(cam.get("y") or 0.0) + distance * math.cos(heading), 3),
    "z": 0.0,
}))
PY
}

# dedup_seconds_for CAMERA_CODE EVENT_TYPE -> seconds to wait (0 = clear now)
dedup_seconds_for() {
  local events_code
  events_code="$(http_get "/api/events?limit=100" "$admin_token")"
  if [ "$events_code" != "200" ]; then
    echo "0"
    return 0
  fi
  python3 - "$TMP/body.json" "$1" "$2" <<'PY'
import json, sys
from datetime import datetime, timezone

try:
    data = json.load(open(sys.argv[1]))
except Exception:
    print("0")
    sys.exit(0)
items = data.get("items") if isinstance(data, dict) else None
now = datetime.now(timezone.utc)
last = None
for event in items or []:
    if not isinstance(event, dict):
        continue
    if event.get("camera_code") != sys.argv[2] or event.get("type") != sys.argv[3]:
        continue
    try:
        occurred = datetime.fromisoformat(str(event.get("occurred_at")).replace("Z", "+00:00"))
    except Exception:
        continue
    if occurred.tzinfo is None:
        occurred = occurred.replace(tzinfo=timezone.utc)
    if last is None or occurred > last:
        last = occurred
if last is None:
    print("0")
else:
    age = (now - last).total_seconds()
    print("%.3f" % max(0.0, 5.3 - age) if age < 5.3 else "0")
PY
}

# wait_dedup_event CAMERA_CODE EVENT_TYPE
# The API dedups (camera, class) events on a 5 s window (docs/API.md rule #2).
# The window is re-checked after each sleep because a concurrently running
# simulator can create a newer event while we wait (restarting the window).
wait_dedup_event() {
  local camera_code="$1" event_type="$2" round=0 wait_s
  while [ "$round" -lt 4 ]; do
    round=$((round + 1))
    wait_s="$(dedup_seconds_for "$camera_code" "$event_type")"
    case "$wait_s" in
      '' | *[!0-9.]*) wait_s=0 ;;
    esac
    if python3 -c 'import sys; sys.exit(0 if float(sys.argv[1]) > 0 else 1)' "$wait_s" 2>/dev/null; then
      printf '      waiting %ss for the 5s dedup window on %s/%s\n' "$wait_s" "$camera_code" "$event_type"
      sleep "$wait_s"
    else
      return 0
    fi
  done
}

admin_token=""
operator_token=""
viewer_token=""

# put_notifications_disabled -> HTTP status. The default settings have an empty
# webhook URL while disabled; if the backend rejects that on PUT, fall back to a
# valid-but-unused loopback URL so defaults can still be restored.
put_notifications_disabled() {
  local code
  code="$(http_put_json "/api/settings/notifications" \
    '{"enabled":false,"webhook_url":"","format":"generic","min_severity":"warn","timeout_seconds":5,"max_attempts":3,"backoff_seconds":2,"silence_seconds":30}' \
    "$admin_token")"
  if [ "$code" != "200" ]; then
    code="$(http_put_json "/api/settings/notifications" \
      '{"enabled":false,"webhook_url":"http://127.0.0.1:1/hook","format":"generic","min_severity":"warn","timeout_seconds":5,"max_attempts":3,"backoff_seconds":2,"silence_seconds":30}' \
      "$admin_token")"
  fi
  printf '%s' "$code"
}

# start_receiver [FAIL_FIRST] -> 0 when scripts/webhook_receiver.py is
# listening, sets WS_RECEIVER_PID/WS_RECEIVER_PORT/WS_RECEIVER_LOG. Tries up to
# 3 free ports. FAIL_FIRST (default 0) makes the receiver answer that many POSTs
# with 500 first (P4 retry testing).
start_receiver() {
  local attempt=0 port="" code="" i fail_first="${1:-0}"
  SCRIPT_DIR="${SCRIPT_DIR:-$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)}"
  while [ "$attempt" -lt 3 ]; do
    attempt=$((attempt + 1))
    port="$(python3 -c 'import socket
sock = socket.socket()
sock.bind(("127.0.0.1", 0))
print(sock.getsockname()[1])
sock.close()')"
    WS_RECEIVER_LOG="$TMP/webhook-$attempt.log"
    rm -f "$WS_RECEIVER_LOG"
    WS_RECEIVER_PORT="$port"
    if [ "$fail_first" != "0" ]; then
      python3 "$SCRIPT_DIR/webhook_receiver.py" --port "$port" --out "$WS_RECEIVER_LOG" \
        --fail-first "$fail_first" \
        >"$TMP/receiver-$attempt.out" 2>"$TMP/receiver-$attempt.err" &
    else
      python3 "$SCRIPT_DIR/webhook_receiver.py" --port "$port" --out "$WS_RECEIVER_LOG" \
        >"$TMP/receiver-$attempt.out" 2>"$TMP/receiver-$attempt.err" &
    fi
    WS_RECEIVER_PID=$!
    code=""
    i=0
    while [ "$i" -lt 50 ]; do
      i=$((i + 1))
      code="$(curl -s -o /dev/null -w '%{http_code}' -m 1 "http://127.0.0.1:$port/health" 2>/dev/null || true)"
      if [ "$code" = "200" ]; then
        return 0
      fi
      sleep 0.1
    done
    kill -TERM "$WS_RECEIVER_PID" 2>/dev/null || true
    wait "$WS_RECEIVER_PID" 2>/dev/null || true
    WS_RECEIVER_PID=""
  done
  WS_RECEIVER_PORT=""
  return 1
}

http_delete() { http_do DELETE "$1" "" "${2:-}"; }

# relax_camera_dedupe CAM_ID -> HTTP status of the rules PUT (dedupe_seconds=0).
# Saves the camera's previous rules in RELAXED_CAM_RULES_FILE so they can be
# restored right after a backdated/duplicate-ish event injection.
RELAXED_CAM_ID=""
RELAXED_CAM_RULES_FILE=""
relax_camera_dedupe() {
  local cam_id="$1" code payload
  RELAXED_CAM_ID="$cam_id"
  RELAXED_CAM_RULES_FILE="$TMP/rules-$cam_id.json"
  code="$(http_get "/api/cameras/$cam_id/rules" "$admin_token")"
  if [ "$code" != "200" ]; then
    printf '%s' "$code"
    return 0
  fi
  cp "$TMP/body.json" "$RELAXED_CAM_RULES_FILE" 2>/dev/null || true
  payload="$(python3 - "$RELAXED_CAM_RULES_FILE" <<'PY'
import json, sys
try:
    rules = json.load(open(sys.argv[1]))
except Exception:
    rules = {}
if not isinstance(rules, dict):
    rules = {}
print(json.dumps({
    "confidence_threshold": rules.get("confidence_threshold", 0.6),
    "dedupe_seconds": 0,
    "enabled_classes": rules.get("enabled_classes") or
        ["person", "vehicle", "intrusion", "smoke", "no_helmet"],
    "roi": rules.get("roi"),
}))
PY
)"
  code="$(http_put_json "/api/cameras/$cam_id/rules" "$payload" "$admin_token")"
  printf '%s' "$code"
}

# restore_camera_dedupe CAM_ID -> HTTP status of the rules PUT from the backup.
restore_camera_dedupe() {
  local cam_id="${1:-$RELAXED_CAM_ID}" file="${2:-$RELAXED_CAM_RULES_FILE}" code payload
  if [ -z "$cam_id" ] || [ ! -f "$file" ]; then
    printf '000'
    return 0
  fi
  payload="$(python3 - "$file" <<'PY'
import json, sys
try:
    rules = json.load(open(sys.argv[1]))
except Exception:
    rules = {}
if not isinstance(rules, dict):
    rules = {}
print(json.dumps({
    "confidence_threshold": rules.get("confidence_threshold", 0.6),
    "dedupe_seconds": rules.get("dedupe_seconds", 5),
    "enabled_classes": rules.get("enabled_classes") or
        ["person", "vehicle", "intrusion", "smoke", "no_helmet"],
    "roi": rules.get("roi"),
}))
PY
)"
  code="$(http_put_json "/api/cameras/$cam_id/rules" "$payload" "$admin_token")"
  printf '%s' "$code"
}

# isolate_events: raise every camera's confidence_threshold to 0.995 for a
# short retry/silence test window, so a concurrently running simulator cannot
# interleave its own notifications with the controlled webhook deliveries.
# The original per-camera rules are saved and restored afterwards (and in the
# EXIT trap as a safety net). Event injection during the window uses
# confidence=0.995 so smoke's own events still pass the threshold.
ISOLATE_MARKER="$TMP/iso-rules"
isolate_events() {
  ISOLATE_MARKER="$TMP/iso-rules"
  rm -f "$TMP"/iso-rules-*.json 2>/dev/null || true
  local ids id file payload code
  ids="$(python3 -c 'import json,sys
try:
    items = json.load(open(sys.argv[1])).get("items") or []
except Exception:
    items = []
print(" ".join(str(cam.get("id")) for cam in items if isinstance(cam, dict) and cam.get("id")))' "$TMP/cameras.json")"
  for id in $ids; do
    file="$TMP/iso-rules-$id.json"
    code="$(http_get "/api/cameras/$id/rules" "$admin_token")"
    if [ "$code" != "200" ]; then
      printf '      warning: could not read rules for %s (HTTP %s)\n' "$id" "${code:-none}" >&2
      continue
    fi
    cp "$TMP/body.json" "$file" 2>/dev/null || true
    payload="$(python3 - "$file" <<'PY'
import json, sys
try:
    rules = json.load(open(sys.argv[1]))
except Exception:
    rules = {}
if not isinstance(rules, dict):
    rules = {}
print(json.dumps({
    "confidence_threshold": 0.995,
    "dedupe_seconds": rules.get("dedupe_seconds", 5),
    "enabled_classes": rules.get("enabled_classes") or
        ["person", "vehicle", "intrusion", "smoke", "no_helmet"],
    "roi": rules.get("roi"),
}))
PY
)"
    code="$(http_put_json "/api/cameras/$id/rules" "$payload" "$admin_token")"
    if [ "$code" != "200" ]; then
      printf '      warning: could not isolate camera %s (HTTP %s)\n' "$id" "${code:-none}" >&2
    fi
  done
  printf '      camera event traffic isolated (confidence_threshold=0.995) for retry/silence test\n'
}

# release_events: restore every camera's saved rules from isolate_events().
release_events() {
  local file id payload code
  for file in "$TMP"/iso-rules-*.json; do
    [ -f "$file" ] || continue
    id="${file##*/iso-rules-}"
    id="${id%.json}"
    payload="$(python3 - "$file" <<'PY'
import json, sys
try:
    rules = json.load(open(sys.argv[1]))
except Exception:
    rules = {}
if not isinstance(rules, dict):
    rules = {}
print(json.dumps({
    "confidence_threshold": rules.get("confidence_threshold", 0.6),
    "dedupe_seconds": rules.get("dedupe_seconds", 5),
    "enabled_classes": rules.get("enabled_classes") or
        ["person", "vehicle", "intrusion", "smoke", "no_helmet"],
    "roi": rules.get("roi"),
}))
PY
)"
    code="$(http_put_json "/api/cameras/$id/rules" "$payload" "$admin_token")"
    if [ "$code" != "200" ]; then
      printf '      warning: could not restore rules for %s (HTTP %s)\n' "$id" "${code:-none}" >&2
    fi
  done
  rm -f "$TMP"/iso-rules-*.json 2>/dev/null || true
  printf '      camera event traffic restored\n'
}

printf 'smoke target: %s\n' "$API_BASE"
printf 'auth:        P2 AUTH_MODE=on expected (admin/operator/viewer seeded users)\n'
if [ -n "$INGEST_TOKEN" ]; then
  printf 'ingest auth: INGEST_TOKEN is set (X-Ingest-Token sent on ingest)\n'
else
  printf 'ingest auth: token-free (machine entry)\n'
fi

# ===========================================================================
# 1. health (exempt from auth, retry briefly while the API starts)
# ===========================================================================
step "1/34 health"
health_code=""
attempt=0
while [ "$attempt" -lt 5 ]; do
  attempt=$((attempt + 1))
  health_code="$(http_get "/api/health")"
  [ "$health_code" = "200" ] && break
  sleep 0.5
done

if [ "$health_code" != "200" ]; then
  fail "GET $API_BASE/api/health -> HTTP ${health_code:-none}; is the API running? (expected :8080)"
  summary
  exit 1
fi

if python3 - "$TMP/body.json" <<'PY'
import json, sys
try:
    data = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
sys.exit(0 if isinstance(data, dict) and data.get("status") == "ok" else 1)
PY
then
  pass "health: status=ok"
else
  fail "health: response missing status=ok"
  cat "$TMP/body.json" >&2
fi

# ===========================================================================
# 2. P2 auth: unauthenticated 401, admin login, GET /api/auth/me
# ===========================================================================
step "2/34 auth (P2: no-token 401, login, /api/auth/me)"
noauth_code="$(http_get "/api/cameras")"
if [ "$noauth_code" = "401" ]; then
  noauth_error="$(python3 -c 'import json,sys
try: print(json.load(open(sys.argv[1])).get("error",""))
except Exception: print("")' "$TMP/body.json" 2>/dev/null || echo '')"
  pass "GET /api/cameras without token -> 401 ${noauth_error}"
else
  fail "GET /api/cameras without token -> HTTP ${noauth_code:-none} (expected 401; is AUTH_MODE=on?)"
fi

read -r admin_login_code admin_token < <(login_user admin 'Admin@123456')
if [ "$admin_login_code" = "200" ] && [ -n "$admin_token" ]; then
  admin_role="$(python3 -c 'import json,sys
try: print((json.load(open(sys.argv[1])).get("user") or {}).get("role",""))
except Exception: print("")' "$TMP/login.json" 2>/dev/null || echo '')"
  pass "POST /api/auth/login admin -> 200 token issued (role=$admin_role)"
else
  fail "POST /api/auth/login admin -> HTTP ${admin_login_code:-none} (expected 200; check seeded users)"
fi

me_code="$(http_get "/api/auth/me" "$admin_token")"
if python3 - "$TMP/body.json" "$me_code" <<'PY'
import json, sys
if len(sys.argv) < 3 or sys.argv[2] != "200":
    sys.exit(1)
try:
    data = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
ok = isinstance(data, dict) and data.get("username") == "admin" and data.get("role") == "admin"
sys.exit(0 if ok else 1)
PY
then
  pass "GET /api/auth/me -> username=admin role=admin"
else
  fail "GET /api/auth/me -> HTTP ${me_code:-none} or wrong user object"
fi

# ===========================================================================
# 3. camera inventory: >= 1 camera and the six seeded codes CAM-001..CAM-006
# ===========================================================================
step "3/34 cameras"
cameras_code="$(http_get "/api/cameras" "$admin_token")"
if [ "$cameras_code" != "200" ]; then
  fail "GET /api/cameras -> HTTP ${cameras_code:-none}"
else
  cam_report="$(python3 - "$TMP/body.json" <<'PY'
import json, sys
try:
    data = json.load(open(sys.argv[1]))
except Exception as exc:
    print("ERROR|unreadable camera response: %s" % exc)
    sys.exit(0)
items = data.get("items") if isinstance(data, dict) else None
if not isinstance(items, list):
    print("ERROR|response has no items array")
    sys.exit(0)
codes = [str(cam.get("code") or "") for cam in items if isinstance(cam, dict)]
missing = ["CAM-%03d" % i for i in range(1, 7) if ("CAM-%03d" % i) not in codes]
print("%d|%s" % (len(items), ",".join(missing)))
PY
)"
  cam_count="${cam_report%%|*}"
  cam_missing="${cam_report#*|}"
  case "$cam_count" in
    '' | *[!0-9]*)
      fail "GET /api/cameras: could not parse response ($cam_report)"
      ;;
    *)
      if [ "$cam_count" -ge 1 ]; then
        pass "cameras count=$cam_count (>= 1)"
      else
        fail "cameras count=$cam_count (< 1); seed the camera inventory"
      fi
      ;;
  esac
  if [ -z "$cam_missing" ]; then
    pass "seeded camera codes present: CAM-001..CAM-006"
  else
    fail "missing seeded camera codes: $cam_missing"
  fi
fi

cp "$TMP/body.json" "$TMP/cameras.json" 2>/dev/null || true
cam1_id="$(camera_id_for CAM-001)"
cam2_id="$(camera_id_for CAM-002)"
cam3_id="$(camera_id_for CAM-003)"
cam5_id="$(camera_id_for CAM-005)"
cam6_id="$(camera_id_for CAM-006)"
world_json="$(camera_world_ahead CAM-001 30)"
if [ -z "$world_json" ]; then
  world_json='{"x": 0.0, "y": 0.0, "z": 0.0}'
fi

# ===========================================================================
# 4. ingest one high-confidence person detection for CAM-001 (token-free)
# ===========================================================================
step "4/34 ingest detection (P0, token-free machine entry)"
ingest_code=""
event_id=""
ingest_attempt=0
while [ "$ingest_attempt" -lt 3 ]; do
  ingest_attempt=$((ingest_attempt + 1))
  wait_dedup_event CAM-001 person
  utc_ts="$(utc_now)"
  payload="$(printf '{"camera_code":"CAM-001","ts":"%s","detections":[{"class":"person","confidence":0.987654,"bbox":[0.42,0.35,0.55,0.72],"world":%s}]}' "$utc_ts" "$world_json")"
  ingest_code="$(http_post_json "/api/ingest/detections" "$payload")"
  if [ "$ingest_code" = "200" ] || [ "$ingest_code" = "202" ]; then
    event_id="$(first_event_id)"
  fi
  if [ -n "$event_id" ]; then
    break
  fi
  if [ "$ingest_attempt" -lt 3 ]; then
    printf '      ingest attempt %d produced no event (dedup race); retrying\n' "$ingest_attempt"
  fi
done

if [ "$ingest_code" = "200" ] || [ "$ingest_code" = "202" ]; then
  pass "POST /api/ingest/detections (no user token) -> HTTP $ingest_code (attempt $ingest_attempt)"
  ingest_report="$(python3 - "$TMP/body.json" <<'PY'
import json, sys
try:
    data = json.load(open(sys.argv[1]))
except Exception:
    print("accepted=? dropped=? events=?")
    sys.exit(0)
if not isinstance(data, dict):
    print("accepted=? dropped=? events=?")
    sys.exit(0)
print("accepted=%s dropped=%s events=%s" % (
    data.get("accepted"), data.get("dropped"), len(data.get("events") or [])))
PY
)"
  printf '      ingest: %s\n' "$ingest_report"
else
  fail "POST /api/ingest/detections -> HTTP ${ingest_code:-none} (token-free ingest must be allowed; else set INGEST_TOKEN)"
fi

# ===========================================================================
# 5. poll GET /api/events?limit=20 until the event is visible (<= 5 s)
# ===========================================================================
step "5/34 event appears in /api/events (<= 5s)"
found_id=""
attempt=0
while [ "$attempt" -lt 10 ]; do
  attempt=$((attempt + 1))
  curl -s -m 2 -H "Authorization: Bearer $admin_token" \
    "$API_BASE/api/events?limit=20" -o "$TMP/events.json" 2>/dev/null || true
  found_id="$(python3 - "$TMP/events.json" "$event_id" <<'PY'
import json, sys
try:
    data = json.load(open(sys.argv[1]))
except Exception:
    print("")
    sys.exit(0)
items = data.get("items") if isinstance(data, dict) else None
if items is None:
    print("")
    sys.exit(0)
target = sys.argv[2] if len(sys.argv) > 2 else ""
if target:
    for event in items:
        if isinstance(event, dict) and event.get("id") == target:
            print(target)
            sys.exit(0)
    print("")
    sys.exit(0)
# No id in the ingest response (e.g. rule engine deduped): fall back to
# matching camera + type + the unique confidence we posted.
for event in items:
    if not isinstance(event, dict):
        continue
    if event.get("camera_code") != "CAM-001" or event.get("type") != "person":
        continue
    try:
        confidence = float(event.get("confidence"))
    except (TypeError, ValueError):
        continue
    if abs(confidence - 0.987654) < 1e-3:
        print(event.get("id") or "")
        sys.exit(0)
print("")
PY
)"
  if [ -n "$found_id" ]; then
    break
  fi
  sleep 0.5
done

if [ -n "$found_id" ]; then
  pass "event $found_id visible in GET /api/events (attempt $attempt/10)"
else
  fail "no matching CAM-001 person event in GET /api/events within 5s (rule engine/dedup miss?)"
fi

# ===========================================================================
# 6. ack the event (admin token; operator RBAC is covered in step 15)
# ===========================================================================
step "6/34 ack event (P0)"
if [ -n "$found_id" ]; then
  ack_code="$(http_post_json "/api/events/$found_id/ack" '{}' "$admin_token")"
  if python3 - "$TMP/body.json" "$ack_code" <<'PY'
import json, sys
if len(sys.argv) < 3 or sys.argv[2] != "200":
    sys.exit(1)
try:
    data = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
if not isinstance(data, dict) or data.get("ok") is not True:
    sys.exit(1)
event = data.get("event")
sys.exit(0 if isinstance(event, dict) and event.get("status") == "acked" else 1)
PY
  then
    pass "event $found_id acked (status=acked)"
  else
    fail "ack for $found_id failed (HTTP ${ack_code:-none}, body=$(head -c 300 "$TMP/body.json" 2>/dev/null))"
  fi
else
  fail "skipped ack: no event id from step 5"
fi

# ===========================================================================
# 7. /metrics exposes the required counters (auth-exempt)
# ===========================================================================
step "7/34 metrics"
metrics_code="$(curl -s -o "$TMP/metrics.txt" -w '%{http_code}' -m 5 "$API_BASE/metrics" 2>/dev/null || true)"
if [ "$metrics_code" != "200" ]; then
  fail "GET /metrics -> HTTP ${metrics_code:-none}"
else
  if grep -q 'dsh_twin_events_total' "$TMP/metrics.txt"; then
    pass "metrics contains dsh_twin_events_total"
  else
    fail "metrics missing dsh_twin_events_total"
  fi
  if grep -q 'dsh_twin_ws_clients' "$TMP/metrics.txt"; then
    pass "metrics contains dsh_twin_ws_clients"
  else
    fail "metrics missing dsh_twin_ws_clients"
  fi
  printf '      %s\n' "$(grep -m1 '^dsh_twin_events_total' "$TMP/metrics.txt" 2>/dev/null || echo '<no events_total line>')"
  printf '      %s\n' "$(grep -m1 '^dsh_twin_ws_clients' "$TMP/metrics.txt" 2>/dev/null || echo '<no ws_clients line>')"
fi

# ===========================================================================
# 8. P1.0 calibration: solve -> PUT -> GET camera shows calibration (admin)
# ===========================================================================
step "8/34 calibration (P1: solve -> PUT -> GET camera, admin)"
solve_payload='{"camera_id":"cam_001","points":[{"px":320,"py":800,"x":-84.0,"y":-30.0},{"px":1600,"py":800,"x":-20.0,"y":-30.0},{"px":320,"py":280,"x":-84.0,"y":-56.0},{"px":1600,"py":280,"x":-20.0,"y":-56.0},{"px":960,"py":540,"x":-52.0,"y":-43.0}]}'
if [ -n "$cam1_id" ]; then
  solve_payload="$(python3 - "$TMP/cameras.json" <<'PY'
import json, sys
try:
    items = json.load(open(sys.argv[1])).get("items") or []
except Exception:
    items = []
cam_id = ""
for cam in items:
    if isinstance(cam, dict) and cam.get("code") == "CAM-001":
        cam_id = cam.get("id") or ""
        break
points = [
    {"px": 320, "py": 800, "x": -84.0, "y": -30.0},
    {"px": 1600, "py": 800, "x": -20.0, "y": -30.0},
    {"px": 320, "py": 280, "x": -84.0, "y": -56.0},
    {"px": 1600, "py": 280, "x": -20.0, "y": -56.0},
    {"px": 960, "py": 540, "x": -52.0, "y": -43.0},
]
print(json.dumps({"camera_id": cam_id, "points": points}))
PY
)"
fi

solve_code="$(http_post_json "/api/calibration/solve" "$solve_payload" "$admin_token")"
if python3 - "$TMP/body.json" "$solve_code" <<'PY'
import json, sys
if len(sys.argv) < 3 or sys.argv[2] != "200":
    sys.exit(1)
try:
    data = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
matrix = data.get("matrix") if isinstance(data, dict) else None
ok = isinstance(matrix, list) and len(matrix) == 3 and all(
    isinstance(row, list) and len(row) == 3 for row in matrix
)
ok = ok and isinstance(data.get("rmse"), (int, float))
ok = ok and isinstance(data.get("points_count"), int) and data["points_count"] >= 4
sys.exit(0 if ok else 1)
PY
then
  cal_rmse="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("rmse"))' "$TMP/body.json" 2>/dev/null || echo '?')"
  pass "POST /api/calibration/solve (admin) -> 3x3 matrix, rmse=$cal_rmse"
else
  fail "POST /api/calibration/solve failed (HTTP ${solve_code:-none}, body=$(head -c 300 "$TMP/body.json" 2>/dev/null))"
fi

put_payload="$(python3 - "$TMP/body.json" <<'PY'
import json, sys
try:
    data = json.load(open(sys.argv[1]))
except Exception:
    data = {}
if not isinstance(data, dict):
    data = {}
print(json.dumps({
    "matrix": data.get("matrix"),
    "rmse": data.get("rmse"),
    "points_count": data.get("points_count"),
}))
PY
)"

if [ -n "$cam1_id" ]; then
  put_code="$(http_put_json "/api/cameras/$cam1_id/calibration" "$put_payload" "$admin_token")"
else
  put_code="000"
fi
if python3 - "$TMP/body.json" "$put_code" <<'PY'
import json, sys
if len(sys.argv) < 3 or sys.argv[2] != "200":
    sys.exit(1)
try:
    camera = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
cal = camera.get("calibration") if isinstance(camera, dict) else None
if not isinstance(cal, dict):
    sys.exit(1)
matrix = cal.get("matrix")
ok = isinstance(matrix, list) and len(matrix) == 3 and all(
    isinstance(row, list) and len(row) == 3 for row in matrix
)
sys.exit(0 if ok else 1)
PY
then
  pass "PUT /api/cameras/$cam1_id/calibration (admin) -> camera.calibration saved"
else
  fail "PUT /api/cameras/$cam1_id/calibration failed (HTTP ${put_code:-none}, body=$(head -c 300 "$TMP/body.json" 2>/dev/null))"
fi

if [ -n "$cam1_id" ]; then
  get_cam_code="$(http_get "/api/cameras/$cam1_id" "$admin_token")"
else
  get_cam_code="000"
fi
if python3 - "$TMP/body.json" "$get_cam_code" <<'PY'
import json, sys
if len(sys.argv) < 3 or sys.argv[2] != "200":
    sys.exit(1)
try:
    camera = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
cal = camera.get("calibration") if isinstance(camera, dict) else None
if not isinstance(cal, dict):
    sys.exit(1)
matrix = cal.get("matrix")
ok = isinstance(matrix, list) and len(matrix) == 3 and all(
    isinstance(row, list) and len(row) == 3 for row in matrix
)
ok = ok and isinstance(cal.get("points_count"), int) and cal["points_count"] >= 4
ok = ok and bool(cal.get("solved_at"))
sys.exit(0 if ok else 1)
PY
then
  pass "GET /api/cameras/$cam1_id shows calibration (points_count>=4, solved_at set)"
else
  fail "GET /api/cameras/$cam1_id does not show the saved calibration (HTTP ${get_cam_code:-none})"
fi

# ===========================================================================
# 9. P1.0 tracks: ingest a detection with track_id -> GET /api/tracks returns it
# ===========================================================================
step "9/34 tracks (P1: ingest with track_id -> GET /api/tracks)"
track_id="trk-smoke-$(python3 -c 'import time; print(int(time.time() * 1000))')"
track_payload="$(printf '{"camera_code":"CAM-001","detections":[{"class":"person","confidence":0.77,"bbox":[0.40,0.30,0.60,0.80],"track_id":"%s","world":%s}]}' "$track_id" "$world_json")"
track_ingest_code="$(http_post_json "/api/ingest/detections" "$track_payload")"
if [ "$track_ingest_code" = "200" ] || [ "$track_ingest_code" = "202" ]; then
  pass "POST /api/ingest/detections with track_id=$track_id -> HTTP $track_ingest_code"
else
  fail "tracked ingest failed (HTTP ${track_ingest_code:-none}, body=$(head -c 300 "$TMP/body.json" 2>/dev/null))"
fi

track_found=""
attempt=0
while [ "$attempt" -lt 10 ]; do
  attempt=$((attempt + 1))
  tracks_code="$(http_get "/api/tracks?camera_id=$cam1_id&limit=200" "$admin_token")"
  if [ "$tracks_code" = "200" ]; then
    track_found="$(python3 - "$TMP/body.json" "$track_id" "$cam1_id" <<'PY'
import json, sys
try:
    data = json.load(open(sys.argv[1]))
except Exception:
    print("")
    sys.exit(0)
items = data.get("items") if isinstance(data, dict) else None
for item in items or []:
    if not isinstance(item, dict):
        continue
    if item.get("track_id") == sys.argv[2] and item.get("camera_id") == sys.argv[3]:
        print(json.dumps({
            "track_id": item.get("track_id"),
            "camera_id": item.get("camera_id"),
            "class": item.get("class"),
        }))
        sys.exit(0)
print("")
PY
)"
  fi
  if [ -n "$track_found" ]; then
    break
  fi
  sleep 0.25
done
if [ -n "$track_found" ]; then
  pass "GET /api/tracks?camera_id=$cam1_id returns $track_found"
else
  fail "track $track_id not found in GET /api/tracks?camera_id=$cam1_id within 2.5s"
fi

# ===========================================================================
# 10. P1.0 workflow: create event -> GET detail with empty actions
# ===========================================================================
step "10/34 event workflow (P1: create -> detail actions empty)"
wf_event_id=""
attempt=0
while [ "$attempt" -lt 3 ] && [ -z "$wf_event_id" ]; do
  attempt=$((attempt + 1))
  wait_dedup_event CAM-003 smoke
  wf_payload='{"camera_code":"CAM-003","detections":[{"class":"smoke","confidence":0.934567,"bbox":[0.40,0.30,0.60,0.80],"world":{"x":-50.0,"y":20.0,"z":0.0}}]}'
  wf_ingest_code="$(http_post_json "/api/ingest/detections" "$wf_payload")"
  if [ "$wf_ingest_code" = "200" ] || [ "$wf_ingest_code" = "202" ]; then
    wf_event_id="$(first_event_id)"
  fi
  if [ -z "$wf_event_id" ]; then
    printf '      workflow event not created on attempt %d (dedup window?); retrying\n' "$attempt"
    sleep 5.2
  fi
done

if [ -n "$wf_event_id" ]; then
  pass "workflow event created: $wf_event_id"
else
  fail "could not create a CAM-003 smoke event for the workflow test"
fi

if [ -n "$wf_event_id" ]; then
  wf_detail_code="$(http_get "/api/events/$wf_event_id" "$admin_token")"
else
  wf_detail_code="000"
fi
if python3 - "$TMP/body.json" "$wf_event_id" "$wf_detail_code" <<'PY'
import json, sys
if len(sys.argv) < 4 or sys.argv[3] != "200":
    sys.exit(1)
try:
    data = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
if not isinstance(data, dict) or not isinstance(data.get("event"), dict):
    sys.exit(1)
actions = data.get("actions")
if actions is None:
    actions = []
sys.exit(0 if data["event"].get("id") == sys.argv[2] and isinstance(actions, list)
         and len(actions) == 0 else 1)
PY
then
  pass "GET /api/events/$wf_event_id -> actions=[]"
else
  fail "GET /api/events/$wf_event_id did not return empty actions (HTTP ${wf_detail_code:-none})"
fi

# ===========================================================================
# 11. P1.0 workflow: dispatch (no status change) then close
# ===========================================================================
step "11/34 event workflow (P1: dispatch -> close)"
if [ -n "$wf_event_id" ]; then
  dispatch_code="$(http_post_json "/api/events/$wf_event_id/dispatch" '{"target":"安保A组","note":"请5分钟内到场"}' "$admin_token")"
else
  dispatch_code="000"
fi
if python3 - "$TMP/body.json" "$dispatch_code" <<'PY'
import json, sys
if len(sys.argv) < 3 or sys.argv[2] != "200":
    sys.exit(1)
try:
    data = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
if not isinstance(data, dict) or data.get("ok") is not True:
    sys.exit(1)
action = data.get("action")
ok = isinstance(action, dict) and action.get("action") == "dispatch" and action.get("target") == "安保A组"
sys.exit(0 if ok else 1)
PY
then
  pass "POST /api/events/$wf_event_id/dispatch -> action=dispatch target=安保A组"
else
  fail "dispatch failed (HTTP ${dispatch_code:-none}, body=$(head -c 300 "$TMP/body.json" 2>/dev/null))"
fi

if [ -n "$wf_event_id" ]; then
  close_code="$(http_post_json "/api/events/$wf_event_id/close" '{"note":"现场已处置"}' "$admin_token")"
else
  close_code="000"
fi
if python3 - "$TMP/body.json" "$close_code" <<'PY'
import json, sys
if len(sys.argv) < 3 or sys.argv[2] != "200":
    sys.exit(1)
try:
    data = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
if not isinstance(data, dict) or data.get("ok") is not True:
    sys.exit(1)
event = data.get("event")
ok = isinstance(event, dict) and event.get("status") == "closed" and bool(event.get("closed_at"))
sys.exit(0 if ok else 1)
PY
then
  pass "POST /api/events/$wf_event_id/close -> status=closed"
else
  fail "close failed (HTTP ${close_code:-none}, body=$(head -c 300 "$TMP/body.json" 2>/dev/null))"
fi

# ===========================================================================
# 12. P1.0 workflow: event detail now lists the dispatch + close actions
# ===========================================================================
step "12/34 event detail shows actions (P1)"
if [ -n "$wf_event_id" ]; then
  wf_detail2_code="$(http_get "/api/events/$wf_event_id" "$admin_token")"
else
  wf_detail2_code="000"
fi
if python3 - "$TMP/body.json" "$wf_event_id" "$wf_detail2_code" <<'PY'
import json, sys
if len(sys.argv) < 4 or sys.argv[3] != "200":
    sys.exit(1)
try:
    data = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
if not isinstance(data, dict) or not isinstance(data.get("event"), dict):
    sys.exit(1)
actions = data.get("actions") or []
kinds = [a.get("action") for a in actions if isinstance(a, dict)]
ok = data["event"].get("id") == sys.argv[2] and len(actions) >= 2
ok = ok and "dispatch" in kinds and "close" in kinds
sys.exit(0 if ok else 1)
PY
then
  wf_action_summary="$(python3 - "$TMP/body.json" <<'PY'
import json, sys
try:
    actions = json.load(open(sys.argv[1])).get("actions") or []
except Exception:
    actions = []
print(",".join(str(a.get("action")) for a in actions if isinstance(a, dict)))
PY
)"
  pass "event detail lists actions: $wf_action_summary"
else
  fail "event detail does not list dispatch+close actions (HTTP ${wf_detail2_code:-none})"
fi

# ===========================================================================
# 13. P1.0 audit log records the write operations
# ===========================================================================
step "13/34 audit (P1)"
audit_code="$(http_get '/api/audit?limit=100' "$admin_token")"
if python3 - "$TMP/body.json" "$audit_code" <<'PY'
import json, sys
if len(sys.argv) < 3 or sys.argv[2] != "200":
    sys.exit(1)
try:
    data = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
items = data.get("items") if isinstance(data, dict) else None
if not isinstance(items, list) or len(items) == 0:
    sys.exit(1)
wanted = {"camera.calibration.put", "event.dispatch", "event.close", "event.ack"}
seen = {item.get("action") for item in items if isinstance(item, dict)}
sys.exit(0 if seen & wanted else 1)
PY
then
  audit_count="$(python3 -c 'import json,sys; print(len(json.load(open(sys.argv[1])).get("items") or []))' "$TMP/body.json" 2>/dev/null || echo '?')"
  pass "GET /api/audit -> $audit_count items incl. calibration/workflow actions"
else
  fail "GET /api/audit failed or empty (HTTP ${audit_code:-none}, body=$(head -c 300 "$TMP/body.json" 2>/dev/null))"
fi

# ===========================================================================
# 14. P1.0 stats summary + events CSV export
# ===========================================================================
step "14/34 stats (P1: summary + events.csv)"
summary_code="$(http_get '/api/stats/summary' "$admin_token")"
if python3 - "$TMP/body.json" "$summary_code" <<'PY'
import json, sys
if len(sys.argv) < 3 or sys.argv[2] != "200":
    sys.exit(1)
try:
    data = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
totals = data.get("totals") if isinstance(data, dict) else None
if not isinstance(totals, dict):
    sys.exit(1)
events = totals.get("events")
severity = totals.get("by_severity")
ok = isinstance(events, int) and events > 0
ok = ok and isinstance(severity, dict) and "info" in severity
sys.exit(0 if ok else 1)
PY
then
  summary_events="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["totals"]["events"])' "$TMP/body.json" 2>/dev/null || echo '?')"
  pass "GET /api/stats/summary -> totals.events=$summary_events (> 0)"
else
  fail "GET /api/stats/summary failed or totals.events == 0 (HTTP ${summary_code:-none})"
fi

if [ -n "$admin_token" ]; then
  csv_code="$(curl -s -o "$TMP/events.csv" -w '%{http_code}' -m 5 \
    -H "Authorization: Bearer $admin_token" "$API_BASE/api/stats/events.csv" 2>/dev/null || true)"
else
  csv_code="$(curl -s -o "$TMP/events.csv" -w '%{http_code}' -m 5 \
    "$API_BASE/api/stats/events.csv" 2>/dev/null || true)"
fi
expected_header='id,camera_code,camera_name,type,severity,confidence,x,y,z,occurred_at,status'
csv_header="$(head -n 1 "$TMP/events.csv" 2>/dev/null | tr -d '\r')"
if [ "$csv_code" = "200" ] && [ "$csv_header" = "$expected_header" ]; then
  pass "GET /api/stats/events.csv -> header matches contract"
else
  fail "GET /api/stats/events.csv failed (HTTP ${csv_code:-none}, header='$csv_header')"
fi

# ===========================================================================
# 15. P2.0 RBAC: viewer read-only, operator ack; prepare event for orders
# ===========================================================================
step "15/34 RBAC (P2: viewer read-only, operator ack)"
read -r viewer_login_code viewer_token < <(login_user viewer 'Viewer@123')
read -r operator_login_code operator_token < <(login_user operator 'Operator@123')
printf '      logins: viewer=%s operator=%s\n' "$viewer_login_code" "$operator_login_code"

# Fresh event for the RBAC/order flow.  CAM-001 lives in area_gate, which is
# inside the operator's assigned areas, so operator ack/order writes are allowed.
rbac_event_id=""
attempt=0
while [ "$attempt" -lt 3 ] && [ -z "$rbac_event_id" ]; do
  attempt=$((attempt + 1))
  wait_dedup_event CAM-001 intrusion
  rbac_payload="$(printf '{"camera_code":"CAM-001","detections":[{"class":"intrusion","confidence":0.945678,"bbox":[0.40,0.30,0.60,0.80],"world":%s}]}' "$world_json")"
  rbac_ingest_code="$(http_post_json "/api/ingest/detections" "$rbac_payload")"
  if [ "$rbac_ingest_code" = "200" ] || [ "$rbac_ingest_code" = "202" ]; then
    rbac_event_id="$(first_event_id)"
  fi
  if [ -z "$rbac_event_id" ]; then
    printf '      RBAC event not created on attempt %d (dedup?); retrying\n' "$attempt"
    sleep 5.2
  fi
done
if [ -n "$rbac_event_id" ]; then
  pass "RBAC/orders event prepared: $rbac_event_id on CAM-001/area_gate (token-free ingest)"
else
  fail "could not create a CAM-001 intrusion event for RBAC/order checks"
fi

viewer_get_code="$(http_get "/api/cameras" "$viewer_token")"
if [ "$viewer_get_code" = "200" ]; then
  pass "viewer GET /api/cameras -> 200 (read allowed)"
else
  fail "viewer GET /api/cameras -> HTTP ${viewer_get_code:-none} (expected 200)"
fi

if [ -n "$rbac_event_id" ]; then
  viewer_ack_code="$(http_post_json "/api/events/$rbac_event_id/ack" '{}' "$viewer_token")"
else
  viewer_ack_code="000"
fi
if [ "$viewer_ack_code" = "403" ]; then
  viewer_err="$(python3 -c 'import json,sys
try: print(json.load(open(sys.argv[1])).get("error",""))
except Exception: print("")' "$TMP/body.json" 2>/dev/null || echo '')"
  pass "viewer POST /api/events/{id}/ack -> 403 ${viewer_err} (write denied)"
else
  fail "viewer POST ack -> HTTP ${viewer_ack_code:-none} (expected 403)"
fi

if [ -n "$rbac_event_id" ]; then
  operator_ack_code="$(http_post_json "/api/events/$rbac_event_id/ack" '{}' "$operator_token")"
else
  operator_ack_code="000"
fi
if python3 - "$TMP/body.json" "$operator_ack_code" <<'PY'
import json, sys
if len(sys.argv) < 3 or sys.argv[2] != "200":
    sys.exit(1)
try:
    data = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
event = data.get("event") if isinstance(data, dict) else None
sys.exit(0 if isinstance(event, dict) and event.get("status") == "acked" else 1)
PY
then
  pass "operator POST /api/events/$rbac_event_id/ack -> 200 status=acked"
else
  fail "operator POST ack failed (HTTP ${operator_ack_code:-none}, body=$(head -c 300 "$TMP/body.json" 2>/dev/null))"
fi

# ===========================================================================
# 16. P2.0 orders: create -> processing -> done (closed_at) -> GET
# ===========================================================================
step "16/34 orders (P2: create -> processing -> done -> GET)"
order_id=""
if [ -n "$rbac_event_id" ]; then
  order_payload="$(printf '{"event_id":"%s","title":"人员入侵 · 测试工单","priority":"high","assignee":"安保A组","note":"5分钟内到场"}' "$rbac_event_id")"
  order_create_code="$(http_post_json "/api/orders" "$order_payload" "$operator_token")"
else
  order_create_code="000"
fi
if python3 - "$TMP/body.json" "$order_create_code" "$rbac_event_id" <<'PY'
import json, sys
if len(sys.argv) < 4 or sys.argv[2] != "201":
    sys.exit(1)
try:
    order = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
ok = isinstance(order, dict) and order.get("event_id") == sys.argv[3]
ok = ok and order.get("status") == "open" and order.get("priority") == "high"
sys.exit(0 if ok else 1)
PY
then
  order_id="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("id") or "")' "$TMP/body.json" 2>/dev/null || echo '')"
  pass "operator POST /api/orders -> 201 order=$order_id status=open"
else
  fail "operator POST /api/orders failed (HTTP ${order_create_code:-none}, body=$(head -c 300 "$TMP/body.json" 2>/dev/null))"
fi

if [ -n "$order_id" ]; then
  order_patch_code="$(http_patch_json "/api/orders/$order_id" '{"status":"processing","assignee":"安保B组","note":"已到场"}' "$operator_token")"
else
  order_patch_code="000"
fi
if python3 - "$TMP/body.json" "$order_patch_code" <<'PY'
import json, sys
if len(sys.argv) < 3 or sys.argv[2] != "200":
    sys.exit(1)
try:
    order = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
sys.exit(0 if isinstance(order, dict) and order.get("status") == "processing"
         and order.get("assignee") == "安保B组" else 1)
PY
then
  pass "operator PATCH /api/orders/$order_id -> processing assignee=安保B组"
else
  fail "order PATCH processing failed (HTTP ${order_patch_code:-none}, body=$(head -c 300 "$TMP/body.json" 2>/dev/null))"
fi

if [ -n "$order_id" ]; then
  order_done_code="$(http_patch_json "/api/orders/$order_id" '{"status":"done"}' "$operator_token")"
else
  order_done_code="000"
fi
if python3 - "$TMP/body.json" "$order_done_code" <<'PY'
import json, sys
if len(sys.argv) < 3 or sys.argv[2] != "200":
    sys.exit(1)
try:
    order = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
sys.exit(0 if isinstance(order, dict) and order.get("status") == "done"
         and bool(order.get("closed_at")) else 1)
PY
then
  pass "operator PATCH /api/orders/$order_id -> done (closed_at set)"
else
  fail "order PATCH done failed (HTTP ${order_done_code:-none}, body=$(head -c 300 "$TMP/body.json" 2>/dev/null))"
fi

if [ -n "$order_id" ]; then
  order_get_code="$(http_get "/api/orders/$order_id" "$operator_token")"
else
  order_get_code="000"
fi
if python3 - "$TMP/body.json" "$order_get_code" "$rbac_event_id" <<'PY'
import json, sys
if len(sys.argv) < 4 or sys.argv[2] != "200":
    sys.exit(1)
try:
    order = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
ok = isinstance(order, dict) and order.get("status") == "done"
ok = ok and order.get("event_id") == sys.argv[3] and bool(order.get("closed_at"))
sys.exit(0 if ok else 1)
PY
then
  pass "GET /api/orders/$order_id -> status=done closed_at set"
else
  fail "GET order failed (HTTP ${order_get_code:-none}, body=$(head -c 300 "$TMP/body.json" 2>/dev/null))"
fi

# ===========================================================================
# 17. P2.0 camera rules (admin): high threshold suppresses events, restore
# ===========================================================================
step "17/34 camera rules (P2: confidence_threshold effect, restore defaults)"
rules_high='{"confidence_threshold":0.99,"dedupe_seconds":5,"enabled_classes":["person","vehicle","intrusion","smoke","no_helmet"],"roi":[[0.0,0.0],[1.0,0.0],[1.0,1.0],[0.0,1.0]]}'
rules_default='{"confidence_threshold":0.6,"dedupe_seconds":5,"enabled_classes":["person","vehicle","intrusion","smoke","no_helmet"],"roi":null}'
if [ -n "$cam1_id" ]; then
  rules_put_code="$(http_put_json "/api/cameras/$cam1_id/rules" "$rules_high" "$admin_token")"
else
  rules_put_code="000"
fi
if python3 - "$TMP/body.json" "$rules_put_code" <<'PY'
import json, sys
if len(sys.argv) < 3 or sys.argv[2] != "200":
    sys.exit(1)
try:
    rules = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
sys.exit(0 if isinstance(rules, dict) and rules.get("confidence_threshold") == 0.99 else 1)
PY
then
  pass "admin PUT /api/cameras/$cam1_id/rules confidence_threshold=0.99 -> 200"
else
  fail "admin PUT camera rules failed (HTTP ${rules_put_code:-none}, body=$(head -c 300 "$TMP/body.json" 2>/dev/null))"
fi

rules_ingest_payload="$(printf '{"camera_code":"CAM-001","detections":[{"class":"person","confidence":0.95,"bbox":[0.42,0.35,0.55,0.72],"world":%s}]}' "$world_json")"
rules_ingest_code="$(http_post_json "/api/ingest/detections" "$rules_ingest_payload")"
if python3 - "$TMP/body.json" "$rules_ingest_code" <<'PY'
import json, sys
if len(sys.argv) < 3 or sys.argv[2] != "202":
    sys.exit(1)
try:
    data = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
events = data.get("events") or []
dropped = data.get("dropped") or 0
accepted = data.get("accepted") or 0
sys.exit(0 if len(events) == 0 and dropped >= 1 and accepted == 0 else 1)
PY
then
  pass "ingest confidence=0.95 under threshold 0.99 -> 0 events (rule enforced)"
else
  fail "threshold=0.99 did not suppress a 0.95 detection (HTTP ${rules_ingest_code:-none}, body=$(head -c 300 "$TMP/body.json" 2>/dev/null))"
fi

if [ -n "$cam1_id" ]; then
  rules_restore_code="$(http_put_json "/api/cameras/$cam1_id/rules" "$rules_default" "$admin_token")"
else
  rules_restore_code="000"
fi
if [ "$rules_restore_code" = "200" ]; then
  pass "admin PUT default rules (threshold=0.6) -> 200 restored"
else
  # Some backends reject roi=null on PUT; fall back to an explicit full-frame polygon.
  rules_default_fallback='{"confidence_threshold":0.6,"dedupe_seconds":5,"enabled_classes":["person","vehicle","intrusion","smoke","no_helmet"],"roi":[[0.0,0.0],[1.0,0.0],[1.0,1.0],[0.0,1.0]]}'
  if [ -n "$cam1_id" ]; then
    rules_restore_code="$(http_put_json "/api/cameras/$cam1_id/rules" "$rules_default_fallback" "$admin_token")"
  fi
  if [ "$rules_restore_code" = "200" ]; then
    pass "admin PUT default rules (threshold=0.6, full-frame roi) -> 200 restored"
  else
    fail "could not restore default rules (HTTP ${rules_restore_code:-none}, body=$(head -c 300 "$TMP/body.json" 2>/dev/null))"
  fi
fi

if [ -n "$cam1_id" ]; then
  rules_get_code="$(http_get "/api/cameras/$cam1_id/rules" "$admin_token")"
else
  rules_get_code="000"
fi
if python3 - "$TMP/body.json" "$rules_get_code" <<'PY'
import json, sys
if len(sys.argv) < 3 or sys.argv[2] != "200":
    sys.exit(1)
try:
    rules = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
threshold = rules.get("confidence_threshold") if isinstance(rules, dict) else None
sys.exit(0 if isinstance(threshold, (int, float)) and abs(threshold - 0.6) < 1e-9 else 1)
PY
then
  pass "GET /api/cameras/$cam1_id/rules -> confidence_threshold=0.6 (defaults)"
else
  fail "GET camera rules failed or threshold not restored (HTTP ${rules_get_code:-none})"
fi

# ===========================================================================
# 18. P2.0 operator camera scope + audit of P2 write operations
# ===========================================================================
step "18/34 operator camera scope + P2 audit"
if [ -n "$cam2_id" ]; then
  scope_out_code="$(http_patch_json "/api/cameras/$cam2_id" '{"status":"degraded"}' "$operator_token")"
else
  scope_out_code="000"
fi
if [ "$scope_out_code" = "403" ]; then
  scope_out_error="$(python3 -c 'import json,sys
try: print(json.load(open(sys.argv[1])).get("error",""))
except Exception: print("")' "$TMP/body.json" 2>/dev/null || echo '')"
  if [ "$scope_out_error" = "out_of_scope" ]; then
    pass "operator PATCH cam_002 (outside areas) -> 403 out_of_scope"
  else
    pass "operator PATCH cam_002 (outside areas) -> 403 ${scope_out_error}"
  fi
else
  fail "operator PATCH cam_002 (outside areas) -> HTTP ${scope_out_code:-none} (expected 403 out_of_scope)"
fi

if [ -n "$cam1_id" ]; then
  scope_in_code="$(http_patch_json "/api/cameras/$cam1_id" '{"status":"online"}' "$operator_token")"
else
  scope_in_code="000"
fi
if python3 - "$TMP/body.json" "$scope_in_code" <<'PY'
import json, sys
if len(sys.argv) < 3 or sys.argv[2] != "200":
    sys.exit(1)
try:
    camera = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
sys.exit(0 if isinstance(camera, dict) and camera.get("status") == "online" else 1)
PY
then
  pass "operator PATCH cam_001 (area_gate, in scope) -> 200 status=online"
else
  fail "operator PATCH cam_001 failed (HTTP ${scope_in_code:-none}, body=$(head -c 300 "$TMP/body.json" 2>/dev/null))"
fi

audit2_code="$(http_get '/api/audit?limit=200' "$admin_token")"
if python3 - "$TMP/body.json" "$audit2_code" <<'PY'
import json, sys
if len(sys.argv) < 3 or sys.argv[2] != "200":
    sys.exit(1)
try:
    data = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
items = data.get("items") if isinstance(data, dict) else None
if not isinstance(items, list):
    sys.exit(1)
seen = {item.get("action") for item in items if isinstance(item, dict)}
needed = {"order.create", "order.update", "camera.rules.put"}
sys.exit(0 if needed <= seen else 1)
PY
then
  audit_p2_summary="$(python3 - "$TMP/body.json" <<'PY'
import json, sys
try:
    items = json.load(open(sys.argv[1])).get("items") or []
except Exception:
    items = []
needed = {"order.create", "order.update", "camera.rules.put"}
seen = sorted({item.get("action") for item in items if isinstance(item, dict)} & needed)
actors = sorted({item.get("actor") for item in items
                 if isinstance(item, dict) and item.get("action") == "order.create"})
print("actions=%s actors=%s" % (",".join(seen), ",".join(str(a) for a in actors)))
PY
)"
  pass "GET /api/audit contains order.create/order.update/camera.rules.put ($audit_p2_summary)"
else
  fail "audit missing P2 write actions (HTTP ${audit2_code:-none}, body=$(head -c 300 "$TMP/body.json" 2>/dev/null))"
fi

# ===========================================================================
# 19. P3.0 data-level filtering (operator scoped reads, admin-only lists)
# ===========================================================================
step "19/34 data filtering (P3: operator scoped reads, admin-only audit/notifications)"

op_cams_code="$(http_get "/api/cameras" "$operator_token")"
op_cams=""
if [ "$op_cams_code" = "200" ]; then
  op_cams="$(python3 - "$TMP/body.json" <<'PY'
import json, sys
try:
    items = json.load(open(sys.argv[1])).get("items") or []
except Exception:
    print("ERROR")
    sys.exit(0)
codes = sorted(str(cam.get("code") or "") for cam in items if isinstance(cam, dict))
print(",".join(codes))
PY
)"
fi
if [ "$op_cams" = "CAM-001,CAM-005,CAM-006" ]; then
  pass "operator GET /api/cameras -> only area cameras [$op_cams]"
else
  fail "operator GET /api/cameras -> HTTP ${op_cams_code:-none} codes='$op_cams' (expected CAM-001,CAM-005,CAM-006)"
fi

# Fresh event on CAM-002 (area_building_a, outside the operator's areas). Ingest
# stays token-free; admin must see it, operator must not.
cam2_event_id=""
attempt=0
while [ "$attempt" -lt 3 ] && [ -z "$cam2_event_id" ]; do
  attempt=$((attempt + 1))
  wait_dedup_event CAM-002 smoke
  cam2_payload='{"camera_code":"CAM-002","detections":[{"class":"smoke","confidence":0.912345,"bbox":[0.40,0.30,0.60,0.80]}]}'
  cam2_ingest_code="$(http_post_json "/api/ingest/detections" "$cam2_payload")"
  if [ "$cam2_ingest_code" = "200" ] || [ "$cam2_ingest_code" = "202" ]; then
    cam2_event_id="$(first_event_id)"
  fi
  if [ -z "$cam2_event_id" ]; then
    printf '      CAM-002 event not created on attempt %d (dedup?); retrying
' "$attempt"
    sleep 5.2
  fi
done

if [ -n "$cam2_event_id" ]; then
  admin_cam2_code="$(http_get "/api/events/$cam2_event_id" "$admin_token")"
else
  admin_cam2_code="000"
fi
if python3 - "$TMP/body.json" "$admin_cam2_code" "$cam2_event_id" <<'PY'
import json, sys
if len(sys.argv) < 4 or sys.argv[2] != "200":
    sys.exit(1)
try:
    data = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
event = data.get("event") if isinstance(data, dict) else None
sys.exit(0 if isinstance(event, dict) and event.get("id") == sys.argv[3] else 1)
PY
then
  pass "admin GET /api/events/$cam2_event_id (out-of-area event) -> 200"
else
  fail "admin could not read the fresh CAM-002 event (HTTP ${admin_cam2_code:-none})"
fi

if [ -n "$cam2_event_id" ]; then
  op_list_code="$(http_get "/api/events?limit=200" "$operator_token")"
else
  op_list_code="000"
fi
if python3 - "$TMP/body.json" "$op_list_code" "$cam2_event_id" <<'PY'
import json, sys
if len(sys.argv) < 4 or sys.argv[2] != "200":
    sys.exit(1)
try:
    data = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
items = data.get("items") if isinstance(data, dict) else None
if not isinstance(items, list):
    sys.exit(1)
ids = {item.get("id") for item in items if isinstance(item, dict)}
sys.exit(0 if sys.argv[3] not in ids else 1)
PY
then
  pass "operator GET /api/events does NOT contain CAM-002 event $cam2_event_id"
else
  fail "operator event list contains (or could not verify hiding of) CAM-002 event $cam2_event_id (HTTP ${op_list_code:-none})"
fi

if [ -n "$cam2_event_id" ]; then
  op_detail_code="$(http_get "/api/events/$cam2_event_id" "$operator_token")"
else
  op_detail_code="000"
fi
if [ "$op_detail_code" = "403" ]; then
  op_detail_error="$(python3 -c 'import json,sys
try: print(json.load(open(sys.argv[1])).get("error",""))
except Exception: print("")' "$TMP/body.json" 2>/dev/null || echo '')"
  pass "operator GET out-of-area event detail -> 403 ${op_detail_error}"
else
  fail "operator GET out-of-area event detail -> HTTP ${op_detail_code:-none} (expected 403 out_of_scope)"
fi

op_audit_code="$(http_get '/api/audit?limit=10' "$operator_token")"
if [ "$op_audit_code" = "403" ]; then
  pass "operator GET /api/audit -> 403 forbidden (admin only)"
else
  fail "operator GET /api/audit -> HTTP ${op_audit_code:-none} (expected 403)"
fi

op_notif_code="$(http_get '/api/notifications?limit=10' "$operator_token")"
if [ "$op_notif_code" = "403" ]; then
  pass "operator GET /api/notifications -> 403 forbidden (admin only)"
else
  fail "operator GET /api/notifications -> HTTP ${op_notif_code:-none} (expected 403)"
fi

# ===========================================================================
# 20. P3.0 escalation settings (admin): baseline reset, PUT, GET reflects
# ===========================================================================
step "20/34 escalation settings (P3: PUT -> GET reflects)"
# Baseline reset so an interrupted previous run cannot affect the checks.
http_put_json "/api/settings/escalation" \
  '{"enabled":true,"after_seconds":300,"severities":["warn","critical"],"auto_dispatch":true}' "$admin_token" >/dev/null
printf '      baseline: escalation defaults (enabled=true, after_seconds=300)\n'
put_notifications_disabled >/dev/null
printf '      baseline: notifications disabled\n'

esc_put_code="$(http_put_json "/api/settings/escalation" \
  '{"enabled":true,"after_seconds":1,"severities":["warn","critical"],"auto_dispatch":true}' "$admin_token")"
if python3 - "$TMP/body.json" "$esc_put_code" <<'PY'
import json, sys
if len(sys.argv) < 3 or sys.argv[2] != "200":
    sys.exit(1)
try:
    settings = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
ok = isinstance(settings, dict) and settings.get("enabled") is True
ok = ok and settings.get("after_seconds") == 1
sys.exit(0 if ok else 1)
PY
then
  pass "admin PUT /api/settings/escalation {enabled:true, after_seconds:1} -> 200"
else
  fail "admin PUT escalation settings failed (HTTP ${esc_put_code:-none}, body=$(head -c 300 "$TMP/body.json" 2>/dev/null))"
fi

esc_get_code="$(http_get "/api/settings/escalation" "$admin_token")"
if python3 - "$TMP/body.json" "$esc_get_code" <<'PY'
import json, sys
if len(sys.argv) < 3 or sys.argv[2] != "200":
    sys.exit(1)
try:
    settings = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
severities = settings.get("severities") if isinstance(settings, dict) else None
ok = settings.get("enabled") is True and settings.get("after_seconds") == 1
ok = ok and isinstance(severities, list) and set(severities) == {"warn", "critical"}
sys.exit(0 if ok else 1)
PY
then
  pass "GET /api/settings/escalation reflects the saved settings"
else
  fail "GET escalation settings not reflected (HTTP ${esc_get_code:-none}, body=$(head -c 300 "$TMP/body.json" 2>/dev/null))"
fi

# ===========================================================================
# 21. P3.0 escalation effect: stale warn event gets escalated_at (<= 25 s)
# ===========================================================================
step "21/34 escalation effect (P3: stale warn event -> escalated_at <= 25s)"
# The stale event must carry occurred_at = now-10s, but the normal 5 s dedupe
# window compares that timestamp against the last event for the same
# camera+class: a concurrently running simulator could make a backdated event
# look like a duplicate (negative delta).  P2 per-camera rules allow disabling
# the dedupe window for CAM-005 during the injection; the original rules are
# restored right afterwards (and again in the EXIT trap).
cam5_rules_backup="$TMP/cam5_rules_backup.json"
if [ -n "$cam5_id" ]; then
  cam5_rules_get_code="$(http_get "/api/cameras/$cam5_id/rules" "$admin_token")"
else
  cam5_rules_get_code="000"
fi
if [ "$cam5_rules_get_code" = "200" ]; then
  cp "$TMP/body.json" "$cam5_rules_backup" 2>/dev/null || true
  dedupe0_payload="$(python3 - "$cam5_rules_backup" <<'PY'
import json, sys
try:
    rules = json.load(open(sys.argv[1]))
except Exception:
    rules = {}
if not isinstance(rules, dict):
    rules = {}
print(json.dumps({
    "confidence_threshold": rules.get("confidence_threshold", 0.6),
    "dedupe_seconds": 0,
    "enabled_classes": rules.get("enabled_classes") or
        ["person", "vehicle", "intrusion", "smoke", "no_helmet"],
    "roi": rules.get("roi"),
}))
PY
)"
  http_put_json "/api/cameras/$cam5_id/rules" "$dedupe0_payload" "$admin_token" >/dev/null
  printf '      CAM-005 rules: dedupe_seconds=0 during the stale-event injection\n'
fi

stale_ts="$(python3 -c 'from datetime import datetime, timezone, timedelta; print((datetime.now(timezone.utc) - timedelta(seconds=10)).isoformat(timespec="milliseconds").replace("+00:00", "Z"))')"
esc_event_id=""
attempt=0
while [ "$attempt" -lt 3 ] && [ -z "$esc_event_id" ]; do
  attempt=$((attempt + 1))
  esc_payload="$(printf '{"camera_code":"CAM-005","ts":"%s","detections":[{"class":"no_helmet","confidence":0.92,"bbox":[0.40,0.30,0.60,0.80]}]}' "$stale_ts")"
  esc_ingest_code="$(http_post_json "/api/ingest/detections" "$esc_payload")"
  if [ "$esc_ingest_code" = "200" ] || [ "$esc_ingest_code" = "202" ]; then
    esc_event_id="$(first_event_id)"
  fi
  if [ -z "$esc_event_id" ]; then
    printf '      stale warn event not created on attempt %d (dedup?); retrying\n' "$attempt"
    stale_ts="$(python3 -c 'from datetime import datetime, timezone, timedelta; print((datetime.now(timezone.utc) - timedelta(seconds=10)).isoformat(timespec="milliseconds").replace("+00:00", "Z"))')"
    sleep 5.2
  fi
done

# Restore CAM-005 rules immediately (dedupe window back to its previous value).
if [ -f "$cam5_rules_backup" ] && [ -n "$cam5_id" ]; then
  restore_cam5_payload="$(python3 - "$cam5_rules_backup" <<'PY'
import json, sys
try:
    rules = json.load(open(sys.argv[1]))
except Exception:
    rules = {}
if not isinstance(rules, dict):
    rules = {}
print(json.dumps({
    "confidence_threshold": rules.get("confidence_threshold", 0.6),
    "dedupe_seconds": rules.get("dedupe_seconds", 5),
    "enabled_classes": rules.get("enabled_classes") or
        ["person", "vehicle", "intrusion", "smoke", "no_helmet"],
    "roi": rules.get("roi"),
}))
PY
)"
  http_put_json "/api/cameras/$cam5_id/rules" "$restore_cam5_payload" "$admin_token" >/dev/null
  printf '      CAM-005 rules restored after the injection\n'
fi

if [ -n "$esc_event_id" ]; then
  pass "stale warn event injected on CAM-005 (ts=now-10s, class=no_helmet): $esc_event_id"
else
  fail "could not inject the stale warn event for the escalation test"
fi

escalated_at=""
escalation_started="$(python3 -c 'import time; print(time.time())')"
attempt=0
while [ "$attempt" -lt 50 ] && [ -z "$escalated_at" ]; do
  attempt=$((attempt + 1))
  # Shared demo environments may have other actors flipping the settings; keep
  # re-applying the fast escalation configuration while we wait.
  if [ "$attempt" -gt 1 ] && [ $((attempt % 10)) -eq 0 ]; then
    http_put_json "/api/settings/escalation" \
      '{"enabled":true,"after_seconds":1,"severities":["warn","critical"],"auto_dispatch":true}' \
      "$admin_token" >/dev/null
  fi
  if [ -n "$esc_event_id" ]; then
    esc_detail_code="$(http_get "/api/events/$esc_event_id" "$admin_token")"
  else
    esc_detail_code="000"
  fi
  if [ "$esc_detail_code" = "200" ]; then
    escalated_at="$(python3 - "$TMP/body.json" <<'PY'
import json, sys
try:
    data = json.load(open(sys.argv[1]))
except Exception:
    print("")
    sys.exit(0)
event = data.get("event") if isinstance(data, dict) else None
print((event or {}).get("escalated_at") or "")
PY
)"
  fi
  if [ -n "$escalated_at" ]; then
    break
  fi
  sleep 0.5
done
esc_delay="$(python3 -c 'import time, sys; print("%.1f" % (time.time() - float(sys.argv[1])))' "$escalation_started")"

if [ -n "$escalated_at" ]; then
  pass "event escalated in ${esc_delay}s -> escalated_at=$escalated_at"
else
  fail "event $esc_event_id was not escalated within 25s (after_seconds=1, worker scans every 15s)"
fi

if [ -n "$esc_event_id" ]; then
  esc_list_code="$(http_get "/api/events?limit=200" "$admin_token")"
else
  esc_list_code="000"
fi
if python3 - "$TMP/body.json" "$esc_list_code" "$esc_event_id" <<'PY'
import json, sys
if len(sys.argv) < 4 or sys.argv[2] != "200":
    sys.exit(1)
try:
    data = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
items = data.get("items") if isinstance(data, dict) else None
if not isinstance(items, list):
    sys.exit(1)
for item in items:
    if isinstance(item, dict) and item.get("id") == sys.argv[3]:
        sys.exit(0 if item.get("escalated_at") else 1)
sys.exit(1)
PY
then
  pass "GET /api/events list shows escalated_at for the escalated event"
else
  fail "escalated event $esc_event_id not visible with escalated_at in GET /api/events?limit=200"
fi

# ===========================================================================
# 22. P3.0 notifications + webhook delivery (local receiver)
# ===========================================================================
step "22/34 notifications + webhook (P3: invalid URL 400, delivery ok)"
if start_receiver; then
  pass "webhook receiver listening on http://127.0.0.1:$WS_RECEIVER_PORT (log=$WS_RECEIVER_LOG)"
else
  fail "could not start scripts/webhook_receiver.py on a free port"
fi

invalid_notif_code="$(http_put_json "/api/settings/notifications" \
  '{"enabled":true,"webhook_url":"not-a-url","format":"generic","min_severity":"warn","timeout_seconds":5}' \
  "$admin_token")"
if [ "$invalid_notif_code" = "400" ]; then
  pass "PUT notifications invalid webhook_url -> 400"
else
  fail "PUT notifications invalid webhook_url -> HTTP ${invalid_notif_code:-none} (expected 400)"
fi

notif_url="http://127.0.0.1:${WS_RECEIVER_PORT}/hook"
notif_payload="$(printf '{"enabled":true,"webhook_url":"%s","format":"generic","min_severity":"warn","timeout_seconds":5,"max_attempts":3,"backoff_seconds":2,"silence_seconds":30}' "$notif_url")"
notif_put_code="$(http_put_json "/api/settings/notifications" "$notif_payload" "$admin_token")"
if python3 - "$TMP/body.json" "$notif_put_code" "$notif_url" <<'PY'
import json, sys
if len(sys.argv) < 4 or sys.argv[2] != "200":
    sys.exit(1)
try:
    settings = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
ok = isinstance(settings, dict) and settings.get("enabled") is True
ok = ok and settings.get("webhook_url") == sys.argv[3]
sys.exit(0 if ok else 1)
PY
then
  pass "admin PUT notifications {enabled:true, webhook_url=$notif_url} -> 200"
else
  fail "admin PUT notification settings failed (HTTP ${notif_put_code:-none}, body=$(head -c 300 "$TMP/body.json" 2>/dev/null))"
fi

notif_get_code="$(http_get "/api/settings/notifications" "$admin_token")"
if python3 - "$TMP/body.json" "$notif_get_code" "$notif_url" <<'PY'
import json, sys
if len(sys.argv) < 4 or sys.argv[2] != "200":
    sys.exit(1)
try:
    settings = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
ok = isinstance(settings, dict) and settings.get("enabled") is True
ok = ok and settings.get("webhook_url") == sys.argv[3]
sys.exit(0 if ok else 1)
PY
then
  pass "GET /api/settings/notifications reflects the saved webhook settings"
else
  fail "GET notification settings not reflected (HTTP ${notif_get_code:-none})"
fi

# New warn event on CAM-006 (different camera/class so dedup cannot swallow it).
warn_event_id=""
attempt=0
while [ "$attempt" -lt 3 ] && [ -z "$warn_event_id" ]; do
  attempt=$((attempt + 1))
  wait_dedup_event CAM-006 no_helmet
  warn_payload='{"camera_code":"CAM-006","detections":[{"class":"no_helmet","confidence":0.93,"bbox":[0.40,0.30,0.60,0.80]}]}'
  warn_ingest_code="$(http_post_json "/api/ingest/detections" "$warn_payload")"
  if [ "$warn_ingest_code" = "200" ] || [ "$warn_ingest_code" = "202" ]; then
    warn_event_id="$(first_event_id)"
  fi
  if [ -z "$warn_event_id" ]; then
    printf '      warn event not created on attempt %d (dedup?); retrying
' "$attempt"
    sleep 5.2
  fi
done
if [ -n "$warn_event_id" ]; then
  pass "warn event created on CAM-006 for webhook delivery: $warn_event_id"
else
  fail "could not create the warn event for the webhook delivery test"
fi

notif_ok=""
attempt=0
while [ "$attempt" -lt 30 ] && [ -z "$notif_ok" ]; do
  attempt=$((attempt + 1))
  notif_list_code="$(http_get '/api/notifications?limit=100' "$admin_token")"
  if [ "$notif_list_code" = "200" ]; then
    notif_ok="$(python3 - "$TMP/body.json" "$warn_event_id" <<'PY'
import json, sys
try:
    data = json.load(open(sys.argv[1]))
except Exception:
    print("")
    sys.exit(0)
items = data.get("items") or []
for item in items:
    if isinstance(item, dict) and item.get("event_id") == sys.argv[2] and item.get("ok") is True:
        print(json.dumps({
            "id": item.get("id"),
            "kind": item.get("kind"),
            "status_code": item.get("status_code"),
        }))
        sys.exit(0)
print("")
PY
)"
  fi
  if [ -n "$notif_ok" ]; then
    break
  fi
  sleep 0.5
done
if [ -n "$notif_ok" ]; then
  pass "GET /api/notifications shows ok=true delivery for $warn_event_id: $notif_ok"
else
  fail "no ok=true notification for event $warn_event_id within 15s"
fi

webhook_line=""
attempt=0
while [ "$attempt" -lt 20 ] && [ -z "$webhook_line" ]; do
  attempt=$((attempt + 1))
  if [ -f "$WS_RECEIVER_LOG" ]; then
    webhook_line="$(python3 - "$WS_RECEIVER_LOG" "$warn_event_id" <<'PY'
import json, sys
try:
    lines = open(sys.argv[1], encoding="utf-8", errors="replace").read().splitlines()
except Exception:
    print("")
    sys.exit(0)
for line in lines:
    try:
        record = json.loads(line)
    except Exception:
        continue
    body = record.get("body")
    text = body if isinstance(body, str) else json.dumps(body, ensure_ascii=False)
    if sys.argv[2] in text:
        print(json.dumps({"path": record.get("path"), "bytes": len(text)}))
        sys.exit(0)
print("")
PY
)"
  fi
  if [ -n "$webhook_line" ]; then
    break
  fi
  sleep 0.25
done
if [ -n "$webhook_line" ]; then
  pass "webhook receiver log contains event $warn_event_id ($webhook_line)"
else
  fail "webhook receiver log $WS_RECEIVER_LOG has no POST containing $warn_event_id"
fi

# ===========================================================================
# 23. P3.0 restore defaults + receiver cleanup
# ===========================================================================
step "23/34 restore P3 defaults + receiver cleanup"
restore_esc_code="$(http_put_json "/api/settings/escalation" \
  '{"enabled":true,"after_seconds":300,"severities":["warn","critical"],"auto_dispatch":true}' "$admin_token")"
if python3 - "$TMP/body.json" "$restore_esc_code" <<'PY'
import json, sys
if len(sys.argv) < 3 or sys.argv[2] != "200":
    sys.exit(1)
try:
    settings = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
ok = isinstance(settings, dict) and settings.get("after_seconds") == 300
sys.exit(0 if ok else 1)
PY
then
  pass "admin PUT escalation defaults (after_seconds=300) -> 200"
else
  fail "could not restore escalation defaults (HTTP ${restore_esc_code:-none})"
fi

esc_restore_get_code="$(http_get "/api/settings/escalation" "$admin_token")"
if python3 - "$TMP/body.json" "$esc_restore_get_code" <<'PY'
import json, sys
if len(sys.argv) < 3 or sys.argv[2] != "200":
    sys.exit(1)
try:
    settings = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
sys.exit(0 if isinstance(settings, dict) and settings.get("after_seconds") == 300 else 1)
PY
then
  pass "GET /api/settings/escalation confirms after_seconds=300 (defaults)"
else
  fail "escalation defaults not confirmed (HTTP ${esc_restore_get_code:-none})"
fi

notif_disable_code="$(put_notifications_disabled)"
notif_after_code="$(http_get "/api/settings/notifications" "$admin_token")"
if [ "$notif_disable_code" = "200" ] && python3 - "$TMP/body.json" "$notif_after_code" <<'PY'
import json, sys
if len(sys.argv) < 3 or sys.argv[2] != "200":
    sys.exit(1)
try:
    settings = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
sys.exit(0 if isinstance(settings, dict) and settings.get("enabled") is False else 1)
PY
then
  pass "notifications disabled (defaults restored)"
else
  fail "could not disable notifications (PUT HTTP ${notif_disable_code:-none})"
fi

if [ -n "$WS_RECEIVER_PID" ]; then
  kill -TERM "$WS_RECEIVER_PID" 2>/dev/null || true
  wait "$WS_RECEIVER_PID" 2>/dev/null || true
  printf '      webhook receiver stopped (pid %s, exit=%s)\n' "$WS_RECEIVER_PID" "$?"
  WS_RECEIVER_PID=""
fi

# ===========================================================================
# 24. P4.0 duty groups: CRUD + /api/duty/current + RBAC
# ===========================================================================
step "24/34 duty groups (P4: CRUD + /api/duty/current + RBAC)"
duty_group_name="东门值班组"
duty_group_payload='{"name":"东门值班组","areas":["area_gate","area_parking"],"operators":["operator"],"active":true}'

duty_create_code="$(http_post_json "/api/duty/groups" "$duty_group_payload" "$admin_token")"
if python3 - "$TMP/body.json" "$duty_create_code" <<'PY'
import json, sys
if len(sys.argv) < 3 or sys.argv[2] != "201":
    sys.exit(1)
try:
    group = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
if not isinstance(group, dict):
    sys.exit(1)
areas = group.get("areas") or []
operators = group.get("operators") or []
ok = group.get("name") == "东门值班组" and group.get("active") is True
ok = ok and "area_gate" in areas and "area_parking" in areas and "operator" in operators
sys.exit(0 if ok else 1)
PY
then
  DUTY_GROUP_ID="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("id") or "")' "$TMP/body.json")"
  pass "admin POST /api/duty/groups -> 201 id=$DUTY_GROUP_ID (areas gate+parking, operator)"
else
  fail "admin POST /api/duty/groups failed (HTTP ${duty_create_code:-none}, body=$(head -c 300 "$TMP/body.json" 2>/dev/null))"
fi

duty_list_code="$(http_get "/api/duty/groups" "$admin_token")"
if python3 - "$TMP/body.json" "$duty_list_code" "$DUTY_GROUP_ID" <<'PY'
import json, sys
if len(sys.argv) < 4 or sys.argv[2] != "200":
    sys.exit(1)
try:
    items = json.load(open(sys.argv[1])).get("items") or []
except Exception:
    sys.exit(1)
sys.exit(0 if any(isinstance(g, dict) and g.get("id") == sys.argv[3] for g in items) else 1)
PY
then
  pass "GET /api/duty/groups contains the new group"
else
  fail "GET /api/duty/groups missing the new group (HTTP ${duty_list_code:-none})"
fi

duty_current_code="$(http_get "/api/duty/current?area_id=area_gate" "$admin_token")"
# Other actors may create additional area_gate duty groups in the shared demo
# environment, so only require that *a* valid active group (with its operators)
# is resolved -- not specifically the group created above.
resolved_duty_group="$(python3 - "$TMP/body.json" "$duty_current_code" <<'PY'
import json, sys
if len(sys.argv) < 3 or sys.argv[2] != "200":
    sys.exit(1)
try:
    data = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
group = data.get("group") if isinstance(data, dict) else None
operators = data.get("operators") if isinstance(data, dict) else None
group_operators = group.get("operators") if isinstance(group, dict) else None
ok = isinstance(group, dict) and bool(group.get("id")) and group.get("active") is True
ok = ok and isinstance(operators, list) and operators == group_operators
if not ok:
    sys.exit(1)
print(json.dumps({"id": group.get("id"), "name": group.get("name"), "operators": operators}))
PY
)"
if [ -n "$resolved_duty_group" ]; then
  pass "GET /api/duty/current?area_id=area_gate resolves a group: $resolved_duty_group"
else
  fail "GET /api/duty/current?area_id=area_gate did not resolve a valid group (HTTP ${duty_current_code:-none}, body=$(head -c 300 "$TMP/body.json" 2>/dev/null))"
fi

if [ -n "$DUTY_GROUP_ID" ]; then
  duty_patch_code="$(http_patch_json "/api/duty/groups/$DUTY_GROUP_ID" '{"active":false}' "$admin_token")"
else
  duty_patch_code="000"
fi
if python3 - "$TMP/body.json" "$duty_patch_code" <<'PY'
import json, sys
if len(sys.argv) < 3 or sys.argv[2] != "200":
    sys.exit(1)
try:
    group = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
sys.exit(0 if isinstance(group, dict) and group.get("active") is False else 1)
PY
then
  pass "admin PATCH duty group active=false -> 200"
else
  fail "admin PATCH duty group active=false failed (HTTP ${duty_patch_code:-none})"
fi
# Re-activate for the auto-dispatch test (not counted as a check).
if [ -n "$DUTY_GROUP_ID" ]; then
  duty_reactivate_code="$(http_patch_json "/api/duty/groups/$DUTY_GROUP_ID" '{"active":true}' "$admin_token")"
  [ "$duty_reactivate_code" = "200" ] || printf '      warning: could not re-activate duty group (HTTP %s)\n' "${duty_reactivate_code:-none}" >&2
fi

op_duty_get_code="$(http_get "/api/duty/groups" "$operator_token")"
if [ "$op_duty_get_code" = "200" ]; then
  pass "operator GET /api/duty/groups -> 200 (logged-in users can read)"
else
  fail "operator GET /api/duty/groups -> HTTP ${op_duty_get_code:-none} (expected 200)"
fi

op_duty_post_code="$(http_post_json "/api/duty/groups" "$duty_group_payload" "$operator_token")"
if [ "$op_duty_post_code" = "403" ]; then
  pass "operator POST /api/duty/groups -> 403 forbidden (admin only)"
else
  fail "operator POST /api/duty/groups -> HTTP ${op_duty_post_code:-none} (expected 403)"
fi

if [ -n "$DUTY_GROUP_ID" ]; then
  duty_del_code="$(http_delete "/api/duty/groups/$DUTY_GROUP_ID" "$admin_token")"
else
  duty_del_code="000"
fi
if [ "$duty_del_code" = "204" ]; then
  pass "admin DELETE /api/duty/groups/$DUTY_GROUP_ID -> 204"
  DUTY_GROUP_ID=""
else
  fail "admin DELETE duty group -> HTTP ${duty_del_code:-none} (expected 204)"
fi

# Re-create the group for the auto-dispatch test.
recreate_code="$(http_post_json "/api/duty/groups" "$duty_group_payload" "$admin_token")"
if [ "$recreate_code" = "201" ]; then
  DUTY_GROUP_ID="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("id") or "")' "$TMP/body.json")"
  pass "duty group re-created for auto-dispatch test (id=$DUTY_GROUP_ID)"
else
  fail "could not re-create the duty group (HTTP ${recreate_code:-none})"
fi

# ===========================================================================
# 25. P4.0 auto-dispatch: escalation -> duty-group order
# ===========================================================================
step "25/34 auto-dispatch (P4: escalation creates order for duty group)"
auto_esc_payload='{"enabled":true,"after_seconds":1,"severities":["warn","critical"],"auto_dispatch":true}'
auto_esc_code="$(http_put_json "/api/settings/escalation" "$auto_esc_payload" "$admin_token")"
if python3 - "$TMP/body.json" "$auto_esc_code" <<'PY'
import json, sys
if len(sys.argv) < 3 or sys.argv[2] != "200":
    sys.exit(1)
try:
    settings = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
sys.exit(0 if isinstance(settings, dict) and settings.get("auto_dispatch") is True else 1)
PY
then
  pass "admin PUT escalation {after_seconds:1, auto_dispatch:true} -> 200"
else
  fail "admin PUT escalation auto_dispatch settings failed (HTTP ${auto_esc_code:-none})"
fi

# Resolve the duty group that currently serves area_gate (other actors may have
# created additional groups in the shared demo environment).
auto_resolved_name=""
auto_cur_code="$(http_get "/api/duty/current?area_id=area_gate" "$admin_token")"
if [ "$auto_cur_code" = "200" ]; then
  auto_resolved_name="$(python3 - "$TMP/body.json" <<'PY'
import json, sys
try:
    group = json.load(open(sys.argv[1])).get("group") or {}
except Exception:
    group = {}
print(group.get("name") or "")
PY
)"
fi
[ -n "$auto_resolved_name" ] || auto_resolved_name="$duty_group_name"
printf '      duty group resolved for area_gate: %s\n' "$auto_resolved_name"

# Backdated warn event on CAM-001 (area_gate). Disable the camera's dedupe
# window for the injection so the ts=now-10s event cannot be swallowed.
cam1_relax_code="$(relax_camera_dedupe "$cam1_id")"
[ "$cam1_relax_code" = "200" ] || printf '      warning: could not relax CAM-001 dedupe (HTTP %s); continuing\n' "${cam1_relax_code:-none}" >&2
auto_stale_ts="$(python3 -c 'from datetime import datetime, timezone, timedelta; print((datetime.now(timezone.utc) - timedelta(seconds=10)).isoformat(timespec="milliseconds").replace("+00:00", "Z"))')"
auto_event_id=""
attempt=0
while [ "$attempt" -lt 3 ] && [ -z "$auto_event_id" ]; do
  attempt=$((attempt + 1))
  auto_payload="$(printf '{"camera_code":"CAM-001","ts":"%s","detections":[{"class":"intrusion","confidence":0.93,"bbox":[0.40,0.30,0.60,0.80]}]}' "$auto_stale_ts")"
  auto_ingest_code="$(http_post_json "/api/ingest/detections" "$auto_payload")"
  if [ "$auto_ingest_code" = "200" ] || [ "$auto_ingest_code" = "202" ]; then
    auto_event_id="$(first_event_id)"
  fi
  if [ -z "$auto_event_id" ]; then
    auto_stale_ts="$(python3 -c 'from datetime import datetime, timezone, timedelta; print((datetime.now(timezone.utc) - timedelta(seconds=10)).isoformat(timespec="milliseconds").replace("+00:00", "Z"))')"
    sleep 5.2
  fi
done
restore_camera_dedupe "$cam1_id" >/dev/null
printf '      stale warn event for auto-dispatch: %s (CAM-001/area_gate)\n' "${auto_event_id:-<none>}"

auto_order=""
attempt=0
while [ "$attempt" -lt 60 ] && [ -z "$auto_order" ]; do
  attempt=$((attempt + 1))
  # Keep the fast auto-dispatch escalation settings applied in case another
  # actor changed them while we wait (shared demo backend).
  if [ "$attempt" -gt 1 ] && [ $((attempt % 10)) -eq 0 ]; then
    http_put_json "/api/settings/escalation" \
      '{"enabled":true,"after_seconds":1,"severities":["warn","critical"],"auto_dispatch":true}' \
      "$admin_token" >/dev/null
  fi
  auto_orders_code="$(http_get "/api/orders?limit=100" "$admin_token")"
  if [ "$auto_orders_code" = "200" ]; then
    auto_order="$(python3 - "$TMP/body.json" "$auto_event_id" <<'PY'
import json, sys
try:
    data = json.load(open(sys.argv[1]))
except Exception:
    print("")
    sys.exit(0)
items = data.get("items") or []
for order in items:
    if not isinstance(order, dict) or order.get("event_id") != sys.argv[2]:
        continue
    note = str(order.get("note") or "")
    if "自动派单" in note:
        print(json.dumps({
            "id": order.get("id"),
            "status": order.get("status"),
            "priority": order.get("priority"),
            "assignee": order.get("assignee"),
            "note": note,
        }))
        sys.exit(0)
print("")
PY
)"
  fi
  if [ -n "$auto_order" ]; then
    break
  fi
  sleep 0.5
done

auto_group_names=""
if [ -n "$auto_order" ]; then
  auto_groups_code="$(http_get "/api/duty/groups" "$admin_token")"
  if [ "$auto_groups_code" = "200" ]; then
    auto_group_names="$(python3 - "$TMP/body.json" <<'PY'
import json, sys
try:
    items = json.load(open(sys.argv[1])).get("items") or []
except Exception:
    items = []
print("|".join(str(g.get("name") or "") for g in items if isinstance(g, dict)))
PY
)"
  fi
fi
if python3 - "$auto_order" "$auto_group_names" <<'PY'
import json, sys
if len(sys.argv) < 3 or not sys.argv[1].strip():
    sys.exit(1)
try:
    order = json.loads(sys.argv[1])
except Exception:
    sys.exit(1)
names = {name for name in sys.argv[2].split("|") if name}
note = str(order.get("note") or "")
ok = "自动派单" in note and order.get("assignee") in names
sys.exit(0 if ok else 1)
PY
then
  pass "escalation auto-dispatch -> order $auto_order (assignee is a duty group name)"
else
  fail "no auto-dispatched order assigned to a duty group (event=$auto_event_id, order=${auto_order:-<none>}, groups=${auto_group_names:-<none>}) within 30s"
fi

auto_audit_code="$(http_get '/api/audit?limit=300' "$admin_token")"
if python3 - "$TMP/body.json" "$auto_audit_code" <<'PY'
import json, sys
if len(sys.argv) < 3 or sys.argv[2] != "200":
    sys.exit(1)
try:
    items = json.load(open(sys.argv[1])).get("items") or []
except Exception:
    sys.exit(1)
for item in items:
    if isinstance(item, dict) and item.get("action") == "order.auto_dispatch":
        print(json.dumps({"actor": item.get("actor"), "resource": item.get("resource")}))
        sys.exit(0)
sys.exit(1)
PY
then
  auto_audit_actor="$(python3 -c 'import json,sys
try:
    for item in json.load(open(sys.argv[1])).get("items") or []:
        if item.get("action") == "order.auto_dispatch":
            print(item.get("actor") or "")
            break
except Exception:
    pass' "$TMP/body.json")"
  pass "GET /api/audit contains order.auto_dispatch (actor=$auto_audit_actor)"
else
  fail "audit missing order.auto_dispatch (HTTP ${auto_audit_code:-none})"
fi

restore_auto_code="$(http_put_json "/api/settings/escalation" \
  '{"enabled":true,"after_seconds":300,"severities":["warn","critical"],"auto_dispatch":true}' "$admin_token")"
if python3 - "$TMP/body.json" "$restore_auto_code" <<'PY'
import json, sys
if len(sys.argv) < 3 or sys.argv[2] != "200":
    sys.exit(1)
try:
    settings = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
ok = isinstance(settings, dict) and settings.get("after_seconds") == 300
ok = ok and settings.get("auto_dispatch") is True
sys.exit(0 if ok else 1)
PY
then
  pass "escalation defaults restored (after_seconds=300, auto_dispatch=true)"
else
  fail "could not restore escalation defaults (HTTP ${restore_auto_code:-none})"
fi

# ===========================================================================
# 26. P4.0 notification retry: receiver --fail-first 2, max_attempts=3
# ===========================================================================
step "26/34 notification retry (P4: --fail-first 2, max_attempts=3, backoff 1s)"
# Keep other event traffic out of the retry/silence checks (shared demo env may
# run the simulator concurrently); restored at the end of step 27.
isolate_events
if start_receiver 2; then
  pass "webhook receiver listening with --fail-first 2 (port=$WS_RECEIVER_PORT)"
else
  fail "could not start scripts/webhook_receiver.py --fail-first 2"
fi

retry_notif_payload="$(printf '{"enabled":true,"webhook_url":"http://127.0.0.1:%s/hook","format":"generic","min_severity":"warn","timeout_seconds":5,"max_attempts":3,"backoff_seconds":1,"silence_seconds":0}' "$WS_RECEIVER_PORT")"
retry_put_code="$(http_put_json "/api/settings/notifications" "$retry_notif_payload" "$admin_token")"
if python3 - "$TMP/body.json" "$retry_put_code" <<'PY'
import json, sys
if len(sys.argv) < 3 or sys.argv[2] != "200":
    sys.exit(1)
try:
    settings = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
ok = isinstance(settings, dict) and settings.get("enabled") is True
ok = ok and settings.get("max_attempts") == 3 and settings.get("backoff_seconds") == 1
ok = ok and settings.get("silence_seconds") == 0
sys.exit(0 if ok else 1)
PY
then
  pass "admin PUT notifications max_attempts=3 backoff_seconds=1 -> 200"
else
  fail "admin PUT notification retry settings failed (HTTP ${retry_put_code:-none}, body=$(head -c 300 "$TMP/body.json" 2>/dev/null))"
fi

retry_event_id=""
attempt=0
while [ "$attempt" -lt 3 ] && [ -z "$retry_event_id" ]; do
  attempt=$((attempt + 1))
  wait_dedup_event CAM-005 intrusion
  retry_payload='{"camera_code":"CAM-005","detections":[{"class":"intrusion","confidence":0.995,"bbox":[0.40,0.30,0.60,0.80]}]}'
  retry_ingest_code="$(http_post_json "/api/ingest/detections" "$retry_payload")"
  if [ "$retry_ingest_code" = "200" ] || [ "$retry_ingest_code" = "202" ]; then
    retry_event_id="$(first_event_id)"
  fi
  if [ -z "$retry_event_id" ]; then
    sleep 5.2
  fi
done
printf '      warn event for retry test: %s (CAM-005)\n' "${retry_event_id:-<none>}"

retry_delivery=""
attempt=0
while [ "$attempt" -lt 60 ] && [ -z "$retry_delivery" ]; do
  attempt=$((attempt + 1))
  if [ "$attempt" -gt 1 ] && [ $((attempt % 10)) -eq 0 ]; then
    # Another actor may be editing settings (shared demo backend).
    http_put_json "/api/settings/notifications" "$retry_notif_payload" "$admin_token" >/dev/null
  fi
  retry_list_code="$(http_get '/api/notifications?limit=100' "$admin_token")"
  if [ "$retry_list_code" = "200" ]; then
    retry_delivery="$(python3 - "$TMP/body.json" "$retry_event_id" <<'PY'
import json, sys
try:
    data = json.load(open(sys.argv[1]))
except Exception:
    print("")
    sys.exit(0)
for item in data.get("items") or []:
    if not isinstance(item, dict) or item.get("event_id") != sys.argv[2]:
        continue
    if item.get("ok") is True:
        print(json.dumps({
            "id": item.get("id"),
            "attempts": item.get("attempts"),
            "status_code": item.get("status_code"),
        }))
        sys.exit(0)
print("")
PY
)"
  fi
  if [ -n "$retry_delivery" ]; then
    break
  fi
  sleep 0.5
done
if python3 - "$retry_delivery" <<'PY'
import json, sys
if len(sys.argv) < 2 or not sys.argv[1].strip():
    sys.exit(1)
try:
    delivery = json.loads(sys.argv[1])
except Exception:
    sys.exit(1)
sys.exit(0 if delivery.get("attempts") == 3 and delivery.get("status_code") == 200 else 1)
PY
then
  pass "notification retried -> ok=true attempts=3 status_code=200 ($retry_delivery)"
else
  fail "notification delivery for $retry_event_id did not succeed after 3 attempts within 25s (got ${retry_delivery:-<none>})"
fi

retry_posts=""
attempt=0
while [ "$attempt" -lt 20 ] && [ -z "$retry_posts" ]; do
  attempt=$((attempt + 1))
  if [ -f "$WS_RECEIVER_LOG" ]; then
    retry_posts="$(python3 - "$WS_RECEIVER_LOG" "$retry_event_id" <<'PY'
import json, sys
count = 0
statuses = []
try:
    lines = open(sys.argv[1], encoding="utf-8", errors="replace").read().splitlines()
except Exception:
    lines = []
for line in lines:
    try:
        record = json.loads(line)
    except Exception:
        continue
    body = record.get("body")
    text = body if isinstance(body, str) else json.dumps(body, ensure_ascii=False)
    if sys.argv[2] in text:
        count += 1
        statuses.append(record.get("status"))
if count:
    print(json.dumps({"count": count, "statuses": statuses}))
PY
)"
  fi
  if [ -n "$retry_posts" ]; then
    break
  fi
  sleep 0.25
done
if python3 - "$retry_posts" "$WS_RECEIVER_LOG" <<'PY'
import json, sys
if len(sys.argv) < 2 or not sys.argv[1].strip():
    sys.exit(1)
try:
    info = json.loads(sys.argv[1])
except Exception:
    sys.exit(1)
sys.exit(0 if info.get("count") == 3 and info.get("statuses") == [500, 500, 200] else 1)
PY
then
  pass "receiver log shows 3 POSTs for the event ($retry_posts)"
else
  fail "receiver log does not show 3 attempts [500,500,200] for $retry_event_id (got ${retry_posts:-<none>}, log=%s)" "$WS_RECEIVER_LOG"
fi

# ===========================================================================
# 27. P4.0 notification silence: silence_seconds=60 suppresses duplicates
# ===========================================================================
step "27/34 notification silence (P4: silence_seconds=60, suppressed metric)"
silence_notif_payload="$(printf '{"enabled":true,"webhook_url":"http://127.0.0.1:%s/hook","format":"generic","min_severity":"warn","timeout_seconds":5,"max_attempts":3,"backoff_seconds":1,"silence_seconds":60}' "$WS_RECEIVER_PORT")"
silence_put_code="$(http_put_json "/api/settings/notifications" "$silence_notif_payload" "$admin_token")"
if python3 - "$TMP/body.json" "$silence_put_code" <<'PY'
import json, sys
if len(sys.argv) < 3 or sys.argv[2] != "200":
    sys.exit(1)
try:
    settings = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
sys.exit(0 if isinstance(settings, dict) and settings.get("silence_seconds") == 60 else 1)
PY
then
  pass "admin PUT notifications silence_seconds=60 -> 200"
else
  fail "admin PUT notifications silence_seconds=60 failed (HTTP ${silence_put_code:-none})"
fi

# Pick a (camera, class) silence key that has not been delivered in the last
# ~65 s: a previous smoke run (or another actor) may otherwise leave the 60 s
# silence window active for a key, suppressing our "first" event too.
http_get '/api/notifications?limit=100' "$admin_token" >/dev/null
cp "$TMP/body.json" "$TMP/notifs_before_silence.json" 2>/dev/null || true
http_get '/api/events?limit=200' "$admin_token" >/dev/null
cp "$TMP/body.json" "$TMP/events_before_silence.json" 2>/dev/null || true
silence_choice="$(python3 - "$TMP/notifs_before_silence.json" "$TMP/events_before_silence.json" "$TMP/cameras.json" <<'PY'
import json, sys
from datetime import datetime, timezone, timedelta

def parse(ts):
    try:
        return datetime.fromisoformat(str(ts).replace("Z", "+00:00"))
    except Exception:
        return None

try:
    events = json.load(open(sys.argv[2])).get("items") or []
except Exception:
    events = []
meta = {}
for event in events:
    if isinstance(event, dict):
        meta[event.get("id")] = (event.get("camera_code"), event.get("type"), event.get("severity"))

now = datetime.now(timezone.utc)
recent_keys = set()
try:
    items = json.load(open(sys.argv[1])).get("items") or []
except Exception:
    items = []
for item in items:
    if not isinstance(item, dict) or item.get("kind") != "event" or not item.get("ok"):
        continue
    at = parse(item.get("at"))
    if at is None or (now - at) > timedelta(seconds=65):
        continue
    key = meta.get(item.get("event_id"))
    if key:
        recent_keys.add("|".join(str(part) for part in key))

classes = {
    "intrusion": ("intrusion", "warn"),
    "no_helmet": ("no_helmet", "warn"),
    "smoke": ("smoke", "critical"),
}
try:
    cameras = json.load(open(sys.argv[3])).get("items") or []
except Exception:
    cameras = []
cam_by_code = {cam.get("code"): cam.get("id") for cam in cameras if isinstance(cam, dict)}
candidates = [
    ("CAM-006", "intrusion"), ("CAM-002", "no_helmet"), ("CAM-004", "intrusion"),
    ("CAM-005", "intrusion"), ("CAM-003", "no_helmet"), ("CAM-001", "no_helmet"),
    ("CAM-006", "smoke"), ("CAM-002", "smoke"),
]
for code, cls in candidates:
    if code not in cam_by_code:
        continue
    event_type, severity = classes[cls]
    if "%s|%s|%s" % (code, event_type, severity) in recent_keys:
        continue
    print(json.dumps({"camera_code": code, "camera_id": cam_by_code[code], "class": cls}))
    sys.exit(0)
print(json.dumps({
    "camera_code": "CAM-006",
    "camera_id": cam_by_code.get("CAM-006", "cam_006"),
    "class": "intrusion",
}))
PY
)"
silence_camera_code="$(python3 -c 'import json,sys; print(json.loads(sys.argv[1]).get("camera_code") or "CAM-006")' "$silence_choice")"
silence_camera_id="$(python3 -c 'import json,sys; print(json.loads(sys.argv[1]).get("camera_id") or "")' "$silence_choice")"
silence_class="$(python3 -c 'import json,sys; print(json.loads(sys.argv[1]).get("class") or "intrusion")' "$silence_choice")"
[ -n "$silence_camera_id" ] || silence_camera_id="$(camera_id_for "$silence_camera_code")"
printf '      silence key: %s/%s (chosen to avoid recent deliveries)\n' "$silence_camera_code" "$silence_class"

# Two identical (camera+type+severity) warn events ~2 s apart, with the camera
# dedupe window disabled so both events are created.
silence_relax_code="$(relax_camera_dedupe "$silence_camera_id")"
[ "$silence_relax_code" = "200" ] || printf '      warning: could not relax %s dedupe (HTTP %s); continuing\n' "$silence_camera_code" "${silence_relax_code:-none}" >&2
http_put_json "/api/settings/notifications" "$silence_notif_payload" "$admin_token" >/dev/null
silence_payload="$(printf '{"camera_code":"%s","detections":[{"class":"%s","confidence":0.995,"bbox":[0.40,0.30,0.60,0.80]}]}' "$silence_camera_code" "$silence_class")"
silence_ingest_a="$(http_post_json "/api/ingest/detections" "$silence_payload")"
silence_event_a="$(first_event_id)"
sleep 2
http_put_json "/api/settings/notifications" "$silence_notif_payload" "$admin_token" >/dev/null
silence_ingest_b="$(http_post_json "/api/ingest/detections" "$silence_payload")"
silence_event_b="$(first_event_id)"
restore_camera_dedupe "$silence_camera_id" >/dev/null
printf '      silence test events: a=%s b=%s\n' "${silence_event_a:-<none>}" "${silence_event_b:-<none>}"

silence_first=""
attempt=0
while [ "$attempt" -lt 60 ] && [ -z "$silence_first" ]; do
  attempt=$((attempt + 1))
  if [ "$attempt" -gt 1 ] && [ $((attempt % 10)) -eq 0 ]; then
    http_put_json "/api/settings/notifications" "$silence_notif_payload" "$admin_token" >/dev/null
  fi
  silence_list_code="$(http_get '/api/notifications?limit=100' "$admin_token")"
  if [ "$silence_list_code" = "200" ]; then
    silence_first="$(python3 - "$TMP/body.json" "$silence_event_a" <<'PY'
import json, sys
try:
    data = json.load(open(sys.argv[1]))
except Exception:
    print("")
    sys.exit(0)
for item in data.get("items") or []:
    if not isinstance(item, dict) or item.get("event_id") != sys.argv[2]:
        continue
    if item.get("ok") is True:
        print(json.dumps({"id": item.get("id"), "attempts": item.get("attempts")}))
        sys.exit(0)
print("")
PY
)"
  fi
  if [ -n "$silence_first" ]; then
    break
  fi
  sleep 0.5
done
if [ -n "$silence_first" ]; then
  pass "silence: first identical event delivered ($silence_first)"
else
  fail "silence: first event $silence_event_a was not delivered within 15s"
fi

# Give the suppressed delivery a moment to settle, then count deliveries for the pair.
sleep 3
silence_pair=""
silence_pair_code="$(http_get '/api/notifications?limit=100' "$admin_token")"
if [ "$silence_pair_code" = "200" ]; then
  silence_pair="$(python3 - "$TMP/body.json" "$silence_event_a" "$silence_event_b" <<'PY'
import json, sys
try:
    items = json.load(open(sys.argv[1])).get("items") or []
except Exception:
    items = []
ids = {sys.argv[2], sys.argv[3]}
delivered = [item for item in items
             if isinstance(item, dict) and item.get("event_id") in ids and item.get("ok") is True]
print(json.dumps({"delivered": len(delivered), "ids": [item.get("event_id") for item in delivered]}))
PY
)"
fi
if python3 - "$silence_pair" <<'PY'
import json, sys
if len(sys.argv) < 2 or not sys.argv[1].strip():
    sys.exit(1)
try:
    info = json.loads(sys.argv[1])
except Exception:
    sys.exit(1)
sys.exit(0 if info.get("delivered") == 1 else 1)
PY
then
  pass "silence: only 1 delivery for the identical pair ($silence_pair)"
else
  fail "silence: expected exactly 1 delivery for the identical pair (got ${silence_pair:-<none>})"
fi

curl -s -o "$TMP/metrics_p4.txt" -m 5 "$API_BASE/metrics" 2>/dev/null || true
suppressed_value="$(python3 - "$TMP/metrics_p4.txt" <<'PY'
import sys
total = 0.0
try:
    lines = open(sys.argv[1], encoding="utf-8", errors="replace").read().splitlines()
except Exception:
    lines = []
for line in lines:
    parts = line.split()
    if len(parts) >= 2 and parts[0].split("{")[0] == "dsh_twin_notifications_suppressed_total":
        try:
            total += float(parts[1])
        except ValueError:
            pass
print("%.0f" % total)
PY
)"
if python3 -c 'import sys; sys.exit(0 if float(sys.argv[1]) > 0 else 1)' "${suppressed_value:-0}" 2>/dev/null; then
  pass "metrics dsh_twin_notifications_suppressed_total=$suppressed_value (> 0)"
else
  fail "metrics dsh_twin_notifications_suppressed_total=${suppressed_value:-none} (expected > 0)"
fi
release_events

# ===========================================================================
# 28. P4.0 metrics + cleanup
# ===========================================================================
step "28/34 P4 metrics + cleanup"
metrics_p4_code="$(curl -s -o "$TMP/metrics_p4.txt" -w '%{http_code}' -m 5 "$API_BASE/metrics" 2>/dev/null || true)"
if [ "$metrics_p4_code" = "200" ] \
  && grep -q 'dsh_twin_notifications_total{result="ok"}' "$TMP/metrics_p4.txt" \
  && grep -q 'dsh_twin_notifications_total{result="failed"}' "$TMP/metrics_p4.txt"; then
  pass "metrics contain dsh_twin_notifications_total{result=ok|failed}"
else
  fail "metrics missing dsh_twin_notifications_total{result=...} (HTTP ${metrics_p4_code:-none})"
fi

p4_counters="$(python3 - "$TMP/metrics_p4.txt" <<'PY'
import sys

wanted = {
    "dsh_twin_escalations_total": False,
    "dsh_twin_orders_total": False,
    "dsh_twin_duty_dispatches_total": False,
    "dsh_twin_duty_skipped_total": False,
    "dsh_twin_notifications_suppressed_total": False,
}
values = {}
try:
    lines = open(sys.argv[1], encoding="utf-8", errors="replace").read().splitlines()
except Exception:
    lines = []
for line in lines:
    parts = line.split()
    if len(parts) < 2:
        continue
    name = parts[0].split("{")[0]
    if name in wanted:
        wanted[name] = True
        try:
            values[name] = values.get(name, 0.0) + float(parts[1])
        except ValueError:
            pass
missing = [name for name, seen in wanted.items() if not seen]
dispatches = values.get("dsh_twin_duty_dispatches_total", 0.0)
ok = not missing and dispatches > 0
print("ok=%s missing=%s duty_dispatches=%s" % ("1" if ok else "0", ",".join(missing) or "-", "%.0f" % dispatches))
PY
)"
case "$p4_counters" in
  ok=1*)
    pass "metrics counters present ($p4_counters)"
    ;;
  *)
    fail "metrics counters missing or duty_dispatches=0 ($p4_counters)"
    ;;
esac

if [ -n "$DUTY_GROUP_ID" ]; then
  cleanup_duty_code="$(http_delete "/api/duty/groups/$DUTY_GROUP_ID" "$admin_token")"
else
  cleanup_duty_code="000"
fi
if [ "$cleanup_duty_code" = "204" ]; then
  pass "cleanup: DELETE duty group $DUTY_GROUP_ID -> 204"
  DUTY_GROUP_ID=""
else
  fail "cleanup: DELETE duty group -> HTTP ${cleanup_duty_code:-none} (expected 204)"
fi

cleanup_notif_ok=0
cleanup_notif_code="000"
cleanup_attempt=0
while [ "$cleanup_attempt" -lt 3 ] && [ "$cleanup_notif_ok" -ne 1 ]; do
  cleanup_attempt=$((cleanup_attempt + 1))
  cleanup_notif_code="$(put_notifications_disabled)"
  cleanup_notif_get_code="$(http_get "/api/settings/notifications" "$admin_token")"
  if [ "$cleanup_notif_code" = "200" ] && python3 - "$TMP/body.json" "$cleanup_notif_get_code" <<'PY'
import json, sys
if len(sys.argv) < 3 or sys.argv[2] != "200":
    sys.exit(1)
try:
    settings = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
sys.exit(0 if isinstance(settings, dict) and settings.get("enabled") is False else 1)
PY
  then
    cleanup_notif_ok=1
  else
    sleep 0.5
  fi
done
if [ "$cleanup_notif_ok" -eq 1 ]; then
  pass "cleanup: notifications disabled (defaults restored)"
else
  fail "cleanup: could not disable notifications (PUT HTTP ${cleanup_notif_code:-none})"
fi

cleanup_esc_ok=0
cleanup_esc_code="000"
cleanup_attempt=0
while [ "$cleanup_attempt" -lt 3 ] && [ "$cleanup_esc_ok" -ne 1 ]; do
  cleanup_attempt=$((cleanup_attempt + 1))
  http_put_json "/api/settings/escalation" \
    '{"enabled":true,"after_seconds":300,"severities":["warn","critical"],"auto_dispatch":true}' \
    "$admin_token" >/dev/null
  cleanup_esc_code="$(http_get "/api/settings/escalation" "$admin_token")"
  if python3 - "$TMP/body.json" "$cleanup_esc_code" <<'PY'
import json, sys
if len(sys.argv) < 3 or sys.argv[2] != "200":
    sys.exit(1)
try:
    settings = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
ok = isinstance(settings, dict) and settings.get("after_seconds") == 300
ok = ok and settings.get("auto_dispatch") is True
sys.exit(0 if ok else 1)
PY
  then
    cleanup_esc_ok=1
  else
    sleep 0.5
  fi
done
if [ "$cleanup_esc_ok" -eq 1 ]; then
  pass "cleanup: escalation defaults verified (after_seconds=300, auto_dispatch=true)"
else
  fail "cleanup: escalation defaults not restored (HTTP ${cleanup_esc_code:-none})"
fi

if [ -n "$WS_RECEIVER_PID" ]; then
  kill -TERM "$WS_RECEIVER_PID" 2>/dev/null || true
  wait "$WS_RECEIVER_PID" 2>/dev/null || true
  printf '      webhook receiver stopped (pid %s)\n' "$WS_RECEIVER_PID"
  WS_RECEIVER_PID=""
fi

# ===========================================================================
# 29. P5.0 bus status: admin shape + RBAC
# ===========================================================================
step "29/34 bus status (P5: admin status shape, RBAC)"
bus_status_code="$(http_get "/api/bus/status" "$admin_token")"
bus_status_summary=""
bus_next_offset=""
bus_published_before=""
bus_replayed_before=""
if [ "$bus_status_code" = "200" ]; then
  bus_status_summary="$(python3 - "$TMP/body.json" <<'PY'
import json, sys
try:
    status = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
if not isinstance(status, dict):
    sys.exit(1)
driver = status.get("driver")
published = status.get("published")
next_offset = status.get("next_offset")
replayed = status.get("replayed")
subscribers = status.get("subscribers")
ok = isinstance(driver, str) and bool(driver)
ok = ok and isinstance(published, int) and published >= 0
ok = ok and isinstance(next_offset, int) and next_offset >= 0
ok = ok and isinstance(replayed, int) and replayed >= 0
ok = ok and isinstance(subscribers, int) and subscribers >= 0
if not ok:
    sys.exit(1)
print(json.dumps({
    "driver": driver,
    "published": published,
    "next_offset": next_offset,
    "subscribers": subscribers,
    "replayed": replayed,
    "oldest_offset": status.get("oldest_offset"),
    "path": status.get("path"),
}))
PY
)"
fi
if [ -n "$bus_status_summary" ]; then
  bus_next_offset="$(python3 -c 'import json,sys; print(json.loads(sys.argv[1])["next_offset"])' "$bus_status_summary")"
  bus_published_before="$(python3 -c 'import json,sys; print(json.loads(sys.argv[1])["published"])' "$bus_status_summary")"
  bus_replayed_before="$(python3 -c 'import json,sys; print(json.loads(sys.argv[1])["replayed"])' "$bus_status_summary")"
  pass "admin GET /api/bus/status -> 200 ($bus_status_summary)"
else
  fail "admin GET /api/bus/status failed or malformed (HTTP ${bus_status_code:-none}, body=$(head -c 300 "$TMP/body.json" 2>/dev/null))"
fi

op_bus_status_code="$(http_get "/api/bus/status" "$operator_token")"
if [ "$op_bus_status_code" = "403" ]; then
  pass "operator GET /api/bus/status -> 403 forbidden (admin only)"
else
  fail "operator GET /api/bus/status -> HTTP ${op_bus_status_code:-none} (expected 403)"
fi

# ===========================================================================
# 30. P5.0 bus publish: token-free ingest advances published/next_offset
# ===========================================================================
step "30/34 bus publish (P5: token-free ingest advances the bus)"
wait_dedup_event CAM-002 vehicle
bus_ingest_payload='{"camera_code":"CAM-002","detections":[{"class":"vehicle","confidence":0.91,"bbox":[0.40,0.30,0.60,0.80]}]}'
bus_ingest_code="$(http_post_json "/api/ingest/detections" "$bus_ingest_payload")"
if [ "$bus_ingest_code" = "200" ] || [ "$bus_ingest_code" = "202" ]; then
  pass "token-free ingest for bus publish -> HTTP $bus_ingest_code"
else
  fail "token-free ingest failed (HTTP ${bus_ingest_code:-none}, body=$(head -c 300 "$TMP/body.json" 2>/dev/null))"
fi

bus_advanced=""
attempt=0
while [ "$attempt" -lt 20 ] && [ -z "$bus_advanced" ]; do
  attempt=$((attempt + 1))
  bus_poll_code="$(http_get "/api/bus/status" "$admin_token")"
  if [ "$bus_poll_code" = "200" ]; then
    bus_advanced="$(python3 - "$TMP/body.json" "${bus_next_offset:-0}" "${bus_published_before:-0}" <<'PY'
import json, sys
try:
    status = json.load(open(sys.argv[1]))
except Exception:
    print("")
    sys.exit(0)
if not isinstance(status, dict):
    print("")
    sys.exit(0)
try:
    before_next = int(sys.argv[2] or 0)
    before_published = int(sys.argv[3] or 0)
except ValueError:
    before_next = 0
    before_published = 0
next_offset = status.get("next_offset")
published = status.get("published")
if not (isinstance(next_offset, int) and isinstance(published, int)):
    print("")
    sys.exit(0)
if next_offset > before_next and published > before_published:
    print(json.dumps({
        "before_next_offset": before_next,
        "next_offset": next_offset,
        "before_published": before_published,
        "published": published,
        "last_publish_at": status.get("last_publish_at"),
    }))
else:
    print("")
PY
)"
  fi
  if [ -n "$bus_advanced" ]; then
    break
  fi
  sleep 0.5
done
if [ -n "$bus_advanced" ]; then
  pass "GET /api/bus/status advanced after ingest -> $bus_advanced"
else
  fail "bus published/next_offset did not advance within 10s (before next_offset=${bus_next_offset:-?}, published=${bus_published_before:-?})"
fi

# ===========================================================================
# 31. P5.0 replay: replay the freshly published offset
# ===========================================================================
step "31/34 bus replay (P5: replay freshly published offset)"
bus_replay_from="${bus_next_offset:-0}"
bus_replay_to=$((bus_replay_from + 1))
bus_replay_payload="$(printf '{"from_offset":%s,"to_offset":%s,"notify":false,"limit":10}' "$bus_replay_from" "$bus_replay_to")"
bus_replay_code="$(http_post_json "/api/bus/replay" "$bus_replay_payload" "$admin_token")"
bus_replay_result=""
if [ "$bus_replay_code" = "200" ]; then
  bus_replay_result="$(python3 - "$TMP/body.json" <<'PY'
import json, sys
try:
    data = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
if not isinstance(data, dict) or data.get("ok") is not True:
    sys.exit(1)
replayed = data.get("replayed")
if not (isinstance(replayed, int) and replayed >= 1):
    sys.exit(1)
print(json.dumps({
    "replayed": replayed,
    "from_offset": data.get("from_offset"),
    "to_offset": data.get("to_offset"),
}))
PY
)"
fi
if [ -n "$bus_replay_result" ]; then
  pass "admin POST /api/bus/replay [$bus_replay_from,$bus_replay_to] -> 200 $bus_replay_result"
else
  fail "admin replay failed (HTTP ${bus_replay_code:-none}, body=$(head -c 300 "$TMP/body.json" 2>/dev/null))"
fi

bus_replayed_after=""
if [ -n "$bus_replay_result" ]; then
  attempt=0
  while [ "$attempt" -lt 10 ] && [ -z "$bus_replayed_after" ]; do
    attempt=$((attempt + 1))
    bus_after_code="$(http_get "/api/bus/status" "$admin_token")"
    if [ "$bus_after_code" = "200" ]; then
      bus_replayed_after="$(python3 - "$TMP/body.json" "${bus_replayed_before:-0}" <<'PY'
import json, sys
try:
    status = json.load(open(sys.argv[1]))
except Exception:
    print("")
    sys.exit(0)
try:
    before = int(sys.argv[2] or 0)
except ValueError:
    before = 0
replayed = status.get("replayed") if isinstance(status, dict) else None
if isinstance(replayed, int) and replayed > before:
    print(json.dumps({"before_replayed": before, "replayed": replayed}))
else:
    print("")
PY
)"
    fi
    if [ -n "$bus_replayed_after" ]; then
      break
    fi
    sleep 0.5
  done
fi
if [ -n "$bus_replayed_after" ]; then
  pass "GET /api/bus/status replayed counter increased -> $bus_replayed_after"
else
  fail "bus replayed counter did not increase (before=${bus_replayed_before:-?})"
fi

# ===========================================================================
# 32. P5.0 replay validation: invalid range 400, non-admin 403
# ===========================================================================
step "32/34 bus replay validation (P5: 400 invalid range, 403 non-admin)"
invalid_replay_code="$(http_post_json "/api/bus/replay" \
  '{"from_offset":100,"to_offset":10,"notify":false,"limit":10}' "$admin_token")"
if [ "$invalid_replay_code" = "400" ]; then
  invalid_replay_error="$(python3 -c 'import json,sys
try: print(json.load(open(sys.argv[1])).get("error",""))
except Exception: print("")' "$TMP/body.json" 2>/dev/null || echo '')"
  if [ "$invalid_replay_error" = "bad_request" ]; then
    pass "admin POST /api/bus/replay invalid range (from>to) -> 400 bad_request"
  else
    pass "admin POST /api/bus/replay invalid range (from>to) -> 400 ${invalid_replay_error}"
  fi
else
  fail "invalid replay range -> HTTP ${invalid_replay_code:-none} (expected 400 bad_request)"
fi

op_bus_replay_code="$(http_post_json "/api/bus/replay" "$bus_replay_payload" "$operator_token")"
if [ "$op_bus_replay_code" = "403" ]; then
  pass "operator POST /api/bus/replay -> 403 forbidden (admin only)"
else
  fail "operator POST /api/bus/replay -> HTTP ${op_bus_replay_code:-none} (expected 403)"
fi

# ===========================================================================
# 33. P5.0 bus metrics
# ===========================================================================
step "33/34 bus metrics (P5)"
bus_metrics_code="$(curl -s -o "$TMP/metrics_p5.txt" -w '%{http_code}' -m 5 "$API_BASE/metrics" 2>/dev/null || true)"
bus_metrics_summary="$(python3 - "$TMP/metrics_p5.txt" <<'PY'
import sys

values = {}
try:
    lines = open(sys.argv[1], encoding="utf-8", errors="replace").read().splitlines()
except Exception:
    lines = []
for line in lines:
    parts = line.split()
    if len(parts) < 2:
        continue
    name = parts[0].split("{")[0]
    if name in ("dsh_twin_bus_published_total", "dsh_twin_bus_replayed_total"):
        try:
            values[name] = values.get(name, 0.0) + float(parts[1])
        except ValueError:
            values[name] = 0.0
ok = "dsh_twin_bus_published_total" in values and "dsh_twin_bus_replayed_total" in values
if ok:
    print("ok=1 published=%.0f replayed=%.0f" % (
        values["dsh_twin_bus_published_total"], values["dsh_twin_bus_replayed_total"]))
else:
    print("ok=0")
PY
)"
if [ "$bus_metrics_code" = "200" ] && [ "${bus_metrics_summary#ok=1}" != "$bus_metrics_summary" ]; then
  pass "metrics contain P5 bus counters ($bus_metrics_summary)"
else
  fail "metrics missing dsh_twin_bus_published_total/dsh_twin_bus_replayed_total (HTTP ${bus_metrics_code:-none}, $bus_metrics_summary)"
fi

# ===========================================================================
# 34. P6.0 site config: admin/viewer reads + unauthenticated 401
# ===========================================================================
step "34/34 site config (P6: admin/viewer reads, unauthenticated 401)"
site_admin_code="$(http_get "/api/site" "$admin_token")"
site_admin_summary=""
if [ "$site_admin_code" = "200" ]; then
  site_admin_summary="$(python3 - "$TMP/body.json" <<'PY'
import json, sys
try:
    site = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
if not isinstance(site, dict):
    sys.exit(1)
name = site.get("name")
extent = site.get("extent")
models = site.get("models")
map_cfg = site.get("map")
ok = isinstance(name, str) and bool(name.strip())
ok = ok and isinstance(extent, dict)
ok = ok and all(isinstance(extent.get(key), (int, float))
                for key in ("min_x", "max_x", "min_y", "max_y"))
ok = ok and isinstance(models, list) and len(models) >= 1
ok = ok and isinstance(map_cfg, dict)
ok = ok and isinstance(map_cfg.get("style"), str) and bool(map_cfg.get("style"))
if not ok:
    sys.exit(1)
formats = []
model_ids = []
for model in models:
    if not isinstance(model, dict):
        sys.exit(1)
    model_id = model.get("id")
    fmt = str(model.get("format") or "").lower()
    url = model.get("url")
    if not (isinstance(model_id, str) and model_id):
        sys.exit(1)
    if fmt not in ("gltf", "glb"):
        sys.exit(1)
    if not (isinstance(url, str) and url):
        sys.exit(1)
    formats.append(fmt)
    model_ids.append(model_id)
print(json.dumps({
    "name": name,
    "extent": {key: extent.get(key) for key in ("min_x", "max_x", "min_y", "max_y")},
    "models": len(models),
    "formats": sorted(set(formats)),
    "model_ids": model_ids[:5],
    "map_style": map_cfg.get("style"),
    "origin": site.get("origin"),
}, ensure_ascii=False))
PY
)"
fi
if [ -n "$site_admin_summary" ]; then
  pass "admin GET /api/site -> 200 ($site_admin_summary)"
else
  fail "admin GET /api/site failed or malformed (HTTP ${site_admin_code:-none}, body=$(head -c 400 "$TMP/body.json" 2>/dev/null))"
fi

viewer_site_code="$(http_get "/api/site" "$viewer_token")"
if python3 - "$TMP/body.json" "$viewer_site_code" <<'PY'
import json, sys
if len(sys.argv) < 3 or sys.argv[2] != "200":
    sys.exit(1)
try:
    site = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
if not isinstance(site, dict):
    sys.exit(1)
ok = isinstance(site.get("name"), str) and bool(site.get("name").strip())
ok = ok and isinstance(site.get("models"), list) and len(site["models"]) >= 1
ok = ok and isinstance(site.get("extent"), dict)
sys.exit(0 if ok else 1)
PY
then
  pass "viewer GET /api/site -> 200 (authenticated read allowed)"
else
  fail "viewer GET /api/site -> HTTP ${viewer_site_code:-none} or malformed payload (expected 200)"
fi

site_noauth_code="$(http_get "/api/site")"
if [ "$site_noauth_code" = "401" ]; then
  pass "GET /api/site without token -> 401 unauthorized"
else
  fail "GET /api/site without token -> HTTP ${site_noauth_code:-none} (expected 401)"
fi

summary
if [ "$FAIL_COUNT" -eq 0 ]; then
  exit 0
fi
exit 1
