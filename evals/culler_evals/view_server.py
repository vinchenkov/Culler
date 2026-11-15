"""A loopback view of the durable experiment records."""

from __future__ import annotations

import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
from pathlib import Path
import re
import secrets
import threading
from urllib.parse import urlsplit

from .records import RecordStore
from .viewer import render_view, view_payload


class LiveViewer:
    def __init__(self, experiment: Path, *, port: int = 0, experiment_id: str | None = None):
        self.experiment = Path(experiment).resolve()
        with RecordStore(self.experiment, experiment_id=experiment_id, read_only=True) as store:
            if not store.read_manifest("protocol"):
                raise ValueError("Use an existing experiment directory with a frozen protocol.")
            self.experiment_id = store.experiment_id
        self.prefix = "/" + secrets.token_urlsafe(24) + "/"
        owner = self

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *_):
                pass

            def reply(self, status, data, content_type):
                self.send_response(status)
                self.send_header("Content-Type", content_type)
                self.send_header("Content-Length", str(len(data)))
                self.send_header("Cache-Control", "no-store")
                self.send_header("X-Content-Type-Options", "nosniff")
                self.send_header("Referrer-Policy", "no-referrer")
                self.send_header("Content-Security-Policy", "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'self'; img-src 'self' blob: data:; frame-src 'self' blob:")
                self.end_headers()
                self.wfile.write(data)

            def do_GET(self):
                route = urlsplit(self.path).path
                expected_host = f"127.0.0.1:{owner.server.server_port}"
                if self.headers.get("Host") != expected_host or not route.startswith(owner.prefix):
                    return self.reply(404, b"The view address is unknown.", "text/plain; charset=utf-8")
                resource = route[len(owner.prefix):]
                try:
                    with RecordStore(owner.experiment, experiment_id=owner.experiment_id, read_only=True) as store:
                        if resource in ("", "index.html"):
                            return self.reply(200, render_view(view_payload(store, live=True)).encode(), "text/html; charset=utf-8")
                        if resource == "records.json":
                            return self.reply(200, json.dumps(view_payload(store, live=True)).encode(), "application/json")
                        if re.fullmatch(r"blobs/[0-9a-f]{64}", resource):
                            content_hash = resource.split("/")[1]
                            raw = store.read_blob({"sha256": content_hash})
                            if hashlib.sha256(raw).hexdigest() != content_hash:
                                raise RuntimeError("The artifact content hash differs from its address.")
                            return self.reply(200, raw, "application/octet-stream")
                except (RuntimeError, OSError, ValueError) as error:
                    return self.reply(503, str(error).encode(), "text/plain; charset=utf-8")
                self.reply(404, b"The requested artifact is absent.", "text/plain; charset=utf-8")

        self.server = ThreadingHTTPServer(("127.0.0.1", port), Handler)
        self.server.daemon_threads = True
        self.url = f"http://127.0.0.1:{self.server.server_port}{self.prefix}"
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)

    def __enter__(self):
        self.thread.start()
        return self

    def __exit__(self, *_):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join()

    def wait(self):
        try:
            while self.thread.is_alive():
                self.thread.join(1)
        except KeyboardInterrupt:
            return
