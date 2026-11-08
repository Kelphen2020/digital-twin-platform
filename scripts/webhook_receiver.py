#!/usr/bin/env python3
"""Tiny webhook receiver for the digital-twin P3.0 notification demo.

Stdlib-only HTTP server used by ``scripts/smoke.sh`` (and handy for manual
demos) to prove that the backend delivers notification webhooks:

1. Accepts ``POST`` requests on any path, parses the JSON body and appends one
   JSON line per request to the ``--out`` file:
   ``{"at": "...", "method": "POST", "path": "/hook", "headers": {...}, "body": {...}}``.
2. Responds ``200 {"ok": true, "received": N}`` so the backend records the
   delivery as successful (``GET /api/notifications`` -> ``ok: true``).
3. Prints a ready line to stdout once listening (smoke polls ``GET /health``
   or reads this line) and exits cleanly on Ctrl-C / SIGTERM.

``--fail-first N`` answers the first N POSTs with HTTP 500 (still recording them
in the JSONL log) and 200 afterwards, so notification retry/backoff behaviour
(P4.0) can be tested deterministically.

Only intended for local demos/tests.

Examples::

    python3 scripts/webhook_receiver.py --port 8099 --out /tmp/webhook.log
    python3 scripts/webhook_receiver.py --port 8099 --out /tmp/webhook.log --fail-first 2
    curl -s http://127.0.0.1:8099/health
    tail -n 3 /tmp/webhook.log

Exit codes: 0 on clean shutdown, 2 on usage/bind errors.
"""

from __future__ import annotations

import argparse
import json
import os
import signal
import socketserver
import sys
import threading
from datetime import datetime, timezone
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

DEFAULT_PORT = 8099
DEFAULT_OUT = "/tmp/webhook.log"
DEFAULT_HOST = "127.0.0.1"

_stop = threading.Event()
_lock = threading.Lock()
_received = 0


def utc_now_rfc3339() -> str:
    return datetime.now(timezone.utc).isoformat(timespec="milliseconds").replace("+00:00", "Z")


def log(message: str) -> None:
    print(message, file=sys.stderr, flush=True)


class WebhookServer(ThreadingHTTPServer):
    """ThreadingHTTPServer without the reverse-DNS lookup in ``server_bind``.

    ``HTTPServer.server_bind`` calls ``socket.getfqdn(host)`` which can block
    for seconds on macOS when reverse DNS/mDNS is slow; the receiver must print
    its ready line immediately so smoke tests can poll it.
    """

    daemon_threads = True
    allow_reuse_address = True

    def server_bind(self):
        socketserver.TCPServer.server_bind(self)
        host, port = self.server_address[:2]
        self.server_name = str(host)
        self.server_port = port


class WebhookHandler(BaseHTTPRequestHandler):
    """Records POST bodies as JSONL and answers 200."""

    server_version = "DSHWebhookReceiver/0.1"

    def _send_json(self, status: int, payload) -> None:
        body = json.dumps(payload, ensure_ascii=False).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        try:
            self.wfile.write(body)
        except (BrokenPipeError, ConnectionResetError):
            pass

    def do_GET(self):  # noqa: N802 - http.server API
        if self.path.split("?")[0] == "/health":
            with _lock:
                received = _received
            fail_first = int(getattr(self.server, "fail_first", 0) or 0)  # type: ignore[attr-defined]
            self._send_json(200, {
                "ok": True,
                "received": received,
                "failed": min(received, fail_first),
                "fail_first": fail_first,
                "time": utc_now_rfc3339(),
            })
            return
        self._send_json(404, {"error": "not_found"})

    def do_POST(self):  # noqa: N802 - http.server API
        global _received
        try:
            length = int(self.headers.get("Content-Length") or 0)
        except (TypeError, ValueError):
            length = 0
        raw = self.rfile.read(length) if length > 0 else b""
        text = raw.decode("utf-8", errors="replace")
        try:
            body = json.loads(text) if text else None
        except ValueError:
            body = text

        with _lock:
            _received += 1
            received = _received
            fail_first = int(getattr(self.server, "fail_first", 0) or 0)  # type: ignore[attr-defined]
            status = 500 if received <= fail_first else 200
            record = {
                "at": utc_now_rfc3339(),
                "method": "POST",
                "path": self.path,
                "status": status,
                "headers": {str(key): str(value) for key, value in self.headers.items()},
                "body": body,
            }
            line = json.dumps(record, ensure_ascii=False)
            out_path = self.server.out_path  # type: ignore[attr-defined]
            try:
                with open(out_path, "a", encoding="utf-8") as handle:
                    handle.write(line + "\n")
            except OSError as exc:
                log(f"[webhook] error: could not append to {out_path}: {exc}")

        if status == 500:
            log(f"[webhook] POST {self.path} from {self.client_address[0]} -> 500 injected failure (#{received})")
            self._send_json(500, {"ok": False, "error": "injected_failure", "received": received})
        else:
            log(f"[webhook] POST {self.path} from {self.client_address[0]} -> 200 (#{received})")
            self._send_json(200, {"ok": True, "received": received})

    def log_message(self, format, *args):  # noqa: A002 - silence default access log
        # Keep stderr for our own one-line-per-delivery logs.
        return


def _on_signal(_signum, _frame) -> None:
    _stop.set()


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="webhook_receiver.py",
        description="Record notification webhook POSTs as JSON lines (P3.0 demo helper).",
        formatter_class=argparse.ArgumentDefaultsHelpFormatter,
    )
    parser.add_argument("--port", type=int, default=DEFAULT_PORT, help="TCP port to listen on")
    parser.add_argument("--host", default=DEFAULT_HOST, help="bind address")
    parser.add_argument("--out", default=DEFAULT_OUT, help="JSONL file to append received requests to")
    parser.add_argument(
        "--fail-first",
        type=int,
        default=0,
        metavar="N",
        help="answer the first N POSTs with HTTP 500 (still logged), then 200 (retry testing)",
    )
    return parser


def main(argv=None) -> int:
    global _received
    args = build_parser().parse_args(argv)
    if not 1 <= args.port <= 65535:
        log(f"[webhook] error: --port must be 1-65535 (got {args.port})")
        return 2
    if args.fail_first < 0:
        log(f"[webhook] error: --fail-first must be >= 0 (got {args.fail_first})")
        return 2

    out_path = os.path.abspath(args.out)
    out_dir = os.path.dirname(out_path)
    if out_dir:
        try:
            os.makedirs(out_dir, exist_ok=True)
        except OSError as exc:
            log(f"[webhook] error: cannot create output directory {out_dir}: {exc}")
            return 2
    try:
        with open(out_path, "a", encoding="utf-8"):
            pass
    except OSError as exc:
        log(f"[webhook] error: cannot open output file {out_path}: {exc}")
        return 2

    _received = sum(1 for _ in open(out_path, encoding="utf-8", errors="replace")) if os.path.exists(out_path) else 0

    server = WebhookServer((args.host, args.port), WebhookHandler)
    server.out_path = out_path  # type: ignore[attr-defined]
    server.fail_first = args.fail_first  # type: ignore[attr-defined]

    signal.signal(signal.SIGINT, _on_signal)
    signal.signal(signal.SIGTERM, _on_signal)

    thread = threading.Thread(target=server.serve_forever, name="webhook-receiver", daemon=True)
    thread.start()
    print(
        f"webhook_receiver ready: http://{args.host}:{args.port} "
        f"(out={out_path}, received={_received}, fail_first={args.fail_first})",
        flush=True,
    )

    while not _stop.wait(0.2):
        pass

    server.shutdown()
    server.server_close()
    thread.join(timeout=2.0)
    log(f"[webhook] stopped: received={_received} out={out_path} exit=0")
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except KeyboardInterrupt:  # safety net if the signal handler was not installed
        sys.exit(0)
