/**
 * Pure Members Directory roster projection (ADR 0013).
 * No React — unit-tested grouping / default selection / omission rules.
 */

import type { Agent, AgentRuntime, MemberWithUser } from "@multica/core/types";
import type { MembersSelection } from "@multica/core/paths";
import {
  buildRuntimeMachines,
  type RuntimeMachine,
} from "../runtimes/components/runtime-machines";
import { resolveActorDisplayName } from "@multica/core/identity";

export type ComputerAgentGroup = {
  machineId: string;
  title: string;
  agents: Agent[];
};

export type MembersDirectoryRoster = {
  computerGroups: ComputerAgentGroup[];
  /** Active agents that appear in the rail (have runtime + machine). */
  listedAgents: Agent[];
  humans: MemberWithUser[];
};

export type BuildRosterOptions = {
  now?: number;
  localDaemonId?: string | null;
  localMachineName?: string | null;
  currentUserId?: string | null;
  hasLocalMachine?: boolean;
};

/** Active, non-archived Agents that have a runtime_id. */
export function filterDirectoryAgents(agents: readonly Agent[]): Agent[] {
  return agents.filter((a) => !a.archived_at && !!a.runtime_id);
}

export function buildMembersDirectoryRoster(
  agents: readonly Agent[],
  members: readonly MemberWithUser[],
  runtimes: readonly AgentRuntime[],
  options: BuildRosterOptions = {},
): MembersDirectoryRoster {
  const listedAgents = filterDirectoryAgents(agents);
  const machines = buildRuntimeMachines([...runtimes], {
    now: options.now ?? Date.now(),
    localDaemonId: options.localDaemonId ?? null,
    localMachineName: options.localMachineName ?? null,
    currentUserId: options.currentUserId ?? null,
    ensureLocalMachine: options.hasLocalMachine ?? false,
  });

  const runtimeIdToMachine = new Map<string, RuntimeMachine>();
  for (const machine of machines) {
    for (const r of machine.runtimes) {
      runtimeIdToMachine.set(r.id, machine);
    }
  }

  const byMachine = new Map<string, ComputerAgentGroup>();
  const order: string[] = [];

  for (const agent of listedAgents) {
    const machine = runtimeIdToMachine.get(agent.runtime_id);
    if (!machine) continue; // unbound / unknown runtime — omit (no No-computer group)
    let group = byMachine.get(machine.id);
    if (!group) {
      group = { machineId: machine.id, title: machine.title, agents: [] };
      byMachine.set(machine.id, group);
      order.push(machine.id);
    }
    group.agents.push(agent);
  }

  // Preserve machine order from buildRuntimeMachines
  const machineOrder = machines.map((m) => m.id);
  order.sort(
    (a, b) =>
      (machineOrder.indexOf(a) === -1 ? 999 : machineOrder.indexOf(a)) -
      (machineOrder.indexOf(b) === -1 ? 999 : machineOrder.indexOf(b)),
  );

  for (const g of byMachine.values()) {
    g.agents.sort((a, b) =>
      resolveActorDisplayName(a, a.name).localeCompare(
        resolveActorDisplayName(b, b.name),
      ),
    );
  }

  const computerGroups = order
    .map((id) => byMachine.get(id)!)
    .filter((g) => g.agents.length > 0);

  const humans = [...members].sort((a, b) =>
    resolveActorDisplayName(a, a.user_id).localeCompare(
      resolveActorDisplayName(b, b.user_id),
    ),
  );

  return {
    computerGroups,
    listedAgents: computerGroups.flatMap((g) => g.agents),
    humans,
  };
}

/**
 * Whether agent/member/runtime queries have settled enough to stamp a
 * default URL selection. Must wait for runtimes: with agents ready and
 * runtimes still loading, the roster temporarily looks agent-empty and
 * would incorrectly default to the first human (AC1 race).
 */
export function isMembersDirectoryRosterReady(opts: {
  agentsLoading: boolean;
  membersLoading: boolean;
  runtimesLoading: boolean;
}): boolean {
  return (
    !opts.agentsLoading && !opts.membersLoading && !opts.runtimesLoading
  );
}

/** Default selection: first agent under first computer, else first human. */
export function defaultMembersSelection(
  roster: MembersDirectoryRoster,
): MembersSelection | null {
  const firstAgent = roster.computerGroups[0]?.agents[0];
  if (firstAgent) return { kind: "agent", id: firstAgent.id };
  const firstHuman = roster.humans[0];
  if (firstHuman) return { kind: "user", id: firstHuman.user_id };
  return null;
}

/**
 * Resolve URL selection against the roster. Invalid / missing → default.
 */
export function resolveMembersSelection(
  roster: MembersDirectoryRoster,
  fromUrl: MembersSelection | null,
): MembersSelection | null {
  if (fromUrl?.kind === "agent") {
    if (roster.listedAgents.some((a) => a.id === fromUrl.id)) return fromUrl;
  }
  if (fromUrl?.kind === "user") {
    if (roster.humans.some((h) => h.user_id === fromUrl.id)) return fromUrl;
  }
  return defaultMembersSelection(roster);
}
