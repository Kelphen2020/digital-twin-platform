#!/usr/bin/env bash
# ---------------------------------------------------------------------------
# P0 + P1.0 end-to-end smoke test for the digital-twin API.
#
# P0 chain (docs/API.md, docs/P0_SCOPE.md §4.6):
#   health -> camera inventory (6 seeded cameras) -> ingest a high-confidence
#   person detection on CAM-001 -> event appears via GET /api/events ->
#   ack the event -> /metrics exposes the required counters.
#
# P1.0 additions (docs/API_P1.md):
#   calibration solve (4+ synthetic points for CAM-001) -> PUT calibration ->
#   GET camera shows calibration; ingest a detection carrying track_id ->
#   GET /api/tracks?camera_id=... returns it; event workflow: GET detail
#   (empty actions) -> dispatch -> close -> detail shows the actions;
#   GET /api/audit non-empty; GET /api/stats/summary totals > 0;
#   GET /api/stats/events.csv starts with the documented header.
#
# Dependencies: bash, curl, python3 only.
#
# Usage:
#   ./scripts/smoke.sh                          # default http://127.0.0.1:8080
#   API_BASE=http://127.0.0.1:8080 ./scripts/smoke.sh
#
# Exit codes: 0 = all checks passed, 1 = at least one check failed.
# ---------------------------------------------------------------------------
set -uo pipefail

API_BASE="${API_BASE:-http://127.0.0.1:8080}"
API_BASE="${API_BASE%/}"

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
# tiny HTTP helpers: body always lands in $TMP/body.json, echo is the status
# ---------------------------------------------------------------------------
http_get() {
  curl -s -o "$TMP/body.json" -w '%{http_code}' -m 5 "$API_BASE$1" 2>/dev/null || true
}

http_post_json() {
  curl -s -o "$TMP/body.json" -w '%{http_code}' -m 5 -X POST \
    -H 'Content-Type: application/json' --data "$2" "$API_BASE$1" 2>/dev/null || true
}

http_put_json() {
  curl -s -o "$TMP/body.json" -w '%{http_code}' -m 5 -X PUT \
    -H 'Content-Type: application/json' --data "$2" "$API_BASE$1" 2>/dev/null || true
}

utc_now() {
  python3 -c 'from datetime import datetime, timezone; print(datetime.now(timezone.utc).isoformat(timespec="milliseconds").replace("+00:00", "Z"))'
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

# dedup_seconds_for CAMERA_CODE EVENT_TYPE -> seconds to wait (0 = window clear)
dedup_seconds_for() {
  local events_code
  events_code="$(curl -s -o "$TMP/events_recent.json" -w '%{http_code}' -m 5 \
    "$API_BASE/api/events?limit=100" 2>/dev/null || true)"
  if [ "$events_code" != "200" ]; then
    echo "0"
    return 0
  fi
  python3 - "$TMP/events_recent.json" "$1" "$2" <<'PY'
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
# Wait until a fresh event for this pair can be created.  The window is
# re-checked after each sleep because a concurrently running simulator can
# create a newer event while we wait (which restarts the 5 s window).
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

printf 'smoke target: %s\n' "$API_BASE"

# ===========================================================================
# 1. health (retry briefly in case the API is still starting up)
# ===========================================================================
step "1/13 health"
health_code=""
attempt=0
while [ "$attempt" -lt 5 ]; do
  attempt=$((attempt + 1))
  health_code="$(curl -s -o "$TMP/health.json" -w '%{http_code}' -m 5 "$API_BASE/api/health" 2>/dev/null || true)"
  [ "$health_code" = "200" ] && break
  sleep 0.5
done

if [ "$health_code" != "200" ]; then
  fail "GET $API_BASE/api/health -> HTTP ${health_code:-none}; is the API running? (expected :8080)"
  summary
  exit 1
fi

if python3 - "$TMP/health.json" <<'PY'
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
  cat "$TMP/health.json" >&2
fi

# ===========================================================================
# 2. camera inventory: >= 1 camera and the six seeded codes CAM-001..CAM-006
# ===========================================================================
step "2/13 cameras"
cameras_code="$(curl -s -o "$TMP/cameras.json" -w '%{http_code}' -m 5 "$API_BASE/api/cameras" 2>/dev/null || true)"
if [ "$cameras_code" != "200" ]; then
  fail "GET /api/cameras -> HTTP ${cameras_code:-none}"
else
  cam_report="$(python3 - "$TMP/cameras.json" <<'PY'
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

cam1_id="$(camera_id_for CAM-001)"
cam3_id="$(camera_id_for CAM-003)"
world_json="$(camera_world_ahead CAM-001 30)"
if [ -z "$world_json" ]; then
  world_json='{"x": 0.0, "y": 0.0, "z": 0.0}'
fi

# ===========================================================================
# 3. ingest one high-confidence person detection for CAM-001
# ===========================================================================
step "3/13 ingest detection (P0)"
ingest_code=""
event_id=""
ingest_attempt=0
while [ "$ingest_attempt" -lt 3 ]; do
  ingest_attempt=$((ingest_attempt + 1))
  wait_dedup_event CAM-001 person
  utc_ts="$(utc_now)"
  payload="$(printf '{"camera_code":"CAM-001","ts":"%s","detections":[{"class":"person","confidence":0.987654,"bbox":[0.42,0.35,0.55,0.72],"world":%s}]}' "$utc_ts" "$world_json")"
  ingest_code="$(curl -s -o "$TMP/ingest.json" -w '%{http_code}' -m 5 \
    -X POST -H 'Content-Type: application/json' --data "$payload" \
    "$API_BASE/api/ingest/detections" 2>/dev/null || true)"
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
  pass "POST /api/ingest/detections -> HTTP $ingest_code (attempt $ingest_attempt)"
  ingest_report="$(python3 - "$TMP/ingest.json" <<'PY'
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
  fail "POST /api/ingest/detections -> HTTP ${ingest_code:-none} body=$(head -c 300 "$TMP/ingest.json" 2>/dev/null)"
fi

# ===========================================================================
# 4. poll GET /api/events?limit=20 until the event is visible (<= 5 s)
# ===========================================================================
step "4/13 event appears in /api/events (<= 5s)"
found_id=""
attempt=0
while [ "$attempt" -lt 10 ]; do
  attempt=$((attempt + 1))
  curl -s -m 2 "$API_BASE/api/events?limit=20" -o "$TMP/events.json" 2>/dev/null || true
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
# 5. ack the event
# ===========================================================================
step "5/13 ack event (P0)"
if [ -n "$found_id" ]; then
  ack_code="$(curl -s -o "$TMP/ack.json" -w '%{http_code}' -m 5 -X POST \
    "$API_BASE/api/events/$found_id/ack" 2>/dev/null || true)"
  if python3 - "$TMP/ack.json" "$ack_code" <<'PY'
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
    fail "ack for $found_id failed (HTTP ${ack_code:-none}, body=$(head -c 300 "$TMP/ack.json" 2>/dev/null))"
  fi
else
  fail "skipped ack: no event id from step 4"
fi

# ===========================================================================
# 6. /metrics exposes the required counters
# ===========================================================================
step "6/13 metrics"
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
# 7. P1.0 calibration: solve -> PUT -> GET camera shows calibration
# ===========================================================================
step "7/13 calibration (P1: solve -> PUT -> GET camera)"
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

solve_code="$(http_post_json /api/calibration/solve "$solve_payload")"
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
  pass "POST /api/calibration/solve -> 3x3 matrix, rmse=$cal_rmse"
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
  put_code="$(http_put_json "/api/cameras/$cam1_id/calibration" "$put_payload")"
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
  pass "PUT /api/cameras/$cam1_id/calibration -> camera.calibration saved"
else
  fail "PUT /api/cameras/$cam1_id/calibration failed (HTTP ${put_code:-none}, body=$(head -c 300 "$TMP/body.json" 2>/dev/null))"
fi

if [ -n "$cam1_id" ]; then
  get_cam_code="$(http_get "/api/cameras/$cam1_id")"
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
# 8. P1.0 tracks: ingest a detection with track_id -> GET /api/tracks returns it
# ===========================================================================
step "8/13 tracks (P1: ingest with track_id -> GET /api/tracks)"
track_id="trk-smoke-$(python3 -c 'import time; print(int(time.time() * 1000))')"
track_payload="$(printf '{"camera_code":"CAM-001","detections":[{"class":"person","confidence":0.77,"bbox":[0.40,0.30,0.60,0.80],"track_id":"%s","world":%s}]}' "$track_id" "$world_json")"
track_ingest_code="$(http_post_json /api/ingest/detections "$track_payload")"
if [ "$track_ingest_code" = "200" ] || [ "$track_ingest_code" = "202" ]; then
  pass "POST /api/ingest/detections with track_id=$track_id -> HTTP $track_ingest_code"
else
  fail "tracked ingest failed (HTTP ${track_ingest_code:-none}, body=$(head -c 300 "$TMP/body.json" 2>/dev/null))"
fi

track_found=""
attempt=0
while [ "$attempt" -lt 10 ]; do
  attempt=$((attempt + 1))
  tracks_code="$(curl -s -o "$TMP/tracks.json" -w '%{http_code}' -m 3 \
    "$API_BASE/api/tracks?camera_id=$cam1_id&limit=200" 2>/dev/null || true)"
  if [ "$tracks_code" = "200" ]; then
    track_found="$(python3 - "$TMP/tracks.json" "$track_id" "$cam1_id" <<'PY'
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
# 9. P1.0 workflow: create event -> GET detail with empty actions
# ===========================================================================
step "9/13 event workflow (P1: create -> detail actions empty)"
wf_event_id=""
attempt=0
while [ "$attempt" -lt 3 ] && [ -z "$wf_event_id" ]; do
  attempt=$((attempt + 1))
  wait_dedup_event CAM-003 smoke
  wf_payload='{"camera_code":"CAM-003","detections":[{"class":"smoke","confidence":0.934567,"bbox":[0.40,0.30,0.60,0.80],"world":{"x":-50.0,"y":20.0,"z":0.0}}]}'
  wf_ingest_code="$(http_post_json /api/ingest/detections "$wf_payload")"
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
  wf_detail_code="$(http_get "/api/events/$wf_event_id")"
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
# 10. P1.0 workflow: dispatch (no status change) then close
# ===========================================================================
step "10/13 event workflow (P1: dispatch -> close)"
if [ -n "$wf_event_id" ]; then
  dispatch_code="$(http_post_json "/api/events/$wf_event_id/dispatch" '{"target":"安保A组","note":"请5分钟内到场"}')"
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
  close_code="$(http_post_json "/api/events/$wf_event_id/close" '{"note":"现场已处置"}')"
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
# 11. P1.0 workflow: event detail now lists the dispatch + close actions
# ===========================================================================
step "11/13 event detail shows actions (P1)"
if [ -n "$wf_event_id" ]; then
  wf_detail2_code="$(http_get "/api/events/$wf_event_id")"
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
# 12. P1.0 audit log records the write operations
# ===========================================================================
step "12/13 audit (P1)"
audit_code="$(http_get '/api/audit?limit=100')"
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
wanted = {"camera.calibration.put", "event.dispatch", "event.close"}
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
# 13. P1.0 stats summary + events CSV export
# ===========================================================================
step "13/13 stats (P1: summary + events.csv)"
summary_code="$(http_get '/api/stats/summary')"
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

csv_code="$(curl -s -o "$TMP/events.csv" -w '%{http_code}' -m 5 "$API_BASE/api/stats/events.csv" 2>/dev/null || true)"
expected_header='id,camera_code,camera_name,type,severity,confidence,x,y,z,occurred_at,status'
csv_header="$(head -n 1 "$TMP/events.csv" 2>/dev/null | tr -d '\r')"
if [ "$csv_code" = "200" ] && [ "$csv_header" = "$expected_header" ]; then
  pass "GET /api/stats/events.csv -> header matches contract"
else
  fail "GET /api/stats/events.csv failed (HTTP ${csv_code:-none}, header='$csv_header')"
fi

summary
if [ "$FAIL_COUNT" -eq 0 ]; then
  exit 0
fi
exit 1
