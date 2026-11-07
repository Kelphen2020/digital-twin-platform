#!/usr/bin/env bash
# ---------------------------------------------------------------------------
# P0 end-to-end smoke test for the digital-twin API.
#
# Verifies the whole chain required by docs/P0_SCOPE.md §4.6:
#   health -> camera inventory (6 seeded cameras) -> ingest a high-confidence
#   person detection on CAM-001 -> event appears via GET /api/events ->
#   ack the event -> /metrics exposes the required counters.
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

printf 'smoke target: %s\n' "$API_BASE"

# ---------------------------------------------------------------------------
# 1. health (retry briefly in case the API is still starting up)
# ---------------------------------------------------------------------------
step "1/6 health"
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

# ---------------------------------------------------------------------------
# 2. camera inventory: >= 1 camera and the six seeded codes CAM-001..CAM-006
# ---------------------------------------------------------------------------
step "2/6 cameras"
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

# World point 30 m in front of CAM-001 (along its heading) for the detection.
world_json="$(python3 - "$TMP/cameras.json" <<'PY'
import json, math, sys
try:
    data = json.load(open(sys.argv[1]))
except Exception:
    print("")
    sys.exit(0)
items = data.get("items") if isinstance(data, dict) else None
cam = None
for item in items or []:
    if isinstance(item, dict) and item.get("code") == "CAM-001":
        cam = item
        break
if cam is None:
    print("")
    sys.exit(0)
heading = math.radians(float(cam.get("heading") or 0.0))
distance = 30.0
print(json.dumps({
    "x": round(float(cam.get("x") or 0.0) + distance * math.sin(heading), 3),
    "y": round(float(cam.get("y") or 0.0) + distance * math.cos(heading), 3),
    "z": 0.0,
}))
PY
)"
if [ -z "$world_json" ]; then
  world_json='{"x": 0.0, "y": 0.0, "z": 0.0}'
fi

# The API dedups (camera, class) events on a 5 s window (docs/API.md rule #2).
# If a CAM-001/person event was created in the last 5 s our detection would be
# dropped, so wait for the window to clear before posting.
wait_seconds=0
events_code="$(curl -s -o "$TMP/events_before.json" -w '%{http_code}' -m 5 "$API_BASE/api/events?limit=50" 2>/dev/null || true)"
if [ "$events_code" = "200" ]; then
  wait_seconds="$(python3 - "$TMP/events_before.json" <<'PY'
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
    if event.get("camera_code") != "CAM-001" or event.get("type") != "person":
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
)"
fi
case "$wait_seconds" in
  '' | *[!0-9.]*) wait_seconds=0 ;;
esac
if python3 -c 'import sys; sys.exit(0 if float(sys.argv[1]) > 0 else 1)' "$wait_seconds" 2>/dev/null; then
  printf '      waiting %ss for the 5s dedup window on CAM-001/person to clear\n' "$wait_seconds"
  sleep "$wait_seconds"
fi

# ---------------------------------------------------------------------------
# 3. ingest one high-confidence person detection for CAM-001
# ---------------------------------------------------------------------------
step "3/6 ingest detection"
utc_ts="$(python3 -c 'from datetime import datetime, timezone; print(datetime.now(timezone.utc).isoformat(timespec="milliseconds").replace("+00:00", "Z"))')"
payload="$(printf '{"camera_code":"CAM-001","ts":"%s","detections":[{"class":"person","confidence":0.987654,"bbox":[0.42,0.35,0.55,0.72],"world":%s}]}' "$utc_ts" "$world_json")"

ingest_code="$(curl -s -o "$TMP/ingest.json" -w '%{http_code}' -m 5 \
  -X POST -H 'Content-Type: application/json' --data "$payload" \
  "$API_BASE/api/ingest/detections" 2>/dev/null || true)"

if [ "$ingest_code" = "200" ] || [ "$ingest_code" = "202" ]; then
  pass "POST /api/ingest/detections -> HTTP $ingest_code"
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

event_id="$(python3 - "$TMP/ingest.json" <<'PY'
import json, sys
try:
    data = json.load(open(sys.argv[1]))
except Exception:
    print("")
    sys.exit(0)
if not isinstance(data, dict):
    print("")
    sys.exit(0)
events = data.get("events") or []
if events and isinstance(events[0], dict):
    print(events[0].get("id") or "")
else:
    print("")
PY
)"

# ---------------------------------------------------------------------------
# 4. poll GET /api/events?limit=20 until the event is visible (<= 5 s)
# ---------------------------------------------------------------------------
step "4/6 event appears in /api/events (<= 5s)"
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

# ---------------------------------------------------------------------------
# 5. ack the event
# ---------------------------------------------------------------------------
step "5/6 ack event"
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

# ---------------------------------------------------------------------------
# 6. /metrics exposes the required counters
# ---------------------------------------------------------------------------
step "6/6 metrics"
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

summary
if [ "$FAIL_COUNT" -eq 0 ]; then
  exit 0
fi
exit 1
