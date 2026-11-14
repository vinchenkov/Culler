const READ_ACTIONS = new Set([
  "/api/environment/get",
  "/api/evaluation/status", "/api/evaluation/inspect", "/api/evaluation/download",
  "/api/projects/vision-open", "/api/projects/vision-draft", "/api/projects/vision-workspace", "/api/agent-profiles/operation-status", "/api/agent-profiles/models", "/api/projects/details", "/api/projects/setup-preview",
]);

export function isReadRequest(path, options = {}) {
  return (options.method ?? "GET").toUpperCase() === "GET" || READ_ACTIONS.has(path);
}

// Successful reads live only in this browser instance.
export function createRequests({ send, now = Date.now, freshness = () => 10000, denied = () => {} }) {
  const entries = new Map();
  let foreground = 0;
  let speculative = false;
  let blocked = false;

  function invalidate(matches = () => true) {
    for (const [key, entry] of entries) {
      if (matches(entry.path)) { entry.invalidated = true; entries.delete(key); }
    }
  }

  async function read(path, options = {}) {
    if (blocked) throw new Error("Dashboard access requires a valid token.");
    const { signal, maxAge = freshness(path), bytes = false, ...request } = options;
    const key = JSON.stringify([path, bytes, request.headers ?? {}]);
    let entry = entries.get(key);
    if (!entry || (!entry.promise && now() - entry.at >= maxAge)) {
      entry = { path, at: 0, promise: null, consumers: 0, cancelled: false, controller: new AbortController() };
      entries.set(key, entry);
      entry.promise = send(path, { ...request, bytes, signal: entry.controller.signal }).then(value => {
        if (entry.cancelled) throw new DOMException("The read has no consumers.", "AbortError");
        if (blocked) throw new Error("Dashboard access requires a valid token.");
        if (entries.get(key) !== entry) return read(path, { ...options, signal: undefined });
        entry.value = value;
        entry.at = now();
        entry.promise = null;
        return value;
      }).catch(error => {
        if (entry.cancelled) throw error;
        if (error.status === 401) { blocked = true; invalidate(); denied(); }
        if (entries.get(key) === entry) entries.delete(key);
        if (!blocked && entry.invalidated) return read(path, { ...options, signal: undefined });
        throw error;
      });
    }
    const result = entry.promise ?? Promise.resolve(entry.value);
    entry.consumers++;
    let released = false;
    const release = () => {
      if (released) return;
      released = true;
      entry.consumers--;
      if (entry.promise && entry.consumers === 0 && entries.get(key) === entry) {
        entry.cancelled = true;
        entries.delete(key);
        entry.controller.abort();
      }
    };
    if (!signal) return result.finally(release);
    return new Promise((resolve, reject) => {
      const abort = () => { release(); reject(signal.reason); };
      signal.addEventListener("abort", abort, { once: true });
      result.then(resolve, reject).finally(() => { signal.removeEventListener("abort", abort); release(); });
      if (signal.aborted) abort();
    });
  }

  async function request(path, options = {}) {
    if (blocked) throw new Error("Dashboard access requires a valid token.");
    const method = (options.method ?? "GET").toUpperCase();
    foreground++;
    try {
      if (method === "GET") return await read(path, options);
      const result = await send(path, options);
      // Delivery acknowledgments affect only the delivery counts.
      if (!isReadRequest(path, options)) invalidate(path.startsWith("/api/outbox/") ? resource => resource === "/api/outbox/lag" : undefined);
      return result;
    } catch (error) {
      if (error.status === 409) invalidate();
      if (error.status === 401) { blocked = true; invalidate(); denied(); }
      throw error;
    } finally { foreground--; }
  }

  async function prefetch(paths, visible = () => true) {
    if (speculative || foreground || !visible() || blocked) return;
    speculative = true;
    try {
      for (const path of paths) {
        if (foreground || !visible()) break;
        try { await read(path); } catch { /* A page visit can retry this read. */ }
      }
    } finally { speculative = false; }
  }

  return { request, invalidate, prefetch };
}
