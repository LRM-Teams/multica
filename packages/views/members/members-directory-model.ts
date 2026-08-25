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

  const currentUserId = options.currentUserId ?? null;
  for (const g of byMachine.values()) {
    // Mine first, then display name within each computer group.
    g.agents.sort((a, b) => {
      const mineA = currentUserId && a.owner_id === currentUserId ? 0 : 1;
      const mineB = currentUserId && b.owner_id === currentUserId ? 0 : 1;
      if (mineA !== mineB) return mineA - mineB;
      return resolveActorDisplayName(a, a.name).localeCompare(
        resolveActorDisplayName(b, b.name),
      );
    });
  }

  const computerGroups = order
    .map((id) => byMachine.get(id)!)
    .filter((g) => g.agents.length > 0);

  // Current user first, then display name.
  const humans = [...members].sort((a, b) => {
    const selfA = currentUserId && a.user_id === currentUserId ? 0 : 1;
    const selfB = currentUserId && b.user_id === currentUserId ? 0 : 1;
    if (selfA !== selfB) return selfA - selfB;
    return resolveActorDisplayName(a, a.user_id).localeCompare(
      resolveActorDisplayName(b, b.user_id),
    );
  });

  return {
    computerGroups,
    listedAgents: computerGroups.flatMap((g) => g.agents),
    humans,
  };
}

/**
 * Client-side search over the directory roster.
 * Matches agent name/description/machine title and human name/email.
 * Empty / whitespace query returns the input roster unchanged.
 */
export function filterMembersDirectoryRoster(
  roster: MembersDirectoryRoster,
  query: string,
): MembersDirectoryRoster {
  const q = query.trim().toLowerCase();
  if (!q) return roster;

  const computerGroups = roster.computerGroups
    .map((g) => {
      const titleHit = g.title.toLowerCase().includes(q);
      const agents = titleHit
        ? g.agents
        : g.agents.filter((a) => {
            const name = resolveActorDisplayName(a, a.name).toLowerCase();
            const desc = (a.description ?? "").toLowerCase();
            return name.includes(q) || desc.includes(q);
          });
      return { ...g, agents };
    })
    .filter((g) => g.agents.length > 0);

  const humans = roster.humans.filter((h) => {
    const name = resolveActorDisplayName(h, h.user_id).toLowerCase();
    const email = (h.email ?? "").toLowerCase();
    return name.includes(q) || email.includes(q);
  });

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

/** A rail row the user can land on with the keyboard. */
export type DirectoryRowRef = { kind: "agent" | "user"; id: string };

/**
 * Visible rail rows, top to bottom, honouring section collapse. Pure so the
 * ↑/↓ traversal is unit-testable without a DOM.
 */
export function listVisibleDirectoryRows(
  roster: MembersDirectoryRoster,
  opts: { agentsOpen: boolean; humansOpen: boolean },
): DirectoryRowRef[] {
  const rows: DirectoryRowRef[] = [];
  if (opts.agentsOpen) {
    for (const group of roster.computerGroups) {
      for (const agent of group.agents) {
        rows.push({ kind: "agent", id: agent.id });
      }
    }
  }
  if (opts.humansOpen) {
    for (const human of roster.humans) {
      rows.push({ kind: "user", id: human.user_id });
    }
  }
  return rows;
}

/**
 * Move the selection by `delta` within the visible rows. A selection that is
 * filtered out (or absent) enters at the first/last row so ↓ always lands
 * somewhere. Stops at the ends rather than wrapping.
 */
export function stepDirectorySelection(
  rows: readonly DirectoryRowRef[],
  current: DirectoryRowRef | null,
  delta: 1 | -1,
): DirectoryRowRef | null {
  if (rows.length === 0) return null;
  const index = current
    ? rows.findIndex((r) => r.kind === current.kind && r.id === current.id)
    : -1;
  if (index === -1) return delta > 0 ? rows[0]! : rows[rows.length - 1]!;
  const next = index + delta;
  if (next < 0 || next >= rows.length) return null;
  return rows[next]!;
}
