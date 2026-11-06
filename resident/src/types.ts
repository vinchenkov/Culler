// types.ts — the resident's pinned dependency surface. Every dependency the
// loop and the effectors take is injectable, so `bun test` touches neither the
// real filesystem nor Docker.

import type { Logger } from "../../runner/shared/log";
import type { RepositoryPlan } from "./repository";

import type { CollieRuntimeRegistry } from "./collie-runtime";

/** Result of running an external command (mc or docker). */
export interface ExecResult {
  exitCode: number;
  stdout: string;
  stderr: string;
}

/** Runs a command with the given argv (binary path already bound). An abort
 * of `signal` sends SIGTERM to the child; the promise resolves after the child
 * exits. */
export type Exec = (argv: string[], stdin?: Uint8Array, options?: { signal?: AbortSignal }) => Promise<ExecResult>;

/**
 * Static wiring the effectors need. Everything here is decided by whoever
 * spawns the resident; the loop itself computes nothing from it. The resident
 * is the mechanical hand of a dispatch decision already committed in the spine.
 */
export interface ResidentConfig {
  /** Test builds can select a fixture authority for an existing binding.
   * Production builds reject this field before the resident reads credentials. */
  runtimeBindingAuthorities?: Record<string, { token_url: string; client_id: string }>;
  /** The enabled runtime catalog, projected out of config.toml. The resident
   * holds no catalog of its own and refuses to start without this key. */
  runtimeBindings?: Record<string, unknown>;
  /** Host MC_HOME; sessions live at `<mcHome>/sessions/<run_id>`. */
  mcHome: string;
  /** The loopback `host:port` of AgentsView. Without it, the resident copies
   * the traces and sends no request to AgentsView. */
  agentsViewBind?: string;
  /** The Culler service image. */
  image: string;
  /** The default developer image. Existing Projects retain their selected digest. */
  projectImage?: string;
  /** Private Culler files. The mount permits reads and execution, with file changes blocked. */
  runtimeVolume?: string;
  releaseBuildId?: string;
  /** Command run inside the agent container (the skeleton agent runner). */
  agentCmd: string[];
  /** Command run inside the Collie container (the collie runner). Falls back to
   * agentCmd when unset so the fake e2e image can share one entrypoint. */
  collieCmd?: string[];
  /** Command run inside the merge container (the baked `mc-land` script). */
  landCmd: string[];
  /** Host dir of behavior fixtures, mounted RO at /mc/behaviors. */
  behaviorsDir: string;
  /** Runner source for legacy test containers without a private runtime volume. */
  runnerSrcDir: string;
  /** Initial workspace root in the service configuration. */
  workspaceRoot: string;
  /** Initial Project catalog. Collie reads current folders before each launch. */
  workspaceRoots?: Array<{ id: string; root: string }>;
  /** Docker named volume holding the spine. */
  spineVolume: string;
  /** In-container spine db file path; volume mounts at its dirname; exported
   * as MC_SPINE for the runner's env interpolation. */
  spineDbPath: string;
  /** Explicit non-personal identity projected into every role container.
   * Pipeline Git operations never inherit the operator's ambient config. */
  gitAuthorName: string;
  gitAuthorEmail: string;
  /** role → in-container behavior file path for the fake harness. */
  roleBehaviors: Record<string, string>;
  /** Explicit test/development `harness/model_binding` routes that use the
   * fake adapter as a stand-in. Production leaves this empty. The runner
   * selects the adapter from the harness family in the envelope. */
  agentRunnerRoutes?: string[];
}

/** The host issues bounded provider access for each container launch. */
export interface CredentialProjection {
  env: Record<string, string>;
  /** Private launch configuration contains only Culler authority and public trust. */
  proxyConfig?: string;
}

export type CredentialRefusal = { refused: string } | {
  refused: string;
  cause: "credentials";
  evidence: string;
  operator_action: string;
};

/** The resolved runtime carrier as the projector consumes it: the binding id,
 * its harness family, its credential source id, and its credential channel. */
export interface RuntimeCarrier {
  harness: string;
  id: string;
  credential: string;
  channel: string;
}

export interface CredentialProjector {
  has?(runId: string, launchId: string): boolean;
  project(runtime: RuntimeCarrier, launch: ProviderLaunch): CredentialProjection | CredentialRefusal | null;
  revoke(runId: string, launchId?: string): void;
  extend(runId: string, launchId: string, expiresAt: number): void;
}

export interface ProviderLaunch {
  runId: string;
  launchId: string;
  expiresAt: number;
  model: string;
  profileRevision?: string;
  attemptId?: string;
}

/** The injectable dependency bundle for `startTickLoop`. */
export interface TickDeps {
  intervalMs: number;
  /** setInterval-shaped: fn fires every ms until cleared. */
  setTimer: (fn: () => unknown, ms: number) => unknown;
  clearTimer: (handle: unknown) => void;
  /** Invokes the host mc binary (self-delegating). */
  runMc: Exec;
  /** Reads the current host-attested Project folders before a Collie launch. */
  readWorkspaces(): Promise<Array<{ id: string; root: string }>>;
  /** Invokes the docker CLI. */
  docker: Exec;
  /** Bounded in-tick delay. A test exercises a retry budget without spending
   * its wall clock. Required, so a new deps site cannot inherit a real wait. */
  sleep: (ms: number) => Promise<void>;
  /** The zone of the Mac, read at each container creation and carried in as
   * TZ. Undefined when the host names no zone; the container then keeps the
   * zone of the image. */
  hostZone: () => string | undefined;
  /** The structured log. Every record names a level and an event from the
   * frozen registry; there is no free-text form. */
  log: Logger;
  /** A control receipt confirms that the resident accepted the authorized effects. */
  tickComplete?: () => Promise<void>;
  /** Serialized resident chore run before dispatch. A failure skips dispatch
   * for this tick so required durability work retries before any new write. */
  beforeTick?: () => Promise<void>;
  /** Pauses dispatch while an installer pause request is present. */
  maintenance?: () => Promise<boolean>;
  /** Runs after each tick attempt, including a failed one. */
  afterTick?: () => void;
  /** Receives the container id of each Collie container that the resident
   * starts or adopts. */
  collieStarted?: (containerId: string) => void;
  fs: {
    /** mkdir -p semantics by default; exclusive is for a newly derived,
     * run-keyed disposable root that must not adopt existing bytes.
     *
     * `mode` applies on creation only, and exists for exactly one directory:
     * the per-run log leaf, which a repository run's hardened
     * `--user 10002:10002` container must be able to write into. */
    mkdir(path: string, opts?: { exclusive?: boolean; mode?: number }): Promise<void>;
    /** opts.mode applies on creation (the plan sibling must be 0600: the
     * recheck's trust seam refuses a group/other-readable plan file). */
    writeFile(path: string, data: string, opts?: { mode?: number }): Promise<void>;
    /** Exact UTF-8 artifact recheck for Collie created-generation adoption. */
    readFile(path: string): Promise<string>;
    rename(source: string, destination: string): Promise<void>;
    /** Entry names of one directory; rejects with an ENOENT-coded error when
     * it is absent. The operator skill store is read through this. */
    readdir(path: string): Promise<string[]>;
    /** rm -f semantics (missing file is not an error). `recursive` is reserved
     * for a resident-derived disposable directory under MC_HOME/runs. */
    rm(path: string, opts?: { recursive?: boolean }): Promise<void>;
  };
  /** A capability minted only by this resident boot's green composed canary. */
  attachmentConfinement: { assertHealthy(): void };
  /** Host-only, dirfd-safe stable attachment tree creation/revalidation. */
  prepareAttachmentTree(session: string): Promise<AttachmentTreeReceipt>;
  /** The credential projection for real agent routes. An absent projector
   * refuses every non-fake launch. */
  credentials?: CredentialProjector;
  /** Boot-local Collie create-envelope/runtime proof. Rows are intentionally
   * lost on restart so an old unstarted container cannot be adopted. */
  collieRuntime: CollieRuntimeRegistry;
  config: ResidentConfig;
}

export interface AttachmentTreeReceipt {
  inbound_published: string;
  outbound: string;
  session: string;
}

export interface AttachmentCanaryReceipt {
  version: 1;
  generation: string;
  inbound_sha256: string;
  outbound_sha256: string;
  real_uid: 10002;
  effective_uid: 10001;
  saved_uid: 10001;
  fs_uid: 10001;
  warm_inbound_read: true;
  host_outbound_read: true;
  private_gate_healthy: true;
  denied_outbound: string[];
  restart_inbound_read: true;
  restart_outbound_read: true;
  outside_sessions: true;
}

/** One authorized bind of the committed mount plan, mc's closed carrier.
 * The resident consumes only source/destination/access;
 * device/inode/kind/mode/owner are host evidence it carries opaquely to the
 * `mc __mount-recheck` legs. Keys are alphabetical by contract. */
export interface MountPlanEntry {
  access: "ro" | "rw";
  destination: string;
  device: string;
  inode: string;
  kind: "dir" | "file";
  logical_id: string;
  mode: number;
  owner_uid: number;
  source: string;
}

/** The non-secret runtime capability the dispatch tick resolved out of the
 * enabled catalog in config.toml, carried with the effect.
 *
 * The resident owns no route catalog of its own: a launch it cannot build from
 * this carrier is a launch this deployment is not configured to make. The
 * resident requires these six keys and ignores every other key. No field
 * holds a credential value. */
export interface RuntimeBinding {
  authorization_id?: string;
  profile_revision?: string;
  effort?: string;
  base_url: string;
  channel: string;
  credential: string;
  harness: string;
  id: string;
  model: string;
}

/** The packet of a packet conversation, as the wake carries it and as run.json
 * holds it. */
export interface PacketIdentity {
  packet_id: number;
  version_id: number;
  task_id: number;
  project_id: string;
}

/** A host path with the identity evidence `mc __mount-recheck` re-proves. */
export interface PathIdentity {
  canonical: string;
  device: string;
  inode: string;
  owner_uid: number;
}

/** The committed spawn effect's validated mount plan; explicit, never absent. */
export interface MountPlan {
  /** The isolated repository of the run: the repository of its task, or the
   * repository of its wiki update. Absent on a run that reads no repository. */
  repository?: RepositoryPlan;
  entries: MountPlanEntry[];
  /** Run-scoped committed trees for the Strategist and the Arbiter. The real
   * Git roots stay setup-only read-only inputs; the agent receives the derived
   * tree and its empty `.git` cover. */
  seeding_projections?: Array<{
    git_cover_kind: "seeding-source-git-cover";
    object_format: "sha1" | "sha256";
    projection_kind: "seeding-committed-projection";
    target_ref: string;
    workspace_root: PathIdentity;
    project_id: string;
  }>;
  /** Host-attested profile synthetic HOME, mounted by the resident at the
   * fixed runtime plane `/home/agent`. */
  tool_home?: PathIdentity;
  version: number;
}

/** `mc dispatch` effect JSON, mirroring dispatch.Action.
 *
 * One call is one tick. Its stdout is the `tick` member, whose `actions` hold
 * the committed effects in tick order and whose `idle` lists the Projects
 * whose round committed nothing. Every pipeline effect names its Project. */
export type Effect =
  | {
      action: "tick";
      actions: Effect[];
      idle?: { project: string; reason: string }[];
    }
  | { action: "idle"; reason?: string }
  | {
      action: "spawn";
      run_id: string;
      role: string;
      subject_id: number | null;
      /** The Project of this run. An agent run holds the Project lease, or it
       * is the Scribe run of a wiki update. The Project names the container
       * (`mc-<project>-<run_id>`) and the operator skill store. */
      project: string;
      pool_ids: number[];
      harness: string;
      model_binding: string;
      /** The enabled capability the dispatch attest resolved for this route.
       * Required: the resident refuses a spawn it cannot describe from config. */
      runtime_binding: RuntimeBinding;
      /** Immutable opening input rendered inside the dispatch transaction. */
      brief: string;
      /** This run's shipped skills, command surface first, which the agent
       * runner installs before the harness starts. The envelope carries the
       * run's own role set alone. */
      skills: { name: string; body: string }[];
      /** The operator skill store directory this run reads, MC_HOME-relative.
       * Every run names one and the resident refuses a spawn without it. */
      skill_store_dir: string;
      /** The only mount authority the resident may effect. */
      mount_plan: MountPlan;
      /** Required nullable profile override. null means inherit the validated
       * deployment-wide identity; absence is a stripped security carrier. */
      git_identity_override: {
        author_email: string;
        author_name: string;
      } | null;
      session_path?: string;
      heartbeat_interval_s: number;
      environment?: {
        image_digest: string;
        configuration_revision: string;
        configuration: import("../../runner/shared/project-environment").ProjectEnvironment;
      };
    }
  // `reason` is informational for the log. An operator interrupt reaches the
  // resident as a reap carrying reason "interrupted", never as an action of
  // its own. The reap names the Project of the run, so the container to stop
  // is `mc-<project>-<run_id>`.
  | { action: "reap"; project: string; run_id: string; reason?: string; stop_container?: boolean }
  // The lease-free Collie tier. A wake carries no mount plan and no heartbeat
  // interval: the resident binds a fixed operator-read-scope set and the
  // runner never heartbeats. The run id is the session id.
  | {
      action: "collie-wake";
      /** The conversation keeps its original effort across launches. */
      effort?: string;
      session: string;
      vision_project_id?: string;
      /** The packet of a packet conversation. The resident binds the artifact
       * folder of its task read-only. Each other conversation has no packet. */
      packet?: PacketIdentity;
      directive: string;
      /** 16-hex launch generation the dispatch tick persisted. */
      launch: string;
      mode: "fresh" | "native" | "rows";
      /** Harness the session speaks (e.g. "claude"); drives the runner. */
      binding: string;
      /** The enabled capability `binding` resolved to at wake time. Required:
       * the resident refuses a wake it cannot describe from config. */
      runtime_binding: RuntimeBinding;
      /** Frozen host-relative trace directory from the Collie registry. */
      session_path: string;
      /** Required together for native mode; absent for fresh/rows. */
      native_session_ref?: string;
      trace_filename?: string;
      /** Container name the session is pinned to (mc-collie-<session>). */
      container_name: string;
      /** The Collie tier's shipped skills, command surface first, installed by
       * the runner before the harness starts. One static set for every
       * session. */
      skills: { name: string; body: string }[];
      /** The Collie tier's operator skill store directory, MC_HOME-relative. */
      skill_store_dir?: string;
      /** The session's frozen operator-verb allowlist, re-read from the
       * registry at wake, and what makes those verbs callable. It varies by
       * session. The container's `mc` admits a Collie operator verb only when
       * this list names it and the stored list agrees. Required, so an
       * envelope without it ships a Collie that can only read. */
      verb_allowlist: string[];
      /** The session idle timeout in seconds. The runner bounds its own life
       * with it when the stop never arrives. Absent from an older `mc`. */
      idle_timeout_s?: number;
    }
  | { action: "collie-stop"; session: string; container_id?: string; reason?: string }
  | {
      action: "collie-adopt";
      session: string;
      launch: string;
      container_id: string;
      container_name: string;
      envelope_digest: string;
      runtime_generation: string;
    }
  | {
      action: "collie-remove";
      session: string;
      launch: string;
      container_id: string;
      container_name: string;
      reason: "orphan" | "paused" | "restarting" | "removing" | "exited" | "dead" | "starting-timeout";
    };
