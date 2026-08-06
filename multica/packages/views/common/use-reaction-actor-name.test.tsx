"use client";

import type { ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useReactionActorName } from "./use-reaction-actor-name";

const getActorNameMock = vi.fn(
  (type: string, id: string, fallback?: string) => {
    if (type === "agent" && id === "agent-1") return "Research Agent";
    if (type === "member" && id === "user-1") return "Alice Display";
    // Group manager miss — same sentinel production useActorName returns.
    if (type === "agent") return fallback ?? "Unknown Agent";
    return fallback ?? "Unknown";
  },
);

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@multica/core/workspace/hooks", () => ({
  useActorName: () => ({ getActorName: getActorNameMock }),
}));

vi.mock("@multica/core/workspace/queries", () => ({
  memberProfileOptions: (_wsId: string, type: string, id: string) => ({
    queryKey: ["workspaces", "ws-1", "member-profiles", type, id],
    queryFn: async () => {
      if (id === "agent-beckham") {
        return {
          member_type: "agent",
          member_id: "agent-beckham",
          name: "bei-ke-han-mu",
          display_name: "贝克汉姆",
        };
      }
      throw new Error(`profile missing: ${type}/${id}`);
    },
    enabled: !!id,
  }),
}));

function wrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

describe("useReactionActorName (LRM-364)", () => {
  beforeEach(() => {
    getActorNameMock.mockClear();
  });

  it("keeps directory hits and resolves group-manager misses via member-profile", async () => {
    const reactions = [
      { actor_type: "agent", actor_id: "agent-1" },
      { actor_type: "agent", actor_id: "agent-beckham" },
      { actor_type: "member", actor_id: "user-1" },
    ];

    const { result } = renderHook(() => useReactionActorName(reactions), { wrapper });

    expect(result.current("agent", "agent-1")).toBe("Research Agent");
    expect(result.current("member", "user-1")).toBe("Alice Display");
    // Pending profile → honest id placeholder, never Unknown Agent.
    expect(result.current("agent", "agent-beckham")).not.toBe("Unknown Agent");

    await waitFor(() => {
      expect(result.current("agent", "agent-beckham")).toBe("贝克汉姆");
    });
    expect(result.current("agent", "agent-beckham")).not.toContain("Unknown");
  });

  it("never returns Unknown Agent for a hard profile miss — uses id placeholder", async () => {
    const reactions = [{ actor_type: "agent", actor_id: "agent-deleted" }];
    const { result } = renderHook(() => useReactionActorName(reactions), { wrapper });

    await waitFor(() => {
      // Query settles (error); resolver must stay on the id placeholder.
      expect(result.current("agent", "agent-deleted")).toBe("agent-deleted");
    });
    expect(result.current("agent", "agent-deleted")).not.toBe("Unknown Agent");
  });
});
