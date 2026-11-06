const SHA256_ID = /^(?:sha256:)?[0-9a-f]{64}$/;

export interface HomieCreateExpectation {
  binds: string[];
  mounts?: string[];
  tmpfs?: string[];
  cap_drop: string[];
  cmd: string[];
  entrypoint?: string[];
  container_id: string;
  env: string[];
  image: string;
  mc_labels: Record<string, string>;
  memory: number;
  memory_swap: number;
  name: string;
  network_modes: string[];
  pids_limit: number | null;
  security_opt: string[];
  state: "created";
  user: string;
}

/** The envelope retains immutable Docker settings for the create and adopt checks. */
export function canonicalHomieDockerEnvelope(
  value: unknown,
  expected?: HomieCreateExpectation,
): string {
  const record = object(value, "record");
  const config = object(record.Config, "Config");
  const host = object(record.HostConfig, "HostConfig");
  const restart = object(host.RestartPolicy, "HostConfig.RestartPolicy");
  const labels = stringMap(config.Labels, "Config.Labels");
  const mounts = array(record.Mounts, "Mounts").map((item, index) => {
    const mount = object(item, `Mounts[${index}]`);
    return {
      destination: string(mount.Destination, `Mounts[${index}].Destination`),
      driver: absentableString(mount.Driver, `Mounts[${index}].Driver`),
      mode: string(mount.Mode, `Mounts[${index}].Mode`),
      name: absentableString(mount.Name, `Mounts[${index}].Name`),
      propagation: string(mount.Propagation, `Mounts[${index}].Propagation`),
      rw: boolean(mount.RW, `Mounts[${index}].RW`),
      source: normalizeBindSource(string(mount.Source, `Mounts[${index}].Source`)),
      type: string(mount.Type, `Mounts[${index}].Type`),
    };
  }).sort((a, b) =>
    a.destination.localeCompare(b.destination) ||
    a.source.localeCompare(b.source) ||
    a.type.localeCompare(b.type));

  const volumeMounts = array(host.Mounts ?? [], "HostConfig.Mounts").map(volumeMount)
    .sort((a, b) => a.destination.localeCompare(b.destination));
  const tmpfs = stringMap(host.Tmpfs ?? {}, "HostConfig.Tmpfs");
  const envelope = {
    args: strings(record.Args, "Args"),
    config: {
      cmd: nullableStrings(config.Cmd, "Config.Cmd"),
      entrypoint: nullableStrings(config.Entrypoint, "Config.Entrypoint"),
      env: strings(config.Env, "Config.Env"),
      image: string(config.Image, "Config.Image"),
      labels,
      open_stdin: boolean(config.OpenStdin, "Config.OpenStdin"),
      tty: boolean(config.Tty, "Config.Tty"),
      user: string(config.User, "Config.User"),
      working_dir: string(config.WorkingDir, "Config.WorkingDir"),
    },
    host_config: {
      ...(volumeMounts.length ? { mounts: volumeMounts } : {}),
      ...(Object.keys(tmpfs).length ? { tmpfs } : {}),
      auto_remove: boolean(host.AutoRemove, "HostConfig.AutoRemove"),
      // Docker Desktop can report either host path spelling between inspections.
      binds: nullableStrings(host.Binds, "HostConfig.Binds").map(normalizeBindString).sort(),
      cap_add: nullableStrings(host.CapAdd, "HostConfig.CapAdd").sort(),
      cap_drop: nullableStrings(host.CapDrop, "HostConfig.CapDrop").sort(),
      cgroupns_mode: string(host.CgroupnsMode, "HostConfig.CgroupnsMode"),
      dns: nullableStrings(host.Dns, "HostConfig.Dns").sort(),
      dns_options: nullableStrings(host.DnsOptions, "HostConfig.DnsOptions").sort(),
      dns_search: nullableStrings(host.DnsSearch, "HostConfig.DnsSearch").sort(),
      ipc_mode: string(host.IpcMode, "HostConfig.IpcMode"),
      memory: integer(host.Memory, "HostConfig.Memory"),
      memory_swap: integer(host.MemorySwap, "HostConfig.MemorySwap"),
      nano_cpus: integer(host.NanoCpus, "HostConfig.NanoCpus"),
      network_mode: string(host.NetworkMode, "HostConfig.NetworkMode"),
      pid_mode: string(host.PidMode, "HostConfig.PidMode"),
      pids_limit: nullableInteger(host.PidsLimit, "HostConfig.PidsLimit"),
      privileged: boolean(host.Privileged, "HostConfig.Privileged"),
      readonly_rootfs: boolean(host.ReadonlyRootfs, "HostConfig.ReadonlyRootfs"),
      restart_policy: {
        maximum_retry_count: integer(
          restart.MaximumRetryCount,
          "HostConfig.RestartPolicy.MaximumRetryCount",
        ),
        name: string(restart.Name, "HostConfig.RestartPolicy.Name"),
      },
      security_opt: nullableStrings(host.SecurityOpt, "HostConfig.SecurityOpt").sort(),
      uts_mode: string(host.UTSMode, "HostConfig.UTSMode"),
    },
    id: string(record.Id, "Id"),
    image: string(record.Image, "Image"),
    mounts,
    name: string(record.Name, "Name"),
    path: string(record.Path, "Path"),
  };

  if (!SHA256_ID.test(envelope.id) || !SHA256_ID.test(envelope.image)) {
    throw new Error("Homie Docker envelope carries a non-canonical container or image id");
  }
  const destinations = mounts.map((mount) => mount.destination);
  if (new Set(destinations).size !== destinations.length) {
    throw new Error("Homie Docker envelope carries duplicate mount destinations");
  }

  if (expected !== undefined) {
    const state = object(record.State, "State");
    // Both sides use the same Docker Desktop path normalization.
    const expectedBinds = expected.binds.map(normalizeBindString).sort();
    const expectedSecurity = [...expected.security_opt].sort();
    const expectedVolumes = (expected.mounts ?? []).map(volumeArgument)
      .sort((a, b) => a.destination.localeCompare(b.destination));
    const expectedTmpfs = stringMap(Object.fromEntries((expected.tmpfs ?? []).map((item) => {
      const separator = item.indexOf(":");
      if (!item.startsWith("/") || separator < 1 || separator === item.length - 1) {
        throw new Error("Homie create plan carries an invalid temporary mount");
      }
      return [item.slice(0, separator), item.slice(separator + 1)];
    })), "expected temporary mounts");
    if (Object.keys(expectedTmpfs).length !== (expected.tmpfs ?? []).length) {
      throw new Error("Homie create plan carries duplicate temporary mounts");
    }
    const expectedMounts = [...expected.binds.map(bindExpectation), ...expectedVolumes.map((mount) => ({
      destination: mount.destination, rw: !mount.readonly, source: mount.source, type: "volume",
    }))].sort((a, b) => a.destination.localeCompare(b.destination));
    const expectedDestinations = expectedMounts.map((mount) => mount.destination);
    const actualMounts = mounts.map((mount) => ({
      destination: mount.destination,
      rw: mount.rw,
      source: mount.type === "volume" ? mount.name : mount.source,
      type: mount.type,
    }));
    const mcLabels = Object.keys(envelope.config.labels)
      .filter((key) => key.startsWith("mc-"))
      .sort();
    const wantedMcLabels = Object.keys(expected.mc_labels).sort();
    const exactEnv = expected.env.every((entry) =>
      envelope.config.env.filter((candidate) => candidate === entry).length === 1);
    const exactMcLabels =
      JSON.stringify(mcLabels) === JSON.stringify(wantedMcLabels) &&
      wantedMcLabels.every((key) => envelope.config.labels[key] === expected.mc_labels[key]);
    if (actualMounts.length !== expectedMounts.length) {
      throw new Error("Homie Docker envelope mount set does not match the closed create plan");
    }
    for (let index = 0; index < expectedMounts.length; index++) {
      const actual = actualMounts[index]!;
      const wanted = expectedMounts[index]!;
      if (actual.destination !== wanted.destination) {
        throw new Error("Homie Docker envelope mount destination drifted");
      }
      if (actual.source !== wanted.source || actual.type !== wanted.type) {
        throw new Error(
          `Homie Docker envelope mount source/type drifted at ${wanted.destination}: ` +
          `${JSON.stringify(actual)} != ${JSON.stringify(wanted)}`,
        );
      }
      if (actual.rw !== wanted.rw) {
        throw new Error(`Homie Docker envelope mount access drifted at ${wanted.destination}`);
      }
    }
    // Name each failed field. Values can contain credentials.
    const checks: Array<[string, boolean]> = [
      ["container id", envelope.id === expected.container_id],
      ["name", envelope.name === `/${expected.name}`],
      ["state", state.Status === expected.state],
      ["image", envelope.config.image === expected.image],
      ["cmd", JSON.stringify(envelope.config.cmd) === JSON.stringify(expected.cmd)],
      ["entrypoint", expected.entrypoint === undefined || JSON.stringify(envelope.config.entrypoint) === JSON.stringify(expected.entrypoint)],
      ["binds", JSON.stringify(envelope.host_config.binds) === JSON.stringify(expectedBinds)],
      ["volume options", JSON.stringify(volumeMounts) === JSON.stringify(expectedVolumes)],
      ["temporary mounts", JSON.stringify(tmpfs) === JSON.stringify(expectedTmpfs)],
      ["network mode", expected.network_modes.includes(envelope.host_config.network_mode)],
      ["security opt",
        JSON.stringify(envelope.host_config.security_opt) === JSON.stringify(expectedSecurity)],
      ["user", envelope.config.user === expected.user],
      ["cap drop",
        JSON.stringify(envelope.host_config.cap_drop) === JSON.stringify([...expected.cap_drop].sort())],
      ["cap add", envelope.host_config.cap_add.length === 0],
      ["privileged", envelope.host_config.privileged === false],
      ["auto remove", envelope.host_config.auto_remove === true],
      ["memory", envelope.host_config.memory === expected.memory],
      ["memory swap", envelope.host_config.memory_swap === expected.memory_swap],
      ["pids limit", envelope.host_config.pids_limit === expected.pids_limit],
      ["env", exactEnv],
      ["mc labels", exactMcLabels],
      ["mount destinations",
        JSON.stringify(destinations) === JSON.stringify(expectedDestinations)],
    ];
    const drifted = checks.filter(([, holds]) => !holds).map(([field]) => field);
    if (drifted.length > 0) {
      throw new Error(
        `Homie Docker envelope does not match the closed create plan: ${drifted.join(", ")}`,
      );
    }
  }
  return JSON.stringify(envelope);
}

function volumeMount(value: unknown) {
  const row = object(value, "volume mount");
  const options = object(row.VolumeOptions ?? {}, "volume options");
  if (Object.keys(row).some((key) => !["Type", "Source", "Target", "ReadOnly", "VolumeOptions"].includes(key)) ||
      Object.keys(options).some((key) => key !== "NoCopy") || row.Type !== "volume") {
    throw new Error("Homie Docker envelope carries unsupported volume options");
  }
  return {
    destination: string(row.Target, "volume target"),
    no_copy: boolean(options.NoCopy ?? false, "volume copy control"),
    readonly: boolean(row.ReadOnly ?? false, "volume read access"),
    source: string(row.Source, "volume source"),
    type: "volume",
  };
}

function volumeArgument(value: string) {
  const fields = value.split(",");
  const source = fields.find((field) => field.startsWith("src="))?.slice(4);
  const destination = fields.find((field) => field.startsWith("dst="))?.slice(4);
  if (fields.length !== 5 || !fields.includes("type=volume") || !fields.includes("readonly") ||
      !fields.includes("volume-nocopy") || !source || !destination?.startsWith("/")) {
    throw new Error("Homie create plan carries an unsupported volume mount");
  }
  return { destination, no_copy: true, readonly: true, source, type: "volume" };
}

/** Docker Desktop uses both /host_mnt/X and /X for the same host path. */
function normalizeBindSource(source: string): string {
  const remainder = source.slice("/host_mnt".length);
  return source.startsWith("/host_mnt") && remainder.startsWith("/") ? remainder : source;
}

function normalizeBindString(bind: string): string {
  const parts = bindParts(bind);
  const head = `${normalizeBindSource(parts.source)}:${parts.destination}`;
  return parts.access === "ro" ? `${head}:ro` : head;
}

function bindExpectation(bind: string): {
  destination: string;
  rw: boolean;
  source: string;
  type: "bind" | "volume";
} {
  const parts = bindParts(bind);
  // Normalized on BOTH sides, deliberately. Normalizing only the observed side
  // would refuse a plan that legitimately names a `/host_mnt` source, and the
  // two sides must be built by one rule or the comparison means nothing.
  return {
    destination: parts.destination,
    rw: parts.access !== "ro",
    source: normalizeBindSource(parts.source),
    type: parts.source.startsWith("/") ? "bind" : "volume",
  };
}

function bindParts(bind: string): {
  access: "" | "ro";
  destination: string;
  source: string;
} {
  const parts = bind.split(":");
  if ((parts.length !== 2 && parts.length !== 3) ||
      parts[0] === "" ||
      !parts[1]?.startsWith("/") ||
      (parts.length === 3 && parts[2] !== "ro")) {
    throw new Error("Homie create plan carries a non-canonical bind");
  }
  return {
    access: parts.length === 3 ? "ro" : "",
    destination: parts[1],
    source: parts[0]!,
  };
}

function object(value: unknown, field: string): Record<string, unknown> {
  if (value === null || typeof value !== "object" || Array.isArray(value)) {
    throw new Error(`Homie Docker envelope ${field} must be an object`);
  }
  return value as Record<string, unknown>;
}

function array(value: unknown, field: string): unknown[] {
  if (!Array.isArray(value)) throw new Error(`Homie Docker envelope ${field} must be an array`);
  return value;
}

function string(value: unknown, field: string): string {
  if (typeof value !== "string") throw new Error(`Homie Docker envelope ${field} must be a string`);
  return value;
}

/** Docker emits Name and Driver only for VOLUME mounts; a bind mount omits
 * both keys. Absence normalizes to "" so a bind canonicalizes deterministically
 * — but a present value of the wrong type is still a refusal, because this
 * envelope is an attestation and an unexpected shape must never pass. */
function absentableString(value: unknown, field: string): string {
  return value === undefined ? "" : string(value, field);
}

function boolean(value: unknown, field: string): boolean {
  if (typeof value !== "boolean") throw new Error(`Homie Docker envelope ${field} must be boolean`);
  return value;
}

function integer(value: unknown, field: string): number {
  if (!Number.isSafeInteger(value)) throw new Error(`Homie Docker envelope ${field} must be an integer`);
  return value as number;
}

function nullableInteger(value: unknown, field: string): number | null {
  return value === null ? null : integer(value, field);
}

function strings(value: unknown, field: string): string[] {
  return array(value, field).map((item, index) => string(item, `${field}[${index}]`));
}

function nullableStrings(value: unknown, field: string): string[] {
  return value === null ? [] : strings(value, field);
}

function stringMap(value: unknown, field: string): Record<string, string> {
  const source = object(value, field);
  const out: Record<string, string> = {};
  for (const key of Object.keys(source).sort()) {
    out[key] = string(source[key], `${field}.${key}`);
  }
  return out;
}
