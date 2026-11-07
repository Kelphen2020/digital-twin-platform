# Simulator & verification tools (P0)

Tools that play the **AI inference service** for the digital-twin P0 demo and
verify the full detection → rule engine → event → WebSocket chain described in
[`docs/API.md`](../../docs/API.md) and [`docs/P0_SCOPE.md`](../../docs/P0_SCOPE.md).

All Python tools are **stdlib only** (no pip install) and target Python 3.9+
(verified on 3.14). `scripts/smoke.sh` needs only `bash`, `curl` and `python3`.

| File | Purpose |
|---|---|
| `simulator.py` | Continuous synthetic detection feed: loads cameras via REST, POSTs weighted-class detections to `/api/ingest/detections` at a configurable rate, prints live stats. |
| `run_camera_status.py` | Demo helper: sweeps cameras `offline` → `online` one at a time via `PATCH /api/cameras/{id}` so the WebSocket `camera_status` broadcast can be observed. |
| `../../scripts/smoke.sh` | End-to-end smoke test: health → cameras → ingest → event → ack → metrics. |
| `../../scripts/load.py` | Threaded load injector: target QPS, achieved QPS, accepted/dropped, HTTP p50/p95/p99 latency. |

Prerequisite: the Go API must be running on `http://127.0.0.1:8080`
(`make dev-api`) with the seeded camera inventory (`CAM-001` … `CAM-006`).

---

## 1. `simulator.py` — fake AI inference feed

```bash
# mixed scenario, 5 detections/s, run until Ctrl-C
python3 services/simulator/simulator.py

# fixed 30 s run
python3 services/simulator/simulator.py --duration 30

# only CAM-001, 20 detections/s, reproducible seed
python3 services/simulator/simulator.py --camera CAM-001 --qps 20 --seed 42

# print the generated payloads without POSTing them
python3 services/simulator/simulator.py --dry-run --duration 2

# alarm-heavy scenario (intrusion/smoke/no_helmet)
python3 services/simulator/simulator.py --scenario alarm
```

| Flag | Default | Description |
|---|---|---|
| `--api-base` | `http://127.0.0.1:8080` (or `$API_BASE`) | API base URL |
| `--qps` | `5` | Detection generation rate, **detections/second in total across cameras** (float allowed). Each POST batches 1–3 detections of one camera, so requests/s is lower than this. |
| `--duration` | `0` | Seconds to run; `0` = forever (stop with Ctrl-C) |
| `--camera` | all | Only report against this camera **code**, e.g. `CAM-001` |
| `--seed` | random | Seed for reproducible traffic |
| `--scenario` | `mixed` | `mixed` = person .50 / vehicle .25 / intrusion .15 / smoke .05 / no_helmet .05; `normal` = person/vehicle; `alarm` = intrusion/smoke/no_helmet; or a single class name (`person`, `vehicle`, `intrusion`, `smoke`, `no_helmet`) |
| `--dry-run` | off | Print ingest payloads to stdout instead of POSTing |

Behaviour details:

- On start it calls `GET /api/cameras` and retries **3 times** (with backoff);
  if that fails it prints a clear error and exits `2`.
- Confidence is drawn uniformly from `0.45–0.99`, so roughly a quarter of
  detections fall below the API's `0.6` threshold and are counted as `dropped`.
- World positions are sampled 10–80 m in front of each camera along its
  `heading` (±4° jitter, ~1 m positional jitter), in local ENU metres
  (`x` east, `y` north, `z` up, ground `z = 0`).
- A stats line is written to **stderr every 5 s** (final summary on exit):

  ```
  [stats] elapsed=   5.0s sent=25 posts=12 http_errors=0 accepted=18 dropped=7 events=1 rate=5.00det/s p50=1.42ms p95=2.03ms p99=3.11ms
  ```

  `sent` = detections generated, `posts` = ingest requests, `accepted`/`dropped`
  = the API's own `accepted`/`dropped` response fields (note: in the current Go
  P0 implementation `accepted` is `len(events)`, while `dropped` counts
  detections rejected by the confidence/dedup rules), `events` = events
  created, `p50/p95/p99` = ingest HTTP latency.
- HTTP 4xx/5xx and connection errors are reported (rate-limited) but do not
  crash the loop; after 50 consecutive failures it stops with exit code `3`.
- Ctrl-C / SIGTERM prints the final summary and exits `0`.

`stdout` is reserved for `--dry-run` payloads, so you can pipe them:

```bash
python3 services/simulator/simulator.py --dry-run --duration 1 | head -2
```

---

## 2. `run_camera_status.py` — status broadcast demo

Sweeps every camera `offline` one at a time, then `online` one at a time,
sleeping `--interval` seconds between PATCH calls. Each successful PATCH makes
the API broadcast a `camera_status` message to WebSocket clients.

```bash
# default: 10 s between changes, repeat forever (Ctrl-C to stop)
python3 services/simulator/run_camera_status.py

# one full cycle (all offline, then all online) and exit
python3 services/simulator/run_camera_status.py --once --interval 2

# toggle a single camera
python3 services/simulator/run_camera_status.py --camera CAM-001 --interval 1.5
```

| Flag | Default | Description |
|---|---|---|
| `--api-base` | `http://127.0.0.1:8080` (or `$API_BASE`) | API base URL |
| `--interval` | `10` | Seconds between two status changes |
| `--once` | off | Run one offline+online cycle, then exit |
| `--camera` | all | Only toggle this camera code |

Output example:

```
[21:30:02] CAM-001 (cam_001): online -> offline
[21:30:12] CAM-002 (cam_002): online -> offline
...
[21:31:12] CAM-001 (cam_001): offline -> online
```

Exit codes: `0` normal/Ctrl-C, `1` every PATCH in the `--once` cycle failed,
`2` camera inventory unavailable or bad arguments.

---

## 3. `scripts/smoke.sh` — end-to-end acceptance check

```bash
./scripts/smoke.sh
# or against another host/port:
API_BASE=http://127.0.0.1:8080 ./scripts/smoke.sh
```

Checks, printing `PASS`/`FAIL` per step and exiting non-zero if anything fails:

1. `GET /api/health` returns `status=ok`;
2. `GET /api/cameras` has ≥ 1 camera and the six seeded codes `CAM-001…CAM-006`;
3. high-confidence (`0.987654`) person detection for `CAM-001` is accepted
   (`POST /api/ingest/detections` → `202`);
4. the resulting event is visible in `GET /api/events?limit=20` within 5 s;
5. `POST /api/events/{id}/ack` returns `status=acked`;
6. `GET /metrics` contains `dsh_twin_events_total` and `dsh_twin_ws_clients`.

The script waits for the API's 5 s `(camera, class)` dedup window to clear
before posting, so it is reliable even while `simulator.py` is running. All
temporary files live in a `mktemp -d` directory and are removed on exit.

---

## 4. `scripts/load.py` — concurrent load test

```bash
# default: 100 QPS for 5 s with 10 threads
python3 scripts/load.py

# 50 QPS for 5 s, reproducible payloads
python3 scripts/load.py --qps 50 --duration 5 --seed 7
```

| Flag | Default | Description |
|---|---|---|
| `--api-base` | `http://127.0.0.1:8080` (or `$API_BASE`) | API base URL |
| `--qps` | `100` | Target requests/second (1 detection per request) |
| `--duration` | `5` | Injection window, seconds |
| `--workers` | `10` | Injector thread count |
| `--timeout` | `5` | Per-request HTTP timeout, seconds |
| `--seed` | random | Seed for reproducible payloads |

It preloads camera codes from `GET /api/cameras`, paces requests on a shared
schedule (so threads do not drift), and prints a report like:

```
=== load test report ===
target            : 50 QPS for 5s (250 requests, 10 workers)
wall elapsed      : 5.04s
attempted         : 250 (49.60 req/s)  ok=250 failed=0 (100.00% ok)
achieved QPS      : 49.60 (target 50, threshold 40)
accepted/dropped  : 183 / 67   events=17
http latency (ms) : p50=1.31 p95=2.87 p99=4.12 mean=1.55 max=6.20
RESULT            : PASS
```

Exit codes:

- `0` — achieved QPS ≥ 80 % of target **and** zero failed requests;
- `1` — achieved QPS below 80 % of target, or any request failed;
- `2` — bad arguments or camera inventory unavailable.

Compare the `accepted`/`events` numbers with `GET /metrics`
(`dsh_twin_detections_total`, `dsh_twin_events_total`) as required by
`docs/P0_SCOPE.md` §4.7.

---

## 5. Typical demo sequence

```bash
# terminal 1: API
make dev-api

# terminal 2: web UI
make dev-web

# terminal 3: continuous detections (events show up in the 3D scene)
make sim

# terminal 4 (optional): camera status broadcast demo
python3 services/simulator/run_camera_status.py --once --interval 5

# terminal 4: acceptance + load
make smoke
make load
```

`make smoke` / `make sim` / `make load` are thin wrappers defined by the repo
Makefile; the underlying commands are `./scripts/smoke.sh`,
`python3 services/simulator/simulator.py` and `python3 scripts/load.py`.
