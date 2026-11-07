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
trap 'rm -rf "$TMP"' EXIT

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
step "1/18 health"
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
step "2/18 auth (P2: no-token 401, login, /api/auth/me)"
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
step "3/18 cameras"
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
world_json="$(camera_world_ahead CAM-001 30)"
if [ -z "$world_json" ]; then
  world_json='{"x": 0.0, "y": 0.0, "z": 0.0}'
fi

# ===========================================================================
# 4. ingest one high-confidence person detection for CAM-001 (token-free)
# ===========================================================================
step "4/18 ingest detection (P0, token-free machine entry)"
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
step "5/18 event appears in /api/events (<= 5s)"
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
step "6/18 ack event (P0)"
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
step "7/18 metrics"
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
step "8/18 calibration (P1: solve -> PUT -> GET camera, admin)"
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
step "9/18 tracks (P1: ingest with track_id -> GET /api/tracks)"
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
step "10/18 event workflow (P1: create -> detail actions empty)"
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
step "11/18 event workflow (P1: dispatch -> close)"
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
step "12/18 event detail shows actions (P1)"
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
step "13/18 audit (P1)"
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
step "14/18 stats (P1: summary + events.csv)"
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
step "15/18 RBAC (P2: viewer read-only, operator ack)"
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
step "16/18 orders (P2: create -> processing -> done -> GET)"
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
step "17/18 camera rules (P2: confidence_threshold effect, restore defaults)"
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
step "18/18 operator camera scope + P2 audit"
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

summary
if [ "$FAIL_COUNT" -eq 0 ]; then
  exit 0
fi
exit 1
