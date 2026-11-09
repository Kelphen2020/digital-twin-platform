# Simulator & verification tools (P0 + P1.0 + P2.0 + P3.0 + P4.0 + P5.0 + P6.0 + P7.0 + P8.0 + P10.0 + P11.0 + P12.0 + P13.0 + P14.0)

Tools that play the **AI inference service** for the digital-twin demo and
verify the detection → rule engine → event → WebSocket chain described in
[`docs/API.md`](../../docs/API.md) / [`docs/P0_SCOPE.md`](../../docs/P0_SCOPE.md),
the P1.0 additions in [`docs/API_P1.md`](../../docs/API_P1.md) (tracks,
calibration, alarm workflow, audit, stats/CSV export), the P2.0 additions in
[`docs/API_P2.md`](../../docs/API_P2.md) (JWT auth + RBAC, per-camera rules,
work orders), the P3.0 additions in
[`docs/API_P3.md`](../../docs/API_P3.md) (data-level filtering, escalation,
webhook notifications, settings), the P4.0 additions in
[`docs/API_P4.md`](../../docs/API_P4.md) (duty groups, auto-dispatch,
notification retry/silence, extra metrics) and the P5.0 additions in
[`docs/API_P5.md`](../../docs/API_P5.md) (replayable event bus: bus status,
replay, bus metrics), the P6.0 addition in
[`docs/API_P6.md`](../../docs/API_P6.md) (`GET /api/site`: site name, ENU
extent, glTF model manifest, map style for the 2.5D/3D scene) and the P7.0
addition in [`docs/API_P7.md`](../../docs/API_P7.md) (admin user management:
`/api/users` CRUD, roles/areas/active, self-protection) and the P8.0 addition
in [`docs/API_P8.md`](../../docs/API_P8.md) (duty shifts + operator rotation,
open-order dedupe, second escalation / `re_escalated_at`) and the P10.0
security hardening in [`docs/API_P10.md`](../../docs/API_P10.md) (security
headers, login brute-force lockout, password policy, audit CSV export) and the
P11.0 session lifecycle in [`docs/API_P11.md`](../../docs/API_P11.md) (refresh
token rotation, logout/logout-all, admin force-logout, password-change
invalidation, session listing), the P12.0 patrol routes in
[`docs/API_P12.md`](../../docs/API_P12.md) and the P13.0 patrol
schedules/executions in [`docs/API_P13.md`](../../docs/API_P13.md) (scheduled
executions, claim/complete lifecycle, issue → order link) and the P14.0 patrol
reporting/archive in [`docs/API_P14.md`](../../docs/API_P14.md) (stats,
execution CSV export, admin history purge). P9.0 performance tooling/numbers
live in [`docs/PERFORMANCE.md`](../../docs/PERFORMANCE.md).

All Python tools are **stdlib only** (no pip install) and target Python 3.9+
(verified on 3.14). `scripts/smoke.sh` needs only `bash`, `curl` and `python3`.

| File | Purpose |
|---|---|
| `simulator.py` | Continuous synthetic detection feed: loads cameras via REST, POSTs detections (weighted classes, P1 virtual targets with stable `track_id`) to `/api/ingest/detections` at a configurable rate, prints live stats. P2.0 auth tokens optional. |
| `run_camera_status.py` | Demo helper: sweeps cameras `offline` → `online` one at a time via `PATCH /api/cameras/{id}` so the WebSocket `camera_status` broadcast can be observed. |
| `../../scripts/smoke.sh` | End-to-end smoke test: 21 P0/P1.0 + 18 P2.0 + 21 P3.0 + 25 P4.0 + 9 P5.0 + 3 P6.0 + 16 P7.0 + 20 P8.0 + 13 P10.0 + 23 P11.0 + 18 P12.0 + 22 P13.0 + 21 P14.0 = **230 checks** (auth/RBAC, orders, camera rules, calibration, tracks, event workflow, audit, stats/CSV, data filtering, escalation, webhook delivery, settings, duty groups, auto-dispatch, notification retry/silence, P4 metrics, bus status/replay, site config, user management, duty shifts/rotation, re-escalation, security headers, login lockout, password policy, audit CSV export, refresh rotation, session revocation, patrol routes, patrol schedules/executions, patrol stats/export/purge). |
| `../../scripts/load.py` | Threaded load injector: steady or `--ramp` QPS, optional `--read-ratio` event reads, achieved QPS, accepted/dropped, HTTP p50/p95/p99, `/metrics` counter deltas, `--report` Markdown rows + `--json` run records (see [docs/PERFORMANCE.md](../../docs/PERFORMANCE.md)). |
| `../../scripts/webhook_receiver.py` | Tiny stdlib webhook receiver used by smoke (and manual demos) to verify P3.0/P4.0 notification delivery: records POSTs as JSONL, answers 200, `GET /health` readiness, `--fail-first N` returns 500 for the first N POSTs (retry testing). |

Prerequisite: the Go API must be running on `http://127.0.0.1:8080`
(`make dev-api`) with the seeded camera inventory (`CAM-001` … `CAM-006`).

### P2.0 auth quick start (`AUTH_MODE=on`)

The P2.0 backend runs with `AUTH_MODE=on` by default, so user-facing REST calls
need a Bearer token. Seeded demo users: `admin/Admin@123456` (all),
`operator/Operator@123` (write within `area_gate`/`area_parking`),
`viewer/Viewer@123` (read-only). The ingest entry point stays token-free unless
the server sets `INGEST_TOKEN` (then send `X-Ingest-Token`).

```bash
# one-time helper used by the explicit commands below
TOKEN=$(curl -s -X POST http://127.0.0.1:8080/api/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"Admin@123456"}' \
  | python3 -c 'import json,sys; print(json.load(sys.stdin)["token"])')
```

`simulator.py`, `load.py` and `run_camera_status.py` accept the token via
`--api-token` / `--token` or the `API_TOKEN` environment variable; `smoke.sh`
logs in by itself (admin + operator + viewer).

P3.0 data-level filtering: non-admin users with `areas` only see data for their
cameras (operator = `area_gate` + `area_parking` -> `CAM-001`, `CAM-005`,
`CAM-006`); out-of-area detail reads return `403 out_of_scope`, and
`GET /api/audit` + `GET /api/notifications` are admin-only (`403 forbidden`).
`smoke.sh` verifies all of this.

---

## 1. `simulator.py` — fake AI inference feed

```bash
# mixed scenario, 5 detections/s, 2-4 virtual targets per camera, run until Ctrl-C
# (with AUTH_MODE=on, pass a Bearer token for the camera inventory preload)
API_TOKEN="$TOKEN" python3 services/simulator/simulator.py

# fixed 30 s run, only CAM-001, 20 detections/s, 3 persistent tracks
API_TOKEN="$TOKEN" python3 services/simulator/simulator.py --camera CAM-001 --qps 20 --tracks 3 --seed 42

# print the generated payloads without POSTing them (shows track_id per detection)
API_TOKEN="$TOKEN" python3 services/simulator/simulator.py --dry-run --duration 2

# alarm-heavy scenario (intrusion/smoke/no_helmet)
API_TOKEN="$TOKEN" python3 services/simulator/simulator.py --scenario alarm

# pure P0 payloads: no virtual targets, no track_id
API_TOKEN="$TOKEN" python3 services/simulator/simulator.py --tracks 0

# only when the API sets INGEST_TOKEN: send the machine-entry header
API_TOKEN="$TOKEN" python3 services/simulator/simulator.py --ingest-token "$INGEST_TOKEN"
```

| Flag | Default | Description |
|---|---|---|
| `--api-base` | `http://127.0.0.1:8080` (or `$API_BASE`) | API base URL |
| `--qps` | `5` | Detection generation rate, **detections/second in total across cameras** (float allowed). Each POST batches 1–3 detections of one camera, so requests/s is lower than this. |
| `--duration` | `0` | Seconds to run; `0` = forever (stop with Ctrl-C) |
| `--camera` | all | Only report against this camera **code**, e.g. `CAM-001` |
| `--tracks` | random 2–4 per camera | Persistent P1 virtual targets per camera, each carrying a stable `track_id`; `0` disables tracks and restores P0 payloads |
| `--seed` | random | Seed for reproducible traffic |
| `--scenario` | `mixed` | `mixed` = person .50 / vehicle .25 / intrusion .15 / smoke .05 / no_helmet .05; `normal` = person/vehicle; `alarm` = intrusion/smoke/no_helmet; or a single class name (`person`, `vehicle`, `intrusion`, `smoke`, `no_helmet`) |
| `--dry-run` | off | Print ingest payloads to stdout instead of POSTing |
| `--api-token` | `$API_TOKEN` | Bearer token for `GET /api/cameras` when the API runs with `AUTH_MODE=on` |
| `--ingest-token` | `$INGEST_TOKEN` | Optional `X-Ingest-Token` header on ingest (only needed if the API sets `INGEST_TOKEN`) |

Behaviour details:

- On start it calls `GET /api/cameras` and retries **3 times** (with backoff);
  on failure it prints a clear error and exits `2`. With `AUTH_MODE=on`, a 401
  error message explicitly reminds you to pass `--api-token`/`API_TOKEN`.
- Confidence is drawn uniformly from `0.45–0.99`, so roughly a quarter of
  detections fall below the API's `0.6` threshold and are counted as `dropped`.
- **P1.0 virtual targets** (default): each camera owns `--tracks N` (2–4 by
  default) persistent targets. A target has a stable `track_id`, one weighted
  class (so the class mix over time still follows the scenario), and a
  walk/vehicle speed. It bounces along a lateral path across the camera view
  (ping-pong inside the frustum) for a random **30–60 s lifetime**, then
  respawns with a new track id. Every detection from a target carries its
  `track_id` plus a realistic bbox: the box shifts horizontally with the
  target's bearing off the camera axis, shrinks with distance (real-world
  object size / FOV) and its bottom edge follows the ground-contact projection.
  Detections also keep the `world` field, so the API's world-coordinate
  priority (`world` > calibration > 30 m fallback, API_P1 §2) is respected.
- `--tracks 0` restores the exact P0 payload shape (no `track_id`).
- A stats line is written to **stderr every 5 s** (final summary on exit):

  ```
  [stats] elapsed=   5.0s sent=25 posts=12 http_errors=0 accepted=9 dropped=16 events=9 rate=5.00det/s p50=1.42ms p95=2.03ms p99=3.11ms
  ```

  `sent` = detections generated, `posts` = ingest requests, `accepted`/`dropped`
  = the API's own `accepted`/`dropped` response fields (in the current Go
  implementation `accepted` is `len(events)`, while `dropped` counts detections
  rejected by the rules), `events` = events created, `p50/p95/p99` = ingest
  HTTP latency.
- HTTP 4xx/5xx and connection errors are reported (rate-limited) but do not
  crash the loop; after 50 consecutive failures it stops with exit code `3`.
- Ctrl-C / SIGTERM prints the final summary and exits `0`.

`stdout` is reserved for `--dry-run` payloads, so you can pipe them:

```bash
API_TOKEN="$TOKEN" python3 services/simulator/simulator.py --dry-run --duration 1 | head -2
```

Each dry-run payload contains detections like:

```json
{"class":"person","confidence":0.91,"bbox":[0.42,0.71,0.45,0.78],
 "track_id":"trk-00001","world":{"x":-52.7,"y":-43.9,"z":0.0}}
```

---

## 2. `run_camera_status.py` — status broadcast demo

Sweeps every camera `offline` one at a time, then `online` one at a time,
sleeping `--interval` seconds between PATCH calls. Each successful PATCH makes
the API broadcast a `camera_status` message to WebSocket clients. With
`AUTH_MODE=on` the PATCH needs an operator/admin Bearer token (`--token`).

```bash
# default: 10 s between changes, repeat forever (Ctrl-C to stop)
python3 services/simulator/run_camera_status.py --token "$TOKEN"

# one full cycle (all offline, then all online) and exit
python3 services/simulator/run_camera_status.py --once --interval 2 --token "$TOKEN"

# toggle a single camera (operator must own its area; CAM-001 is area_gate)
python3 services/simulator/run_camera_status.py --camera CAM-001 --interval 1.5 --token "$TOKEN"
```

| Flag | Default | Description |
|---|---|---|
| `--api-base` | `http://127.0.0.1:8080` (or `$API_BASE`) | API base URL |
| `--interval` | `10` | Seconds between two status changes |
| `--once` | off | Run one offline+online cycle, then exit |
| `--camera` | all | Only toggle this camera code |
| `--token` | `$API_TOKEN` | Operator/admin Bearer token for the PATCH calls under `AUTH_MODE=on` |

Output example:

```
[21:30:02] CAM-001 (cam_001): online -> offline
[21:30:12] CAM-002 (cam_002): online -> offline
...
[21:31:12] CAM-001 (cam_001): online -> online
```

Exit codes: `0` normal/Ctrl-C, `1` every PATCH in the `--once` cycle failed,
`2` camera inventory unavailable or bad arguments.

---

## 3. `scripts/smoke.sh` — end-to-end acceptance check

```bash
./scripts/smoke.sh
# or against another host/port:
API_BASE=http://127.0.0.1:8080 ./scripts/smoke.sh
# only if the API sets INGEST_TOKEN:
INGEST_TOKEN=secret ./scripts/smoke.sh
```

The backend is expected in **`AUTH_MODE=on`** with the seeded users; the script
logs in itself as admin, operator and viewer (tokens are never printed).
Checks, printing `PASS`/`FAIL` per step and exiting non-zero if anything fails
(**230 checks**: 21 P0/P1.0 + 18 P2.0 + 21 P3.0 + 25 P4.0 + 9 P5.0 + 3 P6.0 + 16 P7.0 + 20 P8.0 + 13 P10.0 + 23 P11.0 + 18 P12.0 + 22 P13.0 + 21 P14.0):

**P0 chain + P1.0 (steps 1, 3–14, 21 checks)**

1. `GET /api/health` returns `status=ok`;
2. `GET /api/cameras` has ≥ 1 camera and the six seeded codes `CAM-001…CAM-006`;
3. high-confidence (`0.987654`) person detection for `CAM-001` is accepted
   **without a user token** (`POST /api/ingest/detections` → `202`);
4. the resulting event is visible in `GET /api/events?limit=20` within 5 s;
5. `POST /api/events/{id}/ack` returns `status=acked`;
6. `GET /metrics` contains `dsh_twin_events_total` and `dsh_twin_ws_clients`;
7. calibration: solve → PUT → `GET /api/cameras/{id}` shows the saved calibration;
8. tracks: ingest with `track_id` → `GET /api/tracks?camera_id=…` returns it;
9–11. workflow: create event → detail `actions=[]` → dispatch → close →
    detail lists `dispatch,close`;
12. `GET /api/audit` non-empty with calibration/workflow actions;
13. `GET /api/stats/summary` totals > 0; `GET /api/stats/events.csv` header.

**P2.0 (steps 2, 15–18, 18 checks)**

14. auth: no token → `401 unauthorized`; admin login returns a token and
    `GET /api/auth/me` returns `username=admin role=admin`;
15. RBAC: viewer `GET /api/cameras` → 200; viewer ack → `403 forbidden`;
    operator ack a fresh CAM-001 (area_gate) event → 200 `status=acked`;
16. orders: operator `POST /api/orders` → `201 status=open`; `PATCH` →
    `processing` (assignee); `PATCH` → `done` with `closed_at`; `GET` order;
17. camera rules (admin): `PUT /api/cameras/cam_001/rules` with
    `confidence_threshold=0.99` → ingress of a `0.95` detection yields
    **0 events**; default rules (`0.6`, full-frame) are restored and verified
    through `GET …/rules`;
18. operator camera scope: `PATCH cam_002` (outside `area_gate`/`area_parking`)
    → `403 out_of_scope`; `PATCH cam_001` (in scope) → 200; `GET /api/audit`
    contains `order.create`, `order.update` and `camera.rules.put` with
    `actor=operator` (not `web`).

**P3.0 (steps 19–23, 21 checks)**

19. data filtering: operator `GET /api/cameras` returns exactly
    `CAM-001,CAM-005,CAM-006`; a fresh CAM-002 (out-of-area) event is readable
    by admin but absent from the operator's event list and its detail returns
    `403 out_of_scope`; operator `GET /api/audit` and `GET /api/notifications`
    return `403 forbidden`;
20. escalation settings: admin `PUT /api/settings/escalation`
    `{enabled:true, after_seconds:1, severities:[warn,critical]}` → 200 and
    `GET` reflects it (defaults reset at the start of the run);
21. escalation effect: a warn event injected with `ts=now-10s` on CAM-005
    (`no_helmet`) receives `escalated_at` within 25 s (observed ~3 s) and the
    event list shows it;
22. notifications + webhook: local `scripts/webhook_receiver.py` listens on a
    free port; invalid `webhook_url` → 400; valid enabled settings (generic
    format, `min_severity=warn`) are saved and read back; a new warn event on
    CAM-006 is delivered → `GET /api/notifications` shows `ok=true,
    status_code=200`, and the receiver JSONL log contains the event id;
23. cleanup: escalation defaults restored (`after_seconds=300`), notifications
    disabled with empty URL, receiver stopped cleanly (also on Ctrl-C/failure
    via the EXIT trap).

**P4.0 (steps 24–28, 25 checks)**

24. duty groups: admin POST (areas `area_gate`+`area_parking`, operator) → 201,
    `GET /api/duty/groups` contains it, `GET /api/duty/current?area_id=area_gate`
    resolves a valid active group with its operators, PATCH `active` toggle →
    200, operator GET → 200 (logged-in read) but operator POST → `403`,
    DELETE → 204, then the group is re-created for the auto-dispatch test;
25. auto-dispatch: escalation settings `{after_seconds:1, auto_dispatch:true}`
    → 200; a stale warn event (`ts=now-10s`, CAM-001/area_gate) is injected and
    an order appears (≤30 s, observed ~3–16 s) whose `assignee` is a duty group
    name and whose `note` contains `自动派单`; audit contains
    `order.auto_dispatch` (`actor=system`); escalation defaults restored;
26. notification retry: receiver started with `--fail-first 2`; notifications
    `max_attempts=3, backoff_seconds=1` saved; a warn event is delivered with
    `ok=true, attempts=3, status_code=200` and the receiver JSONL log shows
    exactly 3 POSTs with statuses `[500, 500, 200]`;
27. notification silence: `silence_seconds=60` saved; two identical events
    (camera+type+severity, dedupe disabled for the injection) produce exactly
    1 delivery and `dsh_twin_notifications_suppressed_total > 0`;
28. metrics + cleanup: `/metrics` contains the P4 counters
    (`dsh_twin_notifications_total{result=ok|failed}`,
    `dsh_twin_escalations_total`, `dsh_twin_orders_total`,
    `dsh_twin_duty_dispatches_total > 0`, `dsh_twin_duty_skipped_total`,
    `dsh_twin_notifications_suppressed_total`), the duty group is deleted
    (204), notifications disabled and escalation defaults
    (`after_seconds=300, auto_dispatch=true`) verified.

**P5.0 (steps 29–33, 9 checks)**

29. bus status: admin `GET /api/bus/status` → 200 with
    `driver/published/next_offset/subscribers/oldest_offset/last_publish_at`
    (a `path` is present for the `jsonl` driver); non-admin `GET` → `403`;
30. bus publish: a token-free ingest (CAM-002/vehicle) advances
    `published`/`next_offset` (polled via `/api/bus/status`);
31. bus replay: admin `POST /api/bus/replay {from_offset: N, to_offset: N+1,
    notify: false}` for the freshly published offset → `200 ok=true
    replayed>=1`, and the status `replayed` counter increases;
32. replay validation: invalid range (`from_offset > to_offset`) → `400
    bad_request`; non-admin replay → `403 forbidden`;
33. bus metrics: `/metrics` exposes `dsh_twin_bus_published_total` and
    `dsh_twin_bus_replayed_total` (plus `dsh_twin_bus_errors_total` /
    `dsh_twin_bus_lag`).

**P6.0 (step 34, 3 checks)**

34. site config: admin `GET /api/site` → 200 with a non-empty `name`, numeric
    ENU `extent` (`min_x/max_x/min_y/max_y`), ≥1 model entry carrying
    `id`/`format` (`gltf`|`glb`)/`url`, and `map.style`; viewer `GET
    /api/site` → 200 (authenticated read); unauthenticated `GET /api/site`
    → `401 unauthorized`.

**P7.0 (step 35, 16 checks)**

35. user management: admin `GET /api/users` → 200 with the ≥3 seeded users
    (admin/operator/viewer) and **no password fields anywhere**; operator
    `GET /api/users` → `403 forbidden`; admin `POST /api/users` → 201 (unique
    `smoke_user_<ts>`, viewer); duplicate username → `409 username_taken`;
    password < 8 chars → 400; login as the new user → 200 (role viewer);
    `PATCH` role→operator + `areas:[area_gate]` → 200, re-login shows
    `role=operator` via `/api/auth/me` and `GET /api/cameras` is area-scoped
    (exactly the `area_gate` cameras); `PATCH active=false` → 200 then login →
    `401 invalid_credentials`; `DELETE` → 204 then login → 401; self `DELETE`
    and self `PATCH active=false` → `400 self_protected`; audit contains
    `user.create`/`user.update`/`user.delete` with `actor=admin`. Cleanup
    deletes the temporary user (also via the EXIT trap).

**P8.0 (steps 36–39, 20 checks)**

36. duty shifts: admin creates a duty group for `area_gate` with
    `rotation=operator` and one all-week `00:00–23:59` shift with operators
    `[operator, viewer]` → 201; `GET /api/duty/current?area_id=area_gate`
    returns the group, `shift.name=全天` and 2 operators; operator POST group
    → 403;
37. rotation: escalation `{after_seconds:1, auto_dispatch:true,
    second_after_seconds:0}` (after sweeping pending events with
    auto_dispatch off so only the two injected events dispatch); a stale warn
    event on CAM-001 produces an order whose `assignee` is a shift operator
    (rotation, not the group name); a second stale warn event on CAM-006 gets
    the *other* operator as assignee; order notes include
    `系统自动派单（告警升级） · <组> · <班次>`;
38. open-order dedupe + re-escalation: `second_after_seconds=1` re-escalates
    the first event (`re_escalated_at`, audit `event.re_escalate`,
    `dsh_twin_re_escalations_total>0`), the re-escalation does **not** create a
    second order (open-order dedupe; audit `order.auto_dispatch_skipped`
    reason=`open_order`, `dsh_twin_orders_deduped_total>0`);
39. validation + cleanup: `second_after_seconds=-1` and `604801` → 400;
    escalation defaults restored (`300 s`, `auto_dispatch=true`,
    `second_after_seconds=0`), notifications disabled, test duty group deleted
    (204) and no leftover `P8值班组`/`smoke_*` test data (groups temporarily
    deactivated for determinism are re-activated).

**P10.0 (steps 40–42, 13 checks)**

40. security headers: `GET /api/health` and authenticated `GET /api/cameras`
    include `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY` and
    `Referrer-Policy: no-referrer`;
41. login lockout: 6 wrong-password logins for a throwaway `lock_<ts>`
    username → the 5th failure locks the account, so the 6th returns
    `429 too_many_attempts` with a `Retry-After: 60` header (observed codes
    `401 401 401 401 401 429`), while a normal admin login still succeeds (no
    global lock); password policy: 9-char password → 400, 16 letters without a
    digit → 400, valid 10+ letter+digit password → 201 (test user deleted);
42. audit CSV export: admin `GET /api/audit/export` → 200
    `text/csv; charset=utf-8` with `Content-Disposition: attachment;
    filename="audit.csv"` and header `id,at,actor,action,resource,detail`;
    operator → 403 forbidden; cleanup deletes the test user and verifies admin
    can still log in.

**P11.0 (steps 43–46, 23 checks)**

43. refresh rotation: admin login → 200 with non-empty `refresh_token` +
    `refresh_expires_at` (access works); `POST /api/auth/refresh` → 200 with a
    new access token and a rotated refresh token; reusing the old refresh token
    → 401 `invalid_refresh_token`; the rotated access token works;
44. logout + logout-all: `POST /api/auth/logout` with a refresh token → 204 and
    that refresh token no longer refreshes (401); a throwaway viewer logs in
    (200 with refresh), `POST /api/auth/logout-all` → 204, its old access token
    becomes 401 (token_version bump) and re-login works (200);
45. password-change invalidation + force logout: admin `PATCH` the viewer's
    password → 200, the viewer's pre-change access token → 401, login with the
    new password → 200; `GET /api/users/{id}/sessions` shows ≥1 active session,
    `POST /api/users/{id}/logout` (force logout) → 204, the target's access
    token → 401 and sessions drop to 0; self force-logout → 400
    `self_protected`; operator force-logout → 403;
46. cleanup: throwaway viewer deleted (204) and admin login still works.

**P12.0 (steps 47–49, 18 checks)** and **P13.0 (steps 50–52, 22 checks)**

P12: admin/operator `GET /api/patrol/routes` (200 items) and viewer read-only
(200) with viewer POST → 403; operator creates a route (2 waypoints,
`camera_ids:[cam_001,cam_006]`, interval 2) → 201 + list contains + PATCH →
200; validation (1 waypoint / 51 waypoints / unknown camera
`400 unknown_camera` / interval 0) → 400; operator deletes own route → 204,
admin deletes an operator-created route → 204; audit has
`patrol.create`/`patrol.update`/`patrol.delete`.

P13: an operator-created patrol route backs a schedule: POST
`/api/patrol/schedules` (`interval_minutes:60`, `enabled:true`) → 201 with
`next_run_at`, list contains it, PATCH name/interval → 200; viewer POST → 403 /
viewer GET → 200; validation (missing/unknown `route_id`, interval 0 or 10081)
→ 400; `POST …/run` → 201 execution `pending` listed by
`GET /api/patrol/executions`; `claim` → 200 `running` with
`assignee=operator`; `complete {result:"done"}` → 200 `done` with
`finished_at`; issue flow (`run` → `claim` → `complete {result:"issue"}`) →
200 `issue` with `order_id`, and the linked order has title
`巡检异常 · <route>`, `assignee=operator`, `status=open`; viewer claim/complete
→ 403; admin deletes the operator's schedule → 204; audit has
`patrol.schedule.create`/`patrol.schedule.run` +
`patrol.execution.claim`/`patrol.execution.complete`; cleanup deletes the test
route and leaves no `smoke 计划 P13` schedules (executions are demo records
without a delete endpoint).

**P14.0 (steps 53–55, 21 checks)**

53. stats: operator-created route + schedule + one `done` and one `issue`
    execution, then admin `GET /api/patrol/stats?from=&to=` → 200 with
    `executions_total`/`done`/`issue`/`pending`/`running` counts,
    `completion_rate`/`issue_rate` consistent with the counts within 0.002,
    `avg_duration_seconds >= 0`, `by_route` containing the test route and
    `by_assignee` containing `operator`; malformed `from`/`to` → 400;
54. export: operator `GET /api/patrol/executions/export` → 200
    `text/csv; charset=utf-8` with `Content-Disposition: attachment;
    filename="patrol-executions.csv"` and the exact header
    `id,schedule_id,route_name,status,assignee,result,note,scheduled_at,started_at,finished_at,order_id`;
    viewer → 403;
55. purge: viewer/operator `DELETE /api/patrol/executions` → 403; admin
    `DELETE …?before=<future>&status=done` → 200 `{deleted:N>=1}`, stats `done`
    decreases while the `issue` execution remains, `status=pending` → 400,
    audit has `patrol.executions.purge` with `deleted` count; final purge of
    `done,issue` removes the smoke executions; route/schedule deleted;
    admin login still works.

The script waits for the API's 5 s `(camera, class)` dedup window to clear
before posting (re-checked after each wait, ingest retried up to 3×), so it is
reliable even while `simulator.py` is running. P3.0 settings are reset to
defaults at the start of the run and restored again on exit (even on failure),
so repeated runs are idempotent. All temporary files live in a `mktemp -d`
directory and are removed on exit. A full run takes ~40–60 s (the escalation
worker can take up to ~15 s to escalate).

---

## 4. `scripts/load.py` — concurrent load test

```bash
# with AUTH_MODE=on, pass a token for the camera inventory preflight
API_TOKEN="$TOKEN" python3 scripts/load.py --qps 50 --duration 5

# default: 100 QPS for 5 s with 10 threads, 50% of detections carrying track_id
API_TOKEN="$TOKEN" python3 scripts/load.py

# all detections tracked, reproducible payloads, P0-only ingest path
API_TOKEN="$TOKEN" python3 scripts/load.py --qps 50 --duration 5 --track-ratio 1 --seed 7
API_TOKEN="$TOKEN" python3 scripts/load.py --qps 50 --duration 5 --track-ratio 0

# only when the API sets INGEST_TOKEN
API_TOKEN="$TOKEN" python3 scripts/load.py --qps 50 --duration 5 --ingest-token "$INGEST_TOKEN"

# P9.0: linear ramp 25% -> 200 QPS over 10 s, Markdown + JSON artifacts
API_TOKEN="$TOKEN" python3 scripts/load.py --qps 200 --duration 10 --ramp \
  --report /tmp/perf_matrix.md --json /tmp/perf_runs.json

# P9.0: 30% of requests are GET /api/events reads instead of ingest
API_TOKEN="$TOKEN" python3 scripts/load.py --qps 100 --duration 5 --read-ratio 0.3
```

| Flag | Default | Description |
|---|---|---|
| `--api-base` | `http://127.0.0.1:8080` (or `$API_BASE`) | API base URL |
| `--qps` | `100` | Target requests/second (1 detection per request) |
| `--duration` | `5` | Injection window, seconds |
| `--workers` | `10` | Injector thread count |
| `--track-ratio` | `0.5` | Fraction of detections carrying a `track_id` (drawn from 3 fixed virtual tracks per camera) so the P1 track-point path is exercised; `0` = P0-only |
| `--timeout` | `5` | Per-request HTTP timeout, seconds |
| `--seed` | random | Seed for reproducible payloads |
| `--ramp` | off | Linear QPS ramp from 25% of `--qps` to 100% over `--duration`; average rate = 62.5% of target (threshold uses the average) |
| `--read-ratio` | `0` | Fraction (0–1) of requests that are `GET /api/events?limit=20` reads instead of ingest POSTs (reads need `--api-token` under `AUTH_MODE=on`) |
| `--report` | — | Append one Markdown table row per run to this path (sharing the path builds the benchmark matrix) |
| `--json` | — | Append this run's full stats + `/metrics` deltas as a JSON object to a list at this path |
| `--api-token` | `$API_TOKEN` | Bearer token for the camera/read preflight under `AUTH_MODE=on` |
| `--ingest-token` | `$INGEST_TOKEN` | Optional `X-Ingest-Token` header on ingest (only if the API sets `INGEST_TOKEN`) |

It preloads camera codes from `GET /api/cameras`, paces requests on a shared
schedule (so threads do not drift), and prints a report like:

```
=== load test report ===
target            : 50 QPS for 5s (250 requests, 10 workers)
mode              : steady
wall elapsed      : 4.98s
attempted         : 250 (50.00 req/s)  ok=250 failed=0 (100.00% ok)
achieved QPS      : 50.00 (avg target 50.00, threshold 40.00)
accepted/dropped  : 25 / 225   events=25
tracked           : 126/250 ingest detections with track_id (--track-ratio 0.5)
read requests     : 0/0 ok (--read-ratio 0)          # only with --read-ratio > 0
http latency (ms) : p50=0.57 p95=0.77 p99=1.10 mean=0.60 max=1.60
metrics delta     : ws_frames=+19 ws_dropped=+0 ingest_rejected=+0 bus_published=+25 track_points_written=+126 ingest_count=+250 ingest_sum=+0.012s
RESULT            : PASS
```

`--ramp` runs report the peak `--qps` on the target line and the average target
on the achieved line; `--report`/`--json` additionally print the paths they
appended to.

See **[docs/PERFORMANCE.md](../../docs/PERFORMANCE.md)** for the P9.0 benchmark
matrix (50/100/200 QPS, read-mix, ramp), `/metrics` snapshots, the 429
`INGEST_RATE_LIMIT` verification and tuning recommendations.

Exit codes:

- `0` — achieved QPS ≥ 80 % of target (average rate for `--ramp`) **and** zero
  failed requests;
- `1` — achieved QPS below that threshold, or any request failed. This is also
  the expected result for the deliberate 429 rate-limit test (see
  `docs/PERFORMANCE.md` §6: failures > 0 with `HTTP 429 rate_limited`);
- `2` — bad arguments or camera inventory/read preflight unavailable (e.g.
  missing token with `AUTH_MODE=on`; the error message points at
  `--api-token`/`API_TOKEN`, and a read preflight is done when
  `--read-ratio > 0`).

Compare the `accepted`/`events` numbers with `GET /metrics`
(`dsh_twin_detections_total`, `dsh_twin_events_total`) as required by
`docs/P0_SCOPE.md` §4.7; tracked detections additionally show up in
`GET /api/tracks` (dedup does not drop track points, only events).
P9.0 performance numbers, the benchmark matrix and the 429 rate-limit
verification live in [docs/PERFORMANCE.md](../../docs/PERFORMANCE.md).

---

## 5. P3.0 escalation & webhook notifications demo

Escalation settings (admin only; defaults `enabled=true, after_seconds=300,
severities=[warn, critical]`) and notification settings (defaults
`enabled=false`) are simple REST resources:

```bash
ADMIN="$TOKEN"

# 1) inspect / tighten escalation so a stale warning escalates quickly
curl -s -H "Authorization: Bearer $ADMIN" http://127.0.0.1:8080/api/settings/escalation
curl -s -X PUT -H "Authorization: Bearer $ADMIN" -H 'Content-Type: application/json' \
  -d '{"enabled":true,"after_seconds":1,"severities":["warn","critical"]}' \
  http://127.0.0.1:8080/api/settings/escalation

# 2) start the local webhook receiver (records POSTs as JSONL, answers 200)
python3 scripts/webhook_receiver.py --port 8099 --out /tmp/webhook.log &
# -> webhook_receiver ready: http://127.0.0.1:8099 (out=/tmp/webhook.log, received=0)
curl -s http://127.0.0.1:8099/health

# 3) point notifications at it and enable delivery for warn+ events
curl -s -X PUT -H "Authorization: Bearer $ADMIN" -H 'Content-Type: application/json' \
  -d '{"enabled":true,"webhook_url":"http://127.0.0.1:8099/hook","format":"generic","min_severity":"warn","timeout_seconds":5}' \
  http://127.0.0.1:8080/api/settings/notifications

# 4) trigger a warn event (token-free ingest) and inspect delivery
curl -s -X POST -H 'Content-Type: application/json' \
  -d '{"camera_code":"CAM-005","detections":[{"class":"no_helmet","confidence":0.93}]}' \
  http://127.0.0.1:8080/api/ingest/detections
sleep 2
curl -s -H "Authorization: Bearer $ADMIN" 'http://127.0.0.1:8080/api/notifications?limit=10'
tail -n 1 /tmp/webhook.log

# 5) restore defaults when done
curl -s -X PUT -H "Authorization: Bearer $ADMIN" -H 'Content-Type: application/json' \
  -d '{"enabled":false,"webhook_url":"","format":"generic","min_severity":"warn","timeout_seconds":5}' \
  http://127.0.0.1:8080/api/settings/notifications
curl -s -X PUT -H "Authorization: Bearer $ADMIN" -H 'Content-Type: application/json' \
  -d '{"enabled":true,"after_seconds":300,"severities":["warn","critical"]}' \
  http://127.0.0.1:8080/api/settings/escalation
```

`webhook_receiver.py` flags: `--port` (default `8099`), `--host` (default
`127.0.0.1`), `--out` (default `/tmp/webhook.log`) and `--fail-first N`
(answer the first N POSTs with HTTP 500, still logging them). Each request
becomes one JSON line with `at`, `method`, `path`, `status`, `headers` and the
parsed `body`; the server logs one line per delivery to stderr and shuts down
cleanly on Ctrl-C. Set `format=feishu` to receive Feishu custom-bot payloads
instead of the raw event JSON.

### P4.0 duty / auto-dispatch / reliability demo

```bash
ADMIN="$TOKEN"

# 1) create a duty group for the gate + parking areas
curl -s -X POST -H "Authorization: Bearer $ADMIN" -H 'Content-Type: application/json' \
  -d '{"name":"东门值班组","areas":["area_gate","area_parking"],"operators":["operator"],"active":true}' \
  http://127.0.0.1:8080/api/duty/groups
curl -s -H "Authorization: Bearer $ADMIN" 'http://127.0.0.1:8080/api/duty/current?area_id=area_gate'

# 2) enable fast escalation with auto-dispatch, then create a stale warn event
#    (ts 10 s in the past) — the escalation worker auto-creates an urgent order
#    assigned to the duty group with note 系统自动派单（告警升级）
curl -s -X PUT -H "Authorization: Bearer $ADMIN" -H 'Content-Type: application/json' \
  -d '{"enabled":true,"after_seconds":1,"severities":["warn","critical"],"auto_dispatch":true}' \
  http://127.0.0.1:8080/api/settings/escalation
STALE=$(python3 -c 'from datetime import datetime, timezone, timedelta; print((datetime.now(timezone.utc)-timedelta(seconds=10)).isoformat(timespec="milliseconds").replace("+00:00","Z"))')
curl -s -X POST -H 'Content-Type: application/json' \
  -d "{\"camera_code\":\"CAM-001\",\"ts\":\"$STALE\",\"detections\":[{\"class\":\"intrusion\",\"confidence\":0.93}]}" \
  http://127.0.0.1:8080/api/ingest/detections
curl -s -H "Authorization: Bearer $ADMIN" 'http://127.0.0.1:8080/api/orders?limit=10'

# 3) notification retry + silence: start the receiver so the first 2 POSTs fail
python3 scripts/webhook_receiver.py --port 8099 --out /tmp/webhook.log --fail-first 2 &
curl -s -X PUT -H "Authorization: Bearer $ADMIN" -H 'Content-Type: application/json' \
  -d '{"enabled":true,"webhook_url":"http://127.0.0.1:8099/hook","format":"generic","min_severity":"warn","timeout_seconds":5,"max_attempts":3,"backoff_seconds":1,"silence_seconds":60}' \
  http://127.0.0.1:8080/api/settings/notifications
# trigger a warn event; after ~3 s /api/notifications shows ok=true attempts=3,
# and /tmp/webhook.log contains 3 POST lines (500, 500, 200). Repeat the same
# camera/type/severity event within 60 s -> suppressed (metric
# dsh_twin_notifications_suppressed_total), only one delivery recorded.

# 4) restore defaults and delete the demo duty group
curl -s -X PUT -H "Authorization: Bearer $ADMIN" -H 'Content-Type: application/json' \
  -d '{"enabled":false,"webhook_url":"","format":"generic","min_severity":"warn","timeout_seconds":5,"max_attempts":3,"backoff_seconds":2,"silence_seconds":30}' \
  http://127.0.0.1:8080/api/settings/notifications
curl -s -X PUT -H "Authorization: Bearer $ADMIN" -H 'Content-Type: application/json' \
  -d '{"enabled":true,"after_seconds":300,"severities":["warn","critical"],"auto_dispatch":true}' \
  http://127.0.0.1:8080/api/settings/escalation
```

P4.0 metrics to watch in `GET /metrics`:

```
dsh_twin_notifications_total{result="ok"} 3
dsh_twin_notifications_total{result="failed"} 2
dsh_twin_notifications_suppressed_total 1
dsh_twin_escalations_total 2
dsh_twin_orders_total 4
dsh_twin_duty_dispatches_total 1
dsh_twin_duty_skipped_total 1
```

---

## 6. P5.0 event bus status & replay demo

The P5 backend publishes every new event (and escalation) to a replayable bus
(`BUS_DRIVER=memory|jsonl`; `memory` keeps an in-process ring buffer, `jsonl`
appends to `data/bus/events.jsonl` and survives restarts). The admin endpoints
expose the bus state and allow replaying an offset range:

```bash
ADMIN="$TOKEN"

# 1) inspect the bus state (driver, published/replayed counters, offsets)
curl -s -H "Authorization: Bearer $ADMIN" http://127.0.0.1:8080/api/bus/status

# 2) publish a new event (token-free machine entry), then note the new offsets
N=$(curl -s -H "Authorization: Bearer $ADMIN" http://127.0.0.1:8080/api/bus/status \
  | python3 -c 'import json,sys; print(json.load(sys.stdin)["next_offset"])')
curl -s -X POST -H 'Content-Type: application/json' \
  -d '{"camera_code":"CAM-002","detections":[{"class":"vehicle","confidence":0.91}]}' \
  http://127.0.0.1:8080/api/ingest/detections

# 3) replay the freshly published offset (notify=false -> no webhook spam);
#    WS clients receive "event_replay" frames with replay:true + offset
curl -s -X POST -H "Authorization: Bearer $ADMIN" -H 'Content-Type: application/json' \
  -d "{\"from_offset\": $N, \"to_offset\": $((N+1)), \"notify\": false, \"limit\": 10}" \
  http://127.0.0.1:8080/api/bus/replay
# -> {"ok":true,"replayed":1,"from_offset":N,"to_offset":N}

# 4) invalid range -> 400 bad_request; replay is admin-only (403 for others)
curl -s -X POST -H "Authorization: Bearer $ADMIN" -H 'Content-Type: application/json' \
  -d '{"from_offset": 100, "to_offset": 10}' http://127.0.0.1:8080/api/bus/replay
```

Bus metrics to watch in `GET /metrics`:

```
dsh_twin_bus_published_total 1280
dsh_twin_bus_replayed_total 12
dsh_twin_bus_errors_total 0
dsh_twin_bus_lag 0
```

> `notify: false` is the default-safe mode: replay pushes to WebSocket clients
> but does not re-trigger webhook notifications. Set `notify: true` only for
> deliberate re-notification demos.

---

## 7. P6.0 site config (`GET /api/site`)

The P6 backend exposes the site/model manifest consumed by the 2.5D base map,
building LOD, glTF loader and video-fusion plane in the web client:

```bash
curl -s -H "Authorization: Bearer $TOKEN" http://127.0.0.1:8080/api/site
```

```json
{
  "name": "示范园区",
  "origin": {"lat": 30.2741, "lng": 120.1551, "alt": 0},
  "extent": {"min_x": -200, "max_x": 200, "min_y": -200, "max_y": 200, "min_z": 0, "max_z": 60},
  "models": [{"id": "site-overview", "name": "园区总览模型", "format": "gltf",
              "url": "/models/site.gltf",
              "transform": {"x": 0, "y": 0, "z": 0, "rotation_y": 0, "scale": 1}}],
  "map": {"style": "vector", "image_url": null}
}
```

- Authenticated (viewer/operator/admin) read; no token → `401`.
- `extent` is the local ENU metre range used to size the ground grid and the
  procedural 2.5D base map.
- `models[].url` is a static front-end asset path (`public/models/...`) or an
  absolute URL; `format` is `gltf` or `glb`.
- `map.style = "vector"` → procedural canvas base map; `map.image_url` can
  carry an orthophoto when present.
- Override the built-in defaults with `SITE_CONFIG_FILE=/path/site.json` on the
  API process (missing fields fall back to defaults).
- `smoke.sh` validates this payload shape plus the viewer/no-token permission
  behaviour (step 34).

## 8. P7.0 user management (`/api/users`)

Admins maintain the account store (the P2 seed users `admin`/`operator`/
`viewer` are auto-created when the user table is empty; passwords are never
returned; `active=false` users cannot log in):

```bash
ADMIN="$TOKEN"

# list users
curl -s -H "Authorization: Bearer $ADMIN" http://127.0.0.1:8080/api/users

# create a viewer account
curl -s -X POST -H "Authorization: Bearer $ADMIN" -H 'Content-Type: application/json' \
  -d '{"username":"operator2","password":"Passw0rd!","display_name":"值班员二",
       "role":"viewer","areas":[],"active":true}' http://127.0.0.1:8080/api/users

# promote + assign an area (takes effect on the next login: NEW JWT)
curl -s -X PATCH -H "Authorization: Bearer $ADMIN" -H 'Content-Type: application/json' \
  -d '{"role":"operator","areas":["area_gate"],"active":true}' \
  http://127.0.0.1:8080/api/users/<id>

# disable (login -> 401 invalid_credentials), re-enable, or delete
curl -s -X PATCH -H "Authorization: Bearer $ADMIN" -H 'Content-Type: application/json' \
  -d '{"active":false}' http://127.0.0.1:8080/api/users/<id>
curl -s -X DELETE -H "Authorization: Bearer $ADMIN" \
  http://127.0.0.1:8080/api/users/<id>

# self-protection: an admin cannot disable or delete their own account
# -> 400 self_protected
```

Notes:

- All `/api/users` endpoints are admin-only (`403 forbidden` for other roles).
- Role/areas/active changes are picked up on the next login (the JWT carries
  the user snapshot; stale tokens keep their old role until expiry).
- New operator `areas` are enforced by P3 data-level filtering, e.g.
  `GET /api/cameras` returns exactly the cameras in those areas.
- Audit entries: `user.create`, `user.update`, `user.delete` (`actor=<admin>`).

## 9. P8.0 duty shifts, rotation, dedupe & re-escalation

P8 completes the "on-call → dispatch → escalate" loop: duty groups can carry
shifts, `rotation=operator` dispatches to a shift member instead of the group
name, repeated auto-dispatch to an event with an open order is deduped, and an
escalated-but-unclosed event can be re-escalated after a second timeout.

```bash
ADMIN="$TOKEN"

# 1) duty group with an all-day shift and operator rotation (area_gate)
curl -s -X POST -H "Authorization: Bearer $ADMIN" -H 'Content-Type: application/json' \
  -d '{"name":"东门值班组","areas":["area_gate"],"operators":["operator","viewer"],
       "active":true,"rotation":"operator",
       "shifts":[{"name":"全天","start":"00:00","end":"23:59","weekdays":[1,2,3,4,5,6,7],
                  "operators":["operator","viewer"]}]}' \
  http://127.0.0.1:8080/api/duty/groups
curl -s -H "Authorization: Bearer $ADMIN" 'http://127.0.0.1:8080/api/duty/current?area_id=area_gate'
# -> { "group": {...}, "shift": {"name":"全天","start":"00:00","end":"23:59"},
#      "operators": ["operator","viewer"] }

# 2) fast escalation with operator rotation; stale warn events get dispatched
#    to operator → viewer → operator ... (per group+shift rotation counter)
curl -s -X PUT -H "Authorization: Bearer $ADMIN" -H 'Content-Type: application/json' \
  -d '{"enabled":true,"after_seconds":1,"severities":["warn","critical"],
       "auto_dispatch":true,"second_after_seconds":0}' \
  http://127.0.0.1:8080/api/settings/escalation
# (create backdated warn events as in the P4 demo; orders get
#  note "系统自动派单（告警升级） · <组> · <班次>" and assignee=operator/viewer)

# 3) second escalation (re_escalated_at) after a further timeout, while the
#    event's open order is deduped instead of creating another order
curl -s -X PUT -H "Authorization: Bearer $ADMIN" -H 'Content-Type: application/json' \
  -d '{"enabled":true,"after_seconds":1,"severities":["warn","critical"],
       "auto_dispatch":true,"second_after_seconds":1}' \
  http://127.0.0.1:8080/api/settings/escalation
curl -s -H "Authorization: Bearer $ADMIN" http://127.0.0.1:8080/api/events/<event_id>
# -> escalated_at + re_escalated_at; audit: event.re_escalate,
#    order.auto_dispatch_skipped {reason:"open_order"}
curl -s http://127.0.0.1:8080/metrics | grep -E 're_escalations|orders_deduped'

# 4) restore defaults when done
curl -s -X PUT -H "Authorization: Bearer $ADMIN" -H 'Content-Type: application/json' \
  -d '{"enabled":true,"after_seconds":300,"severities":["warn","critical"],
       "auto_dispatch":true,"second_after_seconds":0}' \
  http://127.0.0.1:8080/api/settings/escalation
```

P8 metrics: `dsh_twin_re_escalations_total`,
`dsh_twin_orders_deduped_total`. Validation: `second_after_seconds` must be
within `[0, 604800]` (0 = off), otherwise `400 bad_request`.

## 10. P10.0 security hardening

The API now sends security headers on every response, rate-limits repeated
login failures, enforces a password policy and exports the audit log as CSV:

```bash
# 1) security headers (all responses)
curl -s -D - -o /dev/null http://127.0.0.1:8080/api/health \
  | grep -E 'X-Content-Type-Options|X-Frame-Options|Referrer-Policy|Permissions-Policy'
# -> X-Content-Type-Options: nosniff
#    X-Frame-Options: DENY
#    Referrer-Policy: no-referrer
#    Permissions-Policy: geolocation=(), microphone=(), camera=()

# 2) login lockout: 5 consecutive failures for one username+IP -> 429 for 60 s
for i in 1 2 3 4 5 6; do
  curl -s -o /dev/null -w "%{http_code}\n" -X POST \
    -H 'Content-Type: application/json' \
    -d '{"username":"lock_demo","password":"Wrong@12345"}' \
    http://127.0.0.1:8080/api/auth/login
done
# -> 401 401 401 401 401 429   (429 = too_many_attempts, Retry-After: 60)
# other accounts are unaffected; LOGIN_MAX_FAILURES=0 disables lockout.

# 3) password policy for user create/update: >=10 chars, letter + digit
curl -s -X POST -H "Authorization: Bearer $ADMIN" -H 'Content-Type: application/json' \
  -d '{"username":"operator3","password":"OnlyLettersLong","display_name":"x",
       "role":"viewer","areas":[],"active":true}' http://127.0.0.1:8080/api/users
# -> 400 bad_request "password must contain at least one letter and one digit"

# 4) audit CSV export (admin only)
curl -s -D /tmp/audit.headers -H "Authorization: Bearer $ADMIN" \
  'http://127.0.0.1:8080/api/audit/export?limit=100' -o /tmp/audit.csv
head -1 /tmp/audit.csv   # id,at,actor,action,resource,detail
grep -i content-disposition /tmp/audit.headers   # attachment; filename="audit.csv"
```

Notes:

- Lockout key is `username + client IP` (`X-Forwarded-For` first hop behind a
  proxy), `LOGIN_MAX_FAILURES=5` / `LOGIN_LOCKOUT_SECONDS=60` by default; a
  successful login resets the counter.
- `GET /api/audit/export?from=&to=&action=&actor=&limit=5000` returns
  `text/csv; charset=utf-8` with `detail` as a CSV-escaped JSON string;
  `limit` is capped at 20000. Non-admin roles get `403 forbidden`.
- HTTPS deployment (TLS termination + HSTS) is documented in
  `deploy/nginx-tls.conf` and `docs/OPERATIONS.md`.
- `smoke.sh` verifies all of the above (step 40–42).

## 11. P11.0 session lifecycle (refresh / logout / force-logout)

P11 introduces short-lived access JWTs plus rotating refresh tokens, so
sessions can be revoked without waiting for access-token expiry:

```bash
# 1) login returns an access token + a refresh token
curl -s -X POST -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"Admin@123456"}' \
  http://127.0.0.1:8080/api/auth/login > /tmp/login.json
ACCESS=$(python3 -c 'import json;print(json.load(open("/tmp/login.json"))["token"])')
REFRESH=$(python3 -c 'import json;print(json.load(open("/tmp/login.json"))["refresh_token"])')

# 2) rotate: new access + new refresh; the old refresh token becomes invalid
curl -s -X POST -H 'Content-Type: application/json' \
  -d "{\"refresh_token\":\"$REFRESH\"}" http://127.0.0.1:8080/api/auth/refresh
# -> {"token":"...","refresh_token":"<new>","expires_at":...}
# reusing $REFRESH again -> 401 {"error":"invalid_refresh_token"}

# 3) logout revokes a refresh token (204); access token stays valid until expiry
curl -s -o /dev/null -w '%{http_code}\n' -X POST -H 'Content-Type: application/json' \
  -d "{\"refresh_token\":\"$REFRESH\"}" http://127.0.0.1:8080/api/auth/logout

# 4) logout-all bumps the user's token version -> all access tokens die at once
curl -s -o /dev/null -w '%{http_code}\n' -X POST -H "Authorization: Bearer $ACCESS" \
  http://127.0.0.1:8080/api/auth/logout-all

# 5) admin force logout of another user (revokes refresh + bumps token version)
curl -s -H "Authorization: Bearer $ADMIN" http://127.0.0.1:8080/api/users
curl -s -H "Authorization: Bearer $ADMIN" http://127.0.0.1:8080/api/users/<id>/sessions
curl -s -o /dev/null -w '%{http_code}\n' -X POST -H "Authorization: Bearer $ADMIN" \
  http://127.0.0.1:8080/api/users/<id>/logout   # 204
# self force-logout -> 400 self_protected (use /api/auth/logout-all instead)
```

Notes:

- Access-token validation also checks the user's `token_version` and `active`
  flag; a bump (logout-all / admin force-logout / admin password change) makes
  every already-issued access token return `401 unauthorized` immediately.
- `GET /api/users/{id}/sessions` (admin) lists active refresh sessions
  (`id`, `created_at`, `expires_at`, `ip`, `user_agent`); sessions disappear
  after logout/all/force-logout.
- Defaults: `ACCESS_TOKEN_TTL_SECONDS=3600`, `REFRESH_TOKEN_TTL_SECONDS=604800`;
  memory mode loses sessions on restart (PostgreSQL mode persists them).
- `smoke.sh` verifies the whole lifecycle (steps 43–46).

## 12. P13.0 patrol schedules & executions

Routes become schedulable tasks with an execution lifecycle
(`pending → running → done|issue`); an `issue` completion auto-creates a work
order:

```bash
ADMIN="$TOKEN"
OP=$(curl -s -X POST -H 'Content-Type: application/json' \
  -d '{"username":"operator","password":"Operator@123"}' \
  http://127.0.0.1:8080/api/auth/login \
  | python3 -c 'import json,sys;print(json.load(sys.stdin)["token"])')

# 1) create a patrol route, then a schedule for it (operator/admin only)
curl -s -X POST -H "Authorization: Bearer $OP" -H 'Content-Type: application/json' \
  -d '{"name":"东门夜巡路线","waypoints":[{"x":-90,"y":-60,"z":12},{"x":-20,"y":-40,"z":12}],
       "camera_ids":["cam_001"],"interval_seconds":2}' http://127.0.0.1:8080/api/patrol/routes
curl -s -X POST -H "Authorization: Bearer $OP" -H 'Content-Type: application/json' \
  -d '{"name":"东门夜巡","route_id":"<route_id>","interval_minutes":60,"enabled":true}' \
  http://127.0.0.1:8080/api/patrol/schedules
# -> 201 {id, name, route_id, interval_minutes, enabled, next_run_at, created_by, ...}

# 2) run now -> pending execution; claim -> running; complete -> done
curl -s -X POST -H "Authorization: Bearer $OP" \
  http://127.0.0.1:8080/api/patrol/schedules/<schedule_id>/run
curl -s -H "Authorization: Bearer $OP" 'http://127.0.0.1:8080/api/patrol/executions?limit=100'
curl -s -X POST -H "Authorization: Bearer $OP" \
  http://127.0.0.1:8080/api/patrol/executions/<exec_id>/claim
curl -s -X POST -H "Authorization: Bearer $OP" -H 'Content-Type: application/json' \
  -d '{"result":"done","note":"ok"}' \
  http://127.0.0.1:8080/api/patrol/executions/<exec_id>/complete

# 3) issue flow: completing with result=issue creates a linked work order
curl -s -X POST -H "Authorization: Bearer $OP" \
  http://127.0.0.1:8080/api/patrol/schedules/<schedule_id>/run
curl -s -X POST -H "Authorization: Bearer $OP" \
  http://127.0.0.1:8080/api/patrol/executions/<exec_id2>/claim
curl -s -X POST -H "Authorization: Bearer $OP" -H 'Content-Type: application/json' \
  -d '{"result":"issue","note":"发现设备异常"}' \
  http://127.0.0.1:8080/api/patrol/executions/<exec_id2>/complete
# -> {status:"issue", order_id:"ord_...", finished_at:...}
# order: title "巡检异常 · <route_name>", assignee=<submitter>, status=open,
#        note "巡检发现异常（执行 <execution_id>）：<note>", camera_id=第一台相机
curl -s -H "Authorization: Bearer $ADMIN" 'http://127.0.0.1:8080/api/orders?limit=200'

# 4) schedules are cron-like (interval_minutes 1–10080, next_run_at = now+interval);
#    the scheduler scans every PATROL_TICK_SECONDS (default 15 s) and creates a
#    pending execution when due. viewer is read-only; creator/admin can
#    PATCH/DELETE a schedule, admin can manage others' schedules.
```

Notes:

- `interval_minutes` 1–10080; `next_run_at` is advanced by one interval on each
  automatic run. Validation errors are `400 bad_request` (unknown route id is
  also `400`).
- Executions have no delete endpoint; issue completions leave an order and
  `done`/`issue` execution records as audit/demo history.
- Audit entries: `patrol.schedule.create/update/delete/run`,
  `patrol.execution.claim/complete` (+ `patrol.execution.created` by the
  scheduler, `order.created` for issue orders).
- `smoke.sh` verifies the full lifecycle (steps 50–52).

## 13. P14.0 patrol stats, CSV export & history purge

```bash
ADMIN="$TOKEN"
OP=$(curl -s -X POST -H 'Content-Type: application/json' \
  -d '{"username":"operator","password":"Operator@123"}' \
  http://127.0.0.1:8080/api/auth/login \
  | python3 -c 'import json,sys;print(json.load(sys.stdin)["token"])')

# 1) stats (any logged-in user; optional RFC3339 from/to filter by created_at)
curl -s -H "Authorization: Bearer $ADMIN" \
  'http://127.0.0.1:8080/api/patrol/stats?from=2026-11-01T00:00:00Z&to=2026-11-30T23:59:59Z'
# -> {executions_total, pending, running, done, issue, completion_rate,
#     issue_rate, avg_duration_seconds, by_route:[{route_id,route_name,total,done,issue}],
#     by_assignee:[{assignee,total,done,issue}]}

# 2) execution CSV export (operator/admin)
curl -s -D /tmp/exec.headers -H "Authorization: Bearer $OP" \
  'http://127.0.0.1:8080/api/patrol/executions/export?status=done' -o /tmp/exec.csv
head -1 /tmp/exec.csv
# -> id,schedule_id,route_name,status,assignee,result,note,scheduled_at,started_at,finished_at,order_id
grep -i content-disposition /tmp/exec.headers   # attachment; filename="patrol-executions.csv"

# 3) history purge (admin only): delete finished executions created <= before
curl -s -X DELETE -H "Authorization: Bearer $ADMIN" \
  'http://127.0.0.1:8080/api/patrol/executions?before=2026-11-30T00:00:00Z&status=done,issue'
# -> {"deleted": N}; audit patrol.executions.purge (detail: before/statuses/deleted)
```

Notes:

- `completion_rate = done / executions_total`, `issue_rate = issue /
  executions_total` (3 decimals); `avg_duration_seconds` only counts executions
  with both `started_at` and `finished_at`.
- Export is capped at 10000 rows; `status` filter is optional
  (pending/running/done/issue); viewer gets `403`.
- Purge only allows finished states (`done`/`issue`; `pending`/`running` or
  anything else → 400) and defaults `before` to now, `status` to `done,issue`.
  It is a global archive operation (no per-id delete), so it can remove demo
  history — `smoke.sh` purges its own done/issue records in cleanup.
- `smoke.sh` verifies stats/export/purge (steps 53–55).

## 14. Typical demo sequence

```bash
# terminal 1: API (P2: AUTH_MODE=on by default)
make dev-api

# terminal 2: web UI
make dev-web

# token used by the CLI tools below (web UI does its own login)
TOKEN=$(curl -s -X POST http://127.0.0.1:8080/api/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"Admin@123456"}' \
  | python3 -c 'import json,sys; print(json.load(sys.stdin)["token"])')

# terminal 3: continuous detections (events + live tracks show up in the 3D scene)
API_TOKEN="$TOKEN" python3 services/simulator/simulator.py

# terminal 4 (optional): camera status broadcast demo
python3 services/simulator/run_camera_status.py --once --interval 5 --token "$TOKEN"

# terminal 5 (optional): P3.0/P4.0 webhook notifications (see section 5)
python3 scripts/webhook_receiver.py --port 8099 --out /tmp/webhook.log
# optional retry demo: --fail-first 2 makes the backend retry (max_attempts)

# terminal 4: acceptance + load
make smoke                                          # logs in internally
API_TOKEN="$TOKEN" make load                        # API_TOKEN is exported to the recipe
```

`make smoke` / `make sim` / `make load` are thin wrappers defined by the repo
Makefile; the underlying commands are `./scripts/smoke.sh`,
`python3 services/simulator/simulator.py` and `python3 scripts/load.py`.
