# Simulator & verification tools (P0 + P1.0 + P2.0)

Tools that play the **AI inference service** for the digital-twin demo and
verify the detection → rule engine → event → WebSocket chain described in
[`docs/API.md`](../../docs/API.md) / [`docs/P0_SCOPE.md`](../../docs/P0_SCOPE.md),
the P1.0 additions in [`docs/API_P1.md`](../../docs/API_P1.md) (tracks,
calibration, alarm workflow, audit, stats/CSV export) and the P2.0 additions in
[`docs/API_P2.md`](../../docs/API_P2.md) (JWT auth + RBAC, per-camera rules,
work orders).

All Python tools are **stdlib only** (no pip install) and target Python 3.9+
(verified on 3.14). `scripts/smoke.sh` needs only `bash`, `curl` and `python3`.

| File | Purpose |
|---|---|
| `simulator.py` | Continuous synthetic detection feed: loads cameras via REST, POSTs detections (weighted classes, P1 virtual targets with stable `track_id`) to `/api/ingest/detections` at a configurable rate, prints live stats. P2.0 auth tokens optional. |
| `run_camera_status.py` | Demo helper: sweeps cameras `offline` → `online` one at a time via `PATCH /api/cameras/{id}` so the WebSocket `camera_status` broadcast can be observed. |
| `../../scripts/smoke.sh` | End-to-end smoke test: 21 P0/P1.0 checks + 18 P2.0 checks = **39 checks** (auth/RBAC, orders, camera rules, calibration, tracks, event workflow, audit, stats/CSV). |
| `../../scripts/load.py` | Threaded load injector: target QPS, achieved QPS, accepted/dropped, HTTP p50/p95/p99 latency, optional `--track-ratio` and machine-entry tokens. |

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
(**39 checks**: 21 P0/P1.0 + 18 P2.0):

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

The script waits for the API's 5 s `(camera, class)` dedup window to clear
before posting (re-checked after each wait, ingest retried up to 3×), so it is
reliable even while `simulator.py` is running. All temporary files live in a
`mktemp -d` directory and are removed on exit. A full run takes ~15–25 s.

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
| `--api-token` | `$API_TOKEN` | Bearer token for the `GET /api/cameras` preflight under `AUTH_MODE=on` |
| `--ingest-token` | `$INGEST_TOKEN` | Optional `X-Ingest-Token` header on ingest (only if the API sets `INGEST_TOKEN`) |

It preloads camera codes from `GET /api/cameras`, paces requests on a shared
schedule (so threads do not drift), and prints a report like:

```
=== load test report ===
target            : 50 QPS for 5s (250 requests, 10 workers)
wall elapsed      : 5.04s
attempted         : 250 (49.60 req/s)  ok=250 failed=0 (100.00% ok)
achieved QPS      : 49.60 (target 50, threshold 40)
accepted/dropped  : 183 / 67   events=17
tracked           : 124/250 detections with track_id (--track-ratio 0.5)
http latency (ms) : p50=1.31 p95=2.87 p99=4.12 mean=1.55 max=6.20
RESULT            : PASS
```

Exit codes:

- `0` — achieved QPS ≥ 80 % of target **and** zero failed requests;
- `1` — achieved QPS below 80 % of target, or any request failed;
- `2` — bad arguments or camera inventory unavailable (e.g. missing token with
  `AUTH_MODE=on`; the error message points at `--api-token`/`API_TOKEN`).

Compare the `accepted`/`events` numbers with `GET /metrics`
(`dsh_twin_detections_total`, `dsh_twin_events_total`) as required by
`docs/P0_SCOPE.md` §4.7; tracked detections additionally show up in
`GET /api/tracks` (dedup does not drop track points, only events).

---

## 5. Typical demo sequence

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

# terminal 4: acceptance + load
make smoke                                          # logs in internally
API_TOKEN="$TOKEN" make load                        # API_TOKEN is exported to the recipe
```

`make smoke` / `make sim` / `make load` are thin wrappers defined by the repo
Makefile; the underlying commands are `./scripts/smoke.sh`,
`python3 services/simulator/simulator.py` and `python3 scripts/load.py`.
