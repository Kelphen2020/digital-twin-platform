#!/usr/bin/env python3
"""Concurrent load injector for the digital-twin API (stdlib only).

Preloads camera codes, then fires a mix of ingest POSTs
(``POST /api/ingest/detections``) and optional read requests
(``GET /api/events?limit=20``) at a target rate with a pool of threads, and
reports achieved throughput plus HTTP latency percentiles.

Used for the P0-4 load proof and the P9.0 performance matrix
(see ``docs/PERFORMANCE.md``):

    python3 scripts/load.py --qps 100 --duration 5
    python3 scripts/load.py --qps 200 --duration 10 --ramp
    python3 scripts/load.py --qps 100 --duration 5 --read-ratio 0.3
    python3 scripts/load.py --qps 100 --duration 5 --report /tmp/bench.md --json /tmp/runs.json

Modes
-----
* steady (default): constant target QPS for ``--duration`` seconds.
* ``--ramp``: linear ramp from 25% of ``--qps`` at t=0 to 100% at t=end
  (average = 62.5% of ``--qps``; the pass threshold uses that average rate).
* ``--read-ratio R``: R (0..1) of the scheduled slots are
  ``GET /api/events?limit=20`` reads (needs a Bearer token under AUTH_MODE=on,
  e.g. via ``--api-token``/``API_TOKEN``); the rest are ingest POSTs.

Each schedulable slot is one request; ``--qps`` is requests/second.
``--track-ratio`` (P1) keeps a fraction of detections carrying ``track_id``.
Ingest stays token-free unless the API sets ``INGEST_TOKEN``
(``--ingest-token``).

P9.0 also snapshots ``GET /metrics`` before/after every run and reports the
deltas for ws frames/dropped, ingest rejections, ingest duration, bus counters
and track points. ``--report PATH`` appends one Markdown table row per run
(sharing a file across runs builds the benchmark matrix table); ``--json PATH``
appends the run as a JSON object to a list.

Exit codes:
    0  achieved QPS >= 80% of the target (average rate in ramp mode) and no
       failed requests
    1  achieved QPS below that threshold, or at least one request failed
       (expected for the deliberate 429 rate-limit test)
    2  bad arguments, camera inventory unavailable, or read preflight failed
"""

from __future__ import annotations

import argparse
import json
import math
import os
import random
import statistics
import sys
import threading
import time
import urllib.error
import urllib.request
from datetime import datetime, timezone

DEFAULT_API_BASE = "http://127.0.0.1:8080"
DEFAULT_QPS = 100.0
DEFAULT_DURATION = 5.0
DEFAULT_WORKERS = 10
HTTP_TIMEOUT = 5.0
CAMERAS_PATH = "/api/cameras"
INGEST_PATH = "/api/ingest/detections"
READS_PATH = "/api/events?limit=20"
METRICS_PATH = "/metrics"

# Per-camera pool of stable track ids used by --track-ratio (P1).  The ids are
# intentionally short and deterministic so repeated load runs create recognisable
# trajectories without unbounded track growth.
TRACKS_PER_CAMERA = 3

# Same weighted class mix as services/simulator/simulator.py (docs/API.md).
CLASS_WEIGHTS = [
    ("person", 0.50),
    ("vehicle", 0.25),
    ("intrusion", 0.15),
    ("smoke", 0.05),
    ("no_helmet", 0.05),
]

# Counters included in the /metrics before/after snapshot deltas (P9.0).
METRIC_DELTA_KEYS = [
    "dsh_twin_detections_total",
    "dsh_twin_events_total",
    "dsh_twin_ws_frames_total",
    "dsh_twin_ws_dropped_total",
    "dsh_twin_ingest_rejected_total",
    "dsh_twin_ingest_duration_seconds_count",
    "dsh_twin_ingest_duration_seconds_sum",
    "dsh_twin_bus_published_total",
    "dsh_twin_bus_replayed_total",
    "dsh_twin_track_points_written_total",
    "dsh_twin_track_points_broadcast_total",
]

REPORT_HEADER = """# Load test report

| run | mode | target QPS | duration s | achieved QPS | ok | failed | p50 ms | p95 ms | p99 ms | accepted | dropped | events | tracked | wall s | ws_frames+ | ws_dropped+ | rejected+ | bus_pub+ | notes |
|---:|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---|
"""


# ---------------------------------------------------------------------------
# helpers
# ---------------------------------------------------------------------------

def utc_now_rfc3339() -> str:
    return datetime.now(timezone.utc).isoformat(timespec="milliseconds").replace("+00:00", "Z")


def percentile(values, pct: float) -> float:
    """Nearest-rank percentile."""
    if not values:
        return 0.0
    ordered = sorted(values)
    rank = max(1, math.ceil(pct / 100.0 * len(ordered))) - 1
    return ordered[min(rank, len(ordered) - 1)]


def log(message: str) -> None:
    print(message, file=sys.stderr, flush=True)


def decode_body(raw: bytes):
    text = raw.decode("utf-8", errors="replace").strip()
    if not text:
        return None
    try:
        return json.loads(text)
    except ValueError:
        return text


class ApiClient:
    """Minimal JSON HTTP client; transport errors are returned, never raised.

    P2.0 auth (docs/API_P2.md): user-facing REST calls (camera preflight, event
    reads, metrics are exempt) need a Bearer token when the API runs with
    AUTH_MODE=on.  The ingest machine entry stays token-free unless the API
    sets INGEST_TOKEN (then ``X-Ingest-Token`` is required).
    """

    def __init__(
        self,
        api_base: str,
        timeout: float = HTTP_TIMEOUT,
        api_token: str | None = None,
        ingest_token: str | None = None,
    ):
        self.api_base = api_base.rstrip("/")
        self.timeout = timeout
        self.api_token = api_token
        self.ingest_token = ingest_token

    def request_json(self, method: str, path: str, payload=None):
        """Return ``(status, body, latency_ms, error)``; status 0 => transport failure."""
        url = self.api_base + path
        headers = {"Accept": "application/json", "User-Agent": "dsh-twin-load/0.1"}
        if self.api_token:
            headers["Authorization"] = f"Bearer {self.api_token}"
        data = None
        if payload is not None:
            data = json.dumps(payload, ensure_ascii=False).encode("utf-8")
            headers["Content-Type"] = "application/json"
            if self.ingest_token:
                headers["X-Ingest-Token"] = self.ingest_token
        request = urllib.request.Request(url, data=data, headers=headers, method=method)
        started = time.perf_counter()
        try:
            with urllib.request.urlopen(request, timeout=self.timeout) as response:
                raw = response.read()
                latency_ms = (time.perf_counter() - started) * 1000.0
                return response.status, decode_body(raw), latency_ms, None
        except urllib.error.HTTPError as exc:
            raw = exc.read()
            latency_ms = (time.perf_counter() - started) * 1000.0
            return exc.code, decode_body(raw), latency_ms, None
        except (urllib.error.URLError, TimeoutError, OSError) as exc:
            latency_ms = (time.perf_counter() - started) * 1000.0
            return 0, None, latency_ms, str(getattr(exc, "reason", exc))


def load_cameras(client: ApiClient, attempts: int = 3):
    """GET /api/cameras with retries; raises RuntimeError after ``attempts`` failures."""
    last_error = "unknown error"
    for attempt in range(1, attempts + 1):
        status, body, _latency, error = client.request_json("GET", CAMERAS_PATH)
        if error is None and status == 200 and isinstance(body, dict) and isinstance(body.get("items"), list):
            return body["items"]
        last_error = error if error else (f"HTTP {status}: {body!r}" if status != 200 else f"unexpected shape: {body!r}")
        log(f"[load] GET {CAMERAS_PATH} attempt {attempt}/{attempts} failed: {last_error}")
        if attempt < attempts:
            time.sleep(attempt * 0.5)
    hint = "Is the API running?"
    if "401" in last_error:
        hint = (
            "The API runs with AUTH_MODE=on: pass --api-token/API_TOKEN "
            "(token from POST /api/auth/login) so GET /api/cameras is authorized."
        )
    raise RuntimeError(
        f"could not load cameras from {client.api_base}{CAMERAS_PATH} after {attempts} attempts "
        f"({last_error}). {hint} Try: curl -s {client.api_base}/api/health"
    )


def fetch_metrics(client: ApiClient) -> dict:
    """Snapshot GET /metrics as {metric_base_name: summed_value} (labels summed)."""
    status, body, _latency, error = client.request_json("GET", METRICS_PATH)
    if error is not None or status != 200:
        return {}
    text = body if isinstance(body, str) else ""
    values = {}
    for line in text.splitlines():
        line = line.strip()
        if not line or line.startswith("#"):
            continue
        parts = line.split()
        if len(parts) < 2:
            continue
        name = parts[0].split("{")[0]
        try:
            value = float(parts[1])
        except ValueError:
            continue
        values[name] = values.get(name, 0.0) + value
    return values


def metrics_delta(before: dict, after: dict, key: str):
    """Return after-before for one counter (None when the metric is absent)."""
    if key not in after:
        return None
    return after.get(key, 0.0) - before.get(key, 0.0)


def metric_delta_str(before: dict, after: dict) -> str:
    parts = []
    for key in (
        "dsh_twin_ws_frames_total",
        "dsh_twin_ws_dropped_total",
        "dsh_twin_ingest_rejected_total",
        "dsh_twin_bus_published_total",
        "dsh_twin_track_points_written_total",
    ):
        value = metrics_delta(before, after, key)
        short = key.replace("dsh_twin_", "").replace("_total", "")
        parts.append(f"{short}={'?' if value is None else ('%+.0f' % value)}")
    ingest_count = metrics_delta(before, after, "dsh_twin_ingest_duration_seconds_count")
    ingest_sum = metrics_delta(before, after, "dsh_twin_ingest_duration_seconds_sum")
    if ingest_count is not None:
        parts.append("ingest_count=%+.0f" % ingest_count)
    if ingest_sum is not None:
        parts.append("ingest_sum=%+.3fs" % ingest_sum)
    return " ".join(parts)


def build_schedule(total: int, qps: float, duration: float, ramp: bool):
    """Relative arrival times (seconds from start) for ``total`` slots.

    Steady mode spaces requests by 1/qps.  Ramp mode ramps the rate linearly
    from 25% to 100% of ``qps`` so cumulative arrivals follow
    ``C(u) = total * (0.4u + 0.6u^2)`` with ``u = t/duration``; invert that to
    get each slot's time.
    """
    if not ramp:
        return [index / qps for index in range(total)]
    times = []
    for index in range(total):
        fraction = (index + 1) / total
        u = (-0.4 + math.sqrt(0.16 + 2.4 * fraction)) / 1.2
        u = min(1.0, max(0.0, u))
        times.append(u * duration)
    return times


# ---------------------------------------------------------------------------
# payload generation
# ---------------------------------------------------------------------------

def build_payload(rng: random.Random, camera: dict, track_id=None) -> dict:
    """One ingest request carrying a single synthetic detection.

    ``track_id`` is included when given (P1) so the backend also writes a track
    point and broadcasts ``track_point``; plain P0 payloads are unchanged.
    """
    roll = rng.random()
    cumulative = 0.0
    cls = CLASS_WEIGHTS[-1][0]
    for name, weight in CLASS_WEIGHTS:
        cumulative += weight
        if roll <= cumulative:
            cls = name
            break

    heading = math.radians(float(camera.get("heading") or 0.0) + rng.uniform(-4.0, 4.0))
    distance = rng.uniform(10.0, 80.0)
    world = {
        "x": round(float(camera.get("x") or 0.0) + distance * math.sin(heading) + rng.uniform(-1.0, 1.0), 3),
        "y": round(float(camera.get("y") or 0.0) + distance * math.cos(heading) + rng.uniform(-1.0, 1.0), 3),
        "z": 0.0,
    }
    x1 = rng.uniform(0.08, 0.68)
    y1 = rng.uniform(0.10, 0.65)
    bbox = [
        round(x1, 3),
        round(y1, 3),
        round(min(0.99, x1 + rng.uniform(0.06, 0.22)), 3),
        round(min(0.99, y1 + rng.uniform(0.10, 0.30)), 3),
    ]
    detection = {
        "class": cls,
        "confidence": round(rng.uniform(0.45, 0.99), 3),
        "bbox": bbox,
        "world": world,
    }
    if track_id is not None:
        detection["track_id"] = track_id
    return {
        "camera_code": camera.get("code"),
        "ts": utc_now_rfc3339(),
        "detections": [detection],
    }


# ---------------------------------------------------------------------------
# reporting
# ---------------------------------------------------------------------------

def append_markdown_report(path: str, row: dict) -> None:
    """Append one Markdown table row to ``path`` (creating the header if needed)."""
    existing = ""
    run_no = 1
    if os.path.exists(path):
        try:
            with open(path, "r", encoding="utf-8") as handle:
                existing = handle.read()
        except OSError:
            existing = ""
        run_no = sum(1 for line in existing.splitlines() if line.startswith("| ") and line[2:3].isdigit()) + 1
        if not existing.lstrip().startswith("# Load test report"):
            existing = ""
            run_no = 1
    row = dict(row)
    row["notes"] = str(row.get("notes", "")).replace("|", "/")
    line = (
        "| {run} | {mode} | {target:g} | {duration:g} | {achieved:.2f} | {ok} | {failed} | "
        "{p50:.2f} | {p95:.2f} | {p99:.2f} | {accepted} | {dropped} | {events} | {tracked} | "
        "{wall:.2f} | {ws_frames} | {ws_dropped} | {rejected} | {bus_pub} | {notes} |"
    ).format(run=run_no, **row)
    content = existing if existing else REPORT_HEADER
    content = content.rstrip("\n") + "\n" + line + "\n"
    directory = os.path.dirname(os.path.abspath(path))
    if directory:
        try:
            os.makedirs(directory, exist_ok=True)
        except OSError:
            pass
    with open(path, "w", encoding="utf-8") as handle:
        handle.write(content)


def append_json_report(path: str, payload: dict) -> None:
    """Append one run object to a JSON list stored at ``path``."""
    data = []
    if os.path.exists(path):
        try:
            with open(path, "r", encoding="utf-8") as handle:
                loaded = json.load(handle)
            if isinstance(loaded, list):
                data = loaded
            else:
                data = [loaded]
        except (OSError, ValueError):
            data = []
    data.append(payload)
    directory = os.path.dirname(os.path.abspath(path))
    if directory:
        try:
            os.makedirs(directory, exist_ok=True)
        except OSError:
            pass
    with open(path, "w", encoding="utf-8") as handle:
        json.dump(data, handle, indent=2, ensure_ascii=False)
        handle.write("\n")


# ---------------------------------------------------------------------------
# main
# ---------------------------------------------------------------------------

def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="load.py",
        description="Concurrent detection/read-load injector for the digital-twin API.",
        formatter_class=argparse.ArgumentDefaultsHelpFormatter,
    )
    parser.add_argument(
        "--api-base",
        default=os.environ.get("API_BASE", DEFAULT_API_BASE),
        help="API base URL (or set env API_BASE)",
    )
    parser.add_argument("--qps", type=float, default=DEFAULT_QPS, help="target requests per second")
    parser.add_argument("--duration", type=float, default=DEFAULT_DURATION, help="seconds of injection")
    parser.add_argument(
        "--workers",
        type=int,
        default=DEFAULT_WORKERS,
        help="number of injector threads",
    )
    parser.add_argument("--timeout", type=float, default=HTTP_TIMEOUT, help="per-request HTTP timeout, seconds")
    parser.add_argument(
        "--ramp",
        action="store_true",
        help="linearly ramp QPS from 25%% of --qps to 100%% over --duration "
        "(average rate = 62.5%% of --qps; the pass threshold uses that average)",
    )
    parser.add_argument(
        "--read-ratio",
        type=float,
        default=0.0,
        help="fraction (0..1) of requests that are GET /api/events?limit=20 reads "
        "instead of ingest POSTs (reads need a Bearer token under AUTH_MODE=on)",
    )
    parser.add_argument(
        "--report",
        default=None,
        metavar="PATH",
        help="append one Markdown table row per run to PATH; sharing PATH across "
        "runs builds a benchmark matrix table",
    )
    parser.add_argument(
        "--json",
        default=None,
        dest="json_path",
        metavar="PATH",
        help="append this run's metrics as a JSON object to the list stored at PATH",
    )
    parser.add_argument(
        "--ingest-token",
        default=os.environ.get("INGEST_TOKEN"),
        metavar="TOKEN",
        help="optional X-Ingest-Token header on POST /api/ingest/detections "
        "(or set env INGEST_TOKEN; needed only if the API sets INGEST_TOKEN)",
    )
    parser.add_argument(
        "--api-token",
        default=os.environ.get("API_TOKEN"),
        metavar="TOKEN",
        help="optional Bearer token for the camera/read preflight when the API "
        "runs with AUTH_MODE=on (or set env API_TOKEN)",
    )
    parser.add_argument(
        "--track-ratio",
        type=float,
        default=0.5,
        help="fraction of detections carrying a track_id (P1 track path); 0 disables, 1 tracks all",
    )
    parser.add_argument("--seed", type=int, default=None, help="random seed for reproducible payloads")
    return parser


def main(argv=None) -> int:
    args = build_parser().parse_args(argv)
    if args.qps <= 0:
        log(f"[load] error: --qps must be > 0 (got {args.qps})")
        return 2
    if args.duration <= 0:
        log(f"[load] error: --duration must be > 0 (got {args.duration})")
        return 2
    if args.workers < 1:
        log(f"[load] error: --workers must be >= 1 (got {args.workers})")
        return 2
    if not 0.0 <= args.track_ratio <= 1.0:
        log(f"[load] error: --track-ratio must be between 0 and 1 (got {args.track_ratio})")
        return 2
    if not 0.0 <= args.read_ratio <= 1.0:
        log(f"[load] error: --read-ratio must be between 0 and 1 (got {args.read_ratio})")
        return 2

    client = ApiClient(
        args.api_base,
        timeout=args.timeout,
        api_token=args.api_token or None,
        ingest_token=args.ingest_token or None,
    )
    try:
        cameras = load_cameras(client)
        if not cameras:
            raise RuntimeError("API returned an empty camera list; seed the database first")
    except RuntimeError as exc:
        log(f"[load] error: {exc}")
        return 2

    if args.read_ratio > 0:
        status, body, _latency, error = client.request_json("GET", READS_PATH)
        if error is not None or status == 401:
            hint = (
                "GET /api/events needs a Bearer token under AUTH_MODE=on: pass "
                "--api-token/API_TOKEN (read preflight failed: "
                f"{error or ('HTTP %s: %r' % (status, body))})"
            )
            log(f"[load] error: {hint}")
            return 2

    mode = "ramp" if args.ramp else "steady"
    if args.ramp:
        total = max(1, int(round(0.625 * args.qps * args.duration)))
    else:
        total = max(1, int(round(args.qps * args.duration)))
    schedule = build_schedule(total, args.qps, args.duration, args.ramp)
    average_target_qps = total / args.duration
    start = time.monotonic()

    metrics_before = fetch_metrics(client) if os.environ.get("LOAD_NO_METRICS", "") not in ("1", "true", "yes") else {}

    # P1: a small fixed pool of stable track ids per camera so repeated
    # detections form recognisable trajectories instead of one-off points.
    track_pools = []
    for camera_index in range(len(cameras)):
        track_pools.append(
            ["trk-load-%02d-%d" % (camera_index + 1, slot) for slot in range(TRACKS_PER_CAMERA)]
        )

    lock = threading.Lock()
    state = {
        "next_index": 0,
        "attempted": 0,
        "ok": 0,
        "failed": 0,
        "reads": 0,
        "reads_ok": 0,
        "ingests": 0,
        "tracked": 0,
        "accepted": 0,
        "dropped": 0,
        "events": 0,
        "latencies": [],
        "errors": [],
    }

    def worker(worker_id: int) -> None:
        seed = None if args.seed is None else args.seed + worker_id
        rng = random.Random(seed)
        while True:
            with lock:
                index = state["next_index"]
                if index >= total:
                    return
                state["next_index"] += 1
            scheduled_at = start + schedule[index]
            delay = scheduled_at - time.monotonic()
            if delay > 0:
                time.sleep(delay)
            is_read = args.read_ratio > 0 and rng.random() < args.read_ratio
            camera = None
            track_id = None
            if is_read:
                status, body, latency_ms, error = client.request_json("GET", READS_PATH)
                ok = error is None and status == 200
            else:
                camera_index = rng.randrange(len(cameras))
                camera = cameras[camera_index]
                if args.track_ratio > 0 and rng.random() < args.track_ratio:
                    track_id = rng.choice(track_pools[camera_index])
                payload = build_payload(rng, camera, track_id)
                status, body, latency_ms, error = client.request_json("POST", INGEST_PATH, payload)
                ok = error is None and status == 202
            with lock:
                state["attempted"] += 1
                state["latencies"].append(latency_ms)
                if is_read:
                    state["reads"] += 1
                    if ok:
                        state["reads_ok"] += 1
                else:
                    state["ingests"] += 1
                    if track_id is not None:
                        state["tracked"] += 1
                if not ok:
                    state["failed"] += 1
                    if len(state["errors"]) < 5:
                        if is_read:
                            kind = "read /api/events"
                        else:
                            kind = f"ingest camera={camera.get('code') if camera else '?'}"
                        detail = error if error else f"HTTP {status}: {body!r}"
                        state["errors"].append(f"{kind} index={index} {detail}")
                else:
                    state["ok"] += 1
                    if not is_read and isinstance(body, dict):
                        state["accepted"] += int(body.get("accepted") or 0)
                        state["dropped"] += int(body.get("dropped") or 0)
                        state["events"] += len(body.get("events") or [])

    mode_detail = f"ramp 25%->{args.qps:g}" if args.ramp else "steady"
    log(
        f"[load] api={args.api_base} mode={mode_detail} target_qps={args.qps:g} "
        f"duration={args.duration:g}s workers={args.workers} cameras={len(cameras)} "
        f"scheduled={total} track_ratio={args.track_ratio:g} read_ratio={args.read_ratio:g} "
        f"api_auth={'token' if args.api_token else 'none'} "
        f"ingest_auth={'token' if args.ingest_token else 'none'}"
    )

    threads = [
        threading.Thread(target=worker, args=(worker_id,), name=f"load-{worker_id}", daemon=True)
        for worker_id in range(args.workers)
    ]
    for thread in threads:
        thread.start()
    for thread in threads:
        thread.join()

    elapsed = time.monotonic() - start
    metrics_after = fetch_metrics(client) if metrics_before else {}

    window = args.duration  # throughput is measured against the scheduled window
    achieved_qps = state["ok"] / window
    attempted_qps = state["attempted"] / window
    latencies = state["latencies"]
    acceptance = (state["ok"] / state["attempted"] * 100.0) if state["attempted"] else 0.0
    min_qps = 0.8 * average_target_qps
    qps_ok = achieved_qps >= min_qps
    requests_ok = state["failed"] == 0
    passed = qps_ok and requests_ok

    p50 = percentile(latencies, 50)
    p95 = percentile(latencies, 95)
    p99 = percentile(latencies, 99)
    mean_latency = statistics.mean(latencies) if latencies else 0.0
    max_latency = max(latencies) if latencies else 0.0

    m_ws_frames = metrics_delta(metrics_before, metrics_after, "dsh_twin_ws_frames_total") if metrics_before else None
    m_ws_dropped = metrics_delta(metrics_before, metrics_after, "dsh_twin_ws_dropped_total") if metrics_before else None
    m_rejected = metrics_delta(metrics_before, metrics_after, "dsh_twin_ingest_rejected_total") if metrics_before else None
    m_bus_pub = metrics_delta(metrics_before, metrics_after, "dsh_twin_bus_published_total") if metrics_before else None
    m_ingest_count = metrics_delta(metrics_before, metrics_after, "dsh_twin_ingest_duration_seconds_count") if metrics_before else None
    m_ingest_sum = metrics_delta(metrics_before, metrics_after, "dsh_twin_ingest_duration_seconds_sum") if metrics_before else None

    def fmt(value, spec="{:+.0f}"):
        return "?" if value is None else spec.format(value)

    print()
    print("=== load test report ===")
    print(
        f"target            : {args.qps:g} QPS for {args.duration:g}s ({total} requests, "
        f"{args.workers} workers)" + (" [ramp 25% -> 100%]" if args.ramp else "")
    )
    print(f"mode              : {mode_detail}")
    print(f"wall elapsed      : {elapsed:.2f}s")
    print(
        f"attempted         : {state['attempted']} ({attempted_qps:.2f} req/s)  "
        f"ok={state['ok']} failed={state['failed']} ({acceptance:.2f}% ok)"
    )
    print(f"achieved QPS      : {achieved_qps:.2f} (avg target {average_target_qps:.2f}, threshold {min_qps:.2f})")
    print(f"accepted/dropped  : {state['accepted']} / {state['dropped']}   events={state['events']}")
    print(
        f"tracked           : {state['tracked']}/{state['ingests']} ingest detections with track_id "
        f"(--track-ratio {args.track_ratio:g})"
    )
    if args.read_ratio > 0:
        print(
            f"read requests     : {state['reads_ok']}/{state['reads']} ok "
            f"(--read-ratio {args.read_ratio:g})"
        )
    if latencies:
        print(
            f"http latency (ms) : p50={p50:.2f} p95={p95:.2f} p99={p99:.2f} "
            f"mean={mean_latency:.2f} max={max_latency:.2f}"
        )
    if metrics_before:
        print(f"metrics delta     : {metric_delta_str(metrics_before, metrics_after)}")
    if state["errors"]:
        print("error samples     :")
        for error in state["errors"]:
            print(f"  - {error}")

    if args.report:
        mode_notes = []
        if args.ramp:
            mode_notes.append("ramp 25%→100%")
        if args.read_ratio > 0:
            mode_notes.append(f"read_ratio={args.read_ratio:g}")
        mode_notes.append("PASS" if passed else "FAIL")
        try:
            append_markdown_report(args.report, {
                "mode": mode_detail,
                "target": args.qps,
                "duration": args.duration,
                "achieved": achieved_qps,
                "ok": state["ok"],
                "failed": state["failed"],
                "p50": p50,
                "p95": p95,
                "p99": p99,
                "accepted": state["accepted"],
                "dropped": state["dropped"],
                "events": state["events"],
                "tracked": state["tracked"],
                "wall": elapsed,
                "ws_frames": fmt(m_ws_frames),
                "ws_dropped": fmt(m_ws_dropped),
                "rejected": fmt(m_rejected),
                "bus_pub": fmt(m_bus_pub),
                "notes": " ".join(mode_notes),
            })
            print(f"report            : appended a row to {args.report}")
        except OSError as exc:
            log(f"[load] warning: could not write report {args.report}: {exc}")

    if args.json_path:
        run_payload = {
            "at": utc_now_rfc3339(),
            "api_base": args.api_base,
            "mode": mode,
            "ramp": args.ramp,
            "target_qps": args.qps,
            "average_target_qps": round(average_target_qps, 3),
            "duration_seconds": args.duration,
            "workers": args.workers,
            "track_ratio": args.track_ratio,
            "read_ratio": args.read_ratio,
            "scheduled": total,
            "wall_seconds": round(elapsed, 3),
            "attempted": state["attempted"],
            "ok": state["ok"],
            "failed": state["failed"],
            "reads": state["reads"],
            "reads_ok": state["reads_ok"],
            "ingests": state["ingests"],
            "tracked": state["tracked"],
            "accepted": state["accepted"],
            "dropped": state["dropped"],
            "events": state["events"],
            "achieved_qps": round(achieved_qps, 3),
            "min_qps_threshold": round(min_qps, 3),
            "latency_ms": {
                "p50": round(p50, 3),
                "p95": round(p95, 3),
                "p99": round(p99, 3),
                "mean": round(mean_latency, 3),
                "max": round(max_latency, 3),
            },
            "metrics_delta": {
                key.replace("dsh_twin_", ""): (None if metrics_delta(metrics_before, metrics_after, key) is None
                                               else round(metrics_delta(metrics_before, metrics_after, key), 3))
                for key in METRIC_DELTA_KEYS
            } if metrics_before else {},
            "errors": state["errors"],
            "result": "PASS" if passed else "FAIL",
        }
        try:
            append_json_report(args.json_path, run_payload)
            print(f"json              : appended this run to {args.json_path}")
        except OSError as exc:
            log(f"[load] warning: could not write json {args.json_path}: {exc}")

    if passed:
        print("RESULT            : PASS")
    else:
        reasons = []
        if not qps_ok:
            reasons.append(
                f"achieved QPS {achieved_qps:.2f} < 80% of target ({min_qps:.2f})"
            )
        if not requests_ok:
            reasons.append(f"{state['failed']} request(s) failed")
        print(f"RESULT            : FAIL ({'; '.join(reasons)})")
    print("========================")
    return 0 if passed else 1


if __name__ == "__main__":
    try:
        sys.exit(main())
    except KeyboardInterrupt:
        sys.exit(1)
