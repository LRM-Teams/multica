import {
  aggregateRuntimeHealthPresentation,
  deriveRuntimeHealth,
  runtimeCurrentVersion,
  runtimeTargetVersion,
  type RuntimeHealth,
  type RuntimeHealthPresentation,
} from "@multica/core/runtimes";
import { sandboxDisplayName } from "@multica/core/sandboxes/utils";
import type { AgentRuntime, SandboxInstance } from "@multica/core/types";
import { formatDeviceInfo } from "../utils";

export type RuntimeMachineSection = "local" | "remote" | "cloud";
export type RuntimeMachineFilter = "all" | "online" | "issues";

export interface RuntimeWorkloadSummary {
  runningCount: number;
  queuedCount: number;
}

export interface RuntimeMachine {
  id: string;
  daemonId: string | null;
  title: string;
  subtitle: string | null;
  deviceInfo: string | null;
  /**
   * Structured machine label from runtime `device_name` (register metadata).
   * Never derived by parsing `device_info`.
   */
  deviceName: string | null;
  cliVersion: string | null;
  mode: AgentRuntime["runtime_mode"];
  section: RuntimeMachineSection;
  isCurrent: boolean;
  health: RuntimeHealth;
  runtimeHealth: RuntimeHealthPresentation | null;
  updateError: string | null;
  updateTargetVersion: string | null;
  runtimes: AgentRuntime[];
  onlineCount: number;
  issueCount: number;
  runningCount: number;
  queuedCount: number;
  providerNames: string[];
  lastSeenAt: string | null;
  /**
   * Cloud computer created via sandbox/docker that has not yet registered a
   * daemon runtime. Shown in the Computers sidebar with a gray (offline) dot.
   */
  pendingCloud?: boolean;
  /** Sandbox instance id when this row is (or was) backed by a cloud sandbox. */
  sandboxInstanceId?: string | null;
  /** Owner for pending rows that have no runtimes yet. */
  ownerUserId?: string | null;
}

interface RuntimeMachineOptions {
  now: number;
  localDaemonId?: string | null;
  localMachineName?: string | null;
  /**
   * The viewing user's id. Used to scope the device-name consolidation
   * below: the runtime list is workspace-wide (every member's runtimes),
   * so matching purely on a host name could promote another member's
   * identically-named machine to "this machine". Only a local runtime
   * OWNED by the current user may be consolidated by device name.
   */
  currentUserId?: string | null;
  workloadByRuntimeId?: Map<string, RuntimeWorkloadSummary>;
  /**
   * When true, guarantee that the result contains a machine flagged
   * `isCurrent`. If no server-side runtime matches the local daemon
   * (e.g. the daemon is stopped, was never started, or its runtime was
   * already GC'd), a placeholder local machine is synthesized so the
   * caller can still attach controls to it (Start button, etc.).
   * Desktop sets this; web omits it.
   */
  ensureLocalMachine?: boolean;
}

interface RuntimeMachineDraft {
  id: string;
  daemonId: string | null;
  mode: AgentRuntime["runtime_mode"];
  runtimes: AgentRuntime[];
}

const HEALTH_SEVERITY: Record<RuntimeHealth, number> = {
  online: 0,
  recently_lost: 1,
  offline: 2,
  about_to_gc: 3,
};

// Connectivity states that already read as "this machine is unreachable".
// When the title row already shows one of these via the single connectivity
// dot+label, a second `runtimeHealth === "offline"` badge is a duplicate and
// must be suppressed (LRM-624 / Plan A).
const OFFLINE_CONNECTIVITY: ReadonlySet<RuntimeHealth> = new Set([
  "offline",
  "recently_lost",
  "about_to_gc",
]);

/**
 * Selects the runtimeHealth value (if any) the machine-detail title row should
 * render as a secondary badge. The only remaining case is `offline`: when a
 * machine is reachable but one of its runtimes individually reports offline,
 * that's a real signal, not a duplicate of the connectivity dot — unless
 * connectivity is already `offline` / `recently_lost` / `about_to_gc`, in
 * which case a second "Offline" badge would be a duplicate and is suppressed.
 *
 * `update_available` / `ready_to_apply` / `updating` / `failed` are
 * deliberately never badged here: this page already has a dedicated upgrade
 * control (task #1680) that states the same fact ("this machine needs an
 * update" / "already on the latest version") — a second badge for it on the
 * same page would say the same thing twice.
 */
export function headerRuntimeHealthBadge(
  runtimeHealth: RuntimeHealthPresentation | null,
  connectivity: RuntimeHealth,
): RuntimeHealthPresentation | null {
  if (runtimeHealth !== "offline") return null;
  if (OFFLINE_CONNECTIVITY.has(connectivity)) {
    return null;
  }
  return runtimeHealth;
}

// A machine belongs to "mine" if the current user owns any runtime on it —
// a machine's runtimes normally share one owner, so this is unambiguous in
// practice. No dedicated query: reuses the owner_id the runtime list query
// already returns.
export function isMineMachine(
  machine: RuntimeMachine,
  currentUserId: string | null,
): boolean {
  if (!currentUserId) return false;
  if (machine.pendingCloud) {
    return machine.ownerUserId === currentUserId;
  }
  return machine.runtimes.some((r) => r.owner_id === currentUserId);
}

/**
 * LRM-1094 — desktop detail default: isCurrent → first Mine machine.
 * Never fall back to Team `machines[0]`.
 */
export function defaultDesktopSelectedMachineId(
  machines: RuntimeMachine[],
  currentUserId: string | null,
): string | null {
  const current = machines.find((m) => m.isCurrent);
  if (current) return current.id;
  const mine = machines.find((m) => isMineMachine(m, currentUserId));
  return mine?.id ?? null;
}

export function splitRuntimeName(name: string): {
  base: string;
  hostname: string | null;
} {
  if (!name) return { base: name ?? "", hostname: null };
  const m = name.match(/^(.+?)\s+\(([^)]+)\)$/);
  if (!m || !m[1] || !m[2]) return { base: name, hostname: null };
  return { base: m[1], hostname: m[2] };
}

export function buildRuntimeMachines(
  runtimes: AgentRuntime[],
  options: RuntimeMachineOptions,
): RuntimeMachine[] {
  const drafts = new Map<string, RuntimeMachineDraft>();

  for (const runtime of runtimes) {
    const id = runtimeMachineKey(runtime);
    const draft =
      drafts.get(id) ??
      ({
        id,
        daemonId: runtime.daemon_id,
        mode: runtime.runtime_mode,
        runtimes: [],
      } satisfies RuntimeMachineDraft);
    draft.runtimes.push(runtime);
    drafts.set(id, draft);
  }

  const machines = Array.from(drafts.values()).map((draft) =>
    finalizeRuntimeMachine(draft, options),
  );

  if (options.ensureLocalMachine && !machines.some((m) => m.isCurrent)) {
    machines.push(placeholderLocalMachine(options));
  }

  return machines.sort(compareRuntimeMachines);
}

const PENDING_CLOUD_STATUSES = new Set([
  "pending",
  "creating",
  "running",
  "resuming",
  "reconfiguring",
  "stopping",
]);

/** Stable Computers sidebar id for a cloud computer before daemon register. */
export function pendingCloudComputerMachineId(instanceId: string): string {
  return `sandbox:instance:${instanceId}`;
}

export function isDockerCloudComputerInstance(instance: SandboxInstance): boolean {
  const endpointKind =
    typeof instance.endpoint_info?.kind === "string" ? instance.endpoint_info.kind : "";
  const creationMode =
    typeof instance.metadata?.creation_mode === "string"
      ? instance.metadata.creation_mode
      : "";
  return (
    endpointKind === "docker" ||
    creationMode === "docker_container" ||
    instance.template.startsWith("docker:")
  );
}

function sandboxInstanceDaemonId(instance: SandboxInstance): string | null {
  const runtimeEnv = instance.metadata?.runtime_env;
  if (!runtimeEnv || typeof runtimeEnv !== "object" || Array.isArray(runtimeEnv)) {
    return null;
  }
  const daemonId = (runtimeEnv as Record<string, unknown>).MULTICA_DAEMON_ID;
  return typeof daemonId === "string" && daemonId.trim() ? daemonId.trim() : null;
}

function runtimeSandboxInstanceId(runtime: AgentRuntime): string | null {
  const sid = runtime.metadata?.sandbox_instance_id;
  return typeof sid === "string" && sid.trim() ? sid.trim() : null;
}

/**
 * Build a gray-dot sidebar row for a cloud computer whose daemon has not
 * registered yet. Does not touch the Desktop "your computer" placeholder path.
 */
export function buildPendingCloudComputerMachine(
  instance: SandboxInstance,
): RuntimeMachine {
  const daemonId = sandboxInstanceDaemonId(instance);
  return {
    id: pendingCloudComputerMachineId(instance.id),
    daemonId,
    title: sandboxDisplayName(instance),
    subtitle: null,
    deviceInfo: null,
    deviceName: null,
    cliVersion: null,
    mode: "local",
    section: "remote",
    isCurrent: false,
    health: "offline",
    runtimeHealth: null,
    updateError: null,
    updateTargetVersion: null,
    runtimes: [],
    onlineCount: 0,
    issueCount: 0,
    runningCount: 0,
    queuedCount: 0,
    providerNames: [],
    lastSeenAt: null,
    pendingCloud: true,
    sandboxInstanceId: instance.id,
    ownerUserId: instance.creator_user_id,
  };
}

/**
 * Append pending cloud-computer rows for docker sandbox instances that do not
 * yet have a matching registered runtime. Existing runtime machines win.
 */
export function mergePendingCloudComputers(
  machines: RuntimeMachine[],
  sandboxInstances: SandboxInstance[],
): RuntimeMachine[] {
  const claimedSandboxIds = new Set<string>();
  const claimedDaemonIds = new Set<string>();

  for (const machine of machines) {
    if (machine.daemonId) claimedDaemonIds.add(machine.daemonId);
    for (const runtime of machine.runtimes) {
      const sid = runtimeSandboxInstanceId(runtime);
      if (sid) claimedSandboxIds.add(sid);
      if (runtime.daemon_id) claimedDaemonIds.add(runtime.daemon_id);
    }
  }

  const pending = sandboxInstances
    .filter(isDockerCloudComputerInstance)
    .filter((instance) => PENDING_CLOUD_STATUSES.has(instance.status))
    .filter((instance) => !claimedSandboxIds.has(instance.id))
    .filter((instance) => {
      const daemonId = sandboxInstanceDaemonId(instance);
      return !daemonId || !claimedDaemonIds.has(daemonId);
    })
    .map(buildPendingCloudComputerMachine);

  if (pending.length === 0) return machines;
  return [...machines, ...pending].sort(compareRuntimeMachines);
}

/**
 * When a pending cloud row is replaced by a registered daemon machine, map the
 * selection id so the detail pane stays on the same computer.
 */
export function resolveCloudComputerSelectionId(
  machines: RuntimeMachine[],
  selectedId: string | null,
): string | null {
  if (!selectedId) return null;
  if (machines.some((machine) => machine.id === selectedId)) return selectedId;
  if (!selectedId.startsWith("sandbox:instance:")) return selectedId;
  const sandboxId = selectedId.slice("sandbox:instance:".length);
  const match = machines.find(
    (machine) =>
      !machine.pendingCloud &&
      machine.runtimes.some(
        (runtime) => runtimeSandboxInstanceId(runtime) === sandboxId,
      ),
  );
  return match?.id ?? selectedId;
}

/** True when this Computers row was created via Cloud computer (sandbox/docker). */
export function isCloudComputerMachine(machine: RuntimeMachine): boolean {
  return machine.pendingCloud === true || !!machine.sandboxInstanceId?.trim();
}

/** Owner check for deleting a cloud computer (pending or registered). */
export function canDeleteCloudComputerMachine(
  machine: RuntimeMachine,
  userId: string | null | undefined,
): boolean {
  if (!userId || !isCloudComputerMachine(machine)) return false;
  if (machine.pendingCloud || machine.runtimes.length === 0) {
    return machine.ownerUserId === userId;
  }
  return machine.runtimes.every((runtime) => runtime.owner_id === userId);
}

/**
 * Keep Computers sidebar labels stable across pending → daemon-connected.
 * Uses the sandbox create-time name whenever the user has not set display_name.
 * No-op for non-sandbox ("your computer") machines.
 */
export function decorateCloudComputerMachines(
  machines: RuntimeMachine[],
  sandboxInstances: SandboxInstance[],
): RuntimeMachine[] {
  if (sandboxInstances.length === 0) {
    return machines.map((machine) => {
      if (machine.pendingCloud || machine.sandboxInstanceId) return machine;
      let sandboxId: string | null = null;
      for (const runtime of machine.runtimes) {
        const sid = runtimeSandboxInstanceId(runtime);
        if (sid) {
          sandboxId = sid;
          break;
        }
      }
      return sandboxId ? { ...machine, sandboxInstanceId: sandboxId } : machine;
    });
  }

  const byId = new Map(
    sandboxInstances.map((instance) => [instance.id, instance] as const),
  );

  return machines.map((machine) => {
    if (machine.pendingCloud) return machine;

    let sandboxId = machine.sandboxInstanceId ?? null;
    if (!sandboxId) {
      for (const runtime of machine.runtimes) {
        const sid = runtimeSandboxInstanceId(runtime);
        if (sid) {
          sandboxId = sid;
          break;
        }
      }
    }
    if (!sandboxId) return machine;

    const instance = byId.get(sandboxId);
    const hasDisplayName = firstNonEmptyDisplayName(machine.runtimes) !== null;
    if (!instance) {
      return sandboxId === machine.sandboxInstanceId
        ? machine
        : { ...machine, sandboxInstanceId: sandboxId };
    }

    const createName = sandboxDisplayName(instance);
    return {
      ...machine,
      sandboxInstanceId: sandboxId,
      title: hasDisplayName ? machine.title : createName,
    };
  });
}

function placeholderLocalMachine(
  options: RuntimeMachineOptions,
): RuntimeMachine {
  const daemonId = options.localDaemonId ?? null;
  return {
    id: daemonId ? `local:${daemonId}` : "local:placeholder",
    daemonId,
    title: options.localMachineName ?? "This machine",
    subtitle: null,
    deviceInfo: null,
    deviceName: null,
    cliVersion: null,
    mode: "local",
    section: "local",
    isCurrent: true,
    health: "offline",
    runtimeHealth: null,
    updateError: null,
    updateTargetVersion: null,
    runtimes: [],
    onlineCount: 0,
    issueCount: 0,
    runningCount: 0,
    queuedCount: 0,
    providerNames: [],
    lastSeenAt: null,
  };
}

export function filterRuntimeMachines(
  machines: RuntimeMachine[],
  query: string,
  filter: RuntimeMachineFilter,
): RuntimeMachine[] {
  const q = query.trim().toLowerCase();
  return machines.filter((machine) => {
    if (filter === "online" && machine.onlineCount === 0) return false;
    if (filter === "issues" && machine.issueCount === 0) return false;
    if (!q) return true;

    const haystack = [
      machine.title,
      machine.subtitle,
      machine.deviceInfo,
      machine.daemonId,
      machine.updateError,
      machine.providerNames.join(" "),
      machine.runtimes.map((runtime) => runtime.name).join(" "),
    ]
      .filter(Boolean)
      .join(" ")
      .toLowerCase();

    return haystack.includes(q);
  });
}

export function runtimeMachineCounts(machines: RuntimeMachine[]): {
  all: number;
  online: number;
  issues: number;
} {
  return {
    all: machines.length,
    online: machines.filter((machine) => machine.onlineCount > 0).length,
    issues: machines.filter((machine) => machine.issueCount > 0).length,
  };
}

function finalizeRuntimeMachine(
  draft: RuntimeMachineDraft,
  options: RuntimeMachineOptions,
): RuntimeMachine {
  const runtimes = draft.runtimes.toSorted((a, b) =>
    a.provider.localeCompare(b.provider),
  );
  const first = runtimes[0];
  const providerNames = Array.from(new Set(runtimes.map((r) => r.provider))).sort();
  // Device-name consolidation is only safe for the current user's own
  // local runtimes — the list spans the whole workspace, so a host-name
  // match alone could claim another member's identically-named machine.
  const ownsLocalRuntime =
    !!options.currentUserId &&
    runtimes.some((r) => r.owner_id === options.currentUserId);
  const matchesLocalName = (value: string | null | undefined): boolean =>
    !!value && value.toLowerCase() === options.localMachineName?.toLowerCase();
  const isCurrent =
    (!!options.localDaemonId &&
      draft.daemonId === options.localDaemonId &&
      (!options.currentUserId || ownsLocalRuntime)) ||
    (draft.mode === "local" &&
      !!options.localMachineName &&
      ownsLocalRuntime &&
      (matchesLocalName(draft.daemonId) ||
        runtimes.some((r) => matchesLocalName(runtimeDeviceName(r)))));
  const title = machineTitle(runtimes, {
    isCurrent,
    localMachineName: options.localMachineName,
  });
  const deviceInfo = first ? formatDeviceInfo(first.device_info ?? null) : null;
  const deviceName = machineDeviceName(runtimes);
  const subtitle = machineSubtitle({
    title,
    deviceInfo,
    daemonId: draft.daemonId,
    mode: draft.mode,
  });
  const healthByRuntime = runtimes.map((runtime) =>
    deriveRuntimeHealth(runtime, options.now),
  );
  // Per-runtime online/issue counts stay on runtime heartbeats — filters and
  // Code Agents rows answer "is this provider alive". Machine title-row
  // connectivity (`health` / `lastSeenAt`) uses the daemon's own heartbeat
  // when the server provides it (task #58 / #1696).
  const onlineCount = healthByRuntime.filter((h) => h === "online").length;
  const issueCount = runtimes.length - onlineCount;
  const health = deriveMachineConnectivityHealth(runtimes, healthByRuntime);
  const updateIssueRuntime =
    runtimes.find(
      (runtime) => runtime.runtime_health === "failed" && runtime.update_error,
    ) ?? runtimes.find((runtime) => runtime.update_error);
  const workload = runtimes.reduce(
    (sum, runtime) => {
      const entry = options.workloadByRuntimeId?.get(runtime.id);
      return {
        runningCount: sum.runningCount + (entry?.runningCount ?? 0),
        queuedCount: sum.queuedCount + (entry?.queuedCount ?? 0),
      };
    },
    { runningCount: 0, queuedCount: 0 },
  );

  return {
    id: draft.id,
    daemonId: draft.daemonId,
    title,
    subtitle,
    deviceInfo,
    deviceName,
    cliVersion: commonCliVersion(runtimes),
    mode: draft.mode,
    section: isCurrent ? "local" : draft.mode === "cloud" ? "cloud" : "remote",
    isCurrent,
    health,
    runtimeHealth: aggregateRuntimeHealthPresentation(runtimes),
    updateError: updateIssueRuntime?.update_error?.trim() || null,
    updateTargetVersion: updateIssueRuntime
      ? runtimeTargetVersion(updateIssueRuntime)
      : null,
    runtimes,
    onlineCount,
    issueCount,
    runningCount: workload.runningCount,
    queuedCount: workload.queuedCount,
    providerNames,
    lastSeenAt: latestMachineLastSeenAt(runtimes),
  };
}

/**
 * Machine title-row connectivity: prefer the daemon's own heartbeat
 * (`computer_connected`, task #58) so "Online" means the computer is
 * reachable — not "some runtime on it still has a fresh last_seen_at".
 * Falls back to aggregating per-runtime deriveRuntimeHealth only when the
 * response predates #1696 (field absent on every runtime).
 *
 * Binary Online/Offline when the daemon field is present — no recently_lost
 * / about_to_gc on this axis (Parker B-(i), 2026-08-02).
 */
function deriveMachineConnectivityHealth(
  runtimes: AgentRuntime[],
  healthByRuntime: RuntimeHealth[],
): RuntimeHealth {
  const withDaemonField = runtimes.filter(
    (runtime) => typeof runtime.computer_connected === "boolean",
  );
  if (withDaemonField.length > 0) {
    return withDaemonField.some((runtime) => runtime.computer_connected)
      ? "online"
      : "offline";
  }
  const onlineCount = healthByRuntime.filter((h) => h === "online").length;
  if (onlineCount > 0) return "online";
  return healthByRuntime.reduce<RuntimeHealth>(
    (worst, current) =>
      HEALTH_SEVERITY[current] > HEALTH_SEVERITY[worst] ? current : worst,
    "recently_lost",
  );
}

/** Stable machine key used to group code agents that share one computer. */
export function runtimeMachineKey(runtime: AgentRuntime): string {
  if (runtime.daemon_id) return `${runtime.runtime_mode}:${runtime.daemon_id}`;
  const deviceName = runtimeDeviceName(runtime);
  if (deviceName) return `${runtime.runtime_mode}:device:${deviceName}`;
  return `${runtime.runtime_mode}:runtime:${runtime.id}`;
}

/**
 * Authorization-side "same computer" check (LRM-1365 / Frank 2026-08-02).
 * Mirrors server `runtimesShareMachine`: only non-empty `daemon_id` + matching
 * `runtime_mode` count. Do **not** fall back to hostname parsed from
 * `name`/`device_info` — free text is display-only and can wrongly merge two
 * physical machines (or keep a cross-machine option visible in the picker).
 * A runtime with no daemon_id never shares a machine with anything else.
 */
export function runtimesShareMachine(
  a: AgentRuntime,
  b: AgentRuntime,
): boolean {
  if (a.runtime_mode !== b.runtime_mode) return false;
  const aDaemon = a.daemon_id?.trim() ?? "";
  const bDaemon = b.daemon_id?.trim() ?? "";
  if (!aDaemon || !bDaemon) return false;
  return aDaemon === bDaemon;
}

/**
 * Runtimes the agent may switch to while its computer stays fixed.
 * Always includes `bound` itself (even when it has no daemon_id). Peers must
 * share a machine via {@link runtimesShareMachine}.
 */
export function filterRuntimesOnBoundComputer(
  bound: AgentRuntime,
  runtimes: readonly AgentRuntime[],
): AgentRuntime[] {
  return runtimes.filter(
    (r) => r.id === bound.id || runtimesShareMachine(bound, r),
  );
}

// `name` is the daemon-reported raw hostname (e.g. "Cursor (ubuntu)");
// `display_name` is the user-editable label set on the machine detail page.
// Runtime pickers were showing the raw name unconditionally — renaming a
// machine there had no effect in the picker, same "can't tell them apart"
// shape as the runtimes-page ownership grouping. Trimmed-empty falls back
// to name, matching `machine-name-editor.tsx`'s currentDisplayName.
export function runtimeDisplayLabel(runtime: AgentRuntime): string {
  return runtime.display_name?.trim() || runtime.name;
}

/**
 * Machine identity for "which computer" surfaces (ComputerInfoRow), matching
 * machine-detail title priority: user rename → hostname parenthetical →
 * structured device_name. Never falls back to the full `Provider (host)`
 * runtime.name — that reads as a code-agent label, which Frank called out
 * on 2026-08-02 when Info showed the runtime name instead of the computer.
 */
export function runtimeComputerLabel(runtime: AgentRuntime): string {
  const displayName = runtime.display_name?.trim();
  if (displayName) return displayName;

  const hostname = splitRuntimeName(runtime.name ?? "").hostname?.trim();
  if (hostname) return hostname;

  const structured = runtime.device_name?.trim();
  if (structured) return structured;

  // Last resort: device_info first segment / short daemon id — still not
  // the "Cursor (…)" provider string.
  const fromDeviceInfo = runtime.device_info?.trim().split(" · ")[0]?.trim();
  if (fromDeviceInfo) return fromDeviceInfo;

  if (runtime.daemon_id) return shortDaemonId(runtime.daemon_id);
  return runtime.name?.trim() || "—";
}

export function runtimeDeviceName(runtime: AgentRuntime): string | null {
  const host = splitRuntimeName(runtime.name).hostname;
  if (host) return host;

  const raw = runtime.device_info?.trim();
  if (!raw) return null;
  return raw.split(" · ")[0]?.trim() || null;
}

/** First non-empty structured `device_name` on the machine — never parse glue. */
export function machineDeviceName(runtimes: AgentRuntime[]): string | null {
  for (const runtime of runtimes) {
    const value = runtime.device_name?.trim();
    if (value) return value;
  }
  return null;
}

function firstNonEmptyDisplayName(runtimes: AgentRuntime[]): string | null {
  for (const runtime of runtimes) {
    const value = runtime.display_name?.trim();
    if (value) return value;
  }
  return null;
}

function machineTitle(
  runtimes: AgentRuntime[],
  options: { isCurrent: boolean; localMachineName?: string | null },
): string {
  const displayName = firstNonEmptyDisplayName(runtimes);
  if (displayName) return displayName;

  if (options.isCurrent && options.localMachineName) {
    return options.localMachineName;
  }

  const first = runtimes[0];
  if (!first) return "Unknown machine";

  const deviceName = runtimeDeviceName(first);
  if (deviceName) return deviceName;

  if (first.runtime_mode === "cloud") {
    return `${capitalize(first.provider)} cloud`;
  }
  return first.daemon_id ? shortDaemonId(first.daemon_id) : "Unknown machine";
}

/** Hostname placeholder when display_name is unset (grey label in rename UI). */
export function machineHostname(machine: RuntimeMachine): string | null {
  for (const runtime of machine.runtimes) {
    const host = runtimeDeviceName(runtime);
    if (host) return host;
  }
  // Pending cloud rows have a daemon id before register; never use the truncated
  // id as the visible label — keep the create-time sandbox name (machine.title).
  if (machine.pendingCloud) return null;
  if (machine.isCurrent && machine.title) return machine.title;
  return machine.daemonId ? shortDaemonId(machine.daemonId) : null;
}

/** Prefer an online runtime id for API calls scoped to one daemon host. */
export function machinePrimaryRuntimeId(
  machine: RuntimeMachine,
  now: number,
): string | null {
  if (machine.runtimes.length === 0) return null;
  const online = machine.runtimes.find(
    (runtime) => deriveRuntimeHealth(runtime, now) === "online",
  );
  return online?.id ?? machine.runtimes[0]?.id ?? null;
}

/**
 * Select the runtime whose update observation must drive the machine-level
 * Daemon row. A machine can have several online runtime rows; using the
 * general primary runtime here can hide an update reported by one of its
 * siblings. Other daemon-scoped actions should continue to use
 * machinePrimaryRuntimeId.
 */
export function machineDaemonUpgradeRuntimeId(
  machine: RuntimeMachine,
  now: number,
): string | null {
  const updateAvailable = machine.runtimes.find(
    (runtime) => runtime.runtime_health === "update_available",
  );
  return updateAvailable?.id ?? machinePrimaryRuntimeId(machine, now);
}

function machineSubtitle({
  title,
  deviceInfo,
  daemonId,
  mode,
}: {
  title: string;
  deviceInfo: string | null;
  daemonId: string | null;
  mode: AgentRuntime["runtime_mode"];
}): string | null {
  const compact = compactDeviceInfo(deviceInfo, title);
  if (compact) return compact;
  if (daemonId) return `daemon ${shortDaemonId(daemonId)}`;
  return mode === "cloud" ? "Cloud worker" : null;
}

function compactDeviceInfo(
  deviceInfo: string | null,
  title: string,
): string | null {
  if (!deviceInfo) return null;
  const parts = deviceInfo
    .split(" · ")
    .map((part) => part.trim())
    .filter(Boolean)
    .filter((part) => part !== title)
    .filter((part) => !isAgentVersionLike(part));
  const primary = parts[0];
  if (!primary) return null;

  // Reshape OS+arch produced by formatDeviceInfo (e.g. "macOS (x86_64)")
  // into the more scannable "x86_64 macOS". Version strings — the only
  // other shape that historically carried parens — are filtered out
  // above so they can't pollute the per-machine subtitle.
  const osArch = primary.match(/^(.+?)\s+\(([^)]+)\)$/);
  if (osArch?.[1] && osArch[2]) {
    return `${osArch[2]} ${osArch[1]}`;
  }
  return primary;
}

// True for parts that carry an agent CLI version, not machine info —
// e.g. "2.1.5 (Claude Code)", "codex-cli 0.118.0", "1.0.20", "claude 1.0.0".
// Those describe a runtime, not the host, so they should never become a
// machine's subtitle (otherwise every claude-equipped daemon's row reads
// "Claude Code …", drowning out actual per-machine differences).
function isAgentVersionLike(part: string): boolean {
  return /(?:^|\s)v?\d+\.\d+\.\d+/.test(part);
}

function latestLastSeenAt(runtimes: AgentRuntime[]): string | null {
  let latest: string | null = null;
  for (const runtime of runtimes) {
    if (!runtime.last_seen_at) continue;
    if (!latest || new Date(runtime.last_seen_at) > new Date(latest)) {
      latest = runtime.last_seen_at;
    }
  }
  return latest;
}

/** Prefer daemon heartbeat timestamp when present; else runtime last_seen. */
function latestMachineLastSeenAt(runtimes: AgentRuntime[]): string | null {
  let latest: string | null = null;
  let sawDaemon = false;
  for (const runtime of runtimes) {
    const daemonSeen = runtime.daemon_last_seen_at;
    if (!daemonSeen) continue;
    sawDaemon = true;
    if (!latest || new Date(daemonSeen) > new Date(latest)) {
      latest = daemonSeen;
    }
  }
  if (sawDaemon) return latest;
  return latestLastSeenAt(runtimes);
}

/**
 * Daemon version shown in machine Basics. All runtimes on one machine share
 * the daemon, so the strict "every runtime agrees" read would null the row
 * whenever a stale runtime still carries the daemon's previous version —
 * one-off code-agent crashes leave exactly that residue (Frank 2026-08-03:
 * live Pi/Cursor on 0.3.95 + crashed Grok holding 0.3.94 hid the row
 * entirely). Trust the freshest sighting instead: runtimes still reported
 * offline by the health check are dropped from the pool when any active one
 * exists, so a crashed runtime's old version never out-votes the live ones;
 * fall back to every runtime only when nothing is online (daemon stopped —
 * the version is informational then anyway). `v` prefixes are normalized so
 * `0.3.94` and `v0.3.94` count as the same.
 */
function commonCliVersion(runtimes: AgentRuntime[]): string | null {
  const active = runtimes.filter(
    (runtime) => runtime.runtime_health !== "offline",
  );
  const pool = active.length > 0 ? active : runtimes;
  const byNorm = new Map<string, string>();
  for (const runtime of pool) {
    const version = runtimeCurrentVersion(runtime);
    if (!version) continue;
    const norm = version.replace(/^v/i, "");
    if (!byNorm.has(norm)) byNorm.set(norm, version);
  }
  if (byNorm.size === 0) return null;
  if (byNorm.size === 1) return Array.from(byNorm.values())[0] ?? null;

  // Disagreement inside the pool: freshest runtime-level sighting wins.
  // `daemon_last_seen_at` is daemon-level — identical on every runtime row
  // of one machine, so it can't break this tie.
  const seen = (runtime: AgentRuntime): number => {
    const at = Date.parse(runtime.last_seen_at ?? "");
    return Number.isNaN(at) ? 0 : at;
  };
  let best: { version: string; at: number } | null = null;
  for (const runtime of pool) {
    const version = runtimeCurrentVersion(runtime);
    if (!version) continue;
    const at = seen(runtime);
    if (!best || at > best.at) best = { version, at };
  }
  return best?.version ?? null;
}

/** Short form for machine-detail / ops diagnostics (e.g. `a1b2c3··ef`). */
export function shortDaemonId(daemonId: string): string {
  return daemonId.length > 12 ? `${daemonId.slice(0, 8)}...` : daemonId;
}

function capitalize(value: string): string {
  if (!value) return "Runtime";
  return `${value.slice(0, 1).toUpperCase()}${value.slice(1)}`;
}

function compareRuntimeMachines(a: RuntimeMachine, b: RuntimeMachine): number {
  if (a.isCurrent !== b.isCurrent) return a.isCurrent ? -1 : 1;
  const sectionDelta = sectionRank(a.section) - sectionRank(b.section);
  if (sectionDelta !== 0) return sectionDelta;
  if (a.onlineCount !== b.onlineCount) return b.onlineCount - a.onlineCount;
  return a.title.localeCompare(b.title);
}

function sectionRank(section: RuntimeMachineSection): number {
  switch (section) {
    case "local":
      return 0;
    case "remote":
      return 1;
    case "cloud":
      return 2;
  }
}
