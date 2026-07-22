// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach } from "vitest";
import type { ReactNode } from "react";
import { renderHook } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { Agent } from "@multica/core/types";
import { agentDetailKeys } from "@multica/core/agents";
import { workspaceKeys } from "@multica/core/workspace/queries";

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

function makeAgent(overrides: Partial<Agent> = {}): Agent {
  return {
    id: "agent-1",
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
function setup(seed: Agent[], options?: { seedDetail?: boolean }) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  qc.setQueryData(workspaceKeys.agents(WS), seed);
  if (options?.seedDetail !== false) {
    for (const agent of seed) {
      qc.setQueryData(agentDetailKeys.detail(WS, agent.id), agent);
    }
  }
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  );
  const { result } = renderHook(() => useUpdateAgent(WS), { wrapper });
  return { qc, result };
}

function cachedAgent(qc: QueryClient, id: string): Agent | undefined {
  return qc
    .getQueryData<Agent[]>(workspaceKeys.agents(WS))
    ?.find((a) => a.id === id);
}

function cachedDetail(qc: QueryClient, id: string): Agent | undefined {
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
    const pending = result.current("agent-1", { username: "new-handle" });

    const optimistic = cachedAgent(qc, "agent-1")!;
    expect(optimistic.name).toBe("new-handle");
    // The bug wrote a stray `agent.username` that nothing reads; there must be
    // none, and the payload key must map onto `name`.
    expect((optimistic as unknown as Record<string, unknown>).username).toBeUndefined();

    await pending;

    // The request carries the API field name, unchanged.
    expect(mockUpdateAgent).toHaveBeenCalledWith("agent-1", {
      username: "new-handle",
    });
    // After success the server's canonical handle is written to `name`.
    expect(cachedAgent(qc, "agent-1")!.name).toBe("new-handle");
    expect(
      (cachedAgent(qc, "agent-1") as unknown as Record<string, unknown>).username,
    ).toBeUndefined();
  });

  it("writes the server's canonical handle, not the raw request value", async () => {
    const { qc, result } = setup([makeAgent()]);
    // Server normalizes the handle (e.g. lowercases) — the cache must reflect
    // what the server stored, not what was typed.
    mockUpdateAgent.mockResolvedValue(makeAgent({ name: "new-handle" }));

    await result.current("agent-1", { username: "New-Handle" });

    expect(cachedAgent(qc, "agent-1")!.name).toBe("new-handle");
  });

  it("rolls back agent.name to the previous handle when the update fails", async () => {
    const { qc, result } = setup([makeAgent()]);
    mockUpdateAgent.mockRejectedValue(new Error("409 handle taken"));

    await expect(
      result.current("agent-1", { username: "taken-handle" }),
    ).rejects.toThrow();

    // Rollback restores the PREVIOUS handle on `name` (the field the UI reads),
    // and never leaves a stray `username` behind.
    const rolledBack = cachedAgent(qc, "agent-1")!;
    expect(rolledBack.name).toBe("old-handle");
    expect((rolledBack as unknown as Record<string, unknown>).username).toBeUndefined();
  });

  it("still maps non-username fields 1:1 (e.g. model) and rolls them back on failure", async () => {
    const { qc, result } = setup([makeAgent()]);

    mockUpdateAgent.mockResolvedValueOnce(
      makeAgent({ model: "claude-opus-4-8" }),
    );
    await result.current("agent-1", { model: "claude-opus-4-8" });
    expect(cachedAgent(qc, "agent-1")!.model).toBe("claude-opus-4-8");

    mockUpdateAgent.mockRejectedValueOnce(new Error("boom"));
    await expect(
      result.current("agent-1", { model: "claude-haiku" }),
    ).rejects.toThrow();
    expect(cachedAgent(qc, "agent-1")!.model).toBe("claude-opus-4-8");
  });
});

describe("useUpdateAgent — agent detail cache (LRM-292 profile / panel)", () => {
  it("optimistically patches GET /agents/:id cache so profile runtime refreshes without reload", async () => {
    const { qc, result } = setup([makeAgent()]);
    mockUpdateAgent.mockResolvedValue(makeAgent({ runtime_id: "rt-2" }));

    const pending = result.current("agent-1", { runtime_id: "rt-2" });

    expect(cachedDetail(qc, "agent-1")!.runtime_id).toBe("rt-2");
    expect(cachedAgent(qc, "agent-1")!.runtime_id).toBe("rt-2");

    await pending;

    expect(cachedDetail(qc, "agent-1")!.runtime_id).toBe("rt-2");
  });

  it("rolls back the detail cache when the update fails", async () => {
    const { qc, result } = setup([makeAgent()]);
    mockUpdateAgent.mockRejectedValue(new Error("boom"));

    await expect(
      result.current("agent-1", { runtime_id: "rt-2" }),
    ).rejects.toThrow();

    expect(cachedDetail(qc, "agent-1")!.runtime_id).toBe("rt-1");
    expect(cachedAgent(qc, "agent-1")!.runtime_id).toBe("rt-1");
  });

  it("still updates the list when detail cache is cold", async () => {
    const { qc, result } = setup([makeAgent()], { seedDetail: false });
    mockUpdateAgent.mockResolvedValue(makeAgent({ runtime_id: "rt-2" }));

    await result.current("agent-1", { runtime_id: "rt-2" });

    expect(cachedAgent(qc, "agent-1")!.runtime_id).toBe("rt-2");
    // Seeds detail from the server payload so a later profile open is fresh.
    expect(cachedDetail(qc, "agent-1")!.runtime_id).toBe("rt-2");
  });

  it("keeps PATCH detail write when a late GET would have restored the old runtime", async () => {
    const { qc, result } = setup([makeAgent()]);
    mockUpdateAgent.mockResolvedValue(makeAgent({ runtime_id: "rt-2" }));

    // Simulate an in-flight GetAgent that resolves AFTER the mutation with
    // stale pre-PATCH data (the toast-success / chip-stale race in LRM-296).
    const lateGet = qc.fetchQuery({
      queryKey: agentDetailKeys.detail(WS, "agent-1"),
      queryFn: async () => {
        await new Promise((r) => setTimeout(r, 30));
        return makeAgent({ runtime_id: "rt-1" });
      },
    });

    await result.current("agent-1", { runtime_id: "rt-2" });
    await lateGet.catch(() => undefined);

    expect(cachedDetail(qc, "agent-1")!.runtime_id).toBe("rt-2");
  });

  it("does not leave undefined over a good runtime when the PATCH omits the field", async () => {
    const { qc, result } = setup([makeAgent()]);
    // Server echoes agent without runtime_id key — must not wipe the chip.
    mockUpdateAgent.mockResolvedValue(
      makeAgent({ runtime_id: undefined as unknown as string }),
    );
    // Force the returned object to omit runtime_id entirely.
    mockUpdateAgent.mockResolvedValue({
      ...makeAgent(),
      runtime_id: undefined,
    });

    await result.current("agent-1", { runtime_id: "rt-2" });

    expect(cachedDetail(qc, "agent-1")!.runtime_id).toBe("rt-2");
  });
});
