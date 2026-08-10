import { QueryClient } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import {
  agentPresenceOptions,
  agentPresenceKeys,
  buildAgentPresenceMap,
} from "./agent-presence";
import { applyAgentPresenceRealtime } from "./agent-presence-updaters";

describe("Agent Presence workspace cache", () => {
  it("builds the complete binary roster under one Workspace key", () => {
    expect(agentPresenceKeys.workspace("workspace-1")).toEqual([
      "workspaces",
      "workspace-1",
      "agent-presence",
    ]);
    expect(
      buildAgentPresenceMap([
        { agent_id: "agent-1", presence: "online" },
        { agent_id: "agent-2", presence: "offline" },
      ]),
    ).toEqual(
      new Map([
        ["agent-1", "online"],
        ["agent-2", "offline"],
      ]),
    );
  });

  it("deduplicates list and selector reads through one Workspace query", async () => {
    const queryFn = vi.fn(async () => new Map([["agent-1", "online" as const]]));
    const queryClient = new QueryClient();
    const options = { ...agentPresenceOptions("workspace-1"), queryFn };

    const [listRead, selectorRead] = await Promise.all([
      queryClient.fetchQuery(options),
      queryClient.fetchQuery(options),
    ]);

    expect(queryFn).toHaveBeenCalledTimes(1);
    expect(listRead.get("agent-1")).toBe("online");
    expect(selectorRead).toBe(listRead);
  });

  it("patches only the current Workspace without invalidating REST", () => {
    const queryClient = new QueryClient();
    const workspaceA = agentPresenceKeys.workspace("workspace-a");
    const workspaceB = agentPresenceKeys.workspace("workspace-b");
    queryClient.setQueryData(workspaceA, new Map([["agent-1", "offline"]]));
    queryClient.setQueryData(workspaceB, new Map([["agent-1", "offline"]]));

    applyAgentPresenceRealtime(queryClient, "workspace-a", {
      agent_id: "agent-1",
      presence: "online",
    });

    expect(queryClient.getQueryData(workspaceA)).toEqual(
      new Map([["agent-1", "online"]]),
    );
    expect(queryClient.getQueryData(workspaceB)).toEqual(
      new Map([["agent-1", "offline"]]),
    );
    expect(queryClient.getQueryState(workspaceA)?.isInvalidated).toBe(false);
  });

  it("is reference-idempotent and rejects malformed events", () => {
    const queryClient = new QueryClient();
    const key = agentPresenceKeys.workspace("workspace-1");
    const initial = new Map([["agent-1", "online" as const]]);
    queryClient.setQueryData(key, initial);

    applyAgentPresenceRealtime(queryClient, "workspace-1", {
      agent_id: "agent-1",
      presence: "online",
    });
    expect(queryClient.getQueryData(key)).toBe(initial);

    applyAgentPresenceRealtime(queryClient, "workspace-1", {
      agent_id: "agent-1",
      presence: "busy",
    });
    expect(queryClient.getQueryData(key)).toBe(initial);

    applyAgentPresenceRealtime(queryClient, "workspace-1", {
      agent_id: "agent-from-another-workspace",
      presence: "online",
    });
    expect(queryClient.getQueryData(key)).toBe(initial);
  });
});
