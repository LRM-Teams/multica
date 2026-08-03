// @vitest-environment jsdom

import type { ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { agentFleetRankingsOptions } from "../agents/fleet-queries";
import { useActorName } from "./hooks";
import { workspaceKeys } from "./queries";

vi.mock("../hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

function createWrapper(queryClient: QueryClient) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    );
  };
}

describe("useActorName", () => {
  it("resolves the permanent honor level from the existing agent directory", () => {
    const queryClient = new QueryClient({
      defaultOptions: { queries: { staleTime: Infinity } },
    });
    queryClient.setQueryData(workspaceKeys.members("ws-1"), []);
    queryClient.setQueryData(workspaceKeys.agents("ws-1"), [
      { id: "agent-1", name: "research-agent", honor_level: 8 },
      { id: "agent-old", name: "legacy-agent" },
    ]);
    queryClient.setQueryData(agentFleetRankingsOptions("ws-1").queryKey, []);

    const { result } = renderHook(() => useActorName(), {
      wrapper: createWrapper(queryClient),
    });

    expect(result.current.getAgentHonorLevel("agent-1")).toBe(8);
    expect(result.current.getAgentHonorLevel("agent-old")).toBeUndefined();
  });
});
