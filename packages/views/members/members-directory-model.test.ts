import { describe, it, expect } from "vitest";
import type { Agent, AgentRuntime, MemberWithUser } from "@multica/core/types";
import {
  buildMembersDirectoryRoster,
  defaultMembersSelection,
  filterDirectoryAgents,
  filterMembersDirectoryRoster,
  isMembersDirectoryRosterReady,
  resolveMembersSelection,
} from "./members-directory-model";

function agent(partial: Partial<Agent> & Pick<Agent, "id" | "name">): Agent {
  return {
    workspace_id: "ws",
    runtime_id: partial.runtime_id ?? "rt-1",
    owner_id: "u1",
    status: "idle",
    description: null,
    instructions: "",
    avatar_url: null,
    display_name: null,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    archived_at: null,
    ...partial,
  } as Agent;
}

function runtime(
  partial: Partial<AgentRuntime> & Pick<AgentRuntime, "id" | "name">,
): AgentRuntime {
  return {
    workspace_id: "ws",
    owner_id: "u1",
    runtime_mode: "local",
    status: "online",
    daemon_id: "d1",
    device_name: partial.device_name ?? "Mac",
    last_seen_at: new Date().toISOString(),
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    ...partial,
  } as AgentRuntime;
}

function member(
  partial: Partial<MemberWithUser> & Pick<MemberWithUser, "user_id" | "name">,
): MemberWithUser {
  return {
    id: partial.id ?? partial.user_id,
    workspace_id: "ws",
    role: "member",
    email: `${partial.user_id}@ex.com`,
    avatar_url: null,
    created_at: "2026-01-01T00:00:00Z",
    ...partial,
  } as MemberWithUser;
}

describe("filterDirectoryAgents", () => {
  it("drops archived and unbound agents", () => {
    const list = filterDirectoryAgents([
      agent({ id: "a1", name: "A", runtime_id: "rt-1" }),
      agent({
        id: "a2",
        name: "B",
        runtime_id: "rt-1",
        archived_at: "2026-02-01T00:00:00Z",
      }),
      agent({ id: "a3", name: "C", runtime_id: "" }),
    ]);
    expect(list.map((a) => a.id)).toEqual(["a1"]);
  });
});

describe("buildMembersDirectoryRoster", () => {
  it("groups agents by computer and omits unknown runtimes (no No-computer group)", () => {
    const runtimes = [
      runtime({ id: "rt-1", name: "Pi", daemon_id: "d1", device_name: "s144" }),
      runtime({ id: "rt-2", name: "Pi", daemon_id: "d2", device_name: "macbook" }),
    ];
    const agents = [
      agent({ id: "a2", name: "Zed", runtime_id: "rt-1" }),
      agent({ id: "a1", name: "Alice", runtime_id: "rt-1" }),
      agent({ id: "a3", name: "Orphan", runtime_id: "missing" }),
    ];
    const roster = buildMembersDirectoryRoster(
      agents,
      [member({ user_id: "u1", name: "Frank" })],
      runtimes,
      { now: Date.now() },
    );
    expect(roster.listedAgents.map((a) => a.id).sort()).toEqual(["a1", "a2"]);
    expect(roster.computerGroups.length).toBeGreaterThanOrEqual(1);
    const allIds = roster.computerGroups.flatMap((g) => g.agents.map((a) => a.id));
    expect(allIds).not.toContain("a3");
    // sorted by display name within group (no currentUser → alpha)
    const g0 = roster.computerGroups.find((g) =>
      g.agents.some((a) => a.id === "a1"),
    )!;
    expect(g0.agents.map((a) => a.name)).toEqual(["Alice", "Zed"]);
  });

  it("puts current user first among humans and mine agents first", () => {
    const runtimes = [
      runtime({ id: "rt-1", name: "Pi", daemon_id: "d1", device_name: "s144" }),
    ];
    const roster = buildMembersDirectoryRoster(
      [
        agent({ id: "a1", name: "Zebra", runtime_id: "rt-1", owner_id: "other" }),
        agent({ id: "a2", name: "MineBot", runtime_id: "rt-1", owner_id: "me" }),
      ],
      [
        member({ user_id: "z", name: "Zoe" }),
        member({ user_id: "me", name: "Me" }),
        member({ user_id: "a", name: "Ada" }),
      ],
      runtimes,
      { currentUserId: "me" },
    );
    expect(roster.humans.map((h) => h.user_id)).toEqual(["me", "a", "z"]);
    const agents = roster.computerGroups[0]!.agents;
    expect(agents.map((a) => a.id)).toEqual(["a2", "a1"]);
  });
});

describe("filterMembersDirectoryRoster", () => {
  it("filters agents and humans by query", () => {
    const runtimes = [
      runtime({ id: "rt-1", name: "Pi", daemon_id: "d1", device_name: "s144" }),
    ];
    const roster = buildMembersDirectoryRoster(
      [
        agent({
          id: "a1",
          name: "AliceBot",
          runtime_id: "rt-1",
          description: "frontend helper",
        }),
        agent({ id: "a2", name: "Zed", runtime_id: "rt-1", description: "ops" }),
      ],
      [
        member({ user_id: "u1", name: "Frank", email: "frank@ex.com" }),
        member({ user_id: "u2", name: "Joyce", email: "joyce@ex.com" }),
      ],
      runtimes,
    );
    const byName = filterMembersDirectoryRoster(roster, "alice");
    expect(byName.listedAgents.map((a) => a.id)).toEqual(["a1"]);
    expect(byName.humans).toEqual([]);

    const byEmail = filterMembersDirectoryRoster(roster, "joyce@");
    expect(byEmail.humans.map((h) => h.user_id)).toEqual(["u2"]);
    expect(byEmail.listedAgents).toEqual([]);
  });
});

describe("isMembersDirectoryRosterReady", () => {
  it("is false while any of agents/members/runtimes is still loading", () => {
    expect(
      isMembersDirectoryRosterReady({
        agentsLoading: false,
        membersLoading: false,
        runtimesLoading: true,
      }),
    ).toBe(false);
    expect(
      isMembersDirectoryRosterReady({
        agentsLoading: true,
        membersLoading: false,
        runtimesLoading: false,
      }),
    ).toBe(false);
    expect(
      isMembersDirectoryRosterReady({
        agentsLoading: false,
        membersLoading: true,
        runtimesLoading: false,
      }),
    ).toBe(false);
  });

  it("is true only when agents, members, and runtimes have settled", () => {
    expect(
      isMembersDirectoryRosterReady({
        agentsLoading: false,
        membersLoading: false,
        runtimesLoading: false,
      }),
    ).toBe(true);
  });

  it("documents the AC1 race: agents ready + runtimes empty looks human-only until runtimes load", () => {
    // Intermediate state while runtimesLoading=true: page must NOT stamp default.
    expect(
      isMembersDirectoryRosterReady({
        agentsLoading: false,
        membersLoading: false,
        runtimesLoading: true,
      }),
    ).toBe(false);

    const agents = [
      agent({ id: "a1", name: "Alice", runtime_id: "rt-1" }),
    ];
    const humans = [member({ user_id: "u1", name: "Frank" })];

    // Partial roster (runtimes still []): agents cannot group → would default to human.
    const partial = buildMembersDirectoryRoster(agents, humans, []);
    expect(partial.listedAgents).toEqual([]);
    expect(defaultMembersSelection(partial)).toEqual({
      kind: "user",
      id: "u1",
    });

    // After runtimes resolve, default is the agent (what AC1 requires).
    const ready = buildMembersDirectoryRoster(agents, humans, [
      runtime({ id: "rt-1", name: "Pi", daemon_id: "d1", device_name: "s144" }),
    ]);
    expect(defaultMembersSelection(ready)).toEqual({
      kind: "agent",
      id: "a1",
    });
  });
});

describe("default and resolve selection", () => {
  it("defaults to first agent then first human", () => {
    const runtimes = [
      runtime({ id: "rt-1", name: "Pi", daemon_id: "d1", device_name: "s144" }),
    ];
    const withAgent = buildMembersDirectoryRoster(
      [agent({ id: "a1", name: "Alice", runtime_id: "rt-1" })],
      [member({ user_id: "u1", name: "Frank" })],
      runtimes,
    );
    expect(defaultMembersSelection(withAgent)).toEqual({
      kind: "agent",
      id: "a1",
    });

    const humansOnly = buildMembersDirectoryRoster(
      [],
      [member({ user_id: "u2", name: "Joyce" })],
      [],
    );
    expect(defaultMembersSelection(humansOnly)).toEqual({
      kind: "user",
      id: "u2",
    });
  });

  it("falls back when URL selection is invalid", () => {
    const runtimes = [
      runtime({ id: "rt-1", name: "Pi", daemon_id: "d1", device_name: "s144" }),
    ];
    const roster = buildMembersDirectoryRoster(
      [agent({ id: "a1", name: "Alice", runtime_id: "rt-1" })],
      [member({ user_id: "u1", name: "Frank" })],
      runtimes,
    );
    expect(
      resolveMembersSelection(roster, { kind: "agent", id: "gone" }),
    ).toEqual({ kind: "agent", id: "a1" });
    expect(
      resolveMembersSelection(roster, { kind: "agent", id: "a1" }),
    ).toEqual({ kind: "agent", id: "a1" });
  });
});
