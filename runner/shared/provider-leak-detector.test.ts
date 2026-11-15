import { expect, test } from "bun:test";
import { gzipSync } from "node:zlib";
import { detectArchiveLeaks, detectLeaks, requireNoLeaks } from "../../tests/provider-proxy/leak-detector";

function archive(body: Buffer): Buffer {
  const header = Buffer.alloc(512);
  header.write("layer.tar.gz");
  header.write(body.length.toString(8).padStart(11, "0") + "\0", 124);
  header.write("ustar", 257);
  return Buffer.concat([header, body, Buffer.alloc((512 - body.length % 512) % 512 + 1024)]);
}

test("the image detector opens a compressed layer instead of only scanning archive bytes", () => {
  const marker = "synthetic-private-key-positive-control-7411";
  const leaked = archive(gzipSync(Buffer.from(marker)));
  expect(detectLeaks("image", leaked, [marker])).toEqual([]);
  const findings = detectArchiveLeaks("image", leaked, [marker]);
  expect(findings).toEqual([{ surface: "image:layer.tar.gz:gzip", marker: 0 }]);
  expect(() => requireNoLeaks(findings)).toThrow("image:layer.tar.gz:gzip");
  expect(() => requireNoLeaks(detectArchiveLeaks("image", archive(gzipSync(Buffer.from("clean"))), [marker]))).not.toThrow();
});

test("a leak failure identifies its surface without copying the credential", () => {
  const marker = "synthetic-access-token-positive-control-6612";
  const findings = detectLeaks("container-process-environment", Buffer.from(`TOKEN=${marker}\0`), [marker]);
  expect(findings).toEqual([{ surface: "container-process-environment", marker: 0 }]);
  try { requireNoLeaks(findings); throw new Error("The leak gate failed."); }
  catch (error) {
    expect(String(error)).toContain("container-process-environment");
    expect(String(error)).not.toContain(marker);
  }
});
