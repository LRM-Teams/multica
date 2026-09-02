/**
 * Period Work collector selection helpers (ADR 0019).
 *
 * Collectors are dedicated Agents provisioned per local Computer (daemon_id),
 * not arbitrary specialty Agents. Collection is Computer-owner-only: a member
 * may only select collectors bound to Computers they own — never another
 * member's machine, even when that runtime is workspace-visible / public.
 */

import type { Agent, AgentRuntime } from "../types";
import { isPeriodBriefAgent } from "./period-brief-agent";

export const PERIOD_BRIEF_COLLECTOR_NAME_PREFIX = "period-collect-";
export const PERIOD_BRIEF_COLLECTOR_DISPLAY_LEAD = "采集 · ";
const PERIOD_BRIEF_COLLECTOR_DAEMON_SLUG_LEN = 8;
const PERIOD_BRIEF_COLLECTOR_NON_SLUG = /[^a-z0-9]+/g;

export type PeriodBriefCollectorCandidate = Pick<
  Agent,
  "id" | "name" | "display_name" | "runtime_id" | "runtime_mode" | "runtime_status" | "owner_id"
>;

export type PeriodBriefCollectorRuntime = Pick<AgentRuntime, "id" | "status" | "owner_id">;

export type PeriodBriefCollectorSlotRuntime = Pick<
  AgentRuntime,
  "id" | "daemon_id" | "runtime_mode" | "owner_id" | "display_name" | "name" | "status"
>;

export type PeriodBriefCollectorComputer = {
  daemon_id: string;
  owner_id: string;
  deviceName?: string | null;
};

/** One owned Computer that should have a dedicated Period Work collector. */
export type PeriodBriefCollectorSlot = {
  key: string;
  machineId: string;
  label: string;
  expectedName: string;
  runtimeIds: string[];
  collector: PeriodBriefCollectorCandidate | null;
  strayAgentId: string | null;
  /** Computer has a bindable runtime but no collector on it. */
  needsSetup: boolean;
  /** Computer is connected, but no provider runtime has registered yet. */
  needsRuntime: boolean;
};

/** True when this Agent is a provisioned Period Work collector. */
export function isPeriodBriefCollectorAgent(
  agent: Pick<Agent, "name"> | null | undefined,
): boolean {
  return Boolean(agent?.name?.startsWith(PERIOD_BRIEF_COLLECTOR_NAME_PREFIX));
}

/**
 * True when the collector's bound Computer is owned by `userId`.
 * Prefer runtime.owner_id; fall back to agent.owner_id when the runtime row is
 * missing from the local cache (still fail closed if neither matches).
 */
export function isPeriodBriefCollectorOwnedByUser(
  agent: PeriodBriefCollectorCandidate,
  runtimes: readonly PeriodBriefCollectorRuntime[],
  userId: string | null | undefined,
): boolean {
  const uid = userId?.trim();
  if (!uid) return false;
  const runtime = runtimes.find((item) => item.id === agent.runtime_id);
  if (runtime?.owner_id) {
    return runtime.owner_id === uid;
  }
  return agent.owner_id === uid;
}

/** Collectors only — synthesizer and specialty Agents are excluded. */
export function listPeriodBriefCollectorAgents<T extends Pick<Agent, "name">>(
  agents: readonly T[],
): T[] {
  return agents.filter((agent) => isPeriodBriefCollectorAgent(agent) && !isPeriodBriefAgent(agent));
}

/** Collectors the caller may dispatch — own Computers only. */
export function listOwnedPeriodBriefCollectorAgents(
  agents: readonly PeriodBriefCollectorCandidate[],
  runtimes: readonly PeriodBriefCollectorRuntime[],
  userId: string | null | undefined,
): PeriodBriefCollectorCandidate[] {
  return listPeriodBriefCollectorAgents(agents).filter((agent) =>
    isPeriodBriefCollectorOwnedByUser(agent, runtimes, userId),
  );
}

/** True when the Agent's bound runtime is currently online (local or cloud). */
export function isPeriodBriefCollectorOnline(
  agent: PeriodBriefCollectorCandidate,
  runtimes: readonly Pick<AgentRuntime, "id" | "status">[] = [],
): boolean {
  if (agent.runtime_status === "online") return true;
  if (agent.runtime_status === "offline") return false;
  const runtime = runtimes.find((item) => item.id === agent.runtime_id);
  return runtime?.status === "online";
}

/**
 * Default collector set: online dedicated Period Work collectors on Computers
 * owned by the current user.
 */
export function defaultPeriodBriefCollectorIds(
  agents: readonly PeriodBriefCollectorCandidate[],
  runtimes: readonly PeriodBriefCollectorRuntime[] = [],
  userId?: string | null,
): string[] {
  const owned =
    userId === undefined
      ? listPeriodBriefCollectorAgents(agents)
      : listOwnedPeriodBriefCollectorAgents(agents, runtimes, userId);
  return owned
    .filter((agent) => isPeriodBriefCollectorOnline(agent, runtimes))
    .map((agent) => agent.id);
}

export function togglePeriodBriefCollectorId(
  selected: readonly string[],
  agentId: string,
): string[] {
  if (selected.includes(agentId)) {
    return selected.filter((id) => id !== agentId);
  }
  return [...selected, agentId];
}

/** Daemon id used to read/write Computer-local Period Work collect roots. */
export function periodBriefCollectorDaemonId(
  agent: Pick<PeriodBriefCollectorCandidate, "runtime_id">,
  runtimes: readonly PeriodBriefCollectorSlotRuntime[],
): string | null {
  const runtime = runtimes.find((item) => item.id === agent.runtime_id);
  const daemon = runtime?.daemon_id?.trim();
  return daemon || null;
}

/** Daemon id for a collector slot, including computers that still need setup. */
export function periodBriefSlotDaemonId(
  slot: Pick<PeriodBriefCollectorSlot, "machineId" | "runtimeIds">,
  runtimes: readonly PeriodBriefCollectorSlotRuntime[] = [],
): string | null {
  for (const runtimeId of slot.runtimeIds) {
    const runtime = runtimes.find((item) => item.id === runtimeId);
    const daemon = runtime?.daemon_id?.trim();
    if (daemon) return daemon;
  }
  return periodBriefMachineDaemonId(slot.machineId);
}

export function periodBriefMachineDaemonId(machineId: string): string | null {
  const trimmed = machineId.trim();
  const local = /^local:(.+)$/i.exec(trimmed);
  if (local?.[1] && !local[1].toLowerCase().startsWith("runtime:")) {
    return local[1];
  }
  const cloud = /^cloud:(.+)$/i.exec(trimmed);
  if (cloud?.[1] && !cloud[1].toLowerCase().startsWith("runtime:")) {
    return cloud[1];
  }
  return null;
}

/** Label shown in the collector picker (display name already includes 采集 ·). */
export function periodBriefCollectorLabel(
  agent: Pick<Agent, "display_name" | "name">,
): string {
  const display = agent.display_name?.trim();
  if (display) return display;
  return agent.name;
}

/** Canonical handle for the collector that belongs to this Computer seed. */
export function periodBriefCollectorNameForSeed(seed: string): string {
  return PERIOD_BRIEF_COLLECTOR_NAME_PREFIX + periodBriefCollectorDaemonSlug(seed);
}

function periodBriefCollectorDaemonSlug(seed: string): string {
  let cleaned = seed.trim().toLowerCase().replace(PERIOD_BRIEF_COLLECTOR_NON_SLUG, "");
  if (cleaned === "") cleaned = "computer";
  if (cleaned.length > PERIOD_BRIEF_COLLECTOR_DAEMON_SLUG_LEN) {
    cleaned = cleaned.slice(-PERIOD_BRIEF_COLLECTOR_DAEMON_SLUG_LEN);
  }
  while (cleaned.length < PERIOD_BRIEF_COLLECTOR_DAEMON_SLUG_LEN) {
    cleaned += "0";
  }
  return cleaned;
}

function periodBriefComputerIdentity(runtime: PeriodBriefCollectorSlotRuntime): {
  key: string;
  machineId: string;
  seed: string;
  cloud: boolean;
} {
  const mode = (runtime.runtime_mode ?? "local").toLowerCase();
  const daemon = runtime.daemon_id?.trim() ?? "";
  if (mode === "cloud") {
    return {
      key: `cloud:${runtime.id}`,
      machineId: daemon ? `cloud:${daemon}` : `cloud:runtime:${runtime.id}`,
      seed: runtime.id,
      cloud: true,
    };
  }
  if (daemon) {
    return {
      key: `local:${daemon.toLowerCase()}`,
      machineId: `${mode}:${daemon}`,
      seed: daemon,
      cloud: false,
    };
  }
  return {
    key: `local:runtime:${runtime.id}`,
    machineId: `${mode}:runtime:${runtime.id}`,
    seed: runtime.id,
    cloud: false,
  };
}

/**
 * Owned Computers that should each have one Period Work collector.
 * Missing, deleted, or rebound-off-this-computer collectors need setup.
 */
function isOwnedPeriodBriefComputer(
  runtime: PeriodBriefCollectorSlotRuntime,
  userId: string,
  connections: readonly PeriodBriefCollectorComputer[],
): boolean {
  if (runtime.owner_id === userId) return true;
  if (runtime.owner_id && runtime.owner_id !== userId) return false;
  const daemon = runtime.daemon_id?.trim();
  if (!daemon) return false;
  return connections.some(
    (connection) => connection.daemon_id === daemon && connection.owner_id === userId,
  );
}

export function listOwnedPeriodBriefCollectorSlots(
  runtimes: readonly PeriodBriefCollectorSlotRuntime[],
  agents: readonly PeriodBriefCollectorCandidate[],
  userId: string | null | undefined,
  connections: readonly PeriodBriefCollectorComputer[] = [],
): PeriodBriefCollectorSlot[] {
  const uid = userId?.trim();
  if (!uid) return [];

  const groups = new Map<
    string,
    {
      machineId: string;
      seed: string;
      cloud: boolean;
      labelHint?: string;
      runtimes: PeriodBriefCollectorSlotRuntime[];
    }
  >();
  for (const runtime of runtimes) {
    if (!isOwnedPeriodBriefComputer(runtime, uid, connections)) continue;
    const identity = periodBriefComputerIdentity(runtime);
    const group = groups.get(identity.key) ?? {
      machineId: identity.machineId,
      seed: identity.seed,
      cloud: identity.cloud,
      runtimes: [],
    };
    group.runtimes.push(runtime);
    groups.set(identity.key, group);
  }
  for (const connection of connections) {
    if (connection.owner_id !== uid) continue;
    const daemon = connection.daemon_id.trim();
    if (!daemon) continue;
    const key = `local:${daemon.toLowerCase()}`;
    if (groups.has(key)) continue;
    groups.set(key, {
      machineId: `local:${daemon}`,
      seed: daemon,
      cloud: false,
      labelHint: connection.deviceName?.trim() || undefined,
      runtimes: [],
    });
  }

  const collectors = listPeriodBriefCollectorAgents(agents);
  const slots: PeriodBriefCollectorSlot[] = [];
  for (const [key, group] of groups) {
    const expectedName = periodBriefCollectorNameForSeed(group.seed);
    const onSlot =
      collectors.find((agent) => group.runtimes.some((item) => item.id === agent.runtime_id)) ??
      null;
    const named = collectors.find((agent) => agent.name === expectedName) ?? null;
    const namedOnSlot = named
      ? runtimes.find((item) => item.id === named.runtime_id)
      : undefined;
    const strayAgentId =
      named && namedOnSlot && periodBriefComputerIdentity(namedOnSlot).key !== key
        ? named.id
        : named && !namedOnSlot
          ? named.id
          : null;
    const representative =
      group.runtimes.find((item) => item.status === "online") ?? group.runtimes[0];
    const label =
      representative?.display_name?.trim() ||
      representative?.name?.trim() ||
      group.labelHint ||
      (group.cloud ? "Cloud" : "Computer");
    const hasRuntime = group.runtimes.length > 0;
    slots.push({
      key,
      machineId: group.machineId,
      label,
      expectedName,
      runtimeIds: group.runtimes.map((item) => item.id),
      collector: onSlot,
      strayAgentId,
      needsSetup: hasRuntime && !onSlot,
      needsRuntime: !hasRuntime,
    });
  }
  return slots;
}

export function listPeriodBriefCollectorSlotsNeedingSetup(
  runtimes: readonly PeriodBriefCollectorSlotRuntime[],
  agents: readonly PeriodBriefCollectorCandidate[],
  userId: string | null | undefined,
  connections: readonly PeriodBriefCollectorComputer[] = [],
): PeriodBriefCollectorSlot[] {
  return listOwnedPeriodBriefCollectorSlots(runtimes, agents, userId, connections).filter(
    (slot) => slot.needsSetup,
  );
}

export function listPeriodBriefCollectorSlotsNeedingRuntime(
  runtimes: readonly PeriodBriefCollectorSlotRuntime[],
  agents: readonly PeriodBriefCollectorCandidate[],
  userId: string | null | undefined,
  connections: readonly PeriodBriefCollectorComputer[] = [],
): PeriodBriefCollectorSlot[] {
  return listOwnedPeriodBriefCollectorSlots(runtimes, agents, userId, connections).filter(
    (slot) => slot.needsRuntime,
  );
}
