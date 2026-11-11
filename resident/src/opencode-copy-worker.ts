import { nativeSnapshot } from "./opencode-snapshot";

self.onmessage = event => {
  try {
    const { source, main, credentials, masks, standalone } = event.data;
    const snapshot = nativeSnapshot(source, main, credentials, masks, standalone);
    postMessage({ snapshot });
  } catch (error) {
    // SQLite errors can include source values. The host receives no raw database content.
    const code = (error as { code?: unknown }).code;
    const reason = typeof code === "string" && /^SQLITE_[A-Z_]+$/.test(code) ? ` (${code})` : "";
    postMessage({ error: `The native database is unavailable, invalid, or exceeds the copy limits${reason}.` });
  }
};
