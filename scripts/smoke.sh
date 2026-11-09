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
#
# P7.0 additions (docs/API_P7.md):
#   admin GET /api/users (>=3 seeded users, no password fields), operator -> 403;
#   create user 201 / duplicate 409 username_taken / short password 400; login
#   new user 200 (role viewer); PATCH role->operator + areas area_gate -> re-login
#   /api/auth/me shows role operator and GET /api/cameras is area-scoped;
#   PATCH active=false -> login 401; DELETE 204 -> login 401; self PATCH
#   active=false and self DELETE -> 400 self_protected; audit contains
#   user.create/user.update/user.delete with actor=admin.
#
# P8.0 additions (docs/API_P8.md):
#   duty group with rotation=operator + an all-week 00:00-23:59 shift for
#   area_gate -> /api/duty/current returns the shift and 2 operators, operator
#   POST group -> 403; auto-dispatch passes to a shift operator (rotation), a
#   second stale event rotates to the other operator; second_after_seconds=1
#   re-escalates an escalated event (re_escalated_at, audit event.re_escalate,
#   dsh_twin_re_escalations_total>0) while the same event's open order is
#   deduped (no new order, audit order.auto_dispatch_skipped reason=open_order,
#   dsh_twin_orders_deduped_total>0); second_after_seconds=-1/604801 -> 400;
#   cleanup restores settings, deletes the test group and re-activates groups
#   that were temporarily deactivated.
#
# P10.0 additions (docs/API_P10.md):
#   security headers (X-Content-Type-Options=nosniff, X-Frame-Options=DENY,
#   Referrer-Policy=no-referrer) on /api/health and authenticated /api/cameras;
#   login brute-force lockout with a throwaway lock_<ts> username (5 wrong
#   passwords -> 429 too_many_attempts + Retry-After, admin login unaffected);
#   password policy (9 chars -> 400, letters-only 16 chars -> 400, valid 10+
#   letter+digit -> 201 then delete); admin audit CSV export (200 text/csv +
#   attachment; header id,at,actor,action,resource,detail) while operator gets
#   403; cleanup deletes the test user and verifies admin can still log in.
#   Total: 146 checks (21 P0/P1.0 + 18 P2.0 + 21 P3.0 + 25 P4.0 + 9 P5.0 + 3 P6.0 + 16 P7.0 + 20 P8.0 + 13 P10.0).
#
# P11.0 additions (docs/API_P11.md):
#   login returns refresh_token/refresh_expires_at; POST /api/auth/refresh
#   rotates the pair (old refresh reuse -> 401 invalid_refresh_token, new
#   access works); POST /api/auth/logout revokes the refresh; a throwaway
#   viewer's /api/auth/logout-all bumps token_version (old access 401,
#   re-login OK); admin password PATCH invalidates the target's old access;
#   admin GET /api/users/{id}/sessions lists >=1 session then 0 after
#   POST /api/users/{id}/logout (force logout, old access 401); self force
#   logout -> 400 self_protected; operator force logout -> 403; cleanup deletes
#   the throwaway user and verifies admin login.
#   Total: 169 checks (21 P0/P1.0 + 18 P2.0 + 21 P3.0 + 25 P4.0 + 9 P5.0 + 3 P6.0 + 16 P7.0 + 20 P8.0 + 13 P10.0 + 23 P11.0).
#
# P12.0 additions (docs/API_P12.md):
#   admin/operator GET /api/patrol/routes (200 items array) and viewer GET
#   (200, read-only); operator POST route (2 waypoints, camera_ids cam_001/
#   cam_006, interval 2) -> 201, list contains it, PATCH -> 200; viewer POST
#   -> 403; validation (1 waypoint, 51 waypoints, unknown camera
#   -> 400 unknown_camera, interval 0) -> 400; operator DELETE own route ->
#   204, admin DELETE operator's route -> 204; audit has patrol.create/
#   patrol.update/patrol.delete (actor operator/admin); cleanup removes test
#   routes and verifies admin login.
#   Total: 187 checks (21 P0/P1.0 + 18 P2.0 + 21 P3.0 + 25 P4.0 + 9 P5.0 + 3 P6.0 + 16 P7.0 + 20 P8.0 + 13 P10.0 + 23 P11.0 + 18 P12.0).
#
# P13.0 additions (docs/API_P13.md):
#   patrol schedule CRUD (operator create with next_run_at, list contains,
#   PATCH name/interval, viewer POST 403 / GET 200); validation (missing
#   route_id, unknown route, interval 0, interval 10081 -> 400); run-now
#   creates a pending execution, claim -> running assignee=operator,
#   complete done -> done + finished_at; issue flow -> status issue + order_id
#   and a linked order (title 巡检异常 · route, assignee=operator, status open);
#   viewer claim/complete -> 403; admin deletes the operator's schedule -> 204;
#   audit has patrol.schedule.create/run + patrol.execution.claim/complete;
#   cleanup deletes the test route/schedule and verifies admin login.
#   Total: 209 checks (21 P0/P1.0 + 18 P2.0 + 21 P3.0 + 25 P4.0 + 9 P5.0 + 3 P6.0 + 16 P7.0 + 20 P8.0 + 13 P10.0 + 23 P11.0 + 18 P12.0 + 22 P13.0).
#
# P14.0 additions (docs/API_P14.md):
#   patrol stats (executions_total/done/issue/pending/running, completion_rate
#   and issue_rate consistent with counts, avg_duration_seconds >= 0, by_route
#   and by_assignee summaries; malformed from/to -> 400); execution CSV export
#   (operator -> 200 text/csv + attachment filename patrol-executions.csv +
#   exact header, viewer -> 403); history purge (viewer/operator -> 403,
#   admin DELETE ?before=<future>&status=done -> 200 with deleted>=1, stats
#   done decreases, issue records remain, status=pending -> 400, audit
#   patrol.executions.purge with deleted count); final cleanup purge of
#   done,issue, route/schedule deletion and admin login.
#   Total: 230 checks (21 P0/P1.0 + 18 P2.0 + 21 P3.0 + 25 P4.0 + 9 P5.0 + 3 P6.0 + 16 P7.0 + 20 P8.0 + 13 P10.0 + 23 P11.0 + 18 P12.0 + 22 P13.0 + 21 P14.0).
#
# P15.0 additions (docs/API_P15.md):
#   escalation auto_patrol=true (after_seconds=1, auto_dispatch=false) makes a
#   stale CAM-001/intrusion escalation create exactly one pending patrol
#   execution (GET /api/patrol/executions?event_id=..., schedule_id empty,
#   route_id = the cam_001 route, note contains 告警联动); audit
#   patrol.execution.created has detail.source=event and the execution is in
#   the normal list; dedupe: a wait + a forced re-escalation (second_after_
#   seconds=1) keep exactly one pending execution and audit
#   patrol.execution.skipped reason=duplicate; cleanup restores escalation
#   defaults (after 300, auto_dispatch true, second 0, auto_patrol false),
#   claims+completes the execution to done, admin purges done, route deleted
#   and admin login verified.
#   Total: 246 checks (21 P0/P1.0 + 18 P2.0 + 21 P3.0 + 25 P4.0 + 9 P5.0 + 3 P6.0 + 16 P7.0 + 20 P8.0 + 13 P10.0 + 23 P11.0 + 18 P12.0 + 22 P13.0 + 21 P14.0 + 16 P15.0).
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
SMOKE_USER_ID=""
P10_USER_ID=""
P11_USER_ID=""
P12_ROUTE_IDS_FILE="$TMP/p12_route_ids.txt"
P13_ROUTE_IDS_FILE="$TMP/p13_route_ids.txt"
P13_SCHEDULE_IDS_FILE="$TMP/p13_schedule_ids.txt"
P14_ROUTE_IDS_FILE="$TMP/p14_route_ids.txt"
P14_SCHEDULE_IDS_FILE="$TMP/p14_schedule_ids.txt"
P15_ROUTE_IDS_FILE="$TMP/p15_route_ids.txt"

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
      --data '{"enabled":true,"after_seconds":300,"severities":["warn","critical"],"auto_dispatch":true,"second_after_seconds":0}' \
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
    if [ -n "${SMOKE_USER_ID:-}" ]; then
      curl -s -o /dev/null -m 3 -X DELETE -H "Authorization: Bearer $admin_token" \
        "$API_BASE/api/users/$SMOKE_USER_ID" 2>/dev/null || true
    fi
    if [ -n "${P10_USER_ID:-}" ]; then
      curl -s -o /dev/null -m 3 -X DELETE -H "Authorization: Bearer $admin_token" \
        "$API_BASE/api/users/$P10_USER_ID" 2>/dev/null || true
    fi
    if [ -n "${P11_USER_ID:-}" ]; then
      curl -s -o /dev/null -m 3 -X DELETE -H "Authorization: Bearer $admin_token" \
        "$API_BASE/api/users/$P11_USER_ID" 2>/dev/null || true
    fi
    if [ -f "$P12_ROUTE_IDS_FILE" ]; then
      while IFS= read -r p12_route; do
        [ -n "$p12_route" ] || continue
        curl -s -o /dev/null -m 3 -X DELETE -H "Authorization: Bearer $admin_token" \
          "$API_BASE/api/patrol/routes/$p12_route" 2>/dev/null || true
      done < "$P12_ROUTE_IDS_FILE"
    fi
    if [ -f "$P13_SCHEDULE_IDS_FILE" ]; then
      while IFS= read -r p13_schedule; do
        [ -n "$p13_schedule" ] || continue
        curl -s -o /dev/null -m 3 -X DELETE -H "Authorization: Bearer $admin_token" \
          "$API_BASE/api/patrol/schedules/$p13_schedule" 2>/dev/null || true
      done < "$P13_SCHEDULE_IDS_FILE"
    fi
    if [ -f "$P13_ROUTE_IDS_FILE" ]; then
      while IFS= read -r p13_route; do
        [ -n "$p13_route" ] || continue
        curl -s -o /dev/null -m 3 -X DELETE -H "Authorization: Bearer $admin_token" \
          "$API_BASE/api/patrol/routes/$p13_route" 2>/dev/null || true
      done < "$P13_ROUTE_IDS_FILE"
    fi
    if [ -f "$P14_SCHEDULE_IDS_FILE" ]; then
      while IFS= read -r p14_schedule; do
        [ -n "$p14_schedule" ] || continue
        curl -s -o /dev/null -m 3 -X DELETE -H "Authorization: Bearer $admin_token" \
          "$API_BASE/api/patrol/schedules/$p14_schedule" 2>/dev/null || true
      done < "$P14_SCHEDULE_IDS_FILE"
    fi
    if [ -f "$P14_ROUTE_IDS_FILE" ]; then
      while IFS= read -r p14_route; do
        [ -n "$p14_route" ] || continue
        curl -s -o /dev/null -m 3 -X DELETE -H "Authorization: Bearer $admin_token" \
          "$API_BASE/api/patrol/routes/$p14_route" 2>/dev/null || true
      done < "$P14_ROUTE_IDS_FILE"
    fi
    if [ -f "$P15_ROUTE_IDS_FILE" ]; then
      while IFS= read -r p15_route; do
        [ -n "$p15_route" ] || continue
        curl -s -o /dev/null -m 3 -X DELETE -H "Authorization: Bearer $admin_token" \
          "$API_BASE/api/patrol/routes/$p15_route" 2>/dev/null || true
      done < "$P15_ROUTE_IDS_FILE"
    fi
    if [ -f "${P15_CAM001_RULES_FILE:-$TMP/p15_cam001_rules.json}" ] && [ -n "${admin_token:-}" ]; then
      curl -s -o /dev/null -m 3 -X PUT -H "Authorization: Bearer $admin_token" \
        -H 'Content-Type: application/json' \
        --data "$(cat "$TMP/p15_cam001_rules.json")" \
        "$API_BASE/api/cameras/cam_001/rules" 2>/dev/null || true
    fi
    if [ -f "$TMP/p8_deactivated_groups.txt" ]; then
      while IFS= read -r p8_gid; do
        [ -n "$p8_gid" ] || continue
        curl -s -o /dev/null -m 3 -X PATCH -H 'Content-Type: application/json' \
          -H "Authorization: Bearer $admin_token" --data '{"active":true}' \
          "$API_BASE/api/duty/groups/$p8_gid" 2>/dev/null || true
      done < "$TMP/p8_deactivated_groups.txt"
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

# http_get_with_headers PATH [TOKEN] -> status; response headers -> $TMP/headers.txt,
# body -> $TMP/body.json (used by the P10 security-header / CSV-export checks).
http_get_with_headers() {
  local path="$1"
  local token="${2:-}"
  local args
  args=(-s -D "$TMP/headers.txt" -o "$TMP/body.json" -w '%{http_code}' -m 5 -H 'Accept: application/json')
  if [ -n "$token" ]; then
    args+=(-H "Authorization: Bearer $token")
  fi
  curl "${args[@]}" "$API_BASE$path" 2>/dev/null || true
}

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
step "1/58 health"
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
step "2/58 auth (P2: no-token 401, login, /api/auth/me)"
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
step "3/58 cameras"
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
step "4/58 ingest detection (P0, token-free machine entry)"
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
step "5/58 event appears in /api/events (<= 5s)"
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
step "6/58 ack event (P0)"
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
step "7/58 metrics"
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
step "8/58 calibration (P1: solve -> PUT -> GET camera, admin)"
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
step "9/58 tracks (P1: ingest with track_id -> GET /api/tracks)"
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
step "10/58 event workflow (P1: create -> detail actions empty)"
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
step "11/58 event workflow (P1: dispatch -> close)"
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
step "12/58 event detail shows actions (P1)"
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
step "13/58 audit (P1)"
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
step "14/58 stats (P1: summary + events.csv)"
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
step "15/58 RBAC (P2: viewer read-only, operator ack)"
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
step "16/58 orders (P2: create -> processing -> done -> GET)"
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
step "17/58 camera rules (P2: confidence_threshold effect, restore defaults)"
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
step "18/58 operator camera scope + P2 audit"
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
step "19/58 data filtering (P3: operator scoped reads, admin-only audit/notifications)"

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
step "20/58 escalation settings (P3: PUT -> GET reflects)"
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
step "21/58 escalation effect (P3: stale warn event -> escalated_at <= 25s)"
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
step "22/58 notifications + webhook (P3: invalid URL 400, delivery ok)"
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
step "23/58 restore P3 defaults + receiver cleanup"
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
step "24/58 duty groups (P4: CRUD + /api/duty/current + RBAC)"
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
step "25/58 auto-dispatch (P4: escalation creates order for duty group)"
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
while [ "$attempt" -lt 80 ] && [ -z "$auto_order" ]; do
  attempt=$((attempt + 1))
  # Keep the fast auto-dispatch escalation settings applied in case another
  # actor changed them while we wait (shared demo backend).
  if [ "$attempt" -gt 1 ] && [ $((attempt % 10)) -eq 0 ]; then
    http_put_json "/api/settings/escalation" \
      '{"enabled":true,"after_seconds":1,"severities":["warn","critical"],"auto_dispatch":true,"second_after_seconds":0}' \
      "$admin_token" >/dev/null
    # Another actor may have deleted the duty group; make sure one still serves
    # area_gate so the escalation has somewhere to dispatch.
    auto_cur_code="$(http_get "/api/duty/current?area_id=area_gate" "$admin_token")"
    if [ "$auto_cur_code" = "200" ]; then
      auto_has_group="$(python3 -c 'import json,sys
try: print("yes" if isinstance(json.load(open(sys.argv[1])).get("group"), dict) else "no")
except Exception: print("no")' "$TMP/body.json")"
      if [ "$auto_has_group" != "yes" ]; then
        auto_recreate_code="$(http_post_json "/api/duty/groups" "$duty_group_payload" "$admin_token")"
        if [ "$auto_recreate_code" = "201" ]; then
          DUTY_GROUP_ID="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("id") or "")' "$TMP/body.json")"
          printf '      re-created a duty group for area_gate (id=%s)\n' "$DUTY_GROUP_ID"
        fi
      fi
    fi
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
auto_dispatch_operators=""
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
  auto_cur2_code="$(http_get "/api/duty/current?area_id=area_gate" "$admin_token")"
  if [ "$auto_cur2_code" = "200" ]; then
    auto_dispatch_operators="$(python3 - "$TMP/body.json" <<'PY'
import json, sys
try:
    data = json.load(open(sys.argv[1]))
except Exception:
    data = {}
print("|".join(str(name) for name in (data.get("operators") or [])))
PY
)"
  fi
fi
if python3 - "$auto_order" "$auto_group_names" "$auto_dispatch_operators" <<'PY'
import json, sys
if len(sys.argv) < 4 or not sys.argv[1].strip():
    sys.exit(1)
try:
    order = json.loads(sys.argv[1])
except Exception:
    sys.exit(1)
names = {name for name in sys.argv[2].split("|") if name}
operators = {name for name in sys.argv[3].split("|") if name}
note = str(order.get("note") or "")
assignee = order.get("assignee")
# assignee is the duty-group name (rotation=group, P4) or a shift operator
# username (rotation=operator, P8) depending on the resolved duty configuration.
ok = "自动派单" in note and (assignee in names or assignee in operators)
sys.exit(0 if ok else 1)
PY
then
  pass "escalation auto-dispatch -> order $auto_order (assignee is a duty group/operator)"
else
  fail "no auto-dispatched order assigned to a duty group (event=$auto_event_id, order=${auto_order:-<none>}, groups=${auto_group_names:-<none>}, operators=${auto_dispatch_operators:-<none>}) within 40s"
fi

auto_audit_code=""
auto_audit_attempt=0
while [ "$auto_audit_attempt" -lt 4 ]; do
  auto_audit_attempt=$((auto_audit_attempt + 1))
  auto_audit_code="$(http_get '/api/audit?limit=300' "$admin_token")"
  [ "$auto_audit_code" = "200" ] && break
  sleep 0.5
done
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
step "26/58 notification retry (P4: --fail-first 2, max_attempts=3, backoff 1s)"
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
step "27/58 notification silence (P4: silence_seconds=60, suppressed metric)"
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
step "28/58 P4 metrics + cleanup"
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
if [ "${p4_counters#ok=1}" = "$p4_counters" ]; then
  # Counters may still be catching up (or the process restarted); retry briefly.
  p4_metrics_attempt=0
  while [ "$p4_metrics_attempt" -lt 10 ]; do
    p4_metrics_attempt=$((p4_metrics_attempt + 1))
    sleep 0.5
    curl -s -o "$TMP/metrics_p4.txt" -m 5 "$API_BASE/metrics" 2>/dev/null || true
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
    [ "${p4_counters#ok=1}" != "$p4_counters" ] && break
  done
fi
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
elif [ "$cleanup_duty_code" = "404" ]; then
  pass "cleanup: duty group $DUTY_GROUP_ID already deleted (404 tolerated)"
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
step "29/58 bus status (P5: admin status shape, RBAC)"
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
step "30/58 bus publish (P5: token-free ingest advances the bus)"
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
step "31/58 bus replay (P5: replay freshly published offset)"
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
step "32/58 bus replay validation (P5: 400 invalid range, 403 non-admin)"
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
step "33/58 bus metrics (P5)"
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
step "34/58 site config (P6: admin/viewer reads, unauthenticated 401)"
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

# ===========================================================================
# 35. P7.0 user management: admin CRUD, RBAC, self-protection, scope, audit
# ===========================================================================
step "35/58 user management (P7: CRUD, RBAC, self-protection, scope, audit)"
p7_ts="$(python3 -c 'import time; print(int(time.time()))')"
smoke_username="smoke_user_$p7_ts"
smoke_password="Smoke@12345"
smoke_display="冒烟用户"

# Admin identity (for the self-protection checks).
admin_me_code="$(http_get "/api/auth/me" "$admin_token")"
admin_user_id=""
admin_username=""
if [ "$admin_me_code" = "200" ]; then
  admin_user_id="$(python3 -c 'import json,sys
try: print(json.load(open(sys.argv[1])).get("id") or "")
except Exception: print("")' "$TMP/body.json")"
  admin_username="$(python3 -c 'import json,sys
try: print(json.load(open(sys.argv[1])).get("username") or "")
except Exception: print("")' "$TMP/body.json")"
fi

users_code="$(http_get "/api/users" "$admin_token")"
users_summary=""
if [ "$users_code" = "200" ]; then
  users_summary="$(python3 - "$TMP/body.json" <<'PY'
import json, sys

try:
    items = json.load(open(sys.argv[1])).get("items") or []
except Exception:
    sys.exit(1)
if not isinstance(items, list) or len(items) < 3:
    sys.exit(1)
usernames = sorted(str(user.get("username") or "") for user in items if isinstance(user, dict))
if not {"admin", "operator", "viewer"} <= set(usernames):
    sys.exit(1)

def password_fields(node, path=""):
    found = []
    if isinstance(node, dict):
        for key, value in node.items():
            if "password" in str(key).lower():
                found.append(path + "/" + str(key))
            found.extend(password_fields(value, path + "/" + str(key)))
    elif isinstance(node, list):
        for index, value in enumerate(node):
            found.extend(password_fields(value, path + "/" + str(index)))
    return found

leaks = password_fields(items)
if leaks:
    print("leaks=%s" % ",".join(leaks))
    sys.exit(1)
print(json.dumps({
    "count": len(items),
    "usernames": usernames,
    "has_active": all("active" in user for user in items if isinstance(user, dict)),
    "has_created_at": all("created_at" in user for user in items if isinstance(user, dict)),
}, ensure_ascii=False))
PY
)"
fi
if [ -n "$users_summary" ]; then
  pass "admin GET /api/users -> 200 ($users_summary; no password fields)"
else
  fail "admin GET /api/users failed or malformed (HTTP ${users_code:-none}, body=$(head -c 400 "$TMP/body.json" 2>/dev/null))"
fi

op_users_code="$(http_get "/api/users" "$operator_token")"
if [ "$op_users_code" = "403" ]; then
  pass "operator GET /api/users -> 403 forbidden (admin only)"
else
  fail "operator GET /api/users -> HTTP ${op_users_code:-none} (expected 403)"
fi

create_payload="$(printf '{"username":"%s","password":"%s","display_name":"%s","role":"viewer","areas":[],"active":true}' "$smoke_username" "$smoke_password" "$smoke_display")"
create_code="$(http_post_json "/api/users" "$create_payload" "$admin_token")"
SMOKE_USER_ID=""
if [ "$create_code" = "201" ]; then
  SMOKE_USER_ID="$(python3 - "$TMP/body.json" "$smoke_username" <<'PY'
import json, sys
try:
    user = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
if not isinstance(user, dict):
    sys.exit(1)
if user.get("username") != sys.argv[2] or user.get("role") != "viewer":
    sys.exit(1)
if any("password" in str(key).lower() for key in user):
    sys.exit(1)
if not user.get("id"):
    sys.exit(1)
print(user["id"])
PY
)"
fi
if [ -n "$SMOKE_USER_ID" ]; then
  pass "admin POST /api/users -> 201 id=$SMOKE_USER_ID username=$smoke_username role=viewer (no password field)"
else
  fail "admin POST /api/users failed (HTTP ${create_code:-none}, body=$(head -c 400 "$TMP/body.json" 2>/dev/null))"
fi

dup_code="$(http_post_json "/api/users" "$create_payload" "$admin_token")"
dup_error="$(python3 -c 'import json,sys
try: print(json.load(open(sys.argv[1])).get("error",""))
except Exception: print("")' "$TMP/body.json" 2>/dev/null || echo '')"
if [ "$dup_code" = "409" ]; then
  pass "admin POST /api/users duplicate username -> 409 ${dup_error}"
else
  fail "duplicate username -> HTTP ${dup_code:-none} (expected 409 username_taken)"
fi

short_username="smoke_shortpw_$p7_ts"
short_payload="$(printf '{"username":"%s","password":"1234","display_name":"短密码","role":"viewer","areas":[],"active":true}' "$short_username")"
short_code="$(http_post_json "/api/users" "$short_payload" "$admin_token")"
if [ "$short_code" = "400" ]; then
  pass "admin POST /api/users short password (<8) -> 400"
else
  fail "short password -> HTTP ${short_code:-none} (expected 400)"
fi

read -r new_login_code new_token < <(login_user "$smoke_username" "$smoke_password")
new_login_role="$(python3 -c 'import json,sys
try: print((json.load(open(sys.argv[1])).get("user") or {}).get("role") or "")
except Exception: print("")' "$TMP/login.json" 2>/dev/null || echo '')"
new_login_username="$(python3 -c 'import json,sys
try: print((json.load(open(sys.argv[1])).get("user") or {}).get("username") or "")
except Exception: print("")' "$TMP/login.json" 2>/dev/null || echo '')"
if [ "$new_login_code" = "200" ] && [ -n "$new_token" ] && [ "$new_login_role" = "viewer" ] && [ "$new_login_username" = "$smoke_username" ]; then
  pass "login new user -> 200 username=$new_login_username role=viewer"
else
  fail "login new user failed (HTTP ${new_login_code:-none}, role=${new_login_role:-none})"
fi

if [ -n "$SMOKE_USER_ID" ]; then
  patch_payload='{"role":"operator","areas":["area_gate"],"active":true}'
  patch_code="$(http_patch_json "/api/users/$SMOKE_USER_ID" "$patch_payload" "$admin_token")"
else
  patch_code="000"
fi
patch_ok=0
if python3 - "$TMP/body.json" "$patch_code" <<'PY'
import json, sys
if len(sys.argv) < 3 or sys.argv[2] != "200":
    sys.exit(1)
try:
    user = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
areas = user.get("areas") or []
sys.exit(0 if isinstance(user, dict) and user.get("role") == "operator" and "area_gate" in areas else 1)
PY
then
  patch_ok=1
  pass "admin PATCH user role=operator areas=[area_gate] -> 200"
else
  fail "admin PATCH user failed (HTTP ${patch_code:-none}, body=$(head -c 300 "$TMP/body.json" 2>/dev/null))"
fi

read -r relogin_code relogin_token < <(login_user "$smoke_username" "$smoke_password")
if [ "$relogin_code" = "200" ] && [ -n "$relogin_token" ]; then
  relogin_me_code="$(http_get "/api/auth/me" "$relogin_token")"
else
  relogin_me_code="000"
fi
if python3 - "$TMP/body.json" "$relogin_me_code" "$smoke_username" <<'PY'
import json, sys
if len(sys.argv) < 4 or sys.argv[2] != "200":
    sys.exit(1)
try:
    me = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
sys.exit(0 if isinstance(me, dict) and me.get("username") == sys.argv[3]
         and me.get("role") == "operator" and "area_gate" in (me.get("areas") or []) else 1)
PY
then
  pass "re-login + GET /api/auth/me -> username=$smoke_username role=operator areas=[area_gate]"
else
  fail "re-login /api/auth/me did not reflect the updated role/areas"
fi

# Data-level scope: the new operator (areas=[area_gate]) must only see area_gate cameras.
scoped_cams_code="$(http_get "/api/cameras" "$relogin_token")"
scope_check=""
if [ "$scoped_cams_code" = "200" ]; then
  scope_check="$(python3 - "$TMP/body.json" "$TMP/cameras.json" <<'PY'
import json, sys
try:
    visible = json.load(open(sys.argv[1])).get("items") or []
except Exception:
    sys.exit(1)
try:
    all_cams = json.load(open(sys.argv[2])).get("items") or []
except Exception:
    all_cams = []
expected = sorted(cam.get("id") for cam in all_cams
                  if isinstance(cam, dict) and cam.get("area_id") == "area_gate")
observed = sorted(cam.get("id") for cam in visible if isinstance(cam, dict))
ok = observed == expected and len(expected) > 0
print(json.dumps({
    "observed": observed,
    "expected_area_gate": expected,
    "count": len(observed),
}, ensure_ascii=False))
sys.exit(0 if ok else 1)
PY
)" || scope_check=""
fi
if [ -n "$scope_check" ]; then
  pass "operator (areas=[area_gate]) GET /api/cameras is area-scoped -> $scope_check"
else
  fail "operator camera scope wrong (HTTP ${scoped_cams_code:-none}, body=$(head -c 400 "$TMP/body.json" 2>/dev/null))"
fi

if [ -n "$SMOKE_USER_ID" ]; then
  deactivate_code="$(http_patch_json "/api/users/$SMOKE_USER_ID" '{"active":false}' "$admin_token")"
else
  deactivate_code="000"
fi
if [ "$deactivate_code" = "200" ]; then
  pass "admin PATCH user active=false -> 200"
else
  fail "admin PATCH user active=false failed (HTTP ${deactivate_code:-none})"
fi

read -r deact_login_code _deact_token < <(login_user "$smoke_username" "$smoke_password")
deact_login_error="$(python3 -c 'import json,sys
try: print(json.load(open(sys.argv[1])).get("error",""))
except Exception: print("")' "$TMP/login.json" 2>/dev/null || echo '')"
if [ "$deact_login_code" = "401" ]; then
  pass "login deactivated user -> 401 ${deact_login_error}"
else
  fail "login deactivated user -> HTTP ${deact_login_code:-none} (expected 401 invalid_credentials)"
fi

if [ -n "$SMOKE_USER_ID" ]; then
  delete_user_code="$(http_delete "/api/users/$SMOKE_USER_ID" "$admin_token")"
else
  delete_user_code="000"
fi
if [ "$delete_user_code" = "204" ]; then
  pass "admin DELETE /api/users/$SMOKE_USER_ID -> 204"
  SMOKE_USER_ID=""
else
  fail "admin DELETE user -> HTTP ${delete_user_code:-none} (expected 204)"
fi

read -r deleted_login_code _deleted_token < <(login_user "$smoke_username" "$smoke_password")
if [ "$deleted_login_code" = "401" ]; then
  pass "login deleted user -> 401"
else
  fail "login deleted user -> HTTP ${deleted_login_code:-none} (expected 401)"
fi

if [ -n "$admin_user_id" ]; then
  self_delete_code="$(http_delete "/api/users/$admin_user_id" "$admin_token")"
else
  self_delete_code="000"
fi
self_delete_error="$(python3 -c 'import json,sys
try: print(json.load(open(sys.argv[1])).get("error",""))
except Exception: print("")' "$TMP/body.json" 2>/dev/null || echo '')"
if [ "$self_delete_code" = "400" ] && [ "$self_delete_error" = "self_protected" ]; then
  pass "admin DELETE own id ($admin_user_id) -> 400 self_protected"
else
  fail "admin DELETE own id -> HTTP ${self_delete_code:-none} error=${self_delete_error:-none} (expected 400 self_protected)"
fi

if [ -n "$admin_user_id" ]; then
  self_patch_code="$(http_patch_json "/api/users/$admin_user_id" '{"active":false}' "$admin_token")"
else
  self_patch_code="000"
fi
self_patch_error="$(python3 -c 'import json,sys
try: print(json.load(open(sys.argv[1])).get("error",""))
except Exception: print("")' "$TMP/body.json" 2>/dev/null || echo '')"
if [ "$self_patch_code" = "400" ] && [ "$self_patch_error" = "self_protected" ]; then
  pass "admin PATCH own active=false -> 400 self_protected"
else
  fail "admin PATCH own active=false -> HTTP ${self_patch_code:-none} error=${self_patch_error:-none} (expected 400 self_protected)"
fi

user_audit_code="$(http_get '/api/audit?limit=300' "$admin_token")"
user_audit_summary=""
if [ "$user_audit_code" = "200" ]; then
  user_audit_summary="$(python3 - "$TMP/body.json" "$smoke_username" <<'PY'
import json, sys
try:
    items = json.load(open(sys.argv[1])).get("items") or []
except Exception:
    items = []
wanted = {"user.create", "user.update", "user.delete"}
seen = {}
for item in items:
    if not isinstance(item, dict):
        continue
    action = item.get("action")
    if action in wanted and item.get("actor") == "admin":
        seen.setdefault(action, 0)
        seen[action] += 1
missing = sorted(wanted - set(seen))
print(json.dumps({"ok": not missing, "seen": seen, "missing": missing}, ensure_ascii=False))
sys.exit(0 if not missing else 1)
PY
)" || user_audit_summary=""
fi
if [ -n "$user_audit_summary" ]; then
  pass "audit contains user.create/user.update/user.delete actor=admin ($user_audit_summary)"
else
  fail "audit missing user.create/user.update/user.delete with actor=admin (HTTP ${user_audit_code:-none}, $user_audit_summary)"
fi

# ===========================================================================
# 36. P8.0 duty shifts: rotation=operator group + current shift resolution
# ===========================================================================
step "36/58 duty shifts (P8: rotation/shift group, current duty)"
# Other actors may have created active duty groups for area_gate; temporarily
# deactivate them so the rotation/shift checks resolve deterministically to the
# group created below (restored in the EXIT trap and in step 39).
p8_deactivated_file="$TMP/p8_deactivated_groups.txt"
: > "$p8_deactivated_file"
p8_groups_code="$(http_get "/api/duty/groups" "$admin_token")"
if [ "$p8_groups_code" = "200" ]; then
  p8_other_group_ids="$(python3 - "$TMP/body.json" <<'PY'
import json, sys
try:
    items = json.load(open(sys.argv[1])).get("items") or []
except Exception:
    items = []
print(" ".join(str(group.get("id")) for group in items
               if isinstance(group, dict) and group.get("id")
               and group.get("active") and "area_gate" in (group.get("areas") or [])))
PY
)"
  for p8_gid in $p8_other_group_ids; do
    p8_off_code="$(http_patch_json "/api/duty/groups/$p8_gid" '{"active":false}' "$admin_token")"
    if [ "$p8_off_code" = "200" ]; then
      printf '%s\n' "$p8_gid" >> "$p8_deactivated_file"
      printf '      temporarily deactivated other area_gate duty group %s\n' "$p8_gid"
    fi
  done
fi

duty_shift_payload='{"name":"P8值班组","areas":["area_gate"],"operators":["operator","viewer"],"active":true,"rotation":"operator","shifts":[{"name":"全天","start":"00:00","end":"00:00","weekdays":[1,2,3,4,5,6,7],"operators":["operator","viewer"]}]}'
shift_create_code="$(http_post_json "/api/duty/groups" "$duty_shift_payload" "$admin_token")"
DUTY_GROUP_ID=""
if [ "$shift_create_code" = "201" ]; then
  DUTY_GROUP_ID="$(python3 - "$TMP/body.json" <<'PY'
import json, sys
try:
    group = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
if not isinstance(group, dict) or group.get("rotation") != "operator":
    sys.exit(1)
shifts = group.get("shifts") or []
if len(shifts) < 1 or not isinstance(shifts[0], dict) or shifts[0].get("name") != "全天":
    sys.exit(1)
if "area_gate" not in (group.get("areas") or []):
    sys.exit(1)
print(group.get("id") or "")
PY
)"
fi
if [ -n "$DUTY_GROUP_ID" ]; then
  pass "admin POST duty group rotation=operator shift=全天 00:00-23:59 -> 201 id=$DUTY_GROUP_ID"
else
  fail "admin POST duty group with rotation/shifts failed (HTTP ${shift_create_code:-none}, body=$(head -c 500 "$TMP/body.json" 2>/dev/null))"
fi

shift_current_code="$(http_get "/api/duty/current?area_id=area_gate" "$admin_token")"
shift_current_summary=""
if [ "$shift_current_code" = "200" ]; then
  shift_current_summary="$(python3 - "$TMP/body.json" "$DUTY_GROUP_ID" <<'PY'
import json, sys
try:
    data = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
if not isinstance(data, dict):
    sys.exit(1)
group = data.get("group")
shift = data.get("shift")
operators = data.get("operators")
ok = isinstance(group, dict) and group.get("id") == sys.argv[2]
ok = ok and isinstance(shift, dict) and shift.get("name") == "全天"
ok = ok and isinstance(operators, list) and len(operators) == 2
ok = ok and set(operators) == {"operator", "viewer"}
if not ok:
    sys.exit(1)
print(json.dumps({
    "group": group.get("name"),
    "shift": {"name": shift.get("name"), "start": shift.get("start"), "end": shift.get("end")},
    "operators": operators,
}, ensure_ascii=False))
PY
)"
fi
if [ -n "$shift_current_summary" ]; then
  pass "GET /api/duty/current?area_id=area_gate -> $shift_current_summary"
else
  fail "GET /api/duty/current did not return the all-day shift + 2 operators (HTTP ${shift_current_code:-none}, body=$(head -c 500 "$TMP/body.json" 2>/dev/null))"
fi

op_shift_post_code="$(http_post_json "/api/duty/groups" "$duty_shift_payload" "$operator_token")"
if [ "$op_shift_post_code" = "403" ]; then
  pass "operator POST /api/duty/groups -> 403 forbidden (admin only)"
else
  fail "operator POST /api/duty/groups -> HTTP ${op_shift_post_code:-none} (expected 403)"
fi

# ===========================================================================
# 37. P8.0 rotation: auto-dispatch assignee rotates over shift operators
# ===========================================================================
step "37/58 rotation auto-dispatch (P8: assignee rotates over shift operators)"
# First isolate camera event traffic (a concurrently running simulator would
# otherwise keep creating/auto-dispatching events and advance the rotation
# counter unpredictably), then sweep the currently pending warn/critical events
# with auto_dispatch=false so that only the two events injected below are
# dispatched to the rotation group inside the rotation checks.
isolate_events
http_put_json "/api/settings/escalation" \
  '{"enabled":true,"after_seconds":1,"severities":["warn","critical"],"auto_dispatch":false,"second_after_seconds":0}' \
  "$admin_token" >/dev/null
p8_pending=""
attempt=0
while [ "$attempt" -lt 60 ]; do
  attempt=$((attempt + 1))
  p8_events_code="$(http_get "/api/events?limit=200" "$admin_token")"
  if [ "$p8_events_code" = "200" ]; then
    p8_pending="$(python3 - "$TMP/body.json" <<'PY'
import json, sys
try:
    items = json.load(open(sys.argv[1])).get("items") or []
except Exception:
    items = []
pending = 0
for event in items:
    if not isinstance(event, dict):
        continue
    if event.get("status") == "closed":
        continue
    if event.get("severity") not in ("warn", "critical"):
        continue
    if not event.get("escalated_at"):
        pending += 1
print(pending)
PY
)"
  fi
  [ "$p8_pending" = "0" ] && break
  sleep 0.5
done
printf '      pre-escalation sweep: pending warn/critical events=%s (after %s polls)\n' "${p8_pending:-?}" "$attempt"

p8_rotation_settings='{"enabled":true,"after_seconds":1,"severities":["warn","critical"],"auto_dispatch":true,"second_after_seconds":0}'
p8_rotation_code="$(http_put_json "/api/settings/escalation" "$p8_rotation_settings" "$admin_token")"
if python3 - "$TMP/body.json" "$p8_rotation_code" <<'PY'
import json, sys
if len(sys.argv) < 3 or sys.argv[2] != "200":
    sys.exit(1)
try:
    settings = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
ok = isinstance(settings, dict) and settings.get("auto_dispatch") is True
ok = ok and settings.get("second_after_seconds") == 0
ok = ok and settings.get("after_seconds") == 1
sys.exit(0 if ok else 1)
PY
then
  pass "admin PUT escalation after_seconds=1 auto_dispatch=true second_after_seconds=0 -> 200"
else
  fail "admin PUT P8 escalation settings failed (HTTP ${p8_rotation_code:-none}, body=$(head -c 400 "$TMP/body.json" 2>/dev/null))"
fi

# Stale warn event A on CAM-001 (area_gate); dedupe window disabled during injection.
cam1_relax_code="$(relax_camera_dedupe "$cam1_id")"
[ "$cam1_relax_code" = "200" ] || printf '      warning: could not relax CAM-001 dedupe (HTTP %s)\n' "${cam1_relax_code:-none}" >&2
p8_stale_a="$(python3 -c 'from datetime import datetime, timezone, timedelta; print((datetime.now(timezone.utc) - timedelta(seconds=10)).isoformat(timespec="milliseconds").replace("+00:00", "Z"))')"
p8_event_a=""
attempt=0
while [ "$attempt" -lt 3 ] && [ -z "$p8_event_a" ]; do
  attempt=$((attempt + 1))
  p8_payload_a="$(printf '{"camera_code":"CAM-001","ts":"%s","detections":[{"class":"intrusion","confidence":0.995,"bbox":[0.40,0.30,0.60,0.80]}]}' "$p8_stale_a")"
  p8_ingest_a="$(http_post_json "/api/ingest/detections" "$p8_payload_a")"
  if [ "$p8_ingest_a" = "200" ] || [ "$p8_ingest_a" = "202" ]; then
    p8_event_a="$(first_event_id)"
  fi
  if [ -z "$p8_event_a" ]; then
    p8_stale_a="$(python3 -c 'from datetime import datetime, timezone, timedelta; print((datetime.now(timezone.utc) - timedelta(seconds=10)).isoformat(timespec="milliseconds").replace("+00:00", "Z"))')"
    sleep 5.2
  fi
done
restore_camera_dedupe "$cam1_id" >/dev/null

# Stale warn event B on CAM-006 (also area_gate), different class.
cam6_relax_code="$(relax_camera_dedupe "$cam6_id")"
[ "$cam6_relax_code" = "200" ] || printf '      warning: could not relax CAM-006 dedupe (HTTP %s)\n' "${cam6_relax_code:-none}" >&2
p8_stale_b="$(python3 -c 'from datetime import datetime, timezone, timedelta; print((datetime.now(timezone.utc) - timedelta(seconds=10)).isoformat(timespec="milliseconds").replace("+00:00", "Z"))')"
p8_event_b=""
attempt=0
while [ "$attempt" -lt 3 ] && [ -z "$p8_event_b" ]; do
  attempt=$((attempt + 1))
  p8_payload_b="$(printf '{"camera_code":"CAM-006","ts":"%s","detections":[{"class":"no_helmet","confidence":0.995,"bbox":[0.40,0.30,0.60,0.80]}]}' "$p8_stale_b")"
  p8_ingest_b="$(http_post_json "/api/ingest/detections" "$p8_payload_b")"
  if [ "$p8_ingest_b" = "200" ] || [ "$p8_ingest_b" = "202" ]; then
    p8_event_b="$(first_event_id)"
  fi
  if [ -z "$p8_event_b" ]; then
    p8_stale_b="$(python3 -c 'from datetime import datetime, timezone, timedelta; print((datetime.now(timezone.utc) - timedelta(seconds=10)).isoformat(timespec="milliseconds").replace("+00:00", "Z"))')"
    sleep 5.2
  fi
done
restore_camera_dedupe "$cam6_id" >/dev/null
printf '      rotation events: a=%s (CAM-001/intrusion) b=%s (CAM-006/no_helmet)\n' "${p8_event_a:-<none>}" "${p8_event_b:-<none>}"

p8_orders=""
attempt=0
while [ "$attempt" -lt 180 ] && [ -z "$p8_orders" ]; do
  attempt=$((attempt + 1))
  # Other actors may be editing the shared escalation settings; keep the fast
  # auto-dispatch configuration applied so our events escalate promptly.
  if [ "$attempt" -gt 1 ] && [ $((attempt % 6)) -eq 0 ]; then
    http_put_json "/api/settings/escalation" "$p8_rotation_settings" "$admin_token" >/dev/null
  fi
  p8_orders_code="$(http_get "/api/orders?limit=200" "$admin_token")"
  if [ "$p8_orders_code" = "200" ]; then
    p8_orders="$(python3 - "$TMP/body.json" "$p8_event_a" "$p8_event_b" <<'PY'
import json, sys
try:
    items = json.load(open(sys.argv[1])).get("items") or []
except Exception:
    items = []
found = {}
for order in items:
    if not isinstance(order, dict):
        continue
    if "自动派单" not in str(order.get("note") or ""):
        continue
    for key, event_id in (("a", sys.argv[2]), ("b", sys.argv[3])):
        if event_id and order.get("event_id") == event_id:
            found[key] = {
                "id": order.get("id"),
                "assignee": order.get("assignee"),
                "status": order.get("status"),
                "priority": order.get("priority"),
                "note": order.get("note"),
            }
if "a" in found and "b" in found:
    print(json.dumps(found, ensure_ascii=False))
else:
    print("")
PY
)"
  fi
  if [ -n "$p8_orders" ]; then
    break
  fi
  sleep 0.5
done

p8_order_a="$(python3 -c 'import json,sys
try:
    print(json.dumps(json.loads(sys.argv[1]).get("a")))
except Exception:
    print("")' "$p8_orders" 2>/dev/null || echo '')"
p8_order_b="$(python3 -c 'import json,sys
try:
    print(json.dumps(json.loads(sys.argv[1]).get("b")))
except Exception:
    print("")' "$p8_orders" 2>/dev/null || echo '')"

if python3 - "$p8_order_a" <<'PY'
import json, sys
try:
    order = json.loads(sys.argv[1])
except Exception:
    sys.exit(1)
assignee = order.get("assignee")
sys.exit(0 if assignee in ("operator", "viewer") else 1)
PY
then
  pass "first escalations auto-dispatch -> order assignee is a shift operator ($p8_order_a)"
else
  fail "first auto-dispatch order assignee is not a rotation operator (order=${p8_order_a:-<none>})"
fi

p8_assignee_a="$(python3 -c 'import json,sys
try:
    print(json.loads(sys.argv[1]).get("assignee") or "")
except Exception:
    print("")' "$p8_order_a" 2>/dev/null || echo '')"
if python3 - "$p8_order_b" "$p8_assignee_a" <<'PY'
import json, sys
try:
    order = json.loads(sys.argv[1])
except Exception:
    sys.exit(1)
assignee = order.get("assignee")
if assignee not in ("operator", "viewer"):
    sys.exit(1)
sys.exit(0 if assignee != sys.argv[2] else 1)
PY
then
  pass "second escalations auto-dispatch -> rotation assignee switched ($p8_order_b, first was $p8_assignee_a)"
else
  fail "rotation did not switch assignee (first=$p8_assignee_a, second=${p8_order_b:-<none>})"
fi
release_events

# ===========================================================================
# 38. P8.0 open-order dedupe + second escalation (re_escalated_at)
# ===========================================================================
step "38/58 open-order dedupe + re-escalation (P8: second_after_seconds=1)"
p8_second_settings='{"enabled":true,"after_seconds":1,"severities":["warn","critical"],"auto_dispatch":true,"second_after_seconds":1}'
p8_second_code="$(http_put_json "/api/settings/escalation" "$p8_second_settings" "$admin_token")"
if python3 - "$TMP/body.json" "$p8_second_code" <<'PY'
import json, sys
if len(sys.argv) < 3 or sys.argv[2] != "200":
    sys.exit(1)
try:
    settings = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
sys.exit(0 if isinstance(settings, dict) and settings.get("second_after_seconds") == 1 else 1)
PY
then
  pass "admin PUT escalation second_after_seconds=1 -> 200"
else
  fail "admin PUT second_after_seconds=1 failed (HTTP ${p8_second_code:-none}, body=$(head -c 400 "$TMP/body.json" 2>/dev/null))"
fi

p8_re_escalated=""
p8_re_started="$(python3 -c 'import time; print(time.time())')"
attempt=0
while [ "$attempt" -lt 120 ] && [ -z "$p8_re_escalated" ]; do
  attempt=$((attempt + 1))
  if [ "$attempt" -gt 1 ] && [ $((attempt % 10)) -eq 0 ]; then
    http_put_json "/api/settings/escalation" "$p8_second_settings" "$admin_token" >/dev/null
  fi
  if [ -n "$p8_event_a" ]; then
    p8_re_detail_code="$(http_get "/api/events/$p8_event_a" "$admin_token")"
  else
    p8_re_detail_code="000"
  fi
  if [ "$p8_re_detail_code" = "200" ]; then
    p8_re_escalated="$(python3 - "$TMP/body.json" <<'PY'
import json, sys
try:
    event = json.load(open(sys.argv[1])).get("event") or {}
except Exception:
    print("")
    sys.exit(0)
print(event.get("re_escalated_at") or "")
PY
)"
  fi
  if [ -n "$p8_re_escalated" ]; then
    break
  fi
  sleep 0.5
done
p8_re_delay="$(python3 -c 'import time, sys; print("%.1f" % (time.time() - float(sys.argv[1])))' "$p8_re_started")"
if [ -n "$p8_re_escalated" ]; then
  pass "event $p8_event_a re-escalated in ${p8_re_delay}s -> re_escalated_at=$p8_re_escalated"
else
  fail "event $p8_event_a has no re_escalated_at within 60s (second_after_seconds=1)"
fi

sleep 3
p8_order_count_a="$(http_get "/api/orders?limit=200" "$admin_token" >/dev/null; python3 - "$TMP/body.json" "$p8_event_a" <<'PY'
import json, sys
try:
    items = json.load(open(sys.argv[1])).get("items") or []
except Exception:
    items = []
count = 0
for order in items:
    if isinstance(order, dict) and order.get("event_id") == sys.argv[2]:
        count += 1
print(count)
PY
)"
if [ "$p8_order_count_a" = "1" ]; then
  pass "re-escalation did NOT create a second order for event $p8_event_a (open-order dedupe, count=1)"
else
  fail "expected exactly 1 order for event $p8_event_a after re-escalation, got ${p8_order_count_a:-?}"
fi

p8_audit_code="$(http_get '/api/audit?limit=500' "$admin_token")"
p8_skip_summary=""
if [ "$p8_audit_code" = "200" ]; then
  p8_skip_summary="$(python3 - "$TMP/body.json" <<'PY'
import json, sys
try:
    items = json.load(open(sys.argv[1])).get("items") or []
except Exception:
    items = []
for item in items:
    if not isinstance(item, dict) or item.get("action") != "order.auto_dispatch_skipped":
        continue
    detail = item.get("detail") or {}
    if isinstance(detail, dict) and detail.get("reason") == "open_order":
        print(json.dumps({"resource": item.get("resource"), "reason": detail.get("reason")}, ensure_ascii=False))
        sys.exit(0)
sys.exit(1)
PY
)" || p8_skip_summary=""
fi
if [ -n "$p8_skip_summary" ]; then
  pass "audit contains order.auto_dispatch_skipped reason=open_order ($p8_skip_summary)"
else
  fail "audit missing order.auto_dispatch_skipped with reason=open_order (HTTP ${p8_audit_code:-none})"
fi

p8_audit_re_summary=""
if [ "$p8_audit_code" = "200" ]; then
  p8_audit_re_summary="$(python3 - "$TMP/body.json" <<'PY'
import json, sys
try:
    items = json.load(open(sys.argv[1])).get("items") or []
except Exception:
    items = []
for item in items:
    if isinstance(item, dict) and item.get("action") == "event.re_escalate":
        print(json.dumps({"actor": item.get("actor"), "resource": item.get("resource")}, ensure_ascii=False))
        sys.exit(0)
sys.exit(1)
PY
)" || p8_audit_re_summary=""
fi
if [ -n "$p8_audit_re_summary" ]; then
  pass "audit contains event.re_escalate ($p8_audit_re_summary)"
else
  fail "audit missing event.re_escalate (HTTP ${p8_audit_code:-none})"
fi

curl -s -o "$TMP/metrics_p8.txt" -m 5 "$API_BASE/metrics" 2>/dev/null || true
p8_metric_values="$(python3 - "$TMP/metrics_p8.txt" <<'PY'
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
    if name in ("dsh_twin_orders_deduped_total", "dsh_twin_re_escalations_total"):
        try:
            values[name] = values.get(name, 0.0) + float(parts[1])
        except ValueError:
            pass
print("%.0f|%.0f" % (
    values.get("dsh_twin_orders_deduped_total", -1),
    values.get("dsh_twin_re_escalations_total", -1),
))
PY
)"
p8_deduped="$(printf '%s' "$p8_metric_values" | cut -d'|' -f1)"
p8_re_escalations="$(printf '%s' "$p8_metric_values" | cut -d'|' -f2)"
if python3 -c 'import sys; sys.exit(0 if float(sys.argv[1]) > 0 else 1)' "${p8_deduped:-0}" 2>/dev/null; then
  pass "metrics dsh_twin_orders_deduped_total=$p8_deduped (> 0)"
else
  fail "metrics dsh_twin_orders_deduped_total=${p8_deduped:-none} (expected > 0)"
fi
if python3 -c 'import sys; sys.exit(0 if float(sys.argv[1]) > 0 else 1)' "${p8_re_escalations:-0}" 2>/dev/null; then
  pass "metrics dsh_twin_re_escalations_total=$p8_re_escalations (> 0)"
else
  fail "metrics dsh_twin_re_escalations_total=${p8_re_escalations:-none} (expected > 0)"
fi

# ===========================================================================
# 39. P8.0 settings validation + cleanup
# ===========================================================================
step "39/58 P8 settings validation + cleanup"
p8_invalid_low='{"enabled":true,"after_seconds":1,"severities":["warn","critical"],"auto_dispatch":true,"second_after_seconds":-1}'
p8_invalid_low_code="$(http_put_json "/api/settings/escalation" "$p8_invalid_low" "$admin_token")"
if [ "$p8_invalid_low_code" = "400" ]; then
  pass "admin PUT escalation second_after_seconds=-1 -> 400"
else
  fail "second_after_seconds=-1 -> HTTP ${p8_invalid_low_code:-none} (expected 400)"
fi

p8_invalid_high='{"enabled":true,"after_seconds":1,"severities":["warn","critical"],"auto_dispatch":true,"second_after_seconds":604801}'
p8_invalid_high_code="$(http_put_json "/api/settings/escalation" "$p8_invalid_high" "$admin_token")"
if [ "$p8_invalid_high_code" = "400" ]; then
  pass "admin PUT escalation second_after_seconds=604801 -> 400"
else
  fail "second_after_seconds=604801 -> HTTP ${p8_invalid_high_code:-none} (expected 400)"
fi

cleanup_p8_esc_ok=0
cleanup_p8_attempt=0
while [ "$cleanup_p8_attempt" -lt 3 ] && [ "$cleanup_p8_esc_ok" -ne 1 ]; do
  cleanup_p8_attempt=$((cleanup_p8_attempt + 1))
  http_put_json "/api/settings/escalation" \
    '{"enabled":true,"after_seconds":300,"severities":["warn","critical"],"auto_dispatch":true,"second_after_seconds":0}' \
    "$admin_token" >/dev/null
  cleanup_p8_esc_code="$(http_get "/api/settings/escalation" "$admin_token")"
  if python3 - "$TMP/body.json" "$cleanup_p8_esc_code" <<'PY'
import json, sys
if len(sys.argv) < 3 or sys.argv[2] != "200":
    sys.exit(1)
try:
    settings = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
ok = isinstance(settings, dict) and settings.get("after_seconds") == 300
ok = ok and settings.get("auto_dispatch") is True and settings.get("second_after_seconds") == 0
sys.exit(0 if ok else 1)
PY
  then
    cleanup_p8_esc_ok=1
  else
    sleep 0.5
  fi
done
if [ "$cleanup_p8_esc_ok" -eq 1 ]; then
  pass "cleanup: escalation defaults restored (300 s, auto_dispatch=true, second_after_seconds=0)"
else
  fail "cleanup: escalation defaults not restored (HTTP ${cleanup_p8_esc_code:-none})"
fi

cleanup_p8_notif_code="$(put_notifications_disabled)"
cleanup_p8_notif_get="$(http_get "/api/settings/notifications" "$admin_token")"
if [ "$cleanup_p8_notif_code" = "200" ] && python3 - "$TMP/body.json" "$cleanup_p8_notif_get" <<'PY'
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
  pass "cleanup: notifications disabled (defaults restored)"
else
  fail "cleanup: could not disable notifications (PUT HTTP ${cleanup_p8_notif_code:-none})"
fi

if [ -n "$DUTY_GROUP_ID" ]; then
  p8_group_del_code="$(http_delete "/api/duty/groups/$DUTY_GROUP_ID" "$admin_token")"
else
  p8_group_del_code="000"
fi
if [ "$p8_group_del_code" = "204" ]; then
  pass "cleanup: DELETE test duty group $DUTY_GROUP_ID -> 204"
  DUTY_GROUP_ID=""
else
  fail "cleanup: DELETE test duty group -> HTTP ${p8_group_del_code:-none} (expected 204)"
fi

p8_leftover_groups_code="$(http_get "/api/duty/groups" "$admin_token")"
p8_leftover_groups="$(python3 - "$TMP/body.json" "$p8_leftover_groups_code" <<'PY'
import json, sys
if len(sys.argv) < 3 or sys.argv[2] != "200":
    print("")
    sys.exit(0)
try:
    items = json.load(open(sys.argv[1])).get("items") or []
except Exception:
    items = []
names = [str(group.get("name") or "") for group in items if isinstance(group, dict)]
leftover = [name for name in names if name == "P8值班组"]
print(json.dumps({"groups": names, "leftover": leftover}, ensure_ascii=False))
PY
)"
if python3 - "$p8_leftover_groups" <<'PY'
import json, sys
if len(sys.argv) < 2 or not sys.argv[1].strip():
    sys.exit(1)
try:
    info = json.loads(sys.argv[1])
except Exception:
    sys.exit(1)
sys.exit(0 if not info.get("leftover") else 1)
PY
then
  pass "cleanup: no leftover P8 test duty groups ($p8_leftover_groups)"
else
  fail "leftover P8 duty groups remain ($p8_leftover_groups)"
fi

p8_users_code="$(http_get "/api/users" "$admin_token")"
p8_leftover_users="$(python3 - "$TMP/body.json" "$p8_users_code" <<'PY'
import json, sys
if len(sys.argv) < 3 or sys.argv[2] != "200":
    sys.exit(1)
try:
    items = json.load(open(sys.argv[1])).get("items") or []
except Exception:
    items = []
leftover = [str(user.get("username") or "") for user in items
             if isinstance(user, dict) and str(user.get("username") or "").startswith("smoke_")]
sys.exit(0 if not leftover else 1)
PY
)"
if [ "$p8_users_code" = "200" ] && [ -z "$p8_leftover_users" ]; then
  pass "cleanup: no leftover smoke test users in /api/users"
else
  fail "leftover smoke test users remain in /api/users (HTTP ${p8_users_code:-none})"
fi

# Re-activate duty groups that step 36 deactivated for determinism.
if [ -f "$p8_deactivated_file" ]; then
  while IFS= read -r p8_gid; do
    [ -n "$p8_gid" ] || continue
    p8_reactivate_code="$(http_patch_json "/api/duty/groups/$p8_gid" '{"active":true}' "$admin_token")"
    if [ "$p8_reactivate_code" = "200" ]; then
      printf '      re-activated duty group %s\n' "$p8_gid"
    fi
  done < "$p8_deactivated_file"
  rm -f "$p8_deactivated_file"
fi

# ===========================================================================
# 40. P10.0 security headers
# ===========================================================================
step "40/58 security headers (P10: nosniff/DENY/no-referrer)"
p10_headers_health="$(http_get_with_headers "/api/health")"
p10_health_headers_summary="$(python3 - "$TMP/headers.txt" "$p10_headers_health" <<'PY'
import sys

status = sys.argv[2] if len(sys.argv) > 2 else ""
if status != "200":
    sys.exit(1)
headers = {}
try:
    lines = open(sys.argv[1], encoding="utf-8", errors="replace").read().splitlines()
except Exception:
    lines = []
for line in lines:
    if ":" not in line:
        continue
    key, value = line.split(":", 1)
    headers[key.strip().lower()] = value.strip()
want = {
    "x-content-type-options": "nosniff",
    "x-frame-options": "DENY",
    "referrer-policy": "no-referrer",
}
ok = all(headers.get(key, "").lower() == value.lower() for key, value in want.items())
if ok:
    print("nosniff=%s frame=%s referrer=%s" % (
        headers.get("x-content-type-options"),
        headers.get("x-frame-options"),
        headers.get("referrer-policy"),
    ))
sys.exit(0 if ok else 1)
PY
)" || p10_health_headers_summary=""
if [ -n "$p10_health_headers_summary" ]; then
  pass "GET /api/health security headers: $p10_health_headers_summary"
else
  fail "GET /api/health missing P10 security headers (X-Content-Type-Options/X-Frame-Options/Referrer-Policy)"
fi

p10_headers_cams="$(http_get_with_headers "/api/cameras" "$admin_token")"
p10_cam_headers_summary="$(python3 - "$TMP/headers.txt" "$p10_headers_cams" <<'PY'
import sys

status = sys.argv[2] if len(sys.argv) > 2 else ""
if status != "200":
    sys.exit(1)
headers = {}
try:
    lines = open(sys.argv[1], encoding="utf-8", errors="replace").read().splitlines()
except Exception:
    lines = []
for line in lines:
    if ":" not in line:
        continue
    key, value = line.split(":", 1)
    headers[key.strip().lower()] = value.strip()
want = {
    "x-content-type-options": "nosniff",
    "x-frame-options": "DENY",
    "referrer-policy": "no-referrer",
}
ok = all(headers.get(key, "").lower() == value.lower() for key, value in want.items())
if ok:
    print("nosniff=%s frame=%s referrer=%s" % (
        headers.get("x-content-type-options"),
        headers.get("x-frame-options"),
        headers.get("referrer-policy"),
    ))
sys.exit(0 if ok else 1)
PY
)" || p10_cam_headers_summary=""
if [ -n "$p10_cam_headers_summary" ]; then
  pass "GET /api/cameras security headers: $p10_cam_headers_summary"
else
  fail "GET /api/cameras missing P10 security headers (HTTP ${p10_headers_cams:-none})"
fi

# ===========================================================================
# 41. P10.0 login lockout + password policy
# ===========================================================================
step "41/58 login lockout + password policy (P10)"
p10_ts="$(python3 -c 'import time; print(int(time.time()))')"
p10_lock_username="lock_$p10_ts"
p10_lock_codes=""
p10_lock_attempt=0
p10_lock_429_headers=""
p10_lock_429_body=""
while [ "$p10_lock_attempt" -lt 6 ]; do
  p10_lock_attempt=$((p10_lock_attempt + 1))
  p10_lock_code="$(curl -s -D "$TMP/lock-$p10_lock_attempt.headers" -o "$TMP/lock-$p10_lock_attempt.body" \
    -w '%{http_code}' -m 5 -X POST -H 'Content-Type: application/json' \
    --data "{\"username\":\"$p10_lock_username\",\"password\":\"Wrong@12345\"}" \
    "$API_BASE/api/auth/login" 2>/dev/null || true)"
  p10_lock_codes="$p10_lock_codes $p10_lock_code"
  if [ -z "$p10_lock_429_headers" ] && [ "$p10_lock_code" = "429" ]; then
    p10_lock_429_headers="$TMP/lock-$p10_lock_attempt.headers"
    p10_lock_429_body="$TMP/lock-$p10_lock_attempt.body"
  fi
done
printf '      wrong-password logins for %s codes:%s\n' "$p10_lock_username" "$p10_lock_codes"

if python3 - "$p10_lock_codes" "$p10_lock_429_body" <<'PY'
import json, sys
codes = sys.argv[1].split()
if "429" not in codes[3:]:  # the 4th attempt or later must be locked (5th/6th expected)
    sys.exit(1)
try:
    body = json.load(open(sys.argv[2]))
except Exception:
    sys.exit(1)
sys.exit(0 if isinstance(body, dict) and body.get("error") == "too_many_attempts" else 1)
PY
then
  pass "5 wrong-password logins lock the account -> 429 too_many_attempts (codes:$p10_lock_codes)"
else
  fail "login lockout not observed (codes:$p10_lock_codes)"
fi

p10_retry_after="$(python3 - "$p10_lock_429_headers" <<'PY'
import sys

value = ""
try:
    for line in open(sys.argv[1], encoding="utf-8", errors="replace").read().splitlines():
        if ":" not in line:
            continue
        key, val = line.split(":", 1)
        if key.strip().lower() == "retry-after":
            value = val.strip()
            break
except Exception:
    value = ""
print(value)
PY
)"
if python3 -c 'import sys; sys.exit(0 if float(sys.argv[1]) > 0 else 1)' "${p10_retry_after:-0}" 2>/dev/null; then
  pass "lockout response carries Retry-After: ${p10_retry_after}s"
else
  fail "lockout response missing/0 Retry-After (got '${p10_retry_after:-<none>}')"
fi

read -r p10_admin_code p10_admin_token < <(login_user admin 'Admin@123456')
if [ "$p10_admin_code" = "200" ] && [ -n "$p10_admin_token" ]; then
  pass "admin login still works while lock_<ts> is locked (no global lock)"
else
  fail "admin login affected by lockout (HTTP ${p10_admin_code:-none})"
fi

p10_pw_short_payload="$(printf '{"username":"p10_short_%s","password":"Abcd12345","display_name":"短密码","role":"viewer","areas":[],"active":true}' "$p10_ts")"
p10_pw_short_code="$(http_post_json "/api/users" "$p10_pw_short_payload" "$admin_token")"
if [ "$p10_pw_short_code" = "400" ]; then
  pass "password policy: 9-char password -> 400"
else
  fail "9-char password -> HTTP ${p10_pw_short_code:-none} (expected 400)"
fi

p10_pw_nodigit_payload="$(printf '{"username":"p10_nodigit_%s","password":"OnlyLettersLong","display_name":"无数字","role":"viewer","areas":[],"active":true}' "$p10_ts")"
p10_pw_nodigit_code="$(http_post_json "/api/users" "$p10_pw_nodigit_payload" "$admin_token")"
if [ "$p10_pw_nodigit_code" = "400" ]; then
  pass "password policy: 16 letters without a digit -> 400"
else
  fail "letters-only password -> HTTP ${p10_pw_nodigit_code:-none} (expected 400)"
fi

p10_username="smoke_p10_$p10_ts"
p10_pw_valid_payload="$(printf '{"username":"%s","password":"Smoke@12345","display_name":"P10用户","role":"viewer","areas":[],"active":true}' "$p10_username")"
p10_pw_valid_code="$(http_post_json "/api/users" "$p10_pw_valid_payload" "$admin_token")"
P10_USER_ID=""
if [ "$p10_pw_valid_code" = "201" ]; then
  P10_USER_ID="$(python3 -c 'import json,sys
try: print(json.load(open(sys.argv[1])).get("id") or "")
except Exception: print("")' "$TMP/body.json")"
fi
if [ -n "$P10_USER_ID" ]; then
  pass "password policy: valid 10+ letter+digit password -> 201 (user=$p10_username)"
else
  fail "valid password user creation failed (HTTP ${p10_pw_valid_code:-none}, body=$(head -c 300 "$TMP/body.json" 2>/dev/null))"
fi

# ===========================================================================
# 42. P10.0 audit CSV export + cleanup
# ===========================================================================
step "42/58 audit CSV export + cleanup (P10)"
p10_export_code="$(http_get_with_headers "/api/audit/export" "$admin_token")"
p10_export_headers_ok="$(python3 - "$TMP/headers.txt" "$p10_export_code" <<'PY'
import sys
status = sys.argv[2] if len(sys.argv) > 2 else ""
if status != "200":
    sys.exit(1)
headers = {}
try:
    lines = open(sys.argv[1], encoding="utf-8", errors="replace").read().splitlines()
except Exception:
    lines = []
for line in lines:
    if ":" not in line:
        continue
    key, value = line.split(":", 1)
    headers[key.strip().lower()] = value.strip()
content_type = headers.get("content-type", "").lower()
disposition = headers.get("content-disposition", "").lower()
ok = content_type.startswith("text/csv") and "attachment" in disposition and "audit.csv" in disposition
if ok:
    print("content-type=%s disposition=%s" % (content_type, disposition))
sys.exit(0 if ok else 1)
PY
)" || p10_export_headers_ok=""
if [ -n "$p10_export_headers_ok" ]; then
  pass "admin GET /api/audit/export -> 200 ($p10_export_headers_ok)"
else
  fail "admin audit export wrong status/content-type/Content-Disposition (HTTP ${p10_export_code:-none})"
fi

p10_export_header_line="$(head -n 1 "$TMP/body.json" 2>/dev/null | tr -d '\r')"
if [ "$p10_export_header_line" = "id,at,actor,action,resource,detail" ]; then
  pass "audit CSV header matches 'id,at,actor,action,resource,detail'"
else
  fail "audit CSV header mismatch (got '$p10_export_header_line')"
fi

p10_op_export_code="$(http_get "/api/audit/export" "$operator_token")"
if [ "$p10_op_export_code" = "403" ]; then
  pass "operator GET /api/audit/export -> 403 forbidden (admin only)"
else
  fail "operator audit export -> HTTP ${p10_op_export_code:-none} (expected 403)"
fi

if [ -n "$P10_USER_ID" ]; then
  p10_delete_code="$(http_delete "/api/users/$P10_USER_ID" "$admin_token")"
else
  p10_delete_code="000"
fi
if [ "$p10_delete_code" = "204" ]; then
  pass "cleanup: DELETE password-policy test user $P10_USER_ID -> 204"
  P10_USER_ID=""
else
  fail "cleanup: DELETE password-policy test user -> HTTP ${p10_delete_code:-none} (expected 204)"
fi

read -r p10_final_admin_code p10_final_admin_token < <(login_user admin 'Admin@123456')
if [ "$p10_final_admin_code" = "200" ] && [ -n "$p10_final_admin_token" ]; then
  pass "cleanup: admin login still OK after P10 security checks"
else
  fail "cleanup: admin login failed after P10 checks (HTTP ${p10_final_admin_code:-none})"
fi

# ===========================================================================
# 43. P11.0 refresh-token rotation
# ===========================================================================
step "43/58 refresh rotation (P11: login refresh_token, rotate, old reuse 401)"
p11_admin_login_code="$(curl -s -o "$TMP/p11_admin_login.json" -w '%{http_code}' -m 5 \
  -X POST -H 'Content-Type: application/json' \
  --data '{"username":"admin","password":"Admin@123456"}' \
  "$API_BASE/api/auth/login" 2>/dev/null || true)"
p11_admin_token=""
p11_admin_refresh=""
p11_admin_login_summary=""
p11_admin_login_summary="$(python3 - "$TMP/p11_admin_login.json" "$p11_admin_login_code" <<'PY'
import json, sys
if len(sys.argv) < 3 or sys.argv[2] != "200":
    sys.exit(1)
try:
    data = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
token = data.get("token")
refresh = data.get("refresh_token")
refresh_exp = data.get("refresh_expires_at")
ok = isinstance(token, str) and bool(token)
ok = ok and isinstance(refresh, str) and bool(refresh) and refresh != token
ok = ok and isinstance(refresh_exp, str) and bool(refresh_exp)
if not ok:
    sys.exit(1)
print(json.dumps({"access_len": len(token), "refresh_len": len(refresh),
                  "refresh_expires_at": refresh_exp}, ensure_ascii=False))
PY
)" || p11_admin_login_summary=""
if [ -n "$p11_admin_login_summary" ]; then
  p11_admin_token="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["token"])' "$TMP/p11_admin_login.json")"
  p11_admin_refresh="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["refresh_token"])' "$TMP/p11_admin_login.json")"
  pass "admin login -> 200 with refresh_token + refresh_expires_at ($p11_admin_login_summary)"
else
  fail "admin login response missing refresh_token/refresh_expires_at (HTTP ${p11_admin_login_code:-none})"
fi

p11_cams_code="$(http_get "/api/cameras" "$p11_admin_token")"
if [ "$p11_cams_code" = "200" ]; then
  pass "admin access token works -> GET /api/cameras 200"
else
  fail "admin access token -> GET /api/cameras ${p11_cams_code:-none} (expected 200)"
fi

p11_refresh_payload="$(printf '{"refresh_token":"%s"}' "$p11_admin_refresh")"
p11_refresh_code="$(http_post_json "/api/auth/refresh" "$p11_refresh_payload")"
p11_refresh_summary=""
p11_refresh_summary="$(python3 - "$TMP/body.json" "$p11_refresh_code" "$p11_admin_refresh" <<'PY'
import json, sys
if len(sys.argv) < 4 or sys.argv[2] != "200":
    sys.exit(1)
try:
    data = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
token = data.get("token")
refresh = data.get("refresh_token")
ok = isinstance(token, str) and bool(token)
ok = ok and isinstance(refresh, str) and bool(refresh) and refresh != sys.argv[3]
if not ok:
    sys.exit(1)
print(json.dumps({"access_len": len(token), "refresh_rotated": True}, ensure_ascii=False))
PY
)" || p11_refresh_summary=""
p11_new_token=""
if [ -n "$p11_refresh_summary" ]; then
  p11_new_token="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["token"])' "$TMP/body.json")"
  p11_new_refresh="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["refresh_token"])' "$TMP/body.json")"
  pass "POST /api/auth/refresh -> 200 with new access + rotated refresh ($p11_refresh_summary)"
else
  fail "refresh failed (HTTP ${p11_refresh_code:-none}, body=$(head -c 300 "$TMP/body.json" 2>/dev/null))"
fi

p11_old_reuse_code="$(http_post_json "/api/auth/refresh" "$p11_refresh_payload")"
p11_old_reuse_error="$(python3 -c 'import json,sys
try: print(json.load(open(sys.argv[1])).get("error",""))
except Exception: print("")' "$TMP/body.json" 2>/dev/null || echo '')"
if [ "$p11_old_reuse_code" = "401" ] && [ "$p11_old_reuse_error" = "invalid_refresh_token" ]; then
  pass "reusing the rotated-out refresh token -> 401 invalid_refresh_token"
else
  fail "old refresh reuse -> HTTP ${p11_old_reuse_code:-none} error=${p11_old_reuse_error:-none} (expected 401 invalid_refresh_token)"
fi

p11_new_cams_code="$(http_get "/api/cameras" "$p11_new_token")"
if [ "$p11_new_cams_code" = "200" ]; then
  pass "rotated access token works -> GET /api/cameras 200"
else
  fail "rotated access token -> GET /api/cameras ${p11_new_cams_code:-none} (expected 200)"
fi

# ===========================================================================
# 44. P11.0 logout + logout-all (token version bump)
# ===========================================================================
step "44/58 logout + logout-all (P11: refresh revocation, token_version bump)"
p11_logout_payload="$(printf '{"refresh_token":"%s"}' "$p11_new_refresh")"
p11_logout_code="$(http_post_json "/api/auth/logout" "$p11_logout_payload")"
if [ "$p11_logout_code" = "204" ]; then
  pass "POST /api/auth/logout -> 204 (refresh revoked)"
else
  fail "POST /api/auth/logout -> HTTP ${p11_logout_code:-none} (expected 204)"
fi

p11_logged_out_refresh_code="$(http_post_json "/api/auth/refresh" "$p11_logout_payload")"
if [ "$p11_logged_out_refresh_code" = "401" ]; then
  pass "refresh after logout -> 401 (refresh token revoked)"
else
  fail "refresh after logout -> HTTP ${p11_logged_out_refresh_code:-none} (expected 401)"
fi

# Throwaway viewer for logout-all / password invalidation / force-logout.
p11_ts="$(python3 -c 'import time; print(int(time.time()))')"
p11_username="smoke_p11_$p11_ts"
p11_password="Smoke@12345"
p11_user_payload="$(printf '{"username":"%s","password":"%s","display_name":"P11会话用户","role":"viewer","areas":[],"active":true}' "$p11_username" "$p11_password")"
p11_user_create_code="$(http_post_json "/api/users" "$p11_user_payload" "$admin_token")"
P11_USER_ID=""
if [ "$p11_user_create_code" = "201" ]; then
  P11_USER_ID="$(python3 -c 'import json,sys
try: print(json.load(open(sys.argv[1])).get("id") or "")
except Exception: print("")' "$TMP/body.json")"
fi
printf '      throwaway viewer for session checks: %s (id=%s)\n' "$p11_username" "${P11_USER_ID:-<none>}"

p11_user_login_code="$(curl -s -o "$TMP/p11_user_login.json" -w '%{http_code}' -m 5 \
  -X POST -H 'Content-Type: application/json' \
  --data "{\"username\":\"$p11_username\",\"password\":\"$p11_password\"}" \
  "$API_BASE/api/auth/login" 2>/dev/null || true)"
p11_user_token=""
p11_user_refresh=""
p11_user_login_summary="$(python3 - "$TMP/p11_user_login.json" "$p11_user_login_code" <<'PY'
import json, sys
if len(sys.argv) < 3 or sys.argv[2] != "200":
    sys.exit(1)
try:
    data = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
if not isinstance(data.get("refresh_token"), str) or not data.get("refresh_token"):
    sys.exit(1)
print(json.dumps({"role": (data.get("user") or {}).get("role")}, ensure_ascii=False))
PY
)" || p11_user_login_summary=""
if [ -n "$p11_user_login_summary" ]; then
  p11_user_token="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["token"])' "$TMP/p11_user_login.json")"
  p11_user_refresh="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["refresh_token"])' "$TMP/p11_user_login.json")"
  pass "viewer login -> 200 with refresh_token ($p11_user_login_summary)"
else
  fail "viewer login missing refresh_token (HTTP ${p11_user_login_code:-none})"
fi

p11_user_cams_before="$(http_get "/api/cameras" "$p11_user_token")"
if [ "$p11_user_cams_before" = "200" ]; then
  pass "viewer access token works before logout-all -> GET /api/cameras 200"
else
  fail "viewer access token before logout-all -> HTTP ${p11_user_cams_before:-none} (expected 200)"
fi

p11_logout_all_code="$(http_post_json "/api/auth/logout-all" '{}' "$p11_user_token")"
if [ "$p11_logout_all_code" = "204" ]; then
  pass "POST /api/auth/logout-all -> 204 (token_version bumped)"
else
  fail "POST /api/auth/logout-all -> HTTP ${p11_logout_all_code:-none} (expected 204)"
fi

p11_user_cams_after="$(http_get "/api/cameras" "$p11_user_token")"
if [ "$p11_user_cams_after" = "401" ]; then
  pass "old viewer access token after logout-all -> 401"
else
  fail "old viewer access token after logout-all -> HTTP ${p11_user_cams_after:-none} (expected 401)"
fi

read -r p11_relogin_code p11_relogin_token < <(login_user "$p11_username" "$p11_password")
if [ "$p11_relogin_code" = "200" ] && [ -n "$p11_relogin_token" ]; then
  pass "viewer re-login after logout-all -> 200"
else
  fail "viewer re-login after logout-all -> HTTP ${p11_relogin_code:-none} (expected 200)"
fi

# ===========================================================================
# 45. P11.0 password-change invalidation + force logout + sessions
# ===========================================================================
step "45/58 password invalidation + force logout + sessions (P11)"
p11_new_password="Smoke@123456"
if [ -n "$P11_USER_ID" ]; then
  p11_pwd_patch_payload="$(printf '{"password":"%s"}' "$p11_new_password")"
  p11_pwd_patch_code="$(http_patch_json "/api/users/$P11_USER_ID" "$p11_pwd_patch_payload" "$admin_token")"
else
  p11_pwd_patch_code="000"
fi
if [ "$p11_pwd_patch_code" = "200" ]; then
  pass "admin PATCH user password -> 200"
else
  fail "admin PATCH user password -> HTTP ${p11_pwd_patch_code:-none} (expected 200)"
fi

p11_old_token_after_pwd="$(http_get "/api/cameras" "$p11_relogin_token")"
if [ "$p11_old_token_after_pwd" = "401" ]; then
  pass "viewer's pre-password-change access token -> 401 (token_version bumped)"
else
  fail "old access token after password change -> HTTP ${p11_old_token_after_pwd:-none} (expected 401)"
fi

read -r p11_pwd_login_code p11_pwd_login_token < <(login_user "$p11_username" "$p11_new_password")
if [ "$p11_pwd_login_code" = "200" ] && [ -n "$p11_pwd_login_token" ]; then
  pass "login with the new password -> 200"
else
  fail "login with new password -> HTTP ${p11_pwd_login_code:-none} (expected 200)"
fi

if [ -n "$P11_USER_ID" ]; then
  p11_sessions_code="$(http_get "/api/users/$P11_USER_ID/sessions" "$admin_token")"
else
  p11_sessions_code="000"
fi
p11_sessions_before="$(python3 - "$TMP/body.json" "$p11_sessions_code" <<'PY'
import json, sys
if len(sys.argv) < 3 or sys.argv[2] != "200":
    print(-1)
    sys.exit(0)
try:
    items = json.load(open(sys.argv[1])).get("items") or []
except Exception:
    items = []
print(len(items))
PY
)"
if [ "$p11_sessions_code" = "200" ] && [ "${p11_sessions_before:-0}" -ge 1 ] 2>/dev/null; then
  pass "admin GET /api/users/{id}/sessions -> 200 with $p11_sessions_before active session(s)"
else
  fail "admin sessions list before force logout wrong (HTTP ${p11_sessions_code:-none}, items=${p11_sessions_before:-?})"
fi

if [ -n "$P11_USER_ID" ]; then
  p11_force_code="$(http_post_json "/api/users/$P11_USER_ID/logout" '{}' "$admin_token")"
else
  p11_force_code="000"
fi
if [ "$p11_force_code" = "204" ]; then
  pass "admin POST /api/users/{id}/logout (force logout) -> 204"
else
  fail "admin force logout -> HTTP ${p11_force_code:-none} (expected 204)"
fi

p11_forced_access_code="$(http_get "/api/cameras" "$p11_pwd_login_token")"
if [ "$p11_forced_access_code" = "401" ]; then
  pass "forced-out viewer access token -> 401"
else
  fail "forced-out viewer access token -> HTTP ${p11_forced_access_code:-none} (expected 401)"
fi

if [ -n "$P11_USER_ID" ]; then
  p11_sessions_after_code="$(http_get "/api/users/$P11_USER_ID/sessions" "$admin_token")"
else
  p11_sessions_after_code="000"
fi
p11_sessions_after="$(python3 - "$TMP/body.json" "$p11_sessions_after_code" <<'PY'
import json, sys
if len(sys.argv) < 3 or sys.argv[2] != "200":
    print(-1)
    sys.exit(0)
try:
    items = json.load(open(sys.argv[1])).get("items") or []
except Exception:
    items = []
print(len(items))
PY
)"
if [ "$p11_sessions_after_code" = "200" ] && [ "$p11_sessions_after" = "0" ]; then
  pass "admin GET sessions after force logout -> 0 active sessions"
else
  fail "sessions list after force logout wrong (HTTP ${p11_sessions_after_code:-none}, items=${p11_sessions_after:-?})"
fi

if [ -n "$admin_user_id" ]; then
  p11_self_force_code="$(http_post_json "/api/users/$admin_user_id/logout" '{}' "$admin_token")"
else
  p11_self_force_code="000"
fi
p11_self_force_error="$(python3 -c 'import json,sys
try: print(json.load(open(sys.argv[1])).get("error",""))
except Exception: print("")' "$TMP/body.json" 2>/dev/null || echo '')"
if [ "$p11_self_force_code" = "400" ] && [ "$p11_self_force_error" = "self_protected" ]; then
  pass "admin force-logout of self -> 400 self_protected"
else
  fail "admin self force logout -> HTTP ${p11_self_force_code:-none} error=${p11_self_force_error:-none} (expected 400 self_protected)"
fi

if [ -n "$P11_USER_ID" ]; then
  p11_op_force_code="$(http_post_json "/api/users/$P11_USER_ID/logout" '{}' "$operator_token")"
else
  p11_op_force_code="000"
fi
if [ "$p11_op_force_code" = "403" ]; then
  pass "operator force logout -> 403 forbidden (admin only)"
else
  fail "operator force logout -> HTTP ${p11_op_force_code:-none} (expected 403)"
fi

# ===========================================================================
# 46. P11.0 cleanup
# ===========================================================================
step "46/58 P11 cleanup (delete throwaway user, admin login)"
if [ -n "$P11_USER_ID" ]; then
  p11_del_code="$(http_delete "/api/users/$P11_USER_ID" "$admin_token")"
else
  p11_del_code="000"
fi
if [ "$p11_del_code" = "204" ]; then
  pass "cleanup: DELETE throwaway viewer $P11_USER_ID -> 204"
  P11_USER_ID=""
else
  fail "cleanup: DELETE throwaway viewer -> HTTP ${p11_del_code:-none} (expected 204)"
fi

read -r p11_final_admin_code p11_final_admin_token < <(login_user admin 'Admin@123456')
if [ "$p11_final_admin_code" = "200" ] && [ -n "$p11_final_admin_token" ]; then
  pass "cleanup: admin login still OK after P11 session checks"
else
  fail "cleanup: admin login failed after P11 checks (HTTP ${p11_final_admin_code:-none})"
fi

# ===========================================================================
# 47. P12.0 patrol route CRUD + RBAC
# ===========================================================================
step "47/58 patrol routes CRUD (P12: create/list/patch, RBAC)"
: > "$P12_ROUTE_IDS_FILE"
p12_route_payload='{"name":"smoke 巡检","waypoints":[{"x":-90,"y":-60,"z":12,"heading":90},{"x":0,"y":0,"z":14}],"camera_ids":["cam_001","cam_006"],"interval_seconds":2}'

p12_admin_list_code="$(http_get "/api/patrol/routes" "$admin_token")"
if python3 - "$TMP/body.json" "$p12_admin_list_code" <<'PY'
import json, sys
if len(sys.argv) < 3 or sys.argv[2] != "200":
    sys.exit(1)
try:
    data = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
sys.exit(0 if isinstance(data, dict) and isinstance(data.get("items"), list) else 1)
PY
then
  pass "admin GET /api/patrol/routes -> 200 items array"
else
  fail "admin GET /api/patrol/routes failed (HTTP ${p12_admin_list_code:-none})"
fi

p12_op_list_code="$(http_get "/api/patrol/routes" "$operator_token")"
if python3 - "$TMP/body.json" "$p12_op_list_code" <<'PY'
import json, sys
if len(sys.argv) < 3 or sys.argv[2] != "200":
    sys.exit(1)
try:
    data = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
sys.exit(0 if isinstance(data, dict) and isinstance(data.get("items"), list) else 1)
PY
then
  pass "operator GET /api/patrol/routes -> 200 items array"
else
  fail "operator GET /api/patrol/routes failed (HTTP ${p12_op_list_code:-none})"
fi

p12_create_code="$(http_post_json "/api/patrol/routes" "$p12_route_payload" "$operator_token")"
P12_ROUTE_ID=""
if [ "$p12_create_code" = "201" ]; then
  P12_ROUTE_ID="$(python3 - "$TMP/body.json" <<'PY'
import json, sys
try:
    route = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
if not isinstance(route, dict):
    sys.exit(1)
waypoints = route.get("waypoints") or []
camera_ids = route.get("camera_ids") or []
ok = bool(route.get("id"))
ok = ok and route.get("name") == "smoke 巡检"
ok = ok and isinstance(waypoints, list) and len(waypoints) == 2
ok = ok and isinstance(camera_ids, list) and set(camera_ids) == {"cam_001", "cam_006"}
ok = ok and route.get("interval_seconds") == 2
if not ok:
    sys.exit(1)
print(route["id"])
PY
)" || P12_ROUTE_ID=""
fi
if [ -n "$P12_ROUTE_ID" ]; then
  printf '%s\n' "$P12_ROUTE_ID" >> "$P12_ROUTE_IDS_FILE"
  pass "operator POST /api/patrol/routes -> 201 id=$P12_ROUTE_ID (2 waypoints, cam_001+cam_006, interval 2)"
else
  fail "operator POST /api/patrol/routes failed (HTTP ${p12_create_code:-none}, body=$(head -c 400 "$TMP/body.json" 2>/dev/null))"
fi

p12_list_contains_code="$(http_get "/api/patrol/routes" "$operator_token")"
if python3 - "$TMP/body.json" "$p12_list_contains_code" "$P12_ROUTE_ID" <<'PY'
import json, sys
if len(sys.argv) < 4 or sys.argv[2] != "200":
    sys.exit(1)
try:
    items = json.load(open(sys.argv[1])).get("items") or []
except Exception:
    items = []
sys.exit(0 if any(isinstance(r, dict) and r.get("id") == sys.argv[3] for r in items) else 1)
PY
then
  pass "GET /api/patrol/routes contains the newly created route"
else
  fail "created patrol route missing from GET /api/patrol/routes (HTTP ${p12_list_contains_code:-none})"
fi

if [ -n "$P12_ROUTE_ID" ]; then
  p12_patch_code="$(http_patch_json "/api/patrol/routes/$P12_ROUTE_ID" \
    '{"name":"smoke 巡检 updated","interval_seconds":3}' "$operator_token")"
else
  p12_patch_code="000"
fi
if python3 - "$TMP/body.json" "$p12_patch_code" <<'PY'
import json, sys
if len(sys.argv) < 3 or sys.argv[2] != "200":
    sys.exit(1)
try:
    route = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
ok = isinstance(route, dict) and route.get("name") == "smoke 巡检 updated"
ok = ok and route.get("interval_seconds") == 3
sys.exit(0 if ok else 1)
PY
then
  pass "operator PATCH /api/patrol/routes/{id} name+interval -> 200"
else
  fail "operator PATCH patrol route failed (HTTP ${p12_patch_code:-none}, body=$(head -c 300 "$TMP/body.json" 2>/dev/null))"
fi

p12_viewer_post_code="$(http_post_json "/api/patrol/routes" "$p12_route_payload" "$viewer_token")"
if [ "$p12_viewer_post_code" = "403" ]; then
  pass "viewer POST /api/patrol/routes -> 403 forbidden (operator/admin only)"
else
  fail "viewer POST /api/patrol/routes -> HTTP ${p12_viewer_post_code:-none} (expected 403)"
fi

p12_viewer_get_code="$(http_get "/api/patrol/routes" "$viewer_token")"
if [ "$p12_viewer_get_code" = "200" ]; then
  pass "viewer GET /api/patrol/routes -> 200 (read-only allowed)"
else
  fail "viewer GET /api/patrol/routes -> HTTP ${p12_viewer_get_code:-none} (expected 200)"
fi

# ===========================================================================
# 48. P12.0 patrol route validation
# ===========================================================================
step "48/58 patrol routes validation (P12: waypoints/camera/interval 400)"
p12_bad_oneway_payload='{"name":"smoke 巡检 bad","waypoints":[{"x":0,"y":0,"z":10}],"camera_ids":[],"interval_seconds":2}'
p12_oneway_code="$(http_post_json "/api/patrol/routes" "$p12_bad_oneway_payload" "$operator_token")"
if [ "$p12_oneway_code" = "400" ]; then
  pass "POST patrol route with 1 waypoint -> 400 (needs 2-50)"
else
  fail "1-waypoint route -> HTTP ${p12_oneway_code:-none} (expected 400)"
fi

p12_manyway_payload="$(python3 - <<'PY'
import json
waypoints = [{"x": float(i), "y": 0.0, "z": 10.0} for i in range(51)]
print(json.dumps({"name": "smoke 巡检 bad", "waypoints": waypoints,
                  "camera_ids": [], "interval_seconds": 2}))
PY
)"
p12_manyway_code="$(http_post_json "/api/patrol/routes" "$p12_manyway_payload" "$operator_token")"
if [ "$p12_manyway_code" = "400" ]; then
  pass "POST patrol route with 51 waypoints -> 400 (max 50)"
else
  fail "51-waypoint route -> HTTP ${p12_manyway_code:-none} (expected 400)"
fi

p12_unknowncams_payload='{"name":"smoke 巡检 bad","waypoints":[{"x":0,"y":0,"z":10},{"x":1,"y":0,"z":10}],"camera_ids":["cam_does_not_exist"],"interval_seconds":2}'
p12_unknowncams_code="$(http_post_json "/api/patrol/routes" "$p12_unknowncams_payload" "$operator_token")"
p12_unknowncams_error="$(python3 -c 'import json,sys
try: print(json.load(open(sys.argv[1])).get("error",""))
except Exception: print("")' "$TMP/body.json" 2>/dev/null || echo '')"
if [ "$p12_unknowncams_code" = "400" ] && [ "$p12_unknowncams_error" = "unknown_camera" ]; then
  pass "POST patrol route with unknown camera id -> 400 unknown_camera"
else
  fail "unknown camera route -> HTTP ${p12_unknowncams_code:-none} error=${p12_unknowncams_error:-none} (expected 400 unknown_camera)"
fi

p12_badinterval_payload='{"name":"smoke 巡检 bad","waypoints":[{"x":0,"y":0,"z":10},{"x":1,"y":0,"z":10}],"camera_ids":[],"interval_seconds":0}'
p12_badinterval_code="$(http_post_json "/api/patrol/routes" "$p12_badinterval_payload" "$operator_token")"
if [ "$p12_badinterval_code" = "400" ]; then
  pass "POST patrol route with interval_seconds=0 -> 400 (range 1-60)"
else
  fail "interval 0 route -> HTTP ${p12_badinterval_code:-none} (expected 400)"
fi

# ===========================================================================
# 49. P12.0 patrol route delete + audit + cleanup
# ===========================================================================
step "49/58 patrol routes delete + audit + cleanup (P12)"
if [ -n "$P12_ROUTE_ID" ]; then
  p12_delete_own_code="$(http_delete "/api/patrol/routes/$P12_ROUTE_ID" "$operator_token")"
else
  p12_delete_own_code="000"
fi
if [ "$p12_delete_own_code" = "204" ]; then
  pass "operator DELETE own patrol route -> 204"
else
  fail "operator DELETE own patrol route -> HTTP ${p12_delete_own_code:-none} (expected 204)"
fi

p12_after_delete_code="$(http_get "/api/patrol/routes" "$operator_token")"
if python3 - "$TMP/body.json" "$p12_after_delete_code" "$P12_ROUTE_ID" <<'PY'
import json, sys
if len(sys.argv) < 4 or sys.argv[2] != "200":
    sys.exit(1)
try:
    items = json.load(open(sys.argv[1])).get("items") or []
except Exception:
    items = []
sys.exit(0 if not any(isinstance(r, dict) and r.get("id") == sys.argv[3] for r in items) else 1)
PY
then
  pass "deleted patrol route is absent from GET /api/patrol/routes"
else
  fail "deleted patrol route still listed (HTTP ${p12_after_delete_code:-none})"
fi
P12_ROUTE_ID=""

# Admin can delete a route created by another user.
p12_route2_payload='{"name":"smoke 巡检 admin-delete","waypoints":[{"x":-80,"y":-60,"z":12},{"x":-20,"y":-40,"z":12}],"camera_ids":["cam_001"],"interval_seconds":2}'
p12_route2_create_code="$(http_post_json "/api/patrol/routes" "$p12_route2_payload" "$operator_token")"
p12_route2_id=""
if [ "$p12_route2_create_code" = "201" ]; then
  p12_route2_id="$(python3 -c 'import json,sys
try: print(json.load(open(sys.argv[1])).get("id") or "")
except Exception: print("")' "$TMP/body.json")"
  [ -n "$p12_route2_id" ] && printf '%s\n' "$p12_route2_id" >> "$P12_ROUTE_IDS_FILE"
fi
printf '      operator route for admin-delete check: %s\n' "${p12_route2_id:-<none>}"

if [ -n "$p12_route2_id" ]; then
  p12_admin_delete_code="$(http_delete "/api/patrol/routes/$p12_route2_id" "$admin_token")"
else
  p12_admin_delete_code="000"
fi
if [ "$p12_admin_delete_code" = "204" ]; then
  pass "admin DELETE operator-created patrol route -> 204"
else
  fail "admin DELETE operator route -> HTTP ${p12_admin_delete_code:-none} (expected 204)"
fi

p12_after_admin_delete_code="$(http_get "/api/patrol/routes" "$admin_token")"
if python3 - "$TMP/body.json" "$p12_after_admin_delete_code" "$p12_route2_id" <<'PY'
import json, sys
if len(sys.argv) < 4 or sys.argv[2] != "200":
    sys.exit(1)
try:
    items = json.load(open(sys.argv[1])).get("items") or []
except Exception:
    items = []
sys.exit(0 if not any(isinstance(r, dict) and r.get("id") == sys.argv[3] for r in items) else 1)
PY
then
  pass "admin-deleted route is absent from GET /api/patrol/routes"
else
  fail "admin-deleted patrol route still listed (HTTP ${p12_after_admin_delete_code:-none})"
fi

p12_audit_code="$(http_get '/api/audit?limit=500' "$admin_token")"
p12_audit_summary=""
if [ "$p12_audit_code" = "200" ]; then
  p12_audit_summary="$(python3 - "$TMP/body.json" <<'PY'
import json, sys
try:
    items = json.load(open(sys.argv[1])).get("items") or []
except Exception:
    items = []
seen = {}
for item in items:
    if not isinstance(item, dict):
        continue
    action = item.get("action")
    if action in ("patrol.create", "patrol.update", "patrol.delete"):
        seen.setdefault(action, set()).add(str(item.get("actor") or ""))
ok = True
if "operator" not in seen.get("patrol.create", set()):
    ok = False
if "operator" not in seen.get("patrol.update", set()):
    ok = False
deleters = seen.get("patrol.delete", set())
if not ({"operator", "admin"} & deleters):
    ok = False
if ok:
    print(json.dumps({action: sorted(actors) for action, actors in sorted(seen.items())}, ensure_ascii=False))
sys.exit(0 if ok else 1)
PY
)" || p12_audit_summary=""
fi
if [ -n "$p12_audit_summary" ]; then
  pass "audit contains patrol.create/patrol.update/patrol.delete ($p12_audit_summary)"
else
  fail "audit missing patrol create/update/delete entries (HTTP ${p12_audit_code:-none})"
fi

p12_leftover_code="$(http_get "/api/patrol/routes" "$admin_token")"
p12_leftover_count="$(python3 - "$TMP/body.json" "$p12_leftover_code" <<'PY'
import json, sys
if len(sys.argv) < 3 or sys.argv[2] != "200":
    print(-1)
    sys.exit(0)
try:
    items = json.load(open(sys.argv[1])).get("items") or []
except Exception:
    items = []
leftover = [r.get("id") for r in items if isinstance(r, dict) and str(r.get("name") or "").startswith("smoke 巡检")]
print(json.dumps({"leftover": leftover, "count": len(leftover)}, ensure_ascii=False))
PY
)"
if python3 - "$p12_leftover_count" <<'PY'
import json, sys
if len(sys.argv) < 2 or not sys.argv[1].strip():
    sys.exit(1)
try:
    info = json.loads(sys.argv[1])
except Exception:
    sys.exit(1)
sys.exit(0 if info.get("count") == 0 else 1)
PY
then
  pass "cleanup: no leftover 'smoke 巡检' patrol routes ($p12_leftover_count)"
else
  fail "leftover 'smoke 巡检' patrol routes remain ($p12_leftover_count)"
fi
rm -f "$P12_ROUTE_IDS_FILE"

read -r p12_final_admin_code p12_final_admin_token < <(login_user admin 'Admin@123456')
if [ "$p12_final_admin_code" = "200" ] && [ -n "$p12_final_admin_token" ]; then
  pass "cleanup: admin login still OK after patrol checks"
else
  fail "cleanup: admin login failed after patrol checks (HTTP ${p12_final_admin_code:-none})"
fi

# ===========================================================================
# 50. P13.0 patrol schedules: setup + CRUD + RBAC
# ===========================================================================
step "50/58 patrol schedules CRUD (P13: create/list/patch, RBAC)"
: > "$P13_ROUTE_IDS_FILE"
: > "$P13_SCHEDULE_IDS_FILE"
# P13 setup: an operator-created patrol route for the schedule to reference.
p13_route_payload='{"name":"smoke 巡检 P13 路线","waypoints":[{"x":-90,"y":-60,"z":12,"heading":90},{"x":-20,"y":-40,"z":12}],"camera_ids":["cam_001"],"interval_seconds":2}'
p13_route_code="$(http_post_json "/api/patrol/routes" "$p13_route_payload" "$operator_token")"
P13_ROUTE_ID=""
if [ "$p13_route_code" = "201" ]; then
  P13_ROUTE_ID="$(python3 -c 'import json,sys
try: print(json.load(open(sys.argv[1])).get("id") or "")
except Exception: print("")' "$TMP/body.json")"
fi
if [ -n "$P13_ROUTE_ID" ]; then
  printf '%s\n' "$P13_ROUTE_ID" >> "$P13_ROUTE_IDS_FILE"
fi
printf '      P13 setup route (operator): %s\n' "${P13_ROUTE_ID:-<none>}"

p13_sched_payload="$(printf '{"name":"smoke 计划 P13","route_id":"%s","interval_minutes":60,"enabled":true}' "${P13_ROUTE_ID:-}")"
p13_create_code="$(http_post_json "/api/patrol/schedules" "$p13_sched_payload" "$operator_token")"
P13_SCHEDULE_ID=""
if [ "$p13_create_code" = "201" ]; then
  P13_SCHEDULE_ID="$(python3 - "$TMP/body.json" "$P13_ROUTE_ID" <<'PY'
import json, sys
try:
    schedule = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
if not isinstance(schedule, dict):
    sys.exit(1)
ok = bool(schedule.get("id"))
ok = ok and schedule.get("name") == "smoke 计划 P13"
ok = ok and schedule.get("route_id") == sys.argv[2]
ok = ok and schedule.get("interval_minutes") == 60
ok = ok and schedule.get("enabled") is True
ok = ok and isinstance(schedule.get("next_run_at"), str) and bool(schedule.get("next_run_at"))
if not ok:
    sys.exit(1)
print(schedule["id"])
PY
)" || P13_SCHEDULE_ID=""
fi
if [ -n "$P13_SCHEDULE_ID" ]; then
  printf '%s\n' "$P13_SCHEDULE_ID" >> "$P13_SCHEDULE_IDS_FILE"
  pass "operator POST /api/patrol/schedules -> 201 id=$P13_SCHEDULE_ID (next_run_at set, interval 60)"
else
  fail "operator POST /api/patrol/schedules failed (HTTP ${p13_create_code:-none}, body=$(head -c 400 "$TMP/body.json" 2>/dev/null))"
fi

p13_list_code="$(http_get "/api/patrol/schedules" "$operator_token")"
if python3 - "$TMP/body.json" "$p13_list_code" "$P13_SCHEDULE_ID" <<'PY'
import json, sys
if len(sys.argv) < 4 or sys.argv[2] != "200":
    sys.exit(1)
try:
    items = json.load(open(sys.argv[1])).get("items") or []
except Exception:
    items = []
sys.exit(0 if any(isinstance(s, dict) and s.get("id") == sys.argv[3] for s in items) else 1)
PY
then
  pass "operator GET /api/patrol/schedules contains the new schedule"
else
  fail "created schedule missing from GET /api/patrol/schedules (HTTP ${p13_list_code:-none})"
fi

if [ -n "$P13_SCHEDULE_ID" ]; then
  p13_patch_code="$(http_patch_json "/api/patrol/schedules/$P13_SCHEDULE_ID" \
    '{"name":"smoke 计划 P13 updated","interval_minutes":120}' "$operator_token")"
else
  p13_patch_code="000"
fi
if python3 - "$TMP/body.json" "$p13_patch_code" <<'PY'
import json, sys
if len(sys.argv) < 3 or sys.argv[2] != "200":
    sys.exit(1)
try:
    schedule = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
ok = isinstance(schedule, dict) and schedule.get("name") == "smoke 计划 P13 updated"
ok = ok and schedule.get("interval_minutes") == 120
sys.exit(0 if ok else 1)
PY
then
  pass "operator PATCH /api/patrol/schedules/{id} name+interval -> 200"
else
  fail "operator PATCH patrol schedule failed (HTTP ${p13_patch_code:-none}, body=$(head -c 300 "$TMP/body.json" 2>/dev/null))"
fi

p13_viewer_post_code="$(http_post_json "/api/patrol/schedules" "$p13_sched_payload" "$viewer_token")"
if [ "$p13_viewer_post_code" = "403" ]; then
  pass "viewer POST /api/patrol/schedules -> 403 forbidden (operator/admin only)"
else
  fail "viewer POST /api/patrol/schedules -> HTTP ${p13_viewer_post_code:-none} (expected 403)"
fi

p13_viewer_get_code="$(http_get "/api/patrol/schedules" "$viewer_token")"
if [ "$p13_viewer_get_code" = "200" ]; then
  pass "viewer GET /api/patrol/schedules -> 200 (read-only allowed)"
else
  fail "viewer GET /api/patrol/schedules -> HTTP ${p13_viewer_get_code:-none} (expected 200)"
fi

# ===========================================================================
# 51. P13.0 patrol schedule validation
# ===========================================================================
step "51/58 patrol schedules validation (P13: route/interval 400)"
p13_missing_route_code="$(http_post_json "/api/patrol/schedules" \
  '{"name":"smoke 计划 P13 bad","interval_minutes":60,"enabled":true}' "$operator_token")"
if [ "$p13_missing_route_code" = "400" ]; then
  pass "POST schedule without route_id -> 400"
else
  fail "missing route_id schedule -> HTTP ${p13_missing_route_code:-none} (expected 400)"
fi

p13_unknown_route_code="$(http_post_json "/api/patrol/schedules" \
  '{"name":"smoke 计划 P13 bad","route_id":"patrol_does_not_exist","interval_minutes":60,"enabled":true}' "$operator_token")"
if [ "$p13_unknown_route_code" = "400" ]; then
  pass "POST schedule with unknown route_id -> 400"
else
  fail "unknown route schedule -> HTTP ${p13_unknown_route_code:-none} (expected 400)"
fi

p13_interval0_payload="$(printf '{"name":"smoke 计划 P13 bad","route_id":"%s","interval_minutes":0,"enabled":true}' "${P13_ROUTE_ID:-}")"
p13_interval0_code="$(http_post_json "/api/patrol/schedules" "$p13_interval0_payload" "$operator_token")"
if [ "$p13_interval0_code" = "400" ]; then
  pass "POST schedule with interval_minutes=0 -> 400 (range 1-10080)"
else
  fail "interval 0 schedule -> HTTP ${p13_interval0_code:-none} (expected 400)"
fi

p13_interval_high_payload="$(printf '{"name":"smoke 计划 P13 bad","route_id":"%s","interval_minutes":10081,"enabled":true}' "${P13_ROUTE_ID:-}")"
p13_interval_high_code="$(http_post_json "/api/patrol/schedules" "$p13_interval_high_payload" "$operator_token")"
if [ "$p13_interval_high_code" = "400" ]; then
  pass "POST schedule with interval_minutes=10081 -> 400 (range 1-10080)"
else
  fail "interval 10081 schedule -> HTTP ${p13_interval_high_code:-none} (expected 400)"
fi

# ===========================================================================
# 52. P13.0 execution lifecycle + issue/order link + RBAC + audit + cleanup
# ===========================================================================
step "52/58 execution lifecycle + cleanup (P13: run/claim/done/issue/order)"
# 1) run now -> pending execution
if [ -n "$P13_SCHEDULE_ID" ]; then
  p13_run_code="$(http_post_json "/api/patrol/schedules/$P13_SCHEDULE_ID/run" '{}' "$operator_token")"
else
  p13_run_code="000"
fi
P13_EXEC_ID=""
if [ "$p13_run_code" = "201" ]; then
  P13_EXEC_ID="$(python3 - "$TMP/body.json" "$P13_SCHEDULE_ID" <<'PY'
import json, sys
try:
    execution = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
if not isinstance(execution, dict):
    sys.exit(1)
ok = bool(execution.get("id")) and execution.get("status") == "pending"
ok = ok and execution.get("schedule_id") == sys.argv[2]
if not ok:
    sys.exit(1)
print(execution["id"])
PY
)" || P13_EXEC_ID=""
fi
if [ -n "$P13_EXEC_ID" ]; then
  pass "operator POST /api/patrol/schedules/{id}/run -> 201 execution $P13_EXEC_ID status=pending"
else
  fail "run-now failed (HTTP ${p13_run_code:-none}, body=$(head -c 400 "$TMP/body.json" 2>/dev/null))"
fi

p13_exec_list_code="$(http_get "/api/patrol/executions?limit=100" "$operator_token")"
if python3 - "$TMP/body.json" "$p13_exec_list_code" "$P13_EXEC_ID" <<'PY'
import json, sys
if len(sys.argv) < 4 or sys.argv[2] != "200":
    sys.exit(1)
try:
    items = json.load(open(sys.argv[1])).get("items") or []
except Exception:
    items = []
sys.exit(0 if any(isinstance(e, dict) and e.get("id") == sys.argv[3] for e in items) else 1)
PY
then
  pass "GET /api/patrol/executions contains the new execution"
else
  fail "new execution missing from GET /api/patrol/executions (HTTP ${p13_exec_list_code:-none})"
fi

# 2) claim -> running (assignee = operator)
if [ -n "$P13_EXEC_ID" ]; then
  p13_claim_code="$(http_post_json "/api/patrol/executions/$P13_EXEC_ID/claim" '{}' "$operator_token")"
else
  p13_claim_code="000"
fi
if python3 - "$TMP/body.json" "$p13_claim_code" <<'PY'
import json, sys
if len(sys.argv) < 3 or sys.argv[2] != "200":
    sys.exit(1)
try:
    execution = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
ok = isinstance(execution, dict) and execution.get("status") == "running"
ok = ok and execution.get("assignee") == "operator"
sys.exit(0 if ok else 1)
PY
then
  pass "operator POST /api/patrol/executions/{id}/claim -> 200 status=running assignee=operator"
else
  fail "claim failed (HTTP ${p13_claim_code:-none}, body=$(head -c 300 "$TMP/body.json" 2>/dev/null))"
fi

# 3) complete done -> done + finished_at
if [ -n "$P13_EXEC_ID" ]; then
  p13_done_code="$(http_post_json "/api/patrol/executions/$P13_EXEC_ID/complete" \
    '{"result":"done","note":"ok"}' "$operator_token")"
else
  p13_done_code="000"
fi
if python3 - "$TMP/body.json" "$p13_done_code" <<'PY'
import json, sys
if len(sys.argv) < 3 or sys.argv[2] != "200":
    sys.exit(1)
try:
    execution = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
ok = isinstance(execution, dict) and execution.get("status") == "done"
ok = ok and bool(execution.get("finished_at"))
sys.exit(0 if ok else 1)
PY
then
  pass "operator complete execution {result:done} -> 200 status=done finished_at set"
else
  fail "complete done failed (HTTP ${p13_done_code:-none}, body=$(head -c 300 "$TMP/body.json" 2>/dev/null))"
fi

# 4) issue flow: run -> claim -> complete issue -> linked order
if [ -n "$P13_SCHEDULE_ID" ]; then
  p13_run2_code="$(http_post_json "/api/patrol/schedules/$P13_SCHEDULE_ID/run" '{}' "$operator_token")"
else
  p13_run2_code="000"
fi
P13_EXEC2_ID=""
if [ "$p13_run2_code" = "201" ]; then
  P13_EXEC2_ID="$(python3 -c 'import json,sys
try: print(json.load(open(sys.argv[1])).get("id") or "")
except Exception: print("")' "$TMP/body.json")"
fi
if [ -n "$P13_EXEC2_ID" ]; then
  p13_claim2_code="$(http_post_json "/api/patrol/executions/$P13_EXEC2_ID/claim" '{}' "$operator_token")"
else
  p13_claim2_code="000"
fi
printf '      issue-flow execution: %s (claim HTTP %s)\n' "${P13_EXEC2_ID:-<none>}" "${p13_claim2_code:-none}"

if [ -n "$P13_EXEC2_ID" ]; then
  p13_issue_code="$(http_post_json "/api/patrol/executions/$P13_EXEC2_ID/complete" \
    '{"result":"issue","note":"发现设备异常"}' "$operator_token")"
else
  p13_issue_code="000"
fi
P13_ORDER_ID=""
if [ "$p13_issue_code" = "200" ]; then
  P13_ORDER_ID="$(python3 - "$TMP/body.json" <<'PY'
import json, sys
try:
    execution = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
if not isinstance(execution, dict):
    sys.exit(1)
ok = execution.get("status") == "issue"
ok = ok and isinstance(execution.get("order_id"), str) and bool(execution.get("order_id"))
ok = ok and bool(execution.get("finished_at"))
if not ok:
    sys.exit(1)
print(execution["order_id"])
PY
)" || P13_ORDER_ID=""
fi
if [ -n "$P13_ORDER_ID" ]; then
  pass "operator complete execution {result:issue} -> 200 status=issue order_id=$P13_ORDER_ID"
else
  fail "complete issue failed (HTTP ${p13_issue_code:-none}, body=$(head -c 400 "$TMP/body.json" 2>/dev/null))"
fi

p13_orders_code="$(http_get "/api/orders?limit=200" "$admin_token")"
if python3 - "$TMP/body.json" "$p13_orders_code" "$P13_ORDER_ID" <<'PY'
import json, sys
if len(sys.argv) < 4 or sys.argv[2] != "200":
    sys.exit(1)
try:
    items = json.load(open(sys.argv[1])).get("items") or []
except Exception:
    items = []
for order in items:
    if not isinstance(order, dict) or order.get("id") != sys.argv[3]:
        continue
    title = str(order.get("title") or "")
    ok = "巡检异常" in title
    ok = ok and order.get("assignee") == "operator"
    ok = ok and order.get("status") == "open"
    sys.exit(0 if ok else 1)
sys.exit(1)
PY
then
  pass "linked order exists: title contains 巡检异常, assignee=operator, status=open"
else
  fail "linked order for execution issue not found/incorrect in GET /api/orders (HTTP ${p13_orders_code:-none})"
fi

# 5) viewer claim/complete -> 403 (on a fresh pending execution)
if [ -n "$P13_SCHEDULE_ID" ]; then
  p13_run3_code="$(http_post_json "/api/patrol/schedules/$P13_SCHEDULE_ID/run" '{}' "$operator_token")"
else
  p13_run3_code="000"
fi
P13_EXEC3_ID=""
if [ "$p13_run3_code" = "201" ]; then
  P13_EXEC3_ID="$(python3 -c 'import json,sys
try: print(json.load(open(sys.argv[1])).get("id") or "")
except Exception: print("")' "$TMP/body.json")"
fi
if [ -n "$P13_EXEC3_ID" ]; then
  p13_viewer_claim_code="$(http_post_json "/api/patrol/executions/$P13_EXEC3_ID/claim" '{}' "$viewer_token")"
else
  p13_viewer_claim_code="000"
fi
if [ "$p13_viewer_claim_code" = "403" ]; then
  pass "viewer POST execution claim -> 403 forbidden (operator/admin only)"
else
  fail "viewer claim -> HTTP ${p13_viewer_claim_code:-none} (expected 403)"
fi

if [ -n "$P13_EXEC3_ID" ]; then
  p13_viewer_complete_code="$(http_post_json "/api/patrol/executions/$P13_EXEC3_ID/complete" '{"result":"done"}' "$viewer_token")"
else
  p13_viewer_complete_code="000"
fi
if [ "$p13_viewer_complete_code" = "403" ]; then
  pass "viewer POST execution complete -> 403 forbidden"
else
  fail "viewer complete -> HTTP ${p13_viewer_complete_code:-none} (expected 403)"
fi

# 6) admin deletes the operator's schedule
if [ -n "$P13_SCHEDULE_ID" ]; then
  p13_admin_del_code="$(http_delete "/api/patrol/schedules/$P13_SCHEDULE_ID" "$admin_token")"
else
  p13_admin_del_code="000"
fi
if [ "$p13_admin_del_code" = "204" ]; then
  pass "admin DELETE operator-created schedule -> 204"
  P13_SCHEDULE_ID=""
else
  fail "admin DELETE operator schedule -> HTTP ${p13_admin_del_code:-none} (expected 204)"
fi

# 7) audit actions
p13_audit_code="$(http_get '/api/audit?limit=500' "$admin_token")"
p13_audit_summary=""
if [ "$p13_audit_code" = "200" ]; then
  p13_audit_summary="$(python3 - "$TMP/body.json" <<'PY'
import json, sys
try:
    items = json.load(open(sys.argv[1])).get("items") or []
except Exception:
    items = []
seen = {}
for item in items:
    if not isinstance(item, dict):
        continue
    action = item.get("action")
    if action in ("patrol.schedule.create", "patrol.schedule.run",
                  "patrol.execution.claim", "patrol.execution.complete"):
        seen.setdefault(action, set()).add(str(item.get("actor") or ""))
missing = sorted(set(("patrol.schedule.create", "patrol.schedule.run",
                       "patrol.execution.claim", "patrol.execution.complete")) - set(seen))
ok = not missing and "operator" in seen.get("patrol.execution.claim", set())
if ok:
    print(json.dumps({action: sorted(actors) for action, actors in sorted(seen.items())}, ensure_ascii=False))
sys.exit(0 if ok else 1)
PY
)" || p13_audit_summary=""
fi
if [ -n "$p13_audit_summary" ]; then
  pass "audit contains patrol.schedule.create/run + patrol.execution.claim/complete ($p13_audit_summary)"
else
  fail "audit missing P13 patrol actions (HTTP ${p13_audit_code:-none})"
fi

# 8) cleanup: test route + leftover schedules
if [ -n "$P13_ROUTE_ID" ]; then
  p13_route_del_code="$(http_delete "/api/patrol/routes/$P13_ROUTE_ID" "$operator_token")"
else
  p13_route_del_code="000"
fi
if [ "$p13_route_del_code" = "204" ] || [ "$p13_route_del_code" = "404" ]; then
  pass "cleanup: DELETE P13 test route -> HTTP $p13_route_del_code"
  P13_ROUTE_ID=""
else
  fail "cleanup: DELETE P13 test route -> HTTP ${p13_route_del_code:-none} (expected 204)"
fi

p13_leftover_code="$(http_get "/api/patrol/schedules" "$admin_token")"
p13_leftover_count="$(python3 - "$TMP/body.json" "$p13_leftover_code" <<'PY'
import json, sys
if len(sys.argv) < 3 or sys.argv[2] != "200":
    print(-1)
    sys.exit(0)
try:
    items = json.load(open(sys.argv[1])).get("items") or []
except Exception:
    items = []
leftover = [s.get("id") for s in items
            if isinstance(s, dict) and str(s.get("name") or "").startswith("smoke 计划 P13")]
print(json.dumps({"leftover": leftover, "count": len(leftover)}, ensure_ascii=False))
PY
)"
if python3 - "$p13_leftover_count" <<'PY'
import json, sys
if len(sys.argv) < 2 or not sys.argv[1].strip():
    sys.exit(1)
try:
    info = json.loads(sys.argv[1])
except Exception:
    sys.exit(1)
sys.exit(0 if info.get("count") == 0 else 1)
PY
then
  pass "cleanup: no leftover 'smoke 计划 P13' schedules ($p13_leftover_count)"
else
  fail "leftover 'smoke 计划 P13' schedules remain ($p13_leftover_count)"
fi
rm -f "$P13_SCHEDULE_IDS_FILE" "$P13_ROUTE_IDS_FILE"

read -r p13_final_admin_code p13_final_admin_token < <(login_user admin 'Admin@123456')
if [ "$p13_final_admin_code" = "200" ] && [ -n "$p13_final_admin_token" ]; then
  pass "cleanup: admin login still OK after P13 patrol checks"
else
  fail "cleanup: admin login failed after P13 checks (HTTP ${p13_final_admin_code:-none})"
fi

# ===========================================================================
# 53. P14.0 patrol stats (setup route/schedule/executions + stats checks)
# ===========================================================================
step "53/58 patrol stats (P14: counts/rates/by_route/by_assignee, invalid from/to)"
: > "$P14_ROUTE_IDS_FILE"
: > "$P14_SCHEDULE_IDS_FILE"
# Setup: operator-created route + schedule + one done and one issue execution.
p14_route_payload='{"name":"smoke 巡检 P14 路线","waypoints":[{"x":-90,"y":-60,"z":12,"heading":90},{"x":-20,"y":-40,"z":12}],"camera_ids":["cam_001"],"interval_seconds":2}'
p14_route_code="$(http_post_json "/api/patrol/routes" "$p14_route_payload" "$operator_token")"
P14_ROUTE_ID=""
if [ "$p14_route_code" = "201" ]; then
  P14_ROUTE_ID="$(python3 -c 'import json,sys
try: print(json.load(open(sys.argv[1])).get("id") or "")
except Exception: print("")' "$TMP/body.json")"
  [ -n "$P14_ROUTE_ID" ] && printf '%s\n' "$P14_ROUTE_ID" >> "$P14_ROUTE_IDS_FILE"
fi
p14_sched_payload="$(printf '{"name":"smoke 计划 P14","route_id":"%s","interval_minutes":60,"enabled":true}' "${P14_ROUTE_ID:-}")"
p14_sched_code="$(http_post_json "/api/patrol/schedules" "$p14_sched_payload" "$operator_token")"
P14_SCHEDULE_ID=""
if [ "$p14_sched_code" = "201" ]; then
  P14_SCHEDULE_ID="$(python3 -c 'import json,sys
try: print(json.load(open(sys.argv[1])).get("id") or "")
except Exception: print("")' "$TMP/body.json")"
  [ -n "$P14_SCHEDULE_ID" ] && printf '%s\n' "$P14_SCHEDULE_ID" >> "$P14_SCHEDULE_IDS_FILE"
fi
printf '      P14 setup: route=%s schedule=%s\n' "${P14_ROUTE_ID:-<none>}" "${P14_SCHEDULE_ID:-<none>}"

p14_make_execution() {
  local result="$1" run_code exec_id
  run_code="$(http_post_json "/api/patrol/schedules/$P14_SCHEDULE_ID/run" '{}' "$operator_token")"
  exec_id="$(python3 -c 'import json,sys
try: print(json.load(open(sys.argv[1])).get("id") or "")
except Exception: print("")' "$TMP/body.json")"
  if [ -z "$exec_id" ]; then
    printf '%s' ""
    return 0
  fi
  http_post_json "/api/patrol/executions/$exec_id/claim" '{}' "$operator_token" >/dev/null
  http_post_json "/api/patrol/executions/$exec_id/complete" \
    "{\"result\":\"$result\",\"note\":\"p14 smoke $result\"}" "$operator_token" >/dev/null
  printf '%s' "$exec_id"
}
P14_EXEC_DONE_ID=""
P14_EXEC_ISSUE_ID=""
if [ -n "$P14_SCHEDULE_ID" ]; then
  P14_EXEC_DONE_ID="$(p14_make_execution done)"
  P14_EXEC_ISSUE_ID="$(p14_make_execution issue)"
fi
printf '      P14 executions: done=%s issue=%s\n' "${P14_EXEC_DONE_ID:-<none>}" "${P14_EXEC_ISSUE_ID:-<none>}"

p14_from="$(python3 -c 'from datetime import datetime, timezone, timedelta; print((datetime.now(timezone.utc)-timedelta(days=1)).isoformat(timespec="seconds").replace("+00:00","Z"))')"
p14_to="$(python3 -c 'from datetime import datetime, timezone, timedelta; print((datetime.now(timezone.utc)+timedelta(days=1)).isoformat(timespec="seconds").replace("+00:00","Z"))')"
p14_stats_code="$(http_get "/api/patrol/stats?from=$p14_from&to=$p14_to" "$admin_token")"
P14_STATS_JSON=""
if [ "$p14_stats_code" = "200" ]; then
  P14_STATS_JSON="$(python3 - "$TMP/body.json" "$P14_ROUTE_ID" <<'PY'
import json, sys
try:
    stats = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
if not isinstance(stats, dict):
    sys.exit(1)
total = stats.get("executions_total")
done = stats.get("done")
issue = stats.get("issue")
pending = stats.get("pending")
running = stats.get("running")
completion = stats.get("completion_rate")
issue_rate = stats.get("issue_rate")
avg = stats.get("avg_duration_seconds")
counts_ok = all(isinstance(v, int) and v >= 0 for v in (total, done, issue, pending, running))
counts_ok = counts_ok and total >= 2 and done >= 1 and issue >= 1
rates_ok = (isinstance(completion, (int, float)) and isinstance(issue_rate, (int, float))
            and isinstance(avg, (int, float)) and avg >= 0)
if rates_ok and counts_ok:
    rates_ok = abs(float(completion) - round(done / total, 3)) <= 0.002
    rates_ok = rates_ok and abs(float(issue_rate) - round(issue / total, 3)) <= 0.002
routes = stats.get("by_route") or []
route_ok = any(isinstance(r, dict) and r.get("route_id") == sys.argv[2] and int(r.get("total") or 0) >= 1
               for r in routes)
assignees = stats.get("by_assignee") or []
assignee_ok = any(isinstance(a, dict) and a.get("assignee") == "operator" and int(a.get("total") or 0) >= 1
                  for a in assignees)
print(json.dumps({"executions_total": total, "done": done, "issue": issue,
                  "pending": pending, "running": running,
                  "completion_rate": completion, "issue_rate": issue_rate,
                  "avg_duration_seconds": avg, "counts_ok": counts_ok,
                  "rates_ok": rates_ok, "route_ok": route_ok,
                  "assignee_ok": assignee_ok}, ensure_ascii=False))
sys.exit(0)
PY
)" || P14_STATS_JSON=""
fi

p14_stat_field() {
  python3 -c 'import json,sys
try: print(json.loads(sys.argv[1]).get(sys.argv[2]))
except Exception: print("")' "$1" "$2" 2>/dev/null || true
}

if [ -n "$P14_STATS_JSON" ] && [ "$(p14_stat_field "$P14_STATS_JSON" counts_ok)" = "True" ]; then
  pass "admin GET /api/patrol/stats -> 200 counts ok (total=$(p14_stat_field "$P14_STATS_JSON" executions_total), done=$(p14_stat_field "$P14_STATS_JSON" done), issue=$(p14_stat_field "$P14_STATS_JSON" issue), pending=$(p14_stat_field "$P14_STATS_JSON" pending), running=$(p14_stat_field "$P14_STATS_JSON" running))"
else
  fail "patrol stats counts wrong (HTTP ${p14_stats_code:-none})"
fi

if [ -n "$P14_STATS_JSON" ] && [ "$(p14_stat_field "$P14_STATS_JSON" rates_ok)" = "True" ]; then
  pass "stats completion_rate/issue_rate consistent with counts + avg_duration_seconds >= 0 (completion=$(p14_stat_field "$P14_STATS_JSON" completion_rate), issue_rate=$(p14_stat_field "$P14_STATS_JSON" issue_rate), avg=$(p14_stat_field "$P14_STATS_JSON" avg_duration_seconds)s)"
else
  fail "stats rates/avg_duration inconsistent (HTTP ${p14_stats_code:-none})"
fi

if [ -n "$P14_STATS_JSON" ] && [ "$(p14_stat_field "$P14_STATS_JSON" route_ok)" = "True" ]; then
  pass "stats by_route contains the test route ($P14_ROUTE_ID)"
else
  fail "stats by_route missing test route ($P14_ROUTE_ID)"
fi

if [ -n "$P14_STATS_JSON" ] && [ "$(p14_stat_field "$P14_STATS_JSON" assignee_ok)" = "True" ]; then
  pass "stats by_assignee contains operator"
else
  fail "stats by_assignee missing operator"
fi
P14_DONE_BEFORE="$(p14_stat_field "$P14_STATS_JSON" done)"

p14_bad_from_code="$(http_get "/api/patrol/stats?from=not-a-date" "$admin_token")"
if [ "$p14_bad_from_code" = "400" ]; then
  pass "GET /api/patrol/stats?from=<invalid> -> 400"
else
  fail "stats invalid from -> HTTP ${p14_bad_from_code:-none} (expected 400)"
fi

p14_bad_to_code="$(http_get "/api/patrol/stats?to=not-a-date" "$admin_token")"
if [ "$p14_bad_to_code" = "400" ]; then
  pass "GET /api/patrol/stats?to=<invalid> -> 400"
else
  fail "stats invalid to -> HTTP ${p14_bad_to_code:-none} (expected 400)"
fi

# ===========================================================================
# 54. P14.0 execution CSV export
# ===========================================================================
step "54/58 execution CSV export (P14: operator 200 + exact header, viewer 403)"
p14_export_code="$(http_get_with_headers "/api/patrol/executions/export" "$operator_token")"
p14_export_headers_ok="$(python3 - "$TMP/headers.txt" "$p14_export_code" <<'PY'
import sys
status = sys.argv[2] if len(sys.argv) > 2 else ""
if status != "200":
    sys.exit(1)
headers = {}
try:
    lines = open(sys.argv[1], encoding="utf-8", errors="replace").read().splitlines()
except Exception:
    lines = []
for line in lines:
    if ":" not in line:
        continue
    key, value = line.split(":", 1)
    headers[key.strip().lower()] = value.strip()
content_type = headers.get("content-type", "").lower()
disposition = headers.get("content-disposition", "").lower()
ok = content_type.startswith("text/csv") and "attachment" in disposition and "patrol-executions.csv" in disposition
if ok:
    print("content-type=%s disposition=%s" % (content_type, disposition))
sys.exit(0 if ok else 1)
PY
)" || p14_export_headers_ok=""
if [ -n "$p14_export_headers_ok" ]; then
  pass "operator GET /api/patrol/executions/export -> 200 ($p14_export_headers_ok)"
else
  fail "execution export wrong status/content-type/Content-Disposition (HTTP ${p14_export_code:-none})"
fi

p14_export_header_line="$(head -n 1 "$TMP/body.json" 2>/dev/null | tr -d '\r')"
p14_expected_header="id,schedule_id,route_name,status,assignee,result,note,scheduled_at,started_at,finished_at,order_id"
if [ "$p14_export_header_line" = "$p14_expected_header" ]; then
  pass "execution CSV header matches the contract"
else
  fail "execution CSV header mismatch (got '$p14_export_header_line')"
fi

p14_viewer_export_code="$(http_get "/api/patrol/executions/export" "$viewer_token")"
if [ "$p14_viewer_export_code" = "403" ]; then
  pass "viewer GET /api/patrol/executions/export -> 403 forbidden (operator/admin only)"
else
  fail "viewer execution export -> HTTP ${p14_viewer_export_code:-none} (expected 403)"
fi

# ===========================================================================
# 55. P14.0 history purge + audit + cleanup
# ===========================================================================
step "55/58 history purge + audit + cleanup (P14)"
p14_future="$(python3 -c 'from datetime import datetime, timezone, timedelta; print((datetime.now(timezone.utc)+timedelta(days=1)).isoformat(timespec="seconds").replace("+00:00","Z"))')"

p14_viewer_purge_code="$(http_delete "/api/patrol/executions?before=$p14_future&status=done" "$viewer_token")"
if [ "$p14_viewer_purge_code" = "403" ]; then
  pass "viewer DELETE /api/patrol/executions -> 403 forbidden (admin only)"
else
  fail "viewer purge -> HTTP ${p14_viewer_purge_code:-none} (expected 403)"
fi

p14_operator_purge_code="$(http_delete "/api/patrol/executions?before=$p14_future&status=done" "$operator_token")"
if [ "$p14_operator_purge_code" = "403" ]; then
  pass "operator DELETE /api/patrol/executions -> 403 forbidden (admin only)"
else
  fail "operator purge -> HTTP ${p14_operator_purge_code:-none} (expected 403)"
fi

p14_admin_purge_code="$(http_delete "/api/patrol/executions?before=$p14_future&status=done" "$admin_token")"
p14_deleted_done="$(python3 -c 'import json,sys
try: print(int(json.load(open(sys.argv[1])).get("deleted")))
except Exception: print("")' "$TMP/body.json" 2>/dev/null || echo '')"
if [ "$p14_admin_purge_code" = "200" ] && [ "${p14_deleted_done:-0}" -ge 1 ] 2>/dev/null; then
  pass "admin DELETE /api/patrol/executions?status=done -> 200 deleted=$p14_deleted_done"
else
  fail "admin done purge -> HTTP ${p14_admin_purge_code:-none} deleted=${p14_deleted_done:-none} (expected 200 + deleted>=1)"
fi

p14_stats_after_code="$(http_get "/api/patrol/stats" "$admin_token")"
p14_done_after="$(python3 -c 'import json,sys
try: print(int(json.load(open(sys.argv[1])).get("done")))
except Exception: print("")' "$TMP/body.json" 2>/dev/null || echo '')"
if [ "$p14_stats_after_code" = "200" ] && [ -n "${P14_DONE_BEFORE:-}" ] && [ -n "${p14_done_after:-}" ] \
  && [ "$p14_done_after" -lt "$P14_DONE_BEFORE" ] 2>/dev/null; then
  pass "stats done count decreased after purge ($P14_DONE_BEFORE -> $p14_done_after)"
else
  fail "stats done did not decrease after purge (before=${P14_DONE_BEFORE:-?}, after=${p14_done_after:-?}, HTTP ${p14_stats_after_code:-none})"
fi

p14_issue_still_exists="$(http_get "/api/patrol/executions?limit=500" "$operator_token" >/dev/null; python3 - "$TMP/body.json" "$P14_EXEC_ISSUE_ID" <<'PY'
import json, sys
if len(sys.argv) < 3:
    print("")
    sys.exit(0)
try:
    items = json.load(open(sys.argv[1])).get("items") or []
except Exception:
    items = []
print("yes" if any(isinstance(e, dict) and e.get("id") == sys.argv[2] for e in items) else "no")
PY
)"
if [ "$p14_issue_still_exists" = "yes" ]; then
  pass "issue execution remains after done purge ($P14_EXEC_ISSUE_ID)"
else
  fail "issue execution missing after done purge ($P14_EXEC_ISSUE_ID)"
fi

p14_bad_status_code="$(http_delete "/api/patrol/executions?before=$p14_future&status=pending" "$admin_token")"
if [ "$p14_bad_status_code" = "400" ]; then
  pass "admin DELETE executions with status=pending -> 400 (only done/issue)"
else
  fail "invalid purge status -> HTTP ${p14_bad_status_code:-none} (expected 400)"
fi

p14_audit_code="$(http_get '/api/audit?limit=500' "$admin_token")"
p14_audit_summary=""
if [ "$p14_audit_code" = "200" ]; then
  p14_audit_summary="$(python3 - "$TMP/body.json" <<'PY'
import json, sys
try:
    items = json.load(open(sys.argv[1])).get("items") or []
except Exception:
    items = []
for item in items:
    if not isinstance(item, dict) or item.get("action") != "patrol.executions.purge":
        continue
    detail = item.get("detail") or {}
    if isinstance(detail, dict) and int(detail.get("deleted") or 0) >= 1:
        print(json.dumps({"actor": item.get("actor"), "deleted": detail.get("deleted"),
                          "statuses": detail.get("statuses")}, ensure_ascii=False))
        sys.exit(0)
sys.exit(1)
PY
)" || p14_audit_summary=""
fi
if [ -n "$p14_audit_summary" ]; then
  pass "audit contains patrol.executions.purge with deleted count ($p14_audit_summary)"
else
  fail "audit missing patrol.executions.purge with deleted count (HTTP ${p14_audit_code:-none})"
fi

# Final cleanup: purge the remaining done/issue execution records created by smoke.
p14_final_purge_code="$(http_delete "/api/patrol/executions?before=$p14_future&status=done,issue" "$admin_token")"
p14_final_deleted="$(python3 -c 'import json,sys
try: print(int(json.load(open(sys.argv[1])).get("deleted")))
except Exception: print("")' "$TMP/body.json" 2>/dev/null || echo '')"
if [ "$p14_final_purge_code" = "200" ] && [ "${p14_final_deleted:-0}" -ge 1 ] 2>/dev/null; then
  pass "cleanup: final purge status=done,issue -> 200 deleted=$p14_final_deleted"
else
  fail "cleanup: final purge -> HTTP ${p14_final_purge_code:-none} deleted=${p14_final_deleted:-none}"
fi

p14_execs_after="$(http_get "/api/patrol/executions?limit=500" "$operator_token" >/dev/null; python3 - "$TMP/body.json" "$P14_EXEC_DONE_ID" "$P14_EXEC_ISSUE_ID" <<'PY'
import json, sys
try:
    items = json.load(open(sys.argv[1])).get("items") or []
except Exception:
    items = []
ids = {e.get("id") for e in items if isinstance(e, dict)}
remaining = [entry for entry in sys.argv[2:] if entry and entry in ids]
print(json.dumps({"remaining": remaining}, ensure_ascii=False))
sys.exit(0 if not remaining else 1)
PY
)" || p14_execs_after=""
if [ -n "$p14_execs_after" ]; then
  pass "cleanup: no leftover smoke executions ($p14_execs_after)"
else
  fail "leftover smoke executions remain after final purge (${p14_execs_after:-?})"
fi

if [ -n "$P14_SCHEDULE_ID" ]; then
  p14_sched_del_code="$(http_delete "/api/patrol/schedules/$P14_SCHEDULE_ID" "$admin_token")"
else
  p14_sched_del_code="000"
fi
if [ "$p14_sched_del_code" = "204" ]; then
  pass "cleanup: DELETE P14 test schedule -> 204"
  P14_SCHEDULE_ID=""
else
  fail "cleanup: DELETE P14 test schedule -> HTTP ${p14_sched_del_code:-none} (expected 204)"
fi

if [ -n "$P14_ROUTE_ID" ]; then
  p14_route_del_code="$(http_delete "/api/patrol/routes/$P14_ROUTE_ID" "$operator_token")"
else
  p14_route_del_code="000"
fi
if [ "$p14_route_del_code" = "204" ]; then
  pass "cleanup: DELETE P14 test route -> 204"
  P14_ROUTE_ID=""
else
  fail "cleanup: DELETE P14 test route -> HTTP ${p14_route_del_code:-none} (expected 204)"
fi
rm -f "$P14_SCHEDULE_IDS_FILE" "$P14_ROUTE_IDS_FILE"

read -r p14_final_admin_code p14_final_admin_token < <(login_user admin 'Admin@123456')
if [ "$p14_final_admin_code" = "200" ] && [ -n "$p14_final_admin_token" ]; then
  pass "cleanup: admin login still OK after P14 patrol stats checks"
else
  fail "cleanup: admin login failed after P14 checks (HTTP ${p14_final_admin_code:-none})"
fi

# ===========================================================================
# 56. P15.0 alert-triggered patrol execution (auto_patrol)
# ===========================================================================
step "56/58 alert-triggered patrol execution (P15: event -> pending execution)"
: > "$P15_ROUTE_IDS_FILE"
# Setup: an operator route whose camera_ids include cam_001 (the injected event's
# camera), then enable auto_patrol with a 1 s escalation delay.
p15_route_payload='{"name":"smoke 巡检 P15 路线","waypoints":[{"x":-90,"y":-60,"z":12,"heading":90},{"x":-20,"y":-40,"z":12}],"camera_ids":["cam_001"],"interval_seconds":2}'
p15_route_code="$(http_post_json "/api/patrol/routes" "$p15_route_payload" "$operator_token")"
P15_ROUTE_ID=""
if [ "$p15_route_code" = "201" ]; then
  P15_ROUTE_ID="$(python3 -c 'import json,sys
try: print(json.load(open(sys.argv[1])).get("id") or "")
except Exception: print("")' "$TMP/body.json")"
  [ -n "$P15_ROUTE_ID" ] && printf '%s\n' "$P15_ROUTE_ID" >> "$P15_ROUTE_IDS_FILE"
fi
printf '      P15 setup route (cam_001): %s\n' "${P15_ROUTE_ID:-<none>}"

p15_settings_on='{"enabled":true,"after_seconds":1,"severities":["warn","critical"],"auto_dispatch":false,"second_after_seconds":0,"auto_patrol":true}'
p15_set_code="$(http_put_json "/api/settings/escalation" "$p15_settings_on" "$admin_token")"
p15_get_code="$(http_get "/api/settings/escalation" "$admin_token")"
p15_settings_summary="$(python3 - "$TMP/body.json" "$p15_get_code" <<'PY'
import json, sys
if len(sys.argv) < 3 or sys.argv[2] != "200":
    sys.exit(1)
try:
    settings = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
ok = settings.get("auto_patrol") is True
ok = ok and settings.get("after_seconds") == 1
ok = ok and settings.get("auto_dispatch") is False
if ok:
    print(json.dumps({"auto_patrol": True, "after_seconds": settings.get("after_seconds"),
                      "auto_dispatch": settings.get("auto_dispatch"),
                      "second_after_seconds": settings.get("second_after_seconds")},
                     ensure_ascii=False))
sys.exit(0 if ok else 1)
PY
)" || p15_settings_summary=""
if [ -n "$p15_settings_summary" ]; then
  pass "escalation settings accept auto_patrol ($p15_settings_summary)"
else
  fail "escalation settings auto_patrol not accepted (HTTP ${p15_get_code:-none}, body=$(head -c 300 "$TMP/body.json" 2>/dev/null))"
fi

# Temporarily relax CAM-001's dedupe window (other tests/simulators may keep
# injecting intrusion events on CAM-001) so the stale test event is accepted
# deterministically; the original rules are restored right after ingestion (and
# by the EXIT trap if this step aborts).
P15_CAM001_RULES_FILE="$TMP/p15_cam001_rules.json"
rm -f "$P15_CAM001_RULES_FILE"
p15_rules_code="$(http_get "/api/cameras/cam_001/rules" "$admin_token")"
if [ "$p15_rules_code" = "200" ]; then
  python3 - "$TMP/body.json" > "$P15_CAM001_RULES_FILE" <<'PY'
import json, sys
rules = json.load(open(sys.argv[1]))
print(json.dumps({"confidence_threshold": rules.get("confidence_threshold", 0.6),
                  "dedupe_seconds": rules.get("dedupe_seconds", 5),
                  "enabled_classes": rules.get("enabled_classes") or [],
                  "roi": rules.get("roi")}, ensure_ascii=False))
PY
  p15_relaxed_rules="$(python3 - "$P15_CAM001_RULES_FILE" <<'PY'
import json, sys
rules = json.load(open(sys.argv[1]))
rules["dedupe_seconds"] = 0
print(json.dumps(rules, ensure_ascii=False))
PY
)"
  http_put_json "/api/cameras/cam_001/rules" "$p15_relaxed_rules" "$admin_token" >/dev/null
fi

# Inject a stale CAM-001/intrusion event (retry briefly if the 5 s dedupe window
# swallows it) and wait for the escalation scan to create the patrol execution.
P15_EVENT_ID=""
p15_attempt=0
while [ "$p15_attempt" -lt 5 ] && [ -z "$P15_EVENT_ID" ]; do
  p15_attempt=$((p15_attempt + 1))
  p15_ts="$(python3 -c 'from datetime import datetime, timezone, timedelta; print((datetime.now(timezone.utc)-timedelta(seconds=60)).isoformat(timespec="milliseconds").replace("+00:00","Z"))')"
  p15_payload="$(printf '{"camera_code":"CAM-001","ts":"%s","detections":[{"class":"intrusion","confidence":0.995,"bbox":[0.40,0.30,0.60,0.80]}]}' "$p15_ts")"
  p15_ingest_code="$(http_post_json "/api/ingest/detections" "$p15_payload" "")"
  if [ "$p15_ingest_code" = "202" ]; then
    P15_EVENT_ID="$(python3 -c 'import json,sys
try:
    data = json.load(open(sys.argv[1]))
    events = data.get("events") or []
    print(events[0].get("id", "") if events else "")
except Exception: print("")' "$TMP/body.json")"
  fi
  [ -z "$P15_EVENT_ID" ] && sleep 2
done
if [ -f "$P15_CAM001_RULES_FILE" ]; then
  p15_restore_rules="$(cat "$P15_CAM001_RULES_FILE")"
  http_put_json "/api/cameras/cam_001/rules" "$p15_restore_rules" "$admin_token" >/dev/null
  rm -f "$P15_CAM001_RULES_FILE"
fi
if [ -n "$P15_EVENT_ID" ]; then
  pass "stale CAM-001/intrusion ingested -> 202 event $P15_EVENT_ID"
else
  fail "could not inject a CAM-001/intrusion event for the P15 check"
fi

p15_executions_code=""
P15_EXEC_ID=""
p15_exec_poll=0
while [ "$p15_exec_poll" -lt 30 ] && [ -z "$P15_EXEC_ID" ]; do
  p15_exec_poll=$((p15_exec_poll + 1))
  p15_executions_code="$(http_get "/api/patrol/executions?event_id=$P15_EVENT_ID" "$operator_token")"
  if [ "$p15_executions_code" = "200" ]; then
    P15_EXEC_ID="$(python3 - "$TMP/body.json" "$P15_EVENT_ID" <<'PY'
import json, sys
if len(sys.argv) < 3:
    sys.exit(1)
try:
    items = json.load(open(sys.argv[1])).get("items") or []
except Exception:
    sys.exit(1)
matches = [e for e in items if isinstance(e, dict) and e.get("event_id") == sys.argv[2]]
if len(matches) == 1 and matches[0].get("status") == "pending":
    print(matches[0].get("id") or "")
sys.exit(0)
PY
)"
  fi
  [ -z "$P15_EXEC_ID" ] && sleep 2
done
printf '      P15 event execution poll: attempts=%s executions_code=%s exec=%s\n' "$p15_exec_poll" "${p15_executions_code:-<none>}" "${P15_EXEC_ID:-<none>}"

p15_pending_summary="$(python3 - "$TMP/body.json" "$p15_executions_code" "$P15_EVENT_ID" <<'PY'
import json, sys
if len(sys.argv) < 4 or sys.argv[2] != "200":
    sys.exit(1)
try:
    items = json.load(open(sys.argv[1])).get("items") or []
except Exception:
    sys.exit(1)
matches = [e for e in items if isinstance(e, dict) and e.get("event_id") == sys.argv[3]]
if len(matches) != 1 or matches[0].get("status") != "pending":
    sys.exit(1)
print(json.dumps({"count": len(matches), "status": matches[0].get("status")}, ensure_ascii=False))
PY
)" || p15_pending_summary=""
if [ -n "$p15_pending_summary" ] && [ -n "$P15_EXEC_ID" ]; then
  pass "GET /api/patrol/executions?event_id=... -> 200 exactly 1 pending execution ($p15_pending_summary)"
else
  fail "no single pending execution for event $P15_EVENT_ID (poll $p15_exec_poll, HTTP ${p15_executions_code:-none})"
fi

p15_fields_summary="$(python3 - "$TMP/body.json" "$p15_executions_code" "$P15_EVENT_ID" "$P15_ROUTE_ID" <<'PY'
import json, sys
if len(sys.argv) < 5 or sys.argv[2] != "200":
    sys.exit(1)
try:
    items = json.load(open(sys.argv[1])).get("items") or []
except Exception:
    sys.exit(1)
matches = [e for e in items if isinstance(e, dict) and e.get("event_id") == sys.argv[3]]
if len(matches) != 1:
    sys.exit(1)
execution = matches[0]
ok = execution.get("schedule_id") in ("", None)
ok = ok and execution.get("route_id") == sys.argv[4]
ok = ok and "告警联动" in str(execution.get("note") or "")
if ok:
    print(json.dumps({"exec_id": execution.get("id"),
                      "schedule_id": execution.get("schedule_id"),
                      "route_id": execution.get("route_id"),
                      "note": execution.get("note")}, ensure_ascii=False))
sys.exit(0 if ok else 1)
PY
)" || p15_fields_summary=""
if [ -n "$p15_fields_summary" ]; then
  pass "event-linked execution fields ok: schedule_id empty, route matches, note has 告警联动 ($p15_fields_summary)"
else
  fail "event-linked execution fields wrong (schedule_id/route_id/note) for event $P15_EVENT_ID"
fi

p15_audit_created=""
p15_created_poll=0
while [ "$p15_created_poll" -lt 20 ] && [ -z "$p15_audit_created" ]; do
  p15_created_poll=$((p15_created_poll + 1))
  p15_audit_code="$(http_get '/api/audit?limit=500' "$admin_token")"
  if [ "$p15_audit_code" = "200" ]; then
    p15_audit_created="$(python3 - "$TMP/body.json" "$P15_EVENT_ID" <<'PY'
import json, sys
try:
    items = json.load(open(sys.argv[1])).get("items") or []
except Exception:
    items = []
event_id = sys.argv[2] if len(sys.argv) > 2 else ""
for item in items:
    if not isinstance(item, dict) or item.get("action") != "patrol.execution.created":
        continue
    detail = item.get("detail") or {}
    if not isinstance(detail, dict) or detail.get("source") != "event":
        continue
    if detail.get("event_id") in ("", None, event_id):
        print(json.dumps({"actor": item.get("actor"), "source": detail.get("source"),
                          "event_id": detail.get("event_id")}, ensure_ascii=False))
        sys.exit(0)
sys.exit(1)
PY
)" || p15_audit_created=""
  fi
  [ -z "$p15_audit_created" ] && sleep 2
done
if [ -n "$p15_audit_created" ]; then
  pass "audit patrol.execution.created has detail.source=event ($p15_audit_created)"
else
  fail "audit missing patrol.execution.created source=event for $P15_EVENT_ID"
fi

p15_list_code="$(http_get "/api/patrol/executions?limit=500" "$operator_token")"
if python3 - "$TMP/body.json" "$p15_list_code" "$P15_EXEC_ID" <<'PY'
import json, sys
if len(sys.argv) < 4 or sys.argv[2] != "200":
    sys.exit(1)
try:
    items = json.load(open(sys.argv[1])).get("items") or []
except Exception:
    items = []
sys.exit(0 if any(isinstance(e, dict) and e.get("id") == sys.argv[3] for e in items) else 1)
PY
then
  pass "event-linked execution appears in GET /api/patrol/executions"
else
  fail "event-linked execution missing from GET /api/patrol/executions (HTTP ${p15_list_code:-none})"
fi

# ===========================================================================
# 57. P15.0 dedupe (no second execution for the same event)
# ===========================================================================
step "57/58 dedupe: one execution per event (P15: wait + re-escalation)"
sleep 5
p15_dedupe_code="$(http_get "/api/patrol/executions?event_id=$P15_EVENT_ID" "$operator_token")"
p15_dedupe_count="$(python3 - "$TMP/body.json" "$p15_dedupe_code" "$P15_EVENT_ID" <<'PY'
import json, sys
if len(sys.argv) < 4 or sys.argv[2] != "200":
    print(-1)
    sys.exit(0)
try:
    items = json.load(open(sys.argv[1])).get("items") or []
except Exception:
    items = []
print(sum(1 for e in items if isinstance(e, dict) and e.get("event_id") == sys.argv[3]))
PY
)"
if [ "$p15_dedupe_count" = "1" ]; then
  pass "dedupe after a further scan cycle: still exactly 1 execution for the event"
else
  fail "dedupe failed after wait (event executions=${p15_dedupe_count:-?})"
fi

# Force a re-escalation attempt (second_after_seconds=1) and expect a skip.
p15_settings_second='{"enabled":true,"after_seconds":1,"severities":["warn","critical"],"auto_dispatch":false,"second_after_seconds":1,"auto_patrol":true}'
http_put_json "/api/settings/escalation" "$p15_settings_second" "$admin_token" >/dev/null
p15_skipped_duplicate=""
p15_skip_poll=0
while [ "$p15_skip_poll" -lt 30 ] && [ -z "$p15_skipped_duplicate" ]; do
  p15_skip_poll=$((p15_skip_poll + 1))
  p15_audit_code="$(http_get '/api/audit?limit=500' "$admin_token")"
  if [ "$p15_audit_code" = "200" ]; then
    p15_skipped_duplicate="$(python3 - "$TMP/body.json" "$P15_EVENT_ID" <<'PY'
import json, sys
try:
    items = json.load(open(sys.argv[1])).get("items") or []
except Exception:
    items = []
event_id = sys.argv[2] if len(sys.argv) > 2 else ""
for item in items:
    if not isinstance(item, dict) or item.get("action") != "patrol.execution.skipped":
        continue
    detail = item.get("detail") or {}
    if not isinstance(detail, dict) or detail.get("reason") != "duplicate":
        continue
    resource = str(item.get("resource") or "")
    if resource == event_id or detail.get("event_id") == event_id:
        print(json.dumps({"actor": item.get("actor"), "reason": detail.get("reason"),
                          "resource": resource, "execution_id": detail.get("execution_id")},
                         ensure_ascii=False))
        sys.exit(0)
sys.exit(1)
PY
)" || p15_skipped_duplicate=""
  fi
  [ -z "$p15_skipped_duplicate" ] && sleep 2
done
if [ -n "$p15_skipped_duplicate" ]; then
  pass "re-escalation duplicate skip audited ($p15_skipped_duplicate)"
else
  fail "audit missing patrol.execution.skipped reason=duplicate after forced re-escalation"
fi

p15_dedupe2_code="$(http_get "/api/patrol/executions?event_id=$P15_EVENT_ID" "$operator_token")"
p15_dedupe2_count="$(python3 - "$TMP/body.json" "$p15_dedupe2_code" "$P15_EVENT_ID" <<'PY'
import json, sys
if len(sys.argv) < 4 or sys.argv[2] != "200":
    print(-1)
    sys.exit(0)
try:
    items = json.load(open(sys.argv[1])).get("items") or []
except Exception:
    items = []
print(sum(1 for e in items if isinstance(e, dict) and e.get("event_id") == sys.argv[3]))
PY
)"
if [ "$p15_dedupe2_count" = "1" ]; then
  pass "still exactly 1 execution for the event after the re-escalation attempt"
else
  fail "duplicate execution created after re-escalation (count=${p15_dedupe2_count:-?})"
fi

# ===========================================================================
# 58. P15.0 cleanup (restore defaults, finish + purge execution, delete route)
# ===========================================================================
step "58/58 cleanup (P15: restore escalation defaults, done + purge, delete route)"
p15_defaults='{"enabled":true,"after_seconds":300,"severities":["warn","critical"],"auto_dispatch":true,"second_after_seconds":0,"auto_patrol":false}'
p15_restore_code="$(http_put_json "/api/settings/escalation" "$p15_defaults" "$admin_token")"
p15_restore_get="$(http_get "/api/settings/escalation" "$admin_token")"
p15_restore_summary="$(python3 - "$TMP/body.json" "$p15_restore_get" <<'PY'
import json, sys
if len(sys.argv) < 3 or sys.argv[2] != "200":
    sys.exit(1)
try:
    settings = json.load(open(sys.argv[1]))
except Exception:
    sys.exit(1)
ok = (settings.get("enabled") is True and settings.get("after_seconds") == 300
      and settings.get("auto_dispatch") is True and settings.get("second_after_seconds") == 0
      and settings.get("auto_patrol") is False)
if ok:
    print(json.dumps({k: settings.get(k) for k in
                      ("after_seconds", "auto_dispatch", "second_after_seconds", "auto_patrol")},
                     ensure_ascii=False))
sys.exit(0 if ok else 1)
PY
)" || p15_restore_summary=""
if [ -n "$p15_restore_summary" ]; then
  pass "cleanup: escalation defaults restored ($p15_restore_summary)"
else
  fail "could not restore escalation defaults (HTTP ${p15_restore_get:-none})"
fi

if [ -n "$P15_EXEC_ID" ]; then
  p15_claim_code="$(http_post_json "/api/patrol/executions/$P15_EXEC_ID/claim" '{}' "$operator_token")"
else
  p15_claim_code="000"
fi
p15_claim_status="$(python3 -c 'import json,sys
try:
    d=json.load(open(sys.argv[1])); print(d.get("status",""))
except Exception: print("")' "$TMP/body.json" 2>/dev/null || echo '')"
if [ "$p15_claim_code" = "200" ] && [ "$p15_claim_status" = "running" ]; then
  pass "cleanup: operator claims event execution -> 200 running"
else
  fail "cleanup: claim event execution -> HTTP ${p15_claim_code:-none} status=${p15_claim_status:-none}"
fi

if [ -n "$P15_EXEC_ID" ]; then
  p15_done_code="$(http_post_json "/api/patrol/executions/$P15_EXEC_ID/complete" \
    '{"result":"done","note":"p15 smoke cleanup"}' "$operator_token")"
else
  p15_done_code="000"
fi
p15_done_status="$(python3 -c 'import json,sys
try:
    d=json.load(open(sys.argv[1])); print(d.get("status",""))
except Exception: print("")' "$TMP/body.json" 2>/dev/null || echo '')"
if [ "$p15_done_code" = "200" ] && [ "$p15_done_status" = "done" ]; then
  pass "cleanup: complete event execution -> 200 done"
else
  fail "cleanup: complete event execution -> HTTP ${p15_done_code:-none} status=${p15_done_status:-none}"
fi

p15_purge_future="$(python3 -c 'from datetime import datetime, timezone, timedelta; print((datetime.now(timezone.utc)+timedelta(days=1)).isoformat(timespec="seconds").replace("+00:00","Z"))')"
p15_purge_code="$(http_delete "/api/patrol/executions?before=$p15_purge_future&status=done" "$admin_token")"
p15_purge_deleted="$(python3 -c 'import json,sys
try: print(int(json.load(open(sys.argv[1])).get("deleted")))
except Exception: print("")' "$TMP/body.json" 2>/dev/null || echo '')"
if [ "$p15_purge_code" = "200" ] && [ "${p15_purge_deleted:-0}" -ge 1 ] 2>/dev/null; then
  pass "cleanup: admin purge done executions -> 200 deleted=$p15_purge_deleted"
else
  fail "cleanup: purge done executions -> HTTP ${p15_purge_code:-none} deleted=${p15_purge_deleted:-none}"
fi

p15_after_code="$(http_get "/api/patrol/executions?event_id=$P15_EVENT_ID" "$operator_token")"
p15_after_count="$(python3 - "$TMP/body.json" "$p15_after_code" "$P15_EVENT_ID" <<'PY'
import json, sys
if len(sys.argv) < 4 or sys.argv[2] != "200":
    print(-1)
    sys.exit(0)
try:
    items = json.load(open(sys.argv[1])).get("items") or []
except Exception:
    items = []
print(sum(1 for e in items if isinstance(e, dict) and e.get("event_id") == sys.argv[3]))
PY
)"
if [ "$p15_after_count" = "0" ]; then
  pass "cleanup: no execution left for the test event after purge"
else
  fail "execution for test event still present after purge (count=${p15_after_count:-?})"
fi

if [ -n "$P15_ROUTE_ID" ]; then
  p15_route_del_code="$(http_delete "/api/patrol/routes/$P15_ROUTE_ID" "$operator_token")"
else
  p15_route_del_code="000"
fi
if [ "$p15_route_del_code" = "204" ]; then
  pass "cleanup: DELETE P15 test route -> 204"
  P15_ROUTE_ID=""
else
  fail "cleanup: DELETE P15 test route -> HTTP ${p15_route_del_code:-none} (expected 204)"
fi
rm -f "$P15_ROUTE_IDS_FILE"

read -r p15_final_admin_code p15_final_admin_token < <(login_user admin 'Admin@123456')
if [ "$p15_final_admin_code" = "200" ] && [ -n "$p15_final_admin_token" ]; then
  pass "cleanup: admin login still OK after P15 checks"
else
  fail "cleanup: admin login failed after P15 checks (HTTP ${p15_final_admin_code:-none})"
fi

summary
if [ "$FAIL_COUNT" -eq 0 ]; then
  exit 0
fi
exit 1
