"""A browser with the product packet wrapper and a narrow Judge interface."""

from __future__ import annotations

import asyncio
import base64
import datetime as dt
import hashlib
import hmac
import json
from pathlib import Path
import secrets
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


VISIBLE_TEXT = """options => {
  const {all=false} = options || {};
  const viewport = {left:0, top:all ? -Infinity : 0, right:innerWidth, bottom:all ? Infinity : innerHeight};
  const visible = (node, rect) => {
    if (!rect.width || !rect.height) return false;
    let box = {...viewport};
    for (let element = node.parentElement; element; element = element.parentElement) {
      const style = getComputedStyle(element);
      if (style.display === 'none' || style.visibility !== 'visible' || Number(style.opacity) === 0 || style.contentVisibility === 'hidden') return false;
      if (element.tagName === 'DETAILS' && !element.open) {
        const summary = [...element.children].find(child => child.tagName === 'SUMMARY');
        if (!summary || !summary.contains(node)) return false;
      }
      if (/(hidden|clip|auto|scroll)/.test(style.overflowX + style.overflowY)) {
        const bound = element.getBoundingClientRect();
        box.left = Math.max(box.left, bound.left); box.right = Math.min(box.right, bound.right);
        box.top = Math.max(box.top, bound.top); box.bottom = Math.min(box.bottom, bound.bottom);
      }
    }
    return rect.right > box.left && rect.left < box.right && rect.bottom > box.top && rect.top < box.bottom;
  };
  const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
  const words = [];
  const linkWords = new Map(), headingWords = new Map();
  let node;
  while ((node = walker.nextNode())) {
    if (node.parentElement.closest('script,style,template,noscript')) continue;
    const range = document.createRange();
    for (const match of node.textContent.matchAll(/\\S+/g)) {
      range.setStart(node, match.index); range.setEnd(node, match.index + match[0].length);
      if ([...range.getClientRects()].some(rect => visible(node, rect))) {
        words.push(match[0]);
        for (const [selector, entries] of [['a[href^="#"]', linkWords], ['h1,h2,h3,h4,h5,h6', headingWords]]) {
          const element = node.parentElement.closest(selector);
          if (element) { if (!entries.has(element)) entries.set(element, []); entries.get(element).push(match[0]); }
        }
      }
    }
  }
  const links = [...linkWords].map(([element, text]) => ({text:text.join(' '), href:element.getAttribute('href')}));
  const headings = [...headingWords.values()].map(text => text.join(' '));
  return {text:words.join(' '), links, headings, scroll_y:scrollY, viewport:{width:innerWidth,height:innerHeight},
    at_end:scrollY + innerHeight >= document.documentElement.scrollHeight - 2};
}"""


class PacketBrowser:
    """The Judge can inspect visible content, scroll, click, and follow internal links."""

    def __init__(self, source_root: Path, packet_path: Path, output: Path, width: int = 1280, height: int = 900, executable_path: str | None = None):
        self.source_root = Path(source_root)
        self.packet = Path(packet_path)
        self.output = Path(output)
        self.output.mkdir(parents=True, exist_ok=True)
        self.width, self.height = width, height
        self.executable_path = executable_path
        self.trace_path = self.output / "interactions.jsonl"
        self.counter = 0
        self.diagnostics: list[dict] = []
        self.thread = threading.Thread(target=self._run_loop, daemon=True)
        self.ready = threading.Event()
        self.loop = asyncio.new_event_loop()
        self.error = None
        self.visited_headings: set[str] = set()

    def _run_loop(self):
        asyncio.set_event_loop(self.loop)
        try:
            self.loop.run_until_complete(self._start())
        except Exception as error:
            self.error = error
            self.loop.run_until_complete(self._stop())
            self.loop.close()
            self.ready.set()
            return
        self.ready.set()
        self.loop.run_forever()
        self.loop.run_until_complete(self._stop())
        self.loop.close()

    async def _start(self):
        try:
            from playwright.async_api import async_playwright
        except ImportError as error:
            raise RuntimeError("Install the evaluation browser dependencies before browser assessment.") from error
        self.driver = await async_playwright().start()
        self.browser = await self.driver.chromium.launch(**({"executable_path": self.executable_path} if self.executable_path else {}))
        self.context = await self.browser.new_context(viewport={"width": self.width, "height": self.height}, device_scale_factor=1)
        self.page = await self.context.new_page()
        self.page.on("console", lambda message: self.diagnostics.append({"kind": "console", "type": message.type, "text": message.text}))
        self.page.on("pageerror", lambda error: self.diagnostics.append({"kind": "pageerror", "text": str(error)}))
        self.page.on("popup", lambda page: asyncio.create_task(page.close()))
        module = (self.source_root / "dashboard/src/ui/review.js").read_bytes()
        format_module = (self.source_root / "dashboard/src/ui/format.js").read_bytes()
        self.wrapper_hashes = {"dashboard/src/ui/review.js": hashlib.sha256(module).hexdigest(),
                               "dashboard/src/ui/format.js": hashlib.sha256(format_module).hexdigest()}
        packet = self.packet.read_text(encoding="utf-8")
        shell = """<!doctype html><html><head><meta charset="utf-8"><style>
          html,body{margin:0;width:100%;height:100%;overflow:hidden}iframe{width:100%;height:100%;border:0}
          </style></head><body><iframe id="packet" title="Review packet"></iframe><script type="module">
          import {reportDocument, FRAME_SANDBOX} from '/review.js';
          const body=await (await fetch('/packet')).text();
          const frame=document.querySelector('#packet'); frame.sandbox=FRAME_SANDBOX; frame.srcdoc=reportDocument(body);
          window.packetReady=true;
          </script></body></html>"""

        async def route(request):
            url = request.request.url
            if url == "http://packet.eval/":
                await request.fulfill(body=shell, content_type="text/html")
            elif url == "http://packet.eval/review.js":
                await request.fulfill(body=module, content_type="text/javascript")
            elif url == "http://packet.eval/format.js":
                await request.fulfill(body=format_module, content_type="text/javascript")
            elif url == "http://packet.eval/packet":
                await request.fulfill(body=packet, content_type="text/plain")
            else:
                self.diagnostics.append({"kind": "blocked_request", "url": url})
                await request.abort()

        await self.context.route("**/*", route)
        await self.page.goto("http://packet.eval/")
        await self.page.wait_for_function("window.packetReady === true")
        await self.page.frame_locator("#packet").locator("body").wait_for()
        self.frame = next(frame for frame in self.page.frames if frame != self.page.main_frame)
        await self.frame.wait_for_function("document.readyState === 'complete'")
        self.version = self.browser.version
        self.display_counts = await self._display_counts()
        await self._capture("initial", {})

    async def _display_counts(self):
        default = await self.frame.evaluate(VISIBLE_TEXT, {"all": True})
        original = await self.frame.evaluate("() => [...document.querySelectorAll('details')].map(element => element.open)")
        await self.frame.evaluate("() => document.querySelectorAll('details').forEach(element => element.open=true)")
        expanded = await self.frame.evaluate(VISIBLE_TEXT, {"all": True})
        await self.frame.evaluate("states => [...document.querySelectorAll('details')].forEach((element,index) => element.open=states[index])", original)
        return {"default_display": len(default["text"].split()), "complete_expanded_display": len(expanded["text"].split()),
                "expandable_difference": len(expanded["text"].split()) - len(default["text"].split())}

    async def _render_diagnostics(self):
        return await self.frame.evaluate("""() => {
          const section = element => {
            const headings = [...document.querySelectorAll('h1,h2,h3,h4,h5,h6')];
            const heading = headings.filter(item => item.compareDocumentPosition(element) & Node.DOCUMENT_POSITION_FOLLOWING).at(-1);
            return heading ? heading.innerText.trim() : '';
          };
          return {
          horizontal_overflow:document.documentElement.scrollWidth > innerWidth + 2,
          failed_images:[...document.images].filter(image => image.complete && !image.naturalWidth).map(image => ({id:image.id,alt:image.alt,section:section(image)})),
          failed_media:[...document.querySelectorAll('video,audio')].filter(media => media.error).map(media => ({id:media.id,error_code:media.error.code,section:section(media)})),
          collapsed_sections:[...document.querySelectorAll('details:not([open]) h1, details:not([open]) h2')].map(element => element.id)
        }; }""")

    async def _stop(self):
        if hasattr(self, "context"):
            await self.context.close()
        if hasattr(self, "browser"):
            await self.browser.close()
        if hasattr(self, "driver"):
            await self.driver.stop()

    def __enter__(self):
        self.thread.start()
        self.ready.wait(45)
        if self.error:
            raise self.error
        if not self.ready.is_set():
            raise RuntimeError("The evaluation browser did not start.")
        return self

    def __exit__(self, *_args):
        if self.thread.is_alive():
            self.loop.call_soon_threadsafe(self.loop.stop)
            self.thread.join(15)

    async def _capture(self, action: str, arguments: dict) -> dict:
        self.counter += 1
        path = self.output / f"{self.counter:04d}-{action}.png"
        image = await self.page.screenshot(path=str(path))
        visible = await self.frame.evaluate(VISIBLE_TEXT)
        self.visited_headings.update(visible["headings"])
        record = {"at": dt.datetime.now(dt.timezone.utc).isoformat(), "action": action, "arguments": arguments,
                  "screenshot": path.name, "visible": visible}
        with self.trace_path.open("a") as stream:
            stream.write(json.dumps(record) + "\n")
        return {"content": [{"type": "image", "mimeType": "image/png", "data": base64.b64encode(image).decode()},
                            {"type": "text", "text": json.dumps({**visible, "screenshot": path.name})}]}

    async def _call(self, name: str, arguments: dict) -> dict:
        if name == "inspect":
            if arguments:
                raise ValueError("Inspect accepts no arguments.")
        elif name == "scroll":
            pixels = arguments.get("pixels")
            if not isinstance(pixels, (int, float)) or abs(pixels) > self.height * 2:
                raise ValueError("Scroll requires a pixel count within two viewport heights.")
            await self.frame.evaluate("pixels => window.scrollBy(0,pixels)", pixels)
        elif name == "follow_link":
            href = arguments.get("href")
            visible = await self.frame.evaluate(VISIBLE_TEXT)
            if not isinstance(href, str) or href not in [link["href"] for link in visible["links"]]:
                raise ValueError("Follow an internal link from the current visible content.")
            links = self.frame.locator("a")
            clicked = False
            for index in range(await links.count()):
                link = links.nth(index)
                if await link.get_attribute("href") == href and await link.is_visible():
                    await link.click()
                    clicked = True
                    break
            if not clicked:
                raise ValueError("The visible link is no longer available.")
        elif name == "click":
            x, y = arguments.get("x"), arguments.get("y")
            if not isinstance(x, (int, float)) or not isinstance(y, (int, float)) or not (0 <= x < self.width and 0 <= y < self.height):
                raise ValueError("Click requires coordinates inside the packet viewport.")
            await self.page.mouse.click(x, y)
        else:
            raise ValueError("The browser tool is outside the permitted packet interactions.")
        await self.page.wait_for_timeout(80)
        return await self._capture(name, arguments)

    def call(self, name: str, arguments: dict) -> dict:
        return asyncio.run_coroutine_threadsafe(self._call(name, arguments), self.loop).result(timeout=30)

    def report(self) -> dict:
        render = asyncio.run_coroutine_threadsafe(self._render_diagnostics(), self.loop).result(timeout=15)
        return {"browser_version": self.version, "viewport": {"width": self.width, "height": self.height},
                "wrapper_hashes": dict(self.wrapper_hashes),
                "trace_path": str(self.trace_path), "screenshots": [str(path) for path in sorted(self.output.glob("*.png"))],
                "visible_headings": sorted(self.visited_headings), "word_counts": self.display_counts,
                "render": render, "diagnostics": list(self.diagnostics)}


BROWSER_TOOLS = [
    {"name": "inspect", "description": "Inspect the packet viewport through a screenshot and visible text.", "inputSchema": {"type": "object", "properties": {}, "additionalProperties": False}},
    {"name": "scroll", "description": "Scroll the packet by a pixel count within two viewport heights.", "inputSchema": {"type": "object", "properties": {"pixels": {"type": "number"}}, "required": ["pixels"], "additionalProperties": False}},
    {"name": "follow_link", "description": "Follow an internal link from the current visible packet content.", "inputSchema": {"type": "object", "properties": {"href": {"type": "string"}}, "required": ["href"], "additionalProperties": False}},
    {"name": "click", "description": "Click packet coordinates to expand content or operate native media controls.", "inputSchema": {"type": "object", "properties": {"x": {"type": "number"}, "y": {"type": "number"}}, "required": ["x", "y"], "additionalProperties": False}},
]


class BrowserServer:
    def __init__(self, browser: PacketBrowser):
        self.browser = browser
        self.token = secrets.token_urlsafe(32)
        owner = self

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *_args):
                return

            def do_POST(self):
                if self.path != "/tool" or not hmac.compare_digest(self.headers.get("Authorization", ""), "Bearer " + owner.token):
                    self.send_error(403)
                    return
                length = int(self.headers.get("Content-Length", "0"))
                if length <= 0 or length > 4096:
                    self.send_error(413)
                    return
                try:
                    request = json.loads(self.rfile.read(length))
                    result = owner.browser.call(request["name"], request.get("arguments", {}))
                except Exception as error:
                    result = {"isError": True, "content": [{"type": "text", "text": str(error)}]}
                data = json.dumps(result).encode()
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(data)))
                self.end_headers()
                self.wfile.write(data)

        self.server = ThreadingHTTPServer(("0.0.0.0", 0), Handler)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)

    def __enter__(self):
        self.thread.start()
        return self

    @property
    def url(self):
        return f"http://host.docker.internal:{self.server.server_port}/tool"

    def __exit__(self, *_args):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join()
