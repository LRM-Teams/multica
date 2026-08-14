// @vitest-environment jsdom

import type { ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiClient, setApiInstance } from "../api";
import { useRunnerActivitySummaries, useRunnerActivitySummary } from "./use-runner-activity-summary";

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("useRunnerActivitySummary", () => {
  it("shares one Workspace request across Agent consumers", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({
        items: [
          { agent_id: "agent-1", summary: { label: "Online", tone: "success", visibility: "visible" } },
          { agent_id: "agent-2", summary: { label: "Thinking...", tone: "info", visibility: "visible" } },
        ],
      }), { status: 200, headers: { "Content-Type": "application/json" } }),
    );
    vi.stubGlobal("fetch", fetchMock);
    setApiInstance(new ApiClient("https://api.example.test"));
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    const wrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    );

    const { result } = renderHook(() => ({
      one: useRunnerActivitySummary("workspace-1", "agent-1"),
      two: useRunnerActivitySummary("workspace-1", "agent-2"),
    }), { wrapper });

    await waitFor(() => expect(result.current.one.data?.label).toBe("Online"));
    expect(result.current.two.data?.label).toBe("Thinking...");
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(fetchMock.mock.calls[0]?.[0]).toBe(
      "https://api.example.test/api/members/agents/runner-activity-summaries",
    );
  });

  it("returns the Workspace list without per-agent select", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({
        items: [
          { agent_id: "agent-1", summary: { label: "Thinking...", tone: "active", visibility: "visible" } },
        ],
      }), { status: 200, headers: { "Content-Type": "application/json" } }),
    );
    vi.stubGlobal("fetch", fetchMock);
    setApiInstance(new ApiClient("https://api.example.test"));
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    const wrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    );

    const { result } = renderHook(
      () => useRunnerActivitySummaries("workspace-1"),
      { wrapper },
    );

    await waitFor(() => expect(result.current.data?.items).toHaveLength(1));
    expect(result.current.data?.items[0]?.agent_id).toBe("agent-1");
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });
});
