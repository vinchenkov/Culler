import { describe, expect, test } from "bun:test";
import {
  HomieRuntimeRegistry,
  type HomieRuntimeAttestation,
  type HomieRuntimePlan,
} from "./homie-runtime";

const plan: HomieRuntimePlan = {
  session: "h-alpha",
  launch: "a".repeat(16),
  envelope_digest: "e".repeat(64),
};
const container_id = "c".repeat(64);
const docker_envelope = '{"docker":"exact"}';
const artifact_envelope = '{"run_json":"exact"}';

function registry(fill = 1): HomieRuntimeRegistry {
  return new HomieRuntimeRegistry(Buffer.alloc(32, fill));
}

describe("boot-local Homie runtime registration", () => {
  test("attests only the exact registered envelope, generation, and bound container", () => {
    const runtimes = registry();
    const runtime_generation = runtimes.register(plan);
    const exact: HomieRuntimeAttestation = { ...plan, container_id, runtime_generation };

    expect(runtimes.attest(exact)).toBe(false);
    runtimes.bind({ ...exact, docker_envelope });
    runtimes.sealArtifacts({ ...exact, artifact_envelope });
    expect(runtimes.attest(exact)).toBe(true);
    expect(runtimes.attestEnvelope({ ...exact, docker_envelope })).toEqual({
      envelope_digest: expect.stringMatching(/^[0-9a-f]{64}$/),
      runtime_generation,
    });

    for (const changed of [
      { ...exact, session: "h-other" },
      { ...exact, launch: "b".repeat(16) },
      { ...exact, envelope_digest: "f".repeat(64) },
      { ...exact, runtime_generation: "d".repeat(64) },
      { ...exact, container_id: "9".repeat(64) },
    ]) {
      expect(runtimes.attest(changed)).toBe(false);
    }
    expect(runtimes.attestEnvelope({
      ...exact,
      docker_envelope: '{"docker":"drifted"}',
    })).toBeNull();
  });

  test("does not overwrite a live generation or bind it to another container", () => {
    const runtimes = registry();
    const runtime_generation = runtimes.register(plan);
    const exact = { ...plan, container_id, runtime_generation };
    runtimes.bind({ ...exact, docker_envelope });

    expect(() => runtimes.register(plan)).toThrow("already registered");
    expect(() => runtimes.bind({ ...exact, container_id: "d".repeat(64) })).toThrow("already bound");
    expect(() => runtimes.revoke({ ...plan, runtime_generation: "f".repeat(64) }))
      .toThrow("registration mismatch");
    expect(runtimes.attest(exact)).toBe(true);
  });

  test("revocation and a new resident boot both invalidate adoption proof", () => {
    const firstBoot = registry(1);
    const runtime_generation = firstBoot.register(plan);
    const exact = { ...plan, container_id, runtime_generation };
    firstBoot.bind({ ...exact, docker_envelope });
    firstBoot.sealArtifacts({ ...exact, artifact_envelope });

    expect(firstBoot.revoke({ ...plan, runtime_generation })).toBe(true);
    expect(firstBoot.attest(exact)).toBe(false);

    const nextBoot = registry(2);
    expect(nextBoot.attest(exact)).toBe(false);
    expect(nextBoot.register(plan)).not.toBe(runtime_generation);
  });

  test("an expired proxy grant invalidates a current boot container proof", () => {
    const runtimes = registry();
    let authorized = true;
    const runtime_generation = runtimes.register(plan, () => authorized);
    const exact = { ...plan, container_id, runtime_generation };
    runtimes.bind({ ...exact, docker_envelope });
    runtimes.sealArtifacts({ ...exact, artifact_envelope });
    expect(runtimes.attestEnvelope({ ...exact, docker_envelope })).not.toBeNull();
    authorized = false;
    expect(runtimes.attest(exact)).toBe(false);
    expect(runtimes.attestEnvelope({ ...exact, docker_envelope })).toBeNull();
    expect(runtimes.revokeObserved(plan.session, plan.launch, container_id)).toBe(true);
  });

  test("envelope digests are boot-keyed and bounded", () => {
    expect(registry(1).digestEnvelope('{"create":["docker"]}')).toBe(
      registry(1).digestEnvelope('{"create":["docker"]}'),
    );
    expect(registry(2).digestEnvelope('{"create":["docker"]}')).not.toBe(
      registry(1).digestEnvelope('{"create":["docker"]}'),
    );
    expect(() => registry().digestEnvelope("")).toThrow("empty or oversized");
  });

  test("cleanup revokes only an exact observed container generation", () => {
    const runtimes = registry();
    const runtime_generation = runtimes.register(plan);
    const exact = { ...plan, container_id, runtime_generation };
    runtimes.bind(exact);

    expect(runtimes.revokeObserved(plan.session, plan.launch, "d".repeat(64))).toBe(false);
    expect(runtimes.revokeContainer(plan.session, "d".repeat(64))).toBe(false);
    expect(runtimes.attest(exact)).toBe(true);
    expect(runtimes.revokeObserved(plan.session, plan.launch, container_id)).toBe(true);
    expect(runtimes.attest(exact)).toBe(false);
  });

  test("refuses malformed plans before retaining any registration", () => {
    const runtimes = registry();
    expect(() => runtimes.register({ ...plan, session: "../escape" })).toThrow("malformed");
    expect(() => runtimes.register({ ...plan, launch: "short" })).toThrow("malformed");
    expect(() => runtimes.register({ ...plan, envelope_digest: "not-a-digest" })).toThrow("malformed");
    expect(() => new HomieRuntimeRegistry(Buffer.alloc(31))).toThrow("at least 32 bytes");
  });
});
