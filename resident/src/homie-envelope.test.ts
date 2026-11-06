import { describe, expect, test } from "bun:test";
import {
  canonicalHomieDockerEnvelope,
  type HomieCreateExpectation,
} from "./homie-envelope";

const id = "c".repeat(64);
const image = "mc-fake-e2e:test";
const cmd = ["bun", "/app/src/homie-runner/main.ts"];
const binds = [
  "/host/session:/mc/session",
  "/host/run.json:/mc/run.json:ro",
  "/host/sessions:/mc/traces:ro",
  "mc-spine:/mc/spine",
];
const labels = {
  "mc-managed": "true",
  "mc-tier": "homie",
  "mc-session-id": "h-abc",
  "mc-launch-id": "a".repeat(16),
  "mc-envelope-digest": "e".repeat(64),
  "mc-runtime-generation": "f".repeat(64),
};
const env = ["MC_SPINE=/mc/spine/spine.db"];

const expected: HomieCreateExpectation = {
  binds,
  cap_drop: ["ALL"],
  cmd,
  container_id: id,
  env,
  image,
  mc_labels: labels,
  memory: 1073741824,
  memory_swap: 1073741824,
  name: "mc-homie-h-abc",
  network_modes: ["none"],
  pids_limit: 128,
  security_opt: [],
  state: "created",
  user: "10002:10002",
};

function inspectRecord(planBinds: string[] = binds): Record<string, unknown> {
  return {
    Args: cmd.slice(1),
    Config: {
      Cmd: cmd,
      Entrypoint: null,
      Env: ["PATH=/usr/bin", ...env],
      Image: image,
      Labels: { "org.opencontainers.image.version": "test", ...labels },
      OpenStdin: false,
      Tty: false,
      User: "10002:10002",
      WorkingDir: "",
    },
    HostConfig: {
      AutoRemove: true,
      Binds: [...planBinds],
      CapAdd: null,
      CapDrop: ["ALL"],
      CgroupnsMode: "private",
      Dns: [],
      DnsOptions: [],
      DnsSearch: [],
      IpcMode: "private",
      Memory: 1073741824,
      MemorySwap: 1073741824,
      NanoCpus: 0,
      NetworkMode: "none",
      PidMode: "",
      PidsLimit: 128,
      Privileged: false,
      ReadonlyRootfs: false,
      RestartPolicy: { MaximumRetryCount: 0, Name: "no" },
      SecurityOpt: null,
      UTSMode: "",
    },
    Id: id,
    Image: `sha256:${"1".repeat(64)}`,
    Mounts: planBinds.map((bind) => {
      const [source, destination, access] = bind.split(":");
      const volume = !source!.startsWith("/");
      // Shaped as `docker inspect` really writes it: Name and Driver are
      // present for a volume and ABSENT for a bind, not "" for both.
      return {
        Destination: destination,
        Mode: "",
        Propagation: "rprivate",
        RW: access !== "ro",
        Source: volume ? `/var/lib/docker/volumes/${source}/_data` : source,
        Type: volume ? "volume" : "bind",
        ...(volume ? { Driver: "local", Name: source } : {}),
      };
    }),
    Name: "/mc-homie-h-abc",
    Path: "bun",
    State: { Status: "created" },
  };
}

describe("canonical Homie Docker envelope", () => {
  test("validates the create plan and retains immutable security/resource defaults", () => {
    const canonical = canonicalHomieDockerEnvelope(inspectRecord(), expected);
    const parsed = JSON.parse(canonical);
    expect(parsed.id).toBe(id);
    expect(parsed.image).toBe(`sha256:${"1".repeat(64)}`);
    expect(parsed.host_config).toEqual(expect.objectContaining({
      auto_remove: true,
      cap_add: [],
      cap_drop: ["ALL"],
      memory: 1073741824,
      memory_swap: 1073741824,
      nano_cpus: 0,
      network_mode: "none",
      pids_limit: 128,
      privileged: false,
      readonly_rootfs: false,
      security_opt: [],
    }));
    expect(parsed.mounts.map((mount: { destination: string }) => mount.destination)).toEqual([
      "/mc/run.json", "/mc/session", "/mc/spine", "/mc/traces",
    ]);
  });

  test("refuses a Homie container with an absent trace mount", () => {
    const record = inspectRecord() as any;
    record.HostConfig.Binds = record.HostConfig.Binds.filter(
      (bind: string) => !bind.includes(":/mc/traces"),
    );
    record.Mounts = record.Mounts.filter(
      (mount: { Destination: string }) => mount.Destination !== "/mc/traces",
    );
    expect(() => canonicalHomieDockerEnvelope(record, expected))
      .toThrow("mount set does not match the closed create plan");
  });

  test("refuses a Homie container with a writable trace mount", () => {
    const record = inspectRecord() as any;
    record.HostConfig.Binds = record.HostConfig.Binds.map(
      (bind: string) => bind.replace(":/mc/traces:ro", ":/mc/traces"),
    );
    record.Mounts.find(
      (mount: { Destination: string }) => mount.Destination === "/mc/traces",
    ).RW = true;
    expect(() => canonicalHomieDockerEnvelope(record, expected))
      .toThrow("mount access drifted at /mc/traces");
  });

  // The artifact mask of a Project folder and the artifact folder of the task
  // of a packet conversation.
  const artifactBinds = [
    ...binds,
    "/host/runs/artifact-mask:/workspaces/beta/.mc-artifacts:ro",
    "/host/beta/.mc-artifacts/21:/mc/artifacts/beta/21:ro",
  ];
  const artifactExpected: HomieCreateExpectation = { ...expected, binds: artifactBinds };
  const artifactDestinations = ["/workspaces/beta/.mc-artifacts", "/mc/artifacts/beta/21"];

  test("refuses a Homie container with an absent artifact mount", () => {
    expect(() => canonicalHomieDockerEnvelope(inspectRecord(artifactBinds), artifactExpected)).not.toThrow();
    for (const destination of artifactDestinations) {
      const record = inspectRecord(artifactBinds) as any;
      record.HostConfig.Binds = record.HostConfig.Binds.filter(
        (bind: string) => !bind.includes(`:${destination}:`),
      );
      record.Mounts = record.Mounts.filter(
        (mount: { Destination: string }) => mount.Destination !== destination,
      );
      expect(() => canonicalHomieDockerEnvelope(record, artifactExpected))
        .toThrow("mount set does not match the closed create plan");
    }
  });

  test("refuses a Homie container with a writable artifact mount", () => {
    for (const destination of artifactDestinations) {
      const record = inspectRecord(artifactBinds) as any;
      record.HostConfig.Binds = record.HostConfig.Binds.map(
        (bind: string) => bind.replace(`:${destination}:ro`, `:${destination}`),
      );
      record.Mounts.find(
        (mount: { Destination: string }) => mount.Destination === destination,
      ).RW = true;
      expect(() => canonicalHomieDockerEnvelope(record, artifactExpected))
        .toThrow(`mount access drifted at ${destination}`);
    }
  });

  test("refuses a resource cap that drifted from the create plan", () => {
    const record = inspectRecord() as any;
    record.HostConfig.MemorySwap = -1;
    record.HostConfig.PidsLimit = null;
    expect(() => canonicalHomieDockerEnvelope(record, expected))
      .toThrow("does not match the closed create plan: memory swap, pids limit");
  });

  // Real `docker inspect` emits Name and Driver ONLY for volume mounts; a bind
  // mount omits both keys entirely (verified against a live container). This
  // fixture used to write "" for them, so the pin agreed with itself while
  // every real Homie container — which always binds its session dir, and binds
  // a control dir too under a materialized credential binding — was refused
  // post-create with "Mounts[n].Driver must be a string", wedging the wake.
  test("accepts a bind mount that omits Name and Driver, as Docker writes it", () => {
    const record = inspectRecord() as any;
    for (const mount of record.Mounts) {
      if (mount.Type === "bind") {
        delete mount.Name;
        delete mount.Driver;
      }
    }
    const parsed = JSON.parse(canonicalHomieDockerEnvelope(record, expected));
    const binds = parsed.mounts.filter((m: { type: string }) => m.type === "bind");
    expect(binds.length).toBeGreaterThan(0);
    for (const mount of binds) {
      expect(mount.name).toBe("");
      expect(mount.driver).toBe("");
    }
    // Absence normalizes; a wrong TYPE is still a refusal, because the envelope
    // is an attestation and an unexpected shape must never canonicalize.
    const wrongType = inspectRecord() as any;
    wrongType.Mounts[0].Driver = 7;
    expect(() => canonicalHomieDockerEnvelope(wrongType, expected))
      .toThrow("must be a string");
  });

  test("refuses identity, plan, network, label, env, and mount drift", () => {
    const cases: Array<(record: any) => void> = [
      (record) => { record.Id = "d".repeat(64); },
      (record) => { record.Config.Cmd = ["other"]; },
      (record) => { record.Config.Env = ["PATH=/usr/bin"]; },
      (record) => { record.Config.Labels["mc-launch-id"] = "b".repeat(16); },
      (record) => { record.HostConfig.AutoRemove = false; },
      (record) => { record.HostConfig.NetworkMode = "bridge"; },
      (record) => { record.HostConfig.Binds.pop(); },
      (record) => { record.Mounts[0].RW = false; },
      (record) => { record.Mounts[0].Source = "/host/substituted"; },
      (record) => { record.Mounts[0].Type = "volume"; },
      (record) => { record.Mounts[0].Destination = "/mc/private"; },
      (record) => {
        record.Mounts.push({
          ...record.Mounts[0],
          Destination: "/mc/unplanned",
        });
      },
      (record) => { record.State.Status = "running"; },
    ];
    for (const change of cases) {
      const record = inspectRecord();
      change(record);
      expect(() => canonicalHomieDockerEnvelope(record, expected)).toThrow();
    }
  });

  test("refuses a Homie container that can run as root or hold a capability", () => {
    const cases: Array<[string, (record: any) => void]> = [
      ["user", (record) => { record.Config.User = ""; }],
      ["user", (record) => { record.Config.User = "0:0"; }],
      ["cap drop", (record) => { record.HostConfig.CapDrop = null; }],
      ["cap add", (record) => { record.HostConfig.CapAdd = ["SYS_ADMIN"]; }],
      ["privileged", (record) => { record.HostConfig.Privileged = true; }],
    ];
    for (const [field, change] of cases) {
      const record = inspectRecord();
      change(record);
      expect(() => canonicalHomieDockerEnvelope(record, expected))
        .toThrow(`does not match the closed create plan: ${field}`);
    }
  });

  // Docker Desktop reports the SAME bind two ways — the host path, or that path
  // under `/host_mnt`, the VM's mount point for shared host storage — and which
  // one comes back is not stable: observed differing per mount within one
  // deployment and between two inspections of one container. Raw string
  // comparison therefore refused every real Homie wake ("mount source/type
  // drifted at /app/src", roughly once a minute), so no Homie container on this
  // deployment ever reached `docker start` and the whole tier was dead.
  // Both properties below are load-bearing, at the two points that must agree.
  test("accepts Docker Desktop's /host_mnt spelling of the same bind source", () => {
    const prefixed = inspectRecord() as any;
    for (const mount of prefixed.Mounts) {
      if (mount.Type === "bind") mount.Source = `/host_mnt${mount.Source}`;
    }
    // homieWake: the post-create expectation check must still validate.
    expect(() => canonicalHomieDockerEnvelope(prefixed, expected)).not.toThrow();
    // homieAdopt: re-attestation compares the canonical snapshot byte for byte,
    // so a representation flip between the two inspections must not change it.
    expect(canonicalHomieDockerEnvelope(prefixed))
      .toBe(canonicalHomieDockerEnvelope(inspectRecord()));
  });

  // The test above rewrote only `Mounts[].Source`, and that is precisely why it
  // passed while production still refused: Docker Desktop applies the same
  // rewrite to the SOURCE component of the raw `HostConfig.Binds` strings, and
  // the closed-plan comparison reads those separately. Fixing the mounts arm
  // alone did not clear the tier — it MOVED the refusal one field over, from
  // "mount source/type drifted" to the closed-plan check, which is a real live
  // observation and not a hypothetical. Prefixing both is the production shape.
  test("accepts the /host_mnt spelling in HostConfig.Binds as well as Mounts", () => {
    const prefixed = inspectRecord() as any;
    for (const mount of prefixed.Mounts) {
      if (mount.Type === "bind") mount.Source = `/host_mnt${mount.Source}`;
    }
    prefixed.HostConfig.Binds = prefixed.HostConfig.Binds.map((bind: string) =>
      bind.startsWith("/") ? `/host_mnt${bind}` : bind);
    expect(() => canonicalHomieDockerEnvelope(prefixed, expected)).not.toThrow();
    expect(canonicalHomieDockerEnvelope(prefixed))
      .toBe(canonicalHomieDockerEnvelope(inspectRecord()));
  });

  // The other direction, and the reason both sides carry the rule rather than
  // just the observed one: a create plan that legitimately names a /host_mnt
  // source must validate against a container Docker reports plainly. Built by
  // one rule or the comparison means nothing — the same argument bindExpectation
  // already makes for Mounts.
  test("a plan naming a /host_mnt source validates against a plainly-reported container", () => {
    const planBinds = binds.map((bind) => bind.startsWith("/") ? `/host_mnt${bind}` : bind);
    expect(() => canonicalHomieDockerEnvelope(inspectRecord(), { ...expected, binds: planBinds }))
      .not.toThrow();
  });

  test("a substituted bind source is still refused when the string is prefixed", () => {
    const substituted = inspectRecord() as any;
    substituted.HostConfig.Binds = ["/host_mnt/host/elsewhere:/mc/session",
      ...substituted.HostConfig.Binds.slice(1)];
    expect(() => canonicalHomieDockerEnvelope(substituted, expected)).toThrow(/binds/);
  });

  // A refusal that names nothing costs a whole diagnostic cycle to localize,
  // and this one did. Values stay out of the message on purpose: `env` carries
  // the projected access token.
  test("a closed-plan refusal names the field that drifted, never its value", () => {
    const secret = "MC_TOKEN=super-secret-value";
    const withSecret = inspectRecord() as any;
    withSecret.Config.Env = ["PATH=/usr/bin", secret];
    withSecret.Config.Image = "some-other-image:latest";
    let message = "";
    try {
      canonicalHomieDockerEnvelope(withSecret, { ...expected, env: [secret] });
    } catch (err) {
      message = err instanceof Error ? err.message : String(err);
    }
    expect(message).toContain("image");
    expect(message).not.toContain("super-secret-value");
    expect(message).not.toContain("some-other-image");
  });

  test("the /host_mnt collapse widens nothing past the prefix", () => {
    // A genuinely substituted source is still refused, prefixed or not — the
    // path AFTER the prefix must match exactly.
    const substituted = inspectRecord() as any;
    substituted.Mounts[0].Source = "/host_mnt/host/substituted";
    expect(() => canonicalHomieDockerEnvelope(substituted, expected)).toThrow();
    // `/host_mntfoo` is a different directory, not a prefixed one: the
    // remainder must begin at a path separator or nothing is stripped.
    const lookalike = inspectRecord() as any;
    const original = lookalike.Mounts[0].Source;
    lookalike.Mounts[0].Source = `/host_mnt${original}`.replace("/host_mnt/", "/host_mnt");
    expect(() => canonicalHomieDockerEnvelope(lookalike, expected)).toThrow();
  });

  test("later resource or image drift changes the canonical snapshot", () => {
    const original = canonicalHomieDockerEnvelope(inspectRecord(), expected);
    const drifted = inspectRecord() as any;
    drifted.HostConfig.Memory = 1024;
    expect(canonicalHomieDockerEnvelope(drifted)).not.toBe(original);
    drifted.HostConfig.Memory = 0;
    drifted.Image = `sha256:${"2".repeat(64)}`;
    expect(canonicalHomieDockerEnvelope(drifted)).not.toBe(original);
  });
});

test("the OpenCode volume and empty configuration mount match the Docker create plan", () => {
  const volume = "mc-opencode-fixture";
  const target = "/opt/culler-opencode";
  const options = "ro,nosuid,nodev,noexec,mode=0555";
  const record = inspectRecord() as any;
  record.HostConfig.Mounts = [{ Type: "volume", Source: volume, Target: target, ReadOnly: true, VolumeOptions: { NoCopy: true } }];
  record.HostConfig.Tmpfs = { "/etc/opencode": options };
  record.Mounts.push({ Type: "volume", Name: volume, Source: `/var/lib/docker/volumes/${volume}/_data`, Destination: target, Driver: "local", Mode: "z", RW: false, Propagation: "" });
  const plan = { ...expected, mounts: [`type=volume,src=${volume},dst=${target},readonly,volume-nocopy`], tmpfs: [`/etc/opencode:${options}`] };
  const canonical = canonicalHomieDockerEnvelope(record, plan);
  expect(canonicalHomieDockerEnvelope(record)).toBe(canonical);
  for (const change of [
    (r: any) => { r.Mounts.at(-1).RW = true; },
    (r: any) => { r.Mounts.at(-1).Name = "another-volume"; },
    (r: any) => { r.HostConfig.Mounts[0].VolumeOptions.NoCopy = false; },
    (r: any) => { r.HostConfig.Tmpfs["/etc/opencode"] = "rw"; },
    (r: any) => { delete r.HostConfig.Tmpfs; },
    (r: any) => { r.HostConfig.Tmpfs["/workspace"] = "rw"; },
  ]) {
    const changed = structuredClone(record);
    change(changed);
    expect(() => canonicalHomieDockerEnvelope(changed, plan)).toThrow();
    expect(canonicalHomieDockerEnvelope(changed)).not.toBe(canonical);
  }
  expect(() => canonicalHomieDockerEnvelope(record, expected)).toThrow();
});
