#!/usr/bin/env python3
"""Digital-twin detection simulator (P0).

Plays the role of the AI inference service for the digital-twin demo:

1. On start it loads the camera inventory from ``GET /api/cameras`` and keeps
   only the cameras it will report against.
2. It synthesizes detections (weighted classes, confidence, bbox, world
   position in local ENU metres) and posts them in per-camera batches to
   ``POST /api/ingest/detections`` at a configurable detection rate.
3. The API rule engine turns qualifying detections into events and broadcasts
   them over WebSocket (see ``docs/API.md``).

The process is stdlib-only (Python 3.9+).  Periodic stats go to stderr every
5 seconds; stdout is reserved for ``--dry-run`` payloads.

Examples::

    python3 services/simulator/simulator.py                        # mixed, 5 det/s, forever
    python3 services/simulator/simulator.py --duration 30          # run 30 s then exit
    python3 services/simulator/simulator.py --qps 20 --camera CAM-001
    python3 services/simulator/simulator.py --scenario alarm --seed 42
    python3 services/simulator/simulator.py --dry-run --duration 2 # print payloads, no POST

Exit codes:
    0  normal completion (--duration elapsed) or clean Ctrl-C / SIGTERM
    2  camera inventory unavailable or --camera filter matched nothing
    3  gave up after too many consecutive ingest failures
"""

from __future__ import annotations

import argparse
import json
import math
import os
import random
import signal
import sys
import time
import urllib.error
import urllib.request
from datetime import datetime, timezone

DEFAULT_API_BASE = "http://127.0.0.1:8080"
DEFAULT_QPS = 5.0
STATS_INTERVAL = 5.0          # seconds between stderr stat lines
HTTP_TIMEOUT = 5.0            # seconds per HTTP request
CAMERAS_PATH = "/api/cameras"
INGEST_PATH = "/api/ingest/detections"
MAX_LATENCY_SAMPLES = 5000    # rolling window for percentile latency
MAX_CONSECUTIVE_ERRORS = 50   # abort if the API stays unreachable this long

# Weighted class distribution for the default "mixed" scenario (docs/API.md).
CLASS_WEIGHTS_MIXED = [
    ("person", 0.50),
    ("vehicle", 0.25),
    ("intrusion", 0.15),
    ("smoke", 0.05),
    ("no_helmet", 0.05),
]

# Named scenario profiles.  Any single class name is also accepted as a
# scenario and forces that class for every detection.
SCENARIO_PROFILES = {
    "mixed": CLASS_WEIGHTS_MIXED,
    "normal": [("person", 0.60), ("vehicle", 0.40)],
    "alarm": [("intrusion", 0.50), ("smoke", 0.25), ("no_helmet", 0.25)],
}
SINGLE_CLASSES = ("person", "vehicle", "intrusion", "smoke", "no_helmet")
SCENARIO_CHOICES = tuple(SCENARIO_PROFILES) + SINGLE_CLASSES

_stop = False  # set by the SIGINT/SIGTERM handler


# ---------------------------------------------------------------------------
# small helpers
# ---------------------------------------------------------------------------

def log(message: str) -> None:
    """Write one line to stderr (stdout is reserved for --dry-run payloads)."""
    print(message, file=sys.stderr, flush=True)


def utc_now_rfc3339() -> str:
    """Current UTC time as an RFC3339 string with millisecond precision."""
    return datetime.now(timezone.utc).isoformat(timespec="milliseconds").replace("+00:00", "Z")


def percentile(values, pct: float) -> float:
    """Nearest-rank percentile (keeps percentile calculation dependency-free)."""
    if not values:
        return 0.0
    ordered = sorted(values)
    rank = max(1, math.ceil(pct / 100.0 * len(ordered))) - 1
    return ordered[min(rank, len(ordered) - 1)]


def scenario_weights(name: str):
    """Resolve --scenario to a list of (class, weight) pairs."""
    if name in SCENARIO_PROFILES:
        return SCENARIO_PROFILES[name]
    if name in SINGLE_CLASSES:
        return [(name, 1.0)]
    raise ValueError(f"unknown scenario: {name}")


def pick_class(rng: random.Random, weights) -> str:
    """Draw a detection class from a weighted distribution."""
    roll = rng.random()
    cumulative = 0.0
    for name, weight in weights:
        cumulative += weight
        if roll <= cumulative:
            return name
    return weights[-1][0]


# ---------------------------------------------------------------------------
# HTTP client (urllib only)
# ---------------------------------------------------------------------------

class ApiClient:
    """Minimal JSON HTTP client that never raises for transport-level errors."""

    def __init__(self, api_base: str, timeout: float = HTTP_TIMEOUT):
        self.api_base = api_base.rstrip("/")
        self.timeout = timeout

    def request_json(self, method: str, path: str, payload=None):
        """Perform one request.

        Returns ``(status, body, latency_ms, error)`` where ``status == 0``
        means a transport-level failure (connection refused, timeout, ...),
        ``body`` is the decoded JSON (or raw text when not JSON), and
        ``error`` is a human-readable string for transport failures.
        """
        url = self.api_base + path
        headers = {"Accept": "application/json", "User-Agent": "dsh-twin-simulator/0.1"}
        data = None
        if payload is not None:
            data = json.dumps(payload, ensure_ascii=False).encode("utf-8")
            headers["Content-Type"] = "application/json"
        request = urllib.request.Request(url, data=data, headers=headers, method=method)
        started = time.perf_counter()
        try:
            with urllib.request.urlopen(request, timeout=self.timeout) as response:
                raw = response.read()
                latency_ms = (time.perf_counter() - started) * 1000.0
                return response.status, decode_body(raw), latency_ms, None
        except urllib.error.HTTPError as exc:  # 4xx/5xx still carry a body
            raw = exc.read()
            latency_ms = (time.perf_counter() - started) * 1000.0
            return exc.code, decode_body(raw), latency_ms, None
        except (urllib.error.URLError, TimeoutError, OSError) as exc:
            latency_ms = (time.perf_counter() - started) * 1000.0
            return 0, None, latency_ms, str(getattr(exc, "reason", exc))


def decode_body(raw: bytes):
    """Decode an HTTP body as JSON when possible, else as UTF-8 text."""
    text = raw.decode("utf-8", errors="replace").strip()
    if not text:
        return None
    try:
        return json.loads(text)
    except ValueError:
        return text


def load_cameras(client: ApiClient, attempts: int = 3, retry_delay: float = 1.0):
    """GET /api/cameras with up to ``attempts`` tries; raises RuntimeError on failure."""
    last_error = "unknown error"
    for attempt in range(1, attempts + 1):
        status, body, _latency, error = client.request_json("GET", CAMERAS_PATH)
        if error is None and status == 200 and isinstance(body, dict) and isinstance(body.get("items"), list):
            return body["items"]
        if error is not None:
            last_error = f"HTTP transport error: {error}"
        elif status != 200:
            last_error = f"HTTP {status}: {body!r}"
        else:
            last_error = f"unexpected response shape: {body!r}"
        log(f"[simulator] GET {CAMERAS_PATH} attempt {attempt}/{attempts} failed: {last_error}")
        if attempt < attempts:
            time.sleep(retry_delay * attempt)
    raise RuntimeError(
        f"could not load cameras from {client.api_base}{CAMERAS_PATH} after {attempts} attempts "
        f"({last_error}). Is the API running? Try: curl -s {client.api_base}/api/health"
    )


# ---------------------------------------------------------------------------
# synthetic detection generation
# ---------------------------------------------------------------------------

def build_bbox(rng: random.Random):
    """Random normalized bbox [x1, y1, x2, y2] inside the frame."""
    x1 = rng.uniform(0.08, 0.68)
    y1 = rng.uniform(0.10, 0.65)
    width = rng.uniform(0.06, 0.22)
    height = rng.uniform(0.10, 0.30)
    return [
        round(x1, 3),
        round(y1, 3),
        round(min(0.99, x1 + width), 3),
        round(min(0.99, y1 + height), 3),
    ]


def sample_world(rng: random.Random, camera: dict):
    """Random world point 10-80 m in front of the camera along its heading.

    Local ENU metres: x = east, y = north, z = up.  ``heading`` is degrees,
    north = 0, clockwise positive (docs/API.md), so the forward unit vector is
    (sin(heading), cos(heading)).  A few degrees of angular jitter and ~1 m of
    positional jitter keep successive points spread inside the frustum.
    """
    heading = float(camera.get("heading") or 0.0)
    distance = rng.uniform(10.0, 80.0)
    angle = math.radians(heading + rng.uniform(-4.0, 4.0))
    x = float(camera.get("x") or 0.0) + distance * math.sin(angle) + rng.uniform(-1.0, 1.0)
    y = float(camera.get("y") or 0.0) + distance * math.cos(angle) + rng.uniform(-1.0, 1.0)
    return {"x": round(x, 3), "y": round(y, 3), "z": 0.0}


def build_payload(rng: random.Random, camera: dict, weights) -> dict:
    """Create one ingest request: 1-3 detections, all for the same camera."""
    detections = []
    for _ in range(rng.randint(1, 3)):
        detections.append(
            {
                "class": pick_class(rng, weights),
                # Confidence is spread 0.45-0.99 on purpose: the API drops
                # everything below 0.6 (rule #1 in docs/API.md), so the
                # accepted/dropped counters are exercised.
                "confidence": round(rng.uniform(0.45, 0.99), 3),
                "bbox": build_bbox(rng),
                "world": sample_world(rng, camera),
            }
        )
    return {
        "camera_code": camera.get("code"),
        "ts": utc_now_rfc3339(),
        "detections": detections,
    }


# ---------------------------------------------------------------------------
# stats
# ---------------------------------------------------------------------------

class Stats:
    """Cumulative counters for one simulator run (single-threaded on purpose)."""

    def __init__(self):
        self.sent = 0          # detections generated
        self.posts = 0         # ingest HTTP requests attempted
        self.http_errors = 0   # non-202 / transport failures
        self.accepted = 0      # detections accepted by the API (per response)
        self.dropped = 0       # detections dropped by API rules
        self.events = 0        # events emitted by the rule engine
        self.latencies = []    # rolling ingest latency samples, ms

    def record_dry(self, detections: int) -> None:
        self.sent += detections

    def record_post(self, detections: int, status: int, body) -> None:
        self.posts += 1
        self.sent += detections
        if status == 202 and isinstance(body, dict):
            self.accepted += int(body.get("accepted") or 0)
            self.dropped += int(body.get("dropped") or 0)
            self.events += len(body.get("events") or [])
        else:
            self.http_errors += 1

    def record_latency(self, latency_ms: float) -> None:
        self.latencies.append(latency_ms)
        if len(self.latencies) > MAX_LATENCY_SAMPLES:
            del self.latencies[:-MAX_LATENCY_SAMPLES]


def format_stats(stats: Stats, elapsed: float, dry_run: bool, tag: str = "stats") -> str:
    """Render one stats line."""
    p50 = percentile(stats.latencies, 50)
    p95 = percentile(stats.latencies, 95)
    p99 = percentile(stats.latencies, 99)
    rate = (stats.sent / elapsed) if elapsed > 0 else 0.0
    parts = [
        f"[{tag}]",
        f"elapsed={elapsed:6.1f}s",
        f"sent={stats.sent}",
        f"posts={stats.posts}",
        f"http_errors={stats.http_errors}",
        f"accepted={stats.accepted}",
        f"dropped={stats.dropped}",
        f"events={stats.events}",
        f"rate={rate:.2f}det/s",
        f"p50={p50:.2f}ms",
        f"p95={p95:.2f}ms",
        f"p99={p99:.2f}ms",
    ]
    if dry_run:
        parts.insert(2, "dry_run=true")
    return " ".join(parts)


# ---------------------------------------------------------------------------
# run loop
# ---------------------------------------------------------------------------

def _on_signal(signum, _frame) -> None:
    global _stop
    _stop = True


def select_cameras(cameras, camera_filter):
    """Filter the inventory by --camera (code, case-insensitive)."""
    if not camera_filter:
        return list(cameras)
    wanted = str(camera_filter).strip().upper()
    selected = [cam for cam in cameras if str(cam.get("code") or "").strip().upper() == wanted]
    if not selected:
        available = ", ".join(str(cam.get("code")) for cam in cameras) or "<none>"
        raise RuntimeError(f"--camera {camera_filter!r} matched no camera. Available codes: {available}")
    return selected


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="simulator.py",
        description="Generate synthetic detections and POST them to the digital-twin ingest API.",
        formatter_class=argparse.ArgumentDefaultsHelpFormatter,
    )
    parser.add_argument(
        "--api-base",
        default=os.environ.get("API_BASE", DEFAULT_API_BASE),
        help="API base URL (or set env API_BASE)",
    )
    parser.add_argument(
        "--qps",
        type=float,
        default=DEFAULT_QPS,
        help="detection generation rate, detections/second total across cameras "
        "(each POST batches 1-3 detections of the same camera)",
    )
    parser.add_argument(
        "--duration",
        type=float,
        default=0.0,
        help="seconds to run; 0 means run until Ctrl-C",
    )
    parser.add_argument(
        "--camera",
        default=None,
        metavar="CODE",
        help="only report detections for this camera code, e.g. CAM-001",
    )
    parser.add_argument("--seed", type=int, default=None, help="random seed for reproducible traffic")
    parser.add_argument(
        "--scenario",
        default="mixed",
        choices=SCENARIO_CHOICES,
        help="class mix: mixed/normal/alarm, or a single class name",
    )
    parser.add_argument(
        "--dry-run",
        action="store_true",
        help="print generated ingest payloads to stdout instead of POSTing them",
    )
    return parser


def main(argv=None) -> int:
    global _stop
    _stop = False

    args = build_parser().parse_args(argv)
    if args.qps <= 0:
        log(f"[simulator] error: --qps must be > 0 (got {args.qps})")
        return 2
    if args.duration < 0:
        log(f"[simulator] error: --duration must be >= 0 (got {args.duration})")
        return 2

    signal.signal(signal.SIGINT, _on_signal)
    signal.signal(signal.SIGTERM, _on_signal)
    if args.dry_run and hasattr(signal, "SIGPIPE"):
        # Dry-run writes payloads to stdout; if the reader closes the pipe
        # (e.g. `... --dry-run | head`), die silently like a normal Unix tool
        # instead of printing a BrokenPipeError traceback at shutdown.
        signal.signal(signal.SIGPIPE, signal.SIG_DFL)

    client = ApiClient(args.api_base)
    log(
        f"[simulator] api={args.api_base} qps={args.qps:g} duration="
        f"{'forever' if args.duration == 0 else f'{args.duration:g}s'} "
        f"scenario={args.scenario} dry_run={args.dry_run} camera={args.camera or '*'}"
    )

    try:
        cameras = load_cameras(client)
        if not cameras:
            raise RuntimeError("API returned an empty camera list; seed the database first")
        cameras = select_cameras(cameras, args.camera)
    except RuntimeError as exc:
        log(f"[simulator] error: {exc}")
        return 2

    weights = scenario_weights(args.scenario)
    rng = random.Random(args.seed)
    stats = Stats()

    log(
        f"[simulator] reporting for {len(cameras)} camera(s): "
        + ", ".join(str(cam.get("code")) for cam in cameras)
    )

    started = time.monotonic()
    deadline = started + args.duration if args.duration > 0 else None
    next_tick = started
    last_report = started
    consecutive_errors = 0
    reason = "duration_elapsed"
    exit_code = 0

    while True:
        if _stop:
            reason = "signal"
            break

        now = time.monotonic()
        if deadline is not None and now >= deadline:
            reason = "duration_elapsed"
            break

        if now < next_tick:
            # Sleep in short slices so Ctrl-C and the deadline stay responsive.
            time.sleep(min(next_tick - now, 0.2))
            now = time.monotonic()
            if now - last_report >= STATS_INTERVAL:
                log(format_stats(stats, now - started, args.dry_run))
                last_report = now
            continue

        camera = rng.choice(cameras)
        payload = build_payload(rng, camera, weights)
        # Count the detections actually in the batch (build_payload draws 1-3).
        batch_size = len(payload["detections"])

        # Pace by detections generated, not by requests posted.
        next_tick += batch_size / args.qps
        if next_tick < now:
            next_tick = now + batch_size / args.qps  # never burst-catch-up

        if args.dry_run:
            print(json.dumps(payload, ensure_ascii=False), flush=True)
            stats.record_dry(batch_size)
        else:
            status, body, latency_ms, error = client.request_json("POST", INGEST_PATH, payload)
            stats.record_latency(latency_ms)
            stats.record_post(batch_size, status, body)
            if status == 202:
                consecutive_errors = 0
            else:
                consecutive_errors += 1
                # Rate-limit error chatter: first failure, then every 20th.
                if consecutive_errors == 1 or consecutive_errors % 20 == 0:
                    detail = error if error else f"HTTP {status}"
                    log(
                        f"[simulator] ingest error #{consecutive_errors} for "
                        f"{camera.get('code')}: {detail} body={body!r}"
                    )
                if consecutive_errors >= MAX_CONSECUTIVE_ERRORS:
                    log(
                        f"[simulator] error: {consecutive_errors} consecutive ingest failures; "
                        "is the API still running? Stopping."
                    )
                    reason = "too_many_errors"
                    exit_code = 3
                    break

        now = time.monotonic()
        if now - last_report >= STATS_INTERVAL:
            log(format_stats(stats, now - started, args.dry_run))
            last_report = now

    elapsed = time.monotonic() - started
    log(format_stats(stats, elapsed, args.dry_run, tag="summary"))
    log(
        f"[simulator] stopped: reason={reason} elapsed={elapsed:.1f}s "
        f"generated={stats.sent} posts={stats.posts} events={stats.events} exit={exit_code}"
    )
    return exit_code


if __name__ == "__main__":
    try:
        sys.exit(main())
    except KeyboardInterrupt:  # pragma: no cover - safety net if signal handler not installed
        sys.exit(0)
