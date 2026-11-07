#!/usr/bin/env python3
"""Toggle camera status through the digital-twin REST API (P0 demo helper).

Used to demo P0-3 (camera status changes broadcast over WebSocket): sweeps all
cameras offline one at a time, then sweeps them back online one at a time, with
``--interval`` seconds between PATCH calls.  The API broadcasts a
``camera_status`` message for every successful PATCH (docs/API.md).

Stdlib only.

Examples::

    # toggle every camera offline then back online, 10 s between changes, forever
    python3 services/simulator/run_camera_status.py

    # single full cycle (all offline, then all online) and exit
    python3 services/simulator/run_camera_status.py --once --interval 2

    # restrict to one camera, faster demo pace
    python3 services/simulator/run_camera_status.py --camera CAM-001 --interval 1.5

Exit codes:
    0  finished normally (--once cycle completed) or clean Ctrl-C / SIGTERM
    1  every PATCH failed in the completed cycle
    2  camera inventory unavailable / filter matched nothing / bad arguments
"""

from __future__ import annotations

import argparse
import json
import os
import signal
import sys
import time
import urllib.error
import urllib.request
from datetime import datetime, timezone

DEFAULT_API_BASE = "http://127.0.0.1:8080"
DEFAULT_INTERVAL = 10.0
HTTP_TIMEOUT = 5.0
CAMERAS_PATH = "/api/cameras"

_stop = False


def log(message: str) -> None:
    print(message, file=sys.stderr, flush=True)


def utc_now_rfc3339() -> str:
    return datetime.now(timezone.utc).isoformat(timespec="milliseconds").replace("+00:00", "Z")


class ApiClient:
    """Minimal JSON HTTP client (urllib only)."""

    def __init__(self, api_base: str, timeout: float = HTTP_TIMEOUT):
        self.api_base = api_base.rstrip("/")
        self.timeout = timeout

    def request_json(self, method: str, path: str, payload=None):
        """Return ``(status, body, error)``; status 0 means transport failure."""
        url = self.api_base + path
        headers = {"Accept": "application/json", "User-Agent": "dsh-twin-camera-status/0.1"}
        data = None
        if payload is not None:
            data = json.dumps(payload, ensure_ascii=False).encode("utf-8")
            headers["Content-Type"] = "application/json"
        request = urllib.request.Request(url, data=data, headers=headers, method=method)
        try:
            with urllib.request.urlopen(request, timeout=self.timeout) as response:
                return response.status, decode_body(response.read()), None
        except urllib.error.HTTPError as exc:
            return exc.code, decode_body(exc.read()), None
        except (urllib.error.URLError, TimeoutError, OSError) as exc:
            return 0, None, str(getattr(exc, "reason", exc))


def decode_body(raw: bytes):
    text = raw.decode("utf-8", errors="replace").strip()
    if not text:
        return None
    try:
        return json.loads(text)
    except ValueError:
        return text


def load_cameras(client: ApiClient, attempts: int = 3):
    """GET /api/cameras with retries; raises RuntimeError on failure."""
    last_error = "unknown error"
    for attempt in range(1, attempts + 1):
        status, body, error = client.request_json("GET", CAMERAS_PATH)
        if error is None and status == 200 and isinstance(body, dict) and isinstance(body.get("items"), list):
            return body["items"]
        last_error = error if error else (f"HTTP {status}: {body!r}" if status != 200 else f"unexpected shape: {body!r}")
        log(f"[camera-status] GET {CAMERAS_PATH} attempt {attempt}/{attempts} failed: {last_error}")
        if attempt < attempts:
            time.sleep(attempt * 1.0)
    raise RuntimeError(
        f"could not load cameras from {client.api_base}{CAMERAS_PATH} after {attempts} attempts "
        f"({last_error}). Is the API running? Try: curl -s {client.api_base}/api/health"
    )


def hours_min_sec() -> str:
    return datetime.now().strftime("%H:%M:%S")


def sleep_interruptible(seconds: float) -> None:
    """Sleep in short slices so Ctrl-C stays responsive."""
    deadline = time.monotonic() + seconds
    while not _stop:
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            return
        time.sleep(min(remaining, 0.2))


def patch_status(client: ApiClient, camera: dict, status: str):
    """PATCH /api/cameras/{id} with the requested status.

    Returns ``(updated_camera, error)``; ``updated_camera`` is None on failure.
    """
    camera_id = str(camera.get("id") or "")
    path = f"/api/cameras/{camera_id}"
    http_status, body, error = client.request_json("PATCH", path, {"status": status})
    if error is not None:
        return None, f"transport error: {error}"
    if http_status != 200:
        return None, f"HTTP {http_status}: {body!r}"
    if not isinstance(body, dict):
        return None, f"unexpected response: {body!r}"
    return body, None


def _on_signal(_signum, _frame) -> None:
    global _stop
    _stop = True


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="run_camera_status.py",
        description="Toggle camera status offline/online via PATCH /api/cameras/{id} "
        "(for demoing the WebSocket camera_status broadcast).",
        formatter_class=argparse.ArgumentDefaultsHelpFormatter,
    )
    parser.add_argument(
        "--api-base",
        default=os.environ.get("API_BASE", DEFAULT_API_BASE),
        help="API base URL (or set env API_BASE)",
    )
    parser.add_argument(
        "--interval",
        type=float,
        default=DEFAULT_INTERVAL,
        help="seconds to wait between two status changes",
    )
    parser.add_argument(
        "--once",
        action="store_true",
        help="run one full cycle (all cameras offline, then all online) and exit",
    )
    parser.add_argument(
        "--camera",
        default=None,
        metavar="CODE",
        help="only toggle this camera code, e.g. CAM-001",
    )
    return parser


def main(argv=None) -> int:
    global _stop
    _stop = False

    args = build_parser().parse_args(argv)
    if args.interval < 0:
        log(f"[camera-status] error: --interval must be >= 0 (got {args.interval})")
        return 2

    signal.signal(signal.SIGINT, _on_signal)
    signal.signal(signal.SIGTERM, _on_signal)

    client = ApiClient(args.api_base)
    try:
        cameras = load_cameras(client)
        if not cameras:
            raise RuntimeError("API returned an empty camera list; seed the database first")
        if args.camera:
            wanted = args.camera.strip().upper()
            cameras = [cam for cam in cameras if str(cam.get("code") or "").strip().upper() == wanted]
            if not cameras:
                raise RuntimeError(f"--camera {args.camera!r} matched no camera")
    except RuntimeError as exc:
        log(f"[camera-status] error: {exc}")
        return 2

    log(
        f"[camera-status] api={args.api_base} cameras={len(cameras)} interval={args.interval:g}s "
        f"mode={'once' if args.once else 'loop'}"
    )

    cycle = 0
    total_changes = 0
    total_failures = 0
    while not _stop:
        cycle += 1
        cycle_changes = 0
        cycle_failures = 0
        for status in ("offline", "online"):
            for camera in cameras:
                if _stop:
                    break
                code = camera.get("code") or camera.get("id")
                updated, error = patch_status(client, camera, status)
                if error is not None:
                    cycle_failures += 1
                    total_failures += 1
                    log(f"[{hours_min_sec()}] {code}: PATCH status={status} FAILED ({error})")
                else:
                    previous = camera.get("status")
                    # Keep local inventory in sync so the printed transition is accurate.
                    camera["status"] = updated.get("status", status)
                    cycle_changes += 1
                    total_changes += 1
                    log(
                        f"[{hours_min_sec()}] {code} ({camera.get('id')}): "
                        f"{previous or '?'} -> {updated.get('status', status)}"
                    )
                if _stop:
                    break
                sleep_interruptible(args.interval)
        log(
            f"[camera-status] cycle {cycle} done: changes={cycle_changes} "
            f"failures={cycle_failures}"
        )
        if args.once:
            break
        if _stop:
            break

    log(
        f"[camera-status] stopped: cycles={cycle} changes={total_changes} "
        f"failures={total_failures} reason={'signal' if _stop else 'done'}"
    )
    if args.once and total_changes == 0 and total_failures > 0:
        return 1
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except KeyboardInterrupt:  # safety net
        sys.exit(0)
