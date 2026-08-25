// @vitest-environment jsdom

import type { ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiClient, setApiInstance } from "../api";
import { useRunnerActivitySummaries, useRunnerActivitySummary } from "./use-runner-activity-summary";

afterEach(() => vi.unstubAllGlobals());

describe("runner activity summary hooks", () => {
  it("shares one Workspace request across Agent consumers", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ items: [
      { agent_id: "agent-1", summary: { label: "Online", activityKind: "online", detailKind: "idle" } },
      { agent_id: "agent-2", summary: { label: "Thinking...", activityKind: "thinking", detailKind: "thinking_started" } },
    ] }), { status: 200, headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);
    setApiInstance(new ApiClient("https://api.example.test"));
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
    const { result } = renderHook(() => ({
      one: useRunnerActivitySummary("workspace-1", "agent-1"),
      two: useRunnerActivitySummary("workspace-1", "agent-2"),
      all: useRunnerActivitySummaries("workspace-1"),
    }), { wrapper });

    await waitFor(() => expect(result.current.one.data?.activityKind).toBe("online"));
    expect(result.current.two.data).toMatchObject({ label: "Thinking...", detailKind: "thinking_started" });
    expect(result.current.all.data?.items).toHaveLength(2);
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });
});
