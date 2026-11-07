#!/usr/bin/env python3
"""Concurrent ingest load injector for the digital-twin API (stdlib only).

Preloads camera codes from ``GET /api/cameras``, then fires detection POSTs at
a target QPS with a pool of threads and reports achieved throughput plus HTTP
latency percentiles.  Used to prove P0-4 ("可压测"): the API must absorb

    python3 scripts/load.py --qps 100 --duration 5

and expose consistent counters in ``/metrics`` (docs/P0_SCOPE.md §4.7).

P1.0 adds ``--track-ratio`` (default 0.5): that fraction of detections carries
a stable ``track_id`` from a small per-camera virtual-track pool, exercising the
``POST /api/ingest/detections`` -> track-point path (``GET /api/tracks``,
docs/API_P1.md §3) while the rest stay plain P0 detections.

Each scheduled slot sends exactly one detection, so target QPS is both
"requests/second" and "detections/second".

Exit codes:
    0  achieved QPS >= 80% of target and zero failed requests
    1  achieved QPS below 80% of target, or at least one request failed
    2  bad arguments or camera inventory unavailable
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

    P2.0 auth (docs/API_P2.md): GET /api/cameras needs a Bearer token when the
    API runs with AUTH_MODE=on; the ingest machine entry stays token-free
    unless the API sets INGEST_TOKEN (then ``X-Ingest-Token`` is required).
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
# main
# ---------------------------------------------------------------------------

def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="load.py",
        description="Concurrent detection-load injector for POST /api/ingest/detections.",
        formatter_class=argparse.ArgumentDefaultsHelpFormatter,
    )
    parser.add_argument(
        "--api-base",
        default=os.environ.get("API_BASE", DEFAULT_API_BASE),
        help="API base URL (or set env API_BASE)",
    )
    parser.add_argument("--qps", type=float, default=DEFAULT_QPS, help="target requests/detections per second")
    parser.add_argument("--duration", type=float, default=DEFAULT_DURATION, help="seconds of injection")
    parser.add_argument(
        "--workers",
        type=int,
        default=DEFAULT_WORKERS,
        help="number of injector threads",
    )
    parser.add_argument("--timeout", type=float, default=HTTP_TIMEOUT, help="per-request HTTP timeout, seconds")
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
        help="optional Bearer token for the GET /api/cameras preflight when the "
        "API runs with AUTH_MODE=on (or set env API_TOKEN)",
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

    total = max(1, int(round(args.qps * args.duration)))
    target_qps = total / args.duration
    start = time.monotonic()

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
            scheduled_at = start + index / args.qps
            delay = scheduled_at - time.monotonic()
            if delay > 0:
                time.sleep(delay)
            camera_index = rng.randrange(len(cameras))
            camera = cameras[camera_index]
            track_id = None
            if args.track_ratio > 0 and rng.random() < args.track_ratio:
                track_id = rng.choice(track_pools[camera_index])
            payload = build_payload(rng, camera, track_id)
            status, body, latency_ms, error = client.request_json(
                "POST", INGEST_PATH, payload
            )
            with lock:
                state["attempted"] += 1
                state["latencies"].append(latency_ms)
                if track_id is not None:
                    state["tracked"] += 1
                if error is not None or status != 202:
                    state["failed"] += 1
                    if len(state["errors"]) < 5:
                        detail = error if error else f"HTTP {status}: {body!r}"
                        state["errors"].append(
                            f"camera={camera.get('code')} index={index} {detail}"
                        )
                else:
                    state["ok"] += 1
                    if isinstance(body, dict):
                        state["accepted"] += int(body.get("accepted") or 0)
                        state["dropped"] += int(body.get("dropped") or 0)
                        state["events"] += len(body.get("events") or [])

    log(
        f"[load] api={args.api_base} target_qps={args.qps:g} duration={args.duration:g}s "
        f"workers={args.workers} cameras={len(cameras)} scheduled={total} "
        f"track_ratio={args.track_ratio:g} "
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
    window = args.duration  # throughput is measured against the scheduled window
    achieved_qps = state["ok"] / window
    attempted_qps = state["attempted"] / window
    latencies = state["latencies"]
    acceptance = (state["ok"] / state["attempted"] * 100.0) if state["attempted"] else 0.0
    min_qps = 0.8 * args.qps
    qps_ok = achieved_qps >= min_qps
    requests_ok = state["failed"] == 0
    passed = qps_ok and requests_ok

    print()
    print("=== load test report ===")
    print(f"target            : {args.qps:g} QPS for {args.duration:g}s ({total} requests, {args.workers} workers)")
    print(f"wall elapsed      : {elapsed:.2f}s")
    print(
        f"attempted         : {state['attempted']} ({attempted_qps:.2f} req/s)  "
        f"ok={state['ok']} failed={state['failed']} ({acceptance:.2f}% ok)"
    )
    print(f"achieved QPS      : {achieved_qps:.2f} (target {args.qps:g}, threshold {min_qps:g})")
    print(f"accepted/dropped  : {state['accepted']} / {state['dropped']}   events={state['events']}")
    print(
        f"tracked           : {state['tracked']}/{state['attempted']} detections with track_id "
        f"(--track-ratio {args.track_ratio:g})"
    )
    if latencies:
        print(
            f"http latency (ms) : p50={percentile(latencies, 50):.2f} "
            f"p95={percentile(latencies, 95):.2f} p99={percentile(latencies, 99):.2f} "
            f"mean={statistics.mean(latencies):.2f} max={max(latencies):.2f}"
        )
    if state["errors"]:
        print("error samples     :")
        for error in state["errors"]:
            print(f"  - {error}")
    if passed:
        print("RESULT            : PASS")
    else:
        reasons = []
        if not qps_ok:
            reasons.append(f"achieved QPS {achieved_qps:.2f} < 80% of target ({min_qps:g})")
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
