import { expect, test } from "bun:test";
import { ACCESS_MESSAGES, accessPage } from "./agentsview-pages";

test("trace access pages retain nonce protection and safe destination text", async () => {
  const response = accessPage('/sessions/a?msg=<script>"&');
  const page = await response.text();
  const nonce = page.match(/<style nonce="([^"]+)"/)![1]!;
  expect(response.headers.get("content-security-policy")).toBe(
    `default-src 'none'; script-src 'nonce-${nonce}'; style-src 'nonce-${nonce}'; connect-src 'self'; form-action http://127.0.0.1:*; frame-ancestors 'none'; base-uri 'none'`);
  expect(response.headers.get("cache-control")).toBe("no-store");
  expect(response.headers.get("referrer-policy")).toBe("no-referrer");
  expect(page).toContain('/sessions/a?msg=&lt;script&gt;&quot;&amp;');
  expect(page.match(/<script nonce=/g)).toHaveLength(2);
  expect(page).toContain('localStorage.getItem("mc.theme")');
  expect(page).not.toContain("localStorage.setItem");
  expect(page).toContain('authorization: `Bearer ${token}`');
});

test("trace access pages use the light theme unless the operator selects the dark theme", async () => {
  const page = await accessPage("/sessions/trace").text();
  const script = page.match(/<script nonce="[^"]+">([\s\S]*?)<\/script>/)![1]!;
  for (const stored of [null, "sepia", "light", "dark"]) {
    for (const blocked of [false, true]) {
      const root = { dataset: {} as Record<string, string> };
      let follow: (event: { key: string }) => void;
      const storage = { getItem: () => { if (blocked) throw new Error("blocked"); return stored; } };
      new Function("localStorage", "document", "window", script)(storage, { documentElement: root },
        { addEventListener: (_: string, listener: typeof follow) => { follow = listener; } });
      expect(root.dataset.theme).toBe(!blocked && stored === "dark" ? "dark" : "light");
      root.dataset.theme = "other";
      follow!({ key: "mc.theme" });
      expect(root.dataset.theme).toBe(!blocked && stored === "dark" ? "dark" : "light");
    }
  }
});

test("each trace failure retains its message and safe retry action", async () => {
  for (const [failure, message] of Object.entries(ACCESS_MESSAGES)) {
    const response = accessPage("/sessions/trace", { failure: failure as keyof typeof ACCESS_MESSAGES, retry: '/agentsview/open?path="&' });
    const page = await response.text();
    expect(page).toContain(message);
    expect(page).toContain('href="/agentsview/open?path=&quot;&amp;"');
    expect(page).toContain('id="title">Trace access unavailable</h1>');
    expect(page.match(/<script nonce=/g)).toHaveLength(1);
    expect(page).not.toContain('/api/agentsview/access');
  }
});

test("trace palettes match the dashboard stylesheet in both themes", async () => {
  const source = await Bun.file(new URL("./ui/style.css", import.meta.url)).text();
  const sourceBlocks = [source.match(/:root\s*\{([^}]+)\}/)![1]!, source.match(/:root\[data-theme="dark"\][^{]*\{([^}]+)\}/)![1]!];
  const page = await accessPage("/sessions").text();
  const pageBlocks = [page.match(/:root\{([^}]+)\}/)![1]!, page.match(/:root\[data-theme="dark"\]\{([^}]+)\}/)![1]!];
  const roles = {
    paper: "paper", surface: "surface", subtle: "surface-2", ink: "ink", muted: "ink-2", line: "line",
    header: "panel-head", "header-ink": "panel-head-ink", "header-line": "panel-line",
    primary: "action-fill", "primary-ink": "action-ink", "primary-hover": "action-hover", control: "field-line", focus: "focus",
  };
  for (let theme = 0; theme < 2; theme++) {
    for (const [role, token] of Object.entries(roles)) {
      const value = sourceBlocks[theme]!.match(new RegExp(`--${token}:\\s*([^;]+);`))?.[1]?.trim()
        ?? sourceBlocks[0]!.match(new RegExp(`--${token}:\\s*([^;]+);`))![1]!.trim();
      expect(pageBlocks[theme], `${role} in theme ${theme}`).toContain(`--${role}:${value}`);
    }
    expect(pageBlocks[theme]).not.toContain("var(--shade-rgb)");
    expect(pageBlocks[theme]).not.toContain("url(");
  }
});
