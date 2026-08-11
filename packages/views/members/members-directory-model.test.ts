import { describe, it, expect } from "vitest";
import type { Agent, AgentRuntime, MemberWithUser } from "@multica/core/types";
import {
  buildMembersDirectoryRoster,
  defaultMembersSelection,
  filterDirectoryAgents,
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
    // sorted by display name within group
    const g0 = roster.computerGroups.find((g) =>
      g.agents.some((a) => a.id === "a1"),
    )!;
    expect(g0.agents.map((a) => a.name)).toEqual(["Alice", "Zed"]);
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
