// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach } from "vitest";
import { renderHook, act } from "@testing-library/react";

const wsHandlers = vi.hoisted(() => new Map<string, (payload: unknown) => void>());
const reconnect = vi.hoisted(() => ({ cb: null as null | (() => void) }));
const clientHandles = vi.hoisted(() => ({ invalidateQueries: vi.fn() }));

vi.mock("@multica/core/agents", () => ({
  agentRemindersKeys: {
    all: (agentId: string) => ["agent-reminders", agentId],
  },
}));

vi.mock("@tanstack/react-query", async () => {
  const actual = await vi.importActual<typeof import("@tanstack/react-query")>("@tanstack/react-query");
  return { ...actual, useQueryClient: () => ({ invalidateQueries: clientHandles.invalidateQueries }) };
});

vi.mock("@multica/core/realtime", () => ({
  useWSEvent: (event: string, handler: (payload: unknown) => void) => {
    wsHandlers.set(event, handler);
  },
  useWSReconnect: (cb: () => void) => {
    reconnect.cb = cb;
  },
}));

import { useAgentRemindersRealtime } from "./use-agent-reminders-realtime";

function push(payload: { agent_id: string }) {
  act(() => {
    wsHandlers.get("agent_reminder:changed")!(payload);
  });
}

describe("useAgentRemindersRealtime", () => {
  beforeEach(() => {
    wsHandlers.clear();
    reconnect.cb = null;
    clientHandles.invalidateQueries.mockClear();
  });

  it("invalidates the matching agent's reminders query on agent_reminder:changed", () => {
    renderHook(() => useAgentRemindersRealtime("agent-1"));

    push({ agent_id: "agent-1" });

    expect(clientHandles.invalidateQueries).toHaveBeenCalledWith({
      queryKey: ["agent-reminders", "agent-1"],
    });
  });

  it("ignores an event scoped to a different agent", () => {
    renderHook(() => useAgentRemindersRealtime("agent-1"));

    push({ agent_id: "agent-2" });

    expect(clientHandles.invalidateQueries).not.toHaveBeenCalled();
  });

  it("invalidates on WS reconnect, independent of the specific event", () => {
    renderHook(() => useAgentRemindersRealtime("agent-1"));

    act(() => reconnect.cb!());

    expect(clientHandles.invalidateQueries).toHaveBeenCalledWith({
      queryKey: ["agent-reminders", "agent-1"],
    });
  });
});
