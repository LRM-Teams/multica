// @vitest-environment jsdom

import { act, renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const wsHandlers = vi.hoisted(() => new Map<string, (payload: unknown) => void>());
const reconnect = vi.hoisted(() => ({ callback: null as null | (() => void) }));
const invalidateQueries = vi.hoisted(() => vi.fn());

vi.mock("@multica/core/agents", () => ({
  agentRemindersKeys: { all: (agentId: string) => ["agent-reminders", agentId] },
}));
vi.mock("@tanstack/react-query", async () => {
  const actual = await vi.importActual<typeof import("@tanstack/react-query")>("@tanstack/react-query");
  return { ...actual, useQueryClient: () => ({ invalidateQueries }) };
});
vi.mock("@multica/core/realtime", () => ({
  useWSEvent: (event: string, handler: (payload: unknown) => void) => wsHandlers.set(event, handler),
  useWSReconnect: (callback: () => void) => {
    reconnect.callback = callback;
  },
}));

import { useAgentRemindersRealtime } from "./use-agent-reminders-realtime";

describe("useAgentRemindersRealtime", () => {
  beforeEach(() => {
    wsHandlers.clear();
    reconnect.callback = null;
    invalidateQueries.mockClear();
  });

  it("invalidates upcoming reminders for matching events and reconnects", () => {
    renderHook(() => useAgentRemindersRealtime("agent-1"));

    act(() => wsHandlers.get("agent_reminder:changed")?.({ agentId: "agent-1" }));
    act(() => reconnect.callback?.());

    expect(invalidateQueries).toHaveBeenCalledTimes(2);
    expect(invalidateQueries).toHaveBeenCalledWith({
      queryKey: ["agent-reminders", "agent-1"],
    });
  });
});
