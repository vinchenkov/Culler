import { describe, expect, test } from "bun:test";
import * as https from "node:https";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { spawnSync } from "node:child_process";
import { DestinationError, buildDestinationPolicy, canonicalDestination, providerTransport } from "./provider-transport";

const policy = { portAllowlist: [443, 8443, 18443] };

function code(body: () => void): string {
  try {
    body();
    return "accepted";
  } catch (error) {
    return error instanceof DestinationError ? error.code : `thrown:${String(error)}`;
  }
}

describe("canonicalDestination", () => {
  test("accepts canonical forms", () => {
    for (const raw of [
      "https://api.example.com/v1/x",
      "https://xn--caf-dma.com/",
      "https://127.0.0.1/",
      "https://[::1]/",
      "https://example.com:8443/p",
    ]) {
      expect(code(() => canonicalDestination(raw, policy))).toBe("accepted");
    }
  });
  test("rejects the parser-difference surface (§2.1)", () => {
    const cases: Array<[string, string]> = [
      ["https://0x7f.0.0.1/", "ipv4_form"],
      ["https://0177.0.0.1/", "ipv4_form"],
      ["https://2130706433/", "ipv4_form"],
      ["https://10.0.0.01/", "ipv4_form"],
      ["https://1.2.3.4.5/", "unparseable"],
      ["http://api.example.com/", "scheme"],
      ["https://user@api.example.com/", "userinfo"],
      ["https://api.example.com:abc/", "unparseable"],
      ["https://EXAMPLE.com/", "accepted"], // single-parser case folding is the canonical form
      ["https://EXAMPLE.com:444/", "port_denied"],
      ["https://exa mple.com/", "unparseable"],
      ["https://café.com/", "non_canonical_host"], // unicode U-label is not its own A-label
      ["https://[::ffff:127.0.0.1]/", "accepted"], // mapped v4 judged with v4 rules
      ["https://[0:0:0:0:0:ffff:127:0:1]/", "unparseable"],
      ["https://[fe80::1%25en0]/", "unparseable"],
      ["https://[::FFEE]/", "non_canonical_host"], // uppercase hex is not canonical
    ];
    for (const [raw, want] of cases) expect(code(() => canonicalDestination(raw, policy))).toBe(want);
  });
  test("trailing dot normalizes like the single parser does", () => {
    expect(code(() => canonicalDestination("https://example.com./", policy))).toBe("accepted");
    expect(canonicalDestination("https://example.com./", policy).host).toBe("example.com");
  });
});

describe("transport coverage (PS §2.7 static half)", () => {
  test("provider files reach the network only through the policy transport", async () => {
    const { readdirSync, readFileSync } = await import("node:fs");
    const { join } = await import("node:path");
    const sources = readdirSync(import.meta.dir)
      .filter(name => name.startsWith("provider-") && name.endsWith(".ts") && !name.endsWith(".test.ts"))
      .map(name => ({ name, text: readFileSync(join(import.meta.dir, name), "utf8") }));
    expect(sources.length).toBeGreaterThan(3);
    for (const { name, text } of sources) {
      if (name === "provider-transport.ts") {
        expect(text.includes("https.request(")).toBe(true);
        continue;
      }
      // A new provider egress construct must pass through this gate: no raw
      // client sockets and no ambient-fetch calls (the Bun.serve `fetch:`
      // handler is server-side and is excluded by the await-call shape).
      expect(text).not.toMatch(/https?\.request\(|https?\.get\(|net\.connect|net\.createConnection|tls\.connect|dgram|await fetch\(|globalThis\.fetch/);
    }
    const proxy = sources.find(entry => entry.name === "provider-proxy.ts");
    expect(proxy?.text).toContain("providerTransport({ portAllowlist: [443] })");
  });
});

describe("destination policy build", () => {
  test("refuses malformed or host-bit cidr entries", () => {
    expect(code(() => buildDestinationPolicy({ portAllowlist: [443], allowCidrs: ["10.0.0.0/8"] }))).toBe("accepted");
    expect(code(() => buildDestinationPolicy({ portAllowlist: [443], allowCidrs: ["10.0.0.1/8"] }))).toBe("cidr_entry");
    expect(code(() => buildDestinationPolicy({ portAllowlist: [443], allowCidrs: ["0x0a000000/8"] }))).toBe("cidr_entry");
    expect(code(() => buildDestinationPolicy({ portAllowlist: [443], allowCidrs: ["::ffff:10.0.0.0/104"] }))).toBe("accepted");
  });
});

async function certificates(): Promise<{ ca: string; cert: string; key: string }> {
  const root = await mkdtemp(join(tmpdir(), "culler-transport-tls-"));
  try {
    const source = join(root, "main.go");
    await writeFile(source, `package main
import("crypto/ecdsa";"crypto/elliptic";"crypto/rand";"crypto/x509";"crypto/x509/pkix";"encoding/json";"encoding/pem";"math/big";"net";"os";"time")
func main(){
 key,_:=ecdsa.GenerateKey(elliptic.P256(),rand.Reader)
 caKey,_:=ecdsa.GenerateKey(elliptic.P256(),rand.Reader)
 now:=time.Now()
 ca:=&x509.Certificate{SerialNumber:big.NewInt(1),Subject:pkix.Name{CommonName:"Culler transport test CA"},NotBefore:now.Add(-24*time.Hour),NotAfter:now.Add(24*time.Hour),IsCA:true,BasicConstraintsValid:true,KeyUsage:x509.KeyUsageCertSign}
 caBytes,_:=x509.CreateCertificate(rand.Reader,ca,ca,&caKey.PublicKey,caKey)
 c:=&x509.Certificate{SerialNumber:big.NewInt(2),Subject:pkix.Name{CommonName:"localhost"},DNSNames:[]string{"localhost"},IPAddresses:[]net.IP{net.ParseIP("127.0.0.1")},NotBefore:now.Add(-2*time.Hour),NotAfter:now.Add(2*time.Hour),KeyUsage:x509.KeyUsageDigitalSignature,ExtKeyUsage:[]x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
 b,_:=x509.CreateCertificate(rand.Reader,c,ca,&key.PublicKey,caKey)
 raw,_:=x509.MarshalPKCS8PrivateKey(key)
 json.NewEncoder(os.Stdout).Encode(map[string]string{"ca":string(pem.EncodeToMemory(&pem.Block{Type:"CERTIFICATE",Bytes:caBytes})),"key":string(pem.EncodeToMemory(&pem.Block{Type:"PRIVATE KEY",Bytes:raw})),"cert":string(pem.EncodeToMemory(&pem.Block{Type:"CERTIFICATE",Bytes:b}))})
}
`, { mode: 0o600 });
    const result = spawnSync("mise", ["exec", "--", "go", "run", source], { encoding: "utf8", timeout: 30_000 });
    if (result.status !== 0) throw new Error(`transport test certificates failed: ${result.stderr}`);
    return JSON.parse(result.stdout);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
}

describe("providerTransport", () => {
  test("dials the canonical host, never reuses, never follows redirects", async () => {
    const tls = await certificates();
    let connections = 0;
    const server = https.createServer({ cert: tls.cert, key: tls.key }, (request, response) => {
      connections++;
      if (request.url === "/redirect") {
        response.writeHead(302, { location: "https://elsewhere.invalid/" });
        response.end();
        return;
      }
      response.writeHead(200, { "content-type": "application/json" });
      response.end(JSON.stringify({ path: request.url }));
    });
    await new Promise<void>(resolve => server.listen(0, "127.0.0.1", resolve));
    const port = (server.address() as { port: number }).port;
    const transport = providerTransport({ portAllowlist: [port] }, { ca: tls.ca });
    try {
      const first = await transport(`https://127.0.0.1:${port}/a`, { method: "POST", body: "hello" });
      const bodyA = await first.json() as { path: string };
      expect(first.status).toBe(200);
      expect(bodyA.path).toBe("/a");
      const second = await transport(`https://127.0.0.1:${port}/b`);
      expect((await second.json() as { path: string }).path).toBe("/b");
      // §2.3/§2.4: pooling=off — every request re-runs the whole chain.
      expect(connections).toBe(2);
      const redirected = await transport(`https://127.0.0.1:${port}/redirect`);
      expect(redirected.status).toBe(302);
      expect(redirected.headers.get("location")).toBe("https://elsewhere.invalid/");
      void redirected.body?.cancel();
    } finally {
      server.close();
    }
  }, 30_000);

  test("the connect-time filter rejects resolved addresses outside the entry (§2.2)", async () => {
    const tls = await certificates();
    const server = https.createServer({ cert: tls.cert, key: tls.key }, (_request, response) => {
      response.writeHead(200);
      response.end("{}");
    });
    await new Promise<void>(resolve => server.listen(0, "127.0.0.1", resolve));
    const port = (server.address() as { port: number }).port;
    try {
      const denied = providerTransport({ portAllowlist: [port], allowCidrs: ["10.0.0.0/8"] }, { ca: tls.ca });
      await expect(denied(`https://127.0.0.1:${port}/`)).rejects.toThrow("destination_no_permitted_address");
      const allowed = providerTransport({ portAllowlist: [port], allowCidrs: ["127.0.0.0/8"] }, { ca: tls.ca });
      const response = await allowed(`https://127.0.0.1:${port}/ok`);
      expect(response.status).toBe(200);
    } finally {
      server.close();
    }
  }, 30_000);

  test("refuses request forms outside the provider traffic shape", async () => {
    const transport = providerTransport({ portAllowlist: [443] });
    await expect(transport("https://api.example.com/", { method: "TRACE" })).rejects.toThrow("destination_method_form");
    await expect(transport(new Request("https://api.example.com/"), {})).rejects.toThrow("destination_url_form");
  });

  test("abort cancels the in-flight request", async () => {
    const tls = await certificates();
    const server = https.createServer({ cert: tls.cert, key: tls.key }, (_request, response) => {
      // A promised body that never completes: closing the connection early is
      // a truncation the parser can see, so the reader must see a failure.
      response.writeHead(200, { "content-type": "application/json", "content-length": "1000" });
      response.write("x".repeat(16));
    });
    await new Promise<void>(resolve => server.listen(0, "127.0.0.1", resolve));
    const port = (server.address() as { port: number }).port;
    const transport = providerTransport({ portAllowlist: [port] }, { ca: tls.ca });
    const controller = new AbortController();
    try {
      const response = await transport(`https://127.0.0.1:${port}/slow`, { signal: controller.signal });
      const reader = response.body?.getReader();
      if (!reader) throw new Error("no body");
      const first = await reader.read();
      expect(first.done).toBe(false);
      controller.abort();
      await expect(reader.read()).rejects.toThrow();
      server.closeAllConnections();
    } finally {
      server.close();
    }
  }, 30_000);
});
