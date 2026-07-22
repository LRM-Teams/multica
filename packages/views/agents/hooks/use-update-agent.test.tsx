// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach } from "vitest";
import type { ReactNode } from "react";
import { renderHook } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { Agent } from "@multica/core/types";
import { agentDetailKeys } from "@multica/core/agents";
import { workspaceKeys } from "@multica/core/workspace/queries";
import { toast } from "sonner";

// `api.updateAgent` is the single network call; the test controls whether it
// resolves (with the server-persisted Agent) or rejects (e.g. a 409 conflict).
const mockUpdateAgent = vi.hoisted(() => vi.fn());
vi.mock("@multica/core/api", () => ({
  api: { updateAgent: (...args: unknown[]) => mockUpdateAgent(...args) },
}));

vi.mock("sonner", () => ({
  toast: { success: vi.fn(), error: vi.fn() },
}));

vi.mock("../../i18n", () => ({
  useT: () => ({
    t: (
      select: (dict: {
        detail: { agent_updated_toast: string; update_failed_toast: string };
      }) => ReactNode,
    ) =>
      select({
        detail: {
          agent_updated_toast: "Agent updated",
          update_failed_toast: "Update failed",
        },
      }),
  }),
}));

import { useUpdateAgent } from "./use-update-agent";

const WS = "ws-1";
const AGENT_ID = "agent-1";

function makeAgent(overrides: Partial<Agent> = {}): Agent {
  return {
    id: AGENT_ID,
    name: "old-handle",
    display_name: "Old Handle",
    model: "claude-sonnet-4-6",
    runtime_id: "rt-1",
    ...overrides,
  } as unknown as Agent;
}

// A real QueryClient so assertions read the ACTUAL cache the UI renders,
// rather than a stubbed setQueryData spy. Retries off so a rejected mutation
// surfaces immediately.
function setup(seed: Agent[], seedDetail = true) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  qc.setQueryData(workspaceKeys.agents(WS), seed);
  if (seedDetail) {
    const detailAgent = seed.find((a) => a.id === AGENT_ID);
    if (detailAgent) {
      // Profile panel / card body (LRM-292) — independent of the list cache.
      qc.setQueryData(agentDetailKeys.detail(WS, AGENT_ID), { ...detailAgent });
    }
  }
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  );
  const { result } = renderHook(() => useUpdateAgent(WS), { wrapper });
  return { qc, result };
}

function cachedListAgent(qc: QueryClient, id: string): Agent | undefined {
  return qc
    .getQueryData<Agent[]>(workspaceKeys.agents(WS))
    ?.find((a) => a.id === id);
}

function cachedDetailAgent(qc: QueryClient, id: string): Agent | undefined {
  return qc.getQueryData<Agent>(agentDetailKeys.detail(WS, id));
}

beforeEach(() => {
  vi.clearAllMocks();
});

describe("useUpdateAgent — username → name mapping", () => {
  it("optimistically writes the new @handle to agent.name (no stale flash, no stray username field)", async () => {
    const { qc, result } = setup([makeAgent()]);
    // Server echoes the persisted handle back under `name`.
    mockUpdateAgent.mockResolvedValue(makeAgent({ name: "new-handle" }));

    // Do NOT await yet: the optimistic cache write is synchronous, so the new
    // handle must already be visible before the network round-trip resolves.
    const pending = result.current(AGENT_ID, { username: "new-handle" });

    const optimistic = cachedListAgent(qc, AGENT_ID)!;
    expect(optimistic.name).toBe("new-handle");
    // The bug wrote a stray `agent.username` that nothing reads; there must be
    // none, and the payload key must map onto `name`.
    expect((optimistic as unknown as Record<string, unknown>).username).toBeUndefined();

    await pending;

    // The request carries the API field name, unchanged.
    expect(mockUpdateAgent).toHaveBeenCalledWith(AGENT_ID, {
      username: "new-handle",
    });
    // After success the server's canonical handle is written to `name`.
    expect(cachedListAgent(qc, AGENT_ID)!.name).toBe("new-handle");
    expect(
      (cachedListAgent(qc, AGENT_ID) as unknown as Record<string, unknown>).username,
    ).toBeUndefined();
  });

  it("writes the server's canonical handle, not the raw request value", async () => {
    const { qc, result } = setup([makeAgent()]);
    // Server normalizes the handle (e.g. lowercases) — the cache must reflect
    // what the server stored, not what was typed.
    mockUpdateAgent.mockResolvedValue(makeAgent({ name: "new-handle" }));

    await result.current(AGENT_ID, { username: "New-Handle" });

    expect(cachedListAgent(qc, AGENT_ID)!.name).toBe("new-handle");
  });

  it("rolls back agent.name to the previous handle when the update fails", async () => {
    const { qc, result } = setup([makeAgent()]);
    mockUpdateAgent.mockRejectedValue(new Error("409 handle taken"));

    await expect(
      result.current(AGENT_ID, { username: "taken-handle" }),
    ).rejects.toThrow();

    // Rollback restores the PREVIOUS handle on `name` (the field the UI reads),
    // and never leaves a stray `username` behind.
    const rolledBack = cachedListAgent(qc, AGENT_ID)!;
    expect(rolledBack.name).toBe("old-handle");
    expect((rolledBack as unknown as Record<string, unknown>).username).toBeUndefined();
  });

  it("still maps non-username fields 1:1 (e.g. model) and rolls them back on failure", async () => {
    const { qc, result } = setup([makeAgent()]);

    mockUpdateAgent.mockResolvedValueOnce(
      makeAgent({ model: "claude-opus-4-8" }),
    );
    await result.current(AGENT_ID, { model: "claude-opus-4-8" });
    expect(cachedListAgent(qc, AGENT_ID)!.model).toBe("claude-opus-4-8");

    mockUpdateAgent.mockRejectedValueOnce(new Error("boom"));
    await expect(
      result.current(AGENT_ID, { model: "claude-haiku" }),
    ).rejects.toThrow();
    expect(cachedListAgent(qc, AGENT_ID)!.model).toBe("claude-opus-4-8");
  });
});

describe("useUpdateAgent — agent detail cache (LRM-296)", () => {
  it("optimistically patches runtime_id on the GetAgent detail cache (profile panel body)", async () => {
    const { qc, result } = setup([makeAgent()]);
    mockUpdateAgent.mockResolvedValue(makeAgent({ runtime_id: "rt-2" }));

    const pending = result.current(AGENT_ID, { runtime_id: "rt-2" });

    // Profile panel reads agentDetailKeys, not the list — must flip immediately.
    expect(cachedDetailAgent(qc, AGENT_ID)!.runtime_id).toBe("rt-2");
    expect(cachedListAgent(qc, AGENT_ID)!.runtime_id).toBe("rt-2");

    await pending;

    expect(cachedDetailAgent(qc, AGENT_ID)!.runtime_id).toBe("rt-2");
    expect(toast.success).toHaveBeenCalledWith("Agent updated");
  });

  it("writes the server's canonical runtime fields into the detail cache on success", async () => {
    const { qc, result } = setup([makeAgent()]);
    mockUpdateAgent.mockResolvedValue(
      makeAgent({ runtime_id: "rt-server", model: "server-model" }),
    );

    await result.current(AGENT_ID, {
      runtime_id: "rt-client",
      model: "client-model",
    });

    const detail = cachedDetailAgent(qc, AGENT_ID)!;
    expect(detail.runtime_id).toBe("rt-server");
    expect(detail.model).toBe("server-model");
  });

  it("rolls back the detail cache and surfaces an explicit error toast on failure (LRM-238)", async () => {
    const { qc, result } = setup([makeAgent()]);
    mockUpdateAgent.mockRejectedValue(new Error("runtime offline"));

    await expect(
      result.current(AGENT_ID, { runtime_id: "rt-2" }),
    ).rejects.toThrow("runtime offline");

    expect(cachedDetailAgent(qc, AGENT_ID)!.runtime_id).toBe("rt-1");
    expect(cachedListAgent(qc, AGENT_ID)!.runtime_id).toBe("rt-1");
    expect(toast.error).toHaveBeenCalledWith("runtime offline");
    expect(toast.success).not.toHaveBeenCalled();
  });

  it("still refreshes the profile panel when the agent is absent from ListAgents", async () => {
    // Group managers / channel-only agents: list may omit them (LRM-288), but
    // the panel body is always GetAgent — detail cache must still update.
    const detailOnly = makeAgent({ runtime_id: "rt-1" });
    const qc = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    qc.setQueryData(workspaceKeys.agents(WS), [] as Agent[]);
    qc.setQueryData(agentDetailKeys.detail(WS, AGENT_ID), detailOnly);
    const wrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={qc}>{children}</QueryClientProvider>
    );
    const { result } = renderHook(() => useUpdateAgent(WS), { wrapper });
    mockUpdateAgent.mockResolvedValue(makeAgent({ runtime_id: "rt-2" }));

    const pending = result.current(AGENT_ID, { runtime_id: "rt-2" });
    expect(cachedDetailAgent(qc, AGENT_ID)!.runtime_id).toBe("rt-2");
    await pending;
    expect(cachedDetailAgent(qc, AGENT_ID)!.runtime_id).toBe("rt-2");
  });
});
