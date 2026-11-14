import { expect, test } from "bun:test";
import { createServer } from "node:net";
import { networkErrorFromCode, networkErrorFromFetch, networkErrorFromText } from "./network-error";

test("fetch failures without a provider response receive their network class", async () => {
  const refused = await fetch("http://127.0.0.1:1/token", { method: "POST" }).catch((error) => error);
  expect(networkErrorFromFetch(refused)).toBe("connect");
  const server = createServer((socket) => socket.on("data", () => socket.resetAndDestroy())).listen(0, "127.0.0.1");
  await new Promise((resolve) => server.once("listening", resolve));
  try {
    const port = (server.address() as { port: number }).port;
    const reset = await fetch(`http://127.0.0.1:${port}/token`, { method: "POST", body: "{}" }).catch((error) => error);
    expect(networkErrorFromFetch(reset)).toBe("reset");
  } finally {
    server.close();
  }
  expect(networkErrorFromFetch(new DOMException("The operation timed out.", "TimeoutError"))).toBe("timeout");
  expect(networkErrorFromFetch(Object.assign(new Error("certificate has expired"), { code: "CERT_HAS_EXPIRED" }))).toBe("tls");
  expect(networkErrorFromFetch(new Error("unclassified"))).toBe("other");
});

test("harness texts name network failures by code or phrase", () => {
  expect(networkErrorFromText("API Error: Connection refused — a firewall or proxy may be blocking it (ECONNREFUSED)")).toBe("connect");
  expect(networkErrorFromText("API Error: Can't reach the API server — check your internet or DNS (EAI_AGAIN)")).toBe("connect");
  expect(networkErrorFromText("API Error: Connection dropped (ECONNRESET)")).toBe("reset");
  expect(networkErrorFromText("API Error: Connection lost while your computer was asleep")).toBe("reset");
  expect(networkErrorFromText("API Error: Request timed out. Check your internet connection and proxy settings")).toBe("timeout");
  expect(networkErrorFromText("stream error: error sending request for url: error trying to connect: tcp connect error: Connection refused (os error 111)")).toBe("connect");
  expect(networkErrorFromText("API Error: 529 Overloaded")).toBeUndefined();
  expect(networkErrorFromText("API Error: 401 Invalid authentication credentials")).toBeUndefined();
  expect(networkErrorFromCode("ERR_TLS_CERT_ALTNAME_INVALID")).toBe("tls");
  expect(networkErrorFromCode("toString")).toBeUndefined();
});
