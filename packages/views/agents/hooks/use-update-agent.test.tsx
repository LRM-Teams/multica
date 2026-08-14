// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach } from "vitest";
import type { ReactNode } from "react";
import { renderHook } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { Agent } from "@multica/core/types";
import { agentDetailKeys } from "@multica/core/agents";
import { workspaceKeys } from "@multica/core/workspace/queries";

// `api.updateAgent` is the save path; reset preflight + `restart` mode cover
// the post-save process replacement.
const mockUpdateAgent = vi.hoisted(() => vi.fn());
const mockGetPreflight = vi.hoisted(() => vi.fn());
const mockStartLifecycle = vi.hoisted(() => vi.fn());
vi.mock("@multica/core/api", () => ({
  api: {
    updateAgent: (...args: unknown[]) => mockUpdateAgent(...args),
    getAgentRestartPreflight: (...args: unknown[]) => mockGetPreflight(...args),
    resetAgent: (...args: unknown[]) => mockStartLifecycle(...args),
  },
}));

const mockToastSuccess = vi.hoisted(() => vi.fn());
const mockShowErrorToast = vi.hoisted(() => vi.fn());
vi.mock("sonner", () => ({
  toast: { success: (...args: unknown[]) => mockToastSuccess(...args), error: vi.fn() },
}));
vi.mock("@multica/ui/lib/error-toast", () => ({
  showErrorToast: (...args: unknown[]) => mockShowErrorToast(...args),
}));

vi.mock("../../i18n", () => ({
  useT: () => ({
    t: (
      select: (dict: {
        detail: {
          agent_updated_toast: string;
          update_failed_toast: string;
        };
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
import type { AgentRestartPreflight } from "@multica/core/types";

/** Default preflight: force_restart + restart supported. */
function makePreflight(
  overrides: Partial<AgentRestartPreflight> & {
    restart?: Partial<AgentRestartPreflight["actions"]["restart"]>;
    force_restart?: boolean;
  } = {},
): AgentRestartPreflight {
  const { restart: restartOverride, force_restart = true, ...rest } = overrides;
  return {
    actions: {
      restart: {
        supported: true,
        ...restartOverride,
      },
      session: { supported: true },
      full: { supported: true },
    },
    provider_capabilities: {
      force_restart,
      custom_model_id: true,
      model_selection: true,
      thinking_discovery: true,
      canonical_resident: false,
      needs_inline_system_prompt: false,
    },
    ...rest,
  };
}

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
  // Execution-config saves (model/runtime/thinking) call preflight after PATCH.
  // Default happy path so cache-mapping tests keep working without re-stating it.
  mockGetPreflight.mockResolvedValue(makePreflight());
  mockStartLifecycle.mockResolvedValue({ id: "op-1", status: "running" });
});

describe("useUpdateAgent — optimistic fields", () => {
  it("maps fields 1:1 and rolls them back on failure", async () => {
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

describe("useUpdateAgent — restart after execution-config save", () => {
  it("model change → preflight + Raft restart mode", async () => {
    const { result } = setup([makeAgent()]);
    mockUpdateAgent.mockResolvedValue(makeAgent({ model: "claude-opus-4-8" }));

    await result.current("agent-1", { model: "claude-opus-4-8" });

    expect(mockUpdateAgent).toHaveBeenCalledWith("agent-1", {
      model: "claude-opus-4-8",
    });
    expect(mockGetPreflight).toHaveBeenCalledWith("agent-1");
    expect(mockStartLifecycle).toHaveBeenCalledWith(
      "agent-1",
      "restart",
      expect.any(String),
    );
    expect(mockToastSuccess).toHaveBeenCalledWith("Agent updated");
    expect(mockToastSuccess).not.toHaveBeenCalledWith("Saved. Restarting…");
  });

  it("A1: runtime_id change also restarts", async () => {
    const { result } = setup([makeAgent()]);
    mockUpdateAgent.mockResolvedValue(makeAgent({ runtime_id: "rt-2" }));

    await result.current("agent-1", { runtime_id: "rt-2" });

    expect(mockStartLifecycle).toHaveBeenCalledWith(
      "agent-1",
      "restart",
      expect.any(String),
    );
  });

  it("A1: thinking_level change also restarts", async () => {
    const { result } = setup([makeAgent()]);
    mockUpdateAgent.mockResolvedValue(
      makeAgent({ thinking_level: "high" } as Partial<Agent>),
    );

    await result.current("agent-1", { thinking_level: "high" });

    expect(mockStartLifecycle).toHaveBeenCalledWith(
      "agent-1",
      "restart",
      expect.any(String),
    );
  });

  it("force_restart false → save normally, no restart-specific toast", async () => {
    const { result } = setup([makeAgent()]);
    mockUpdateAgent.mockResolvedValue(makeAgent({ model: "claude-opus-4-8" }));
    mockGetPreflight.mockResolvedValue(makePreflight({ force_restart: false }));

    await result.current("agent-1", { model: "claude-opus-4-8" });

    expect(mockStartLifecycle).not.toHaveBeenCalled();
    expect(mockToastSuccess).toHaveBeenCalledWith("Agent updated");
  });

  it("A4: restart unsupported → save normally, no restart-specific toast", async () => {
    const { result } = setup([makeAgent()]);
    mockUpdateAgent.mockResolvedValue(makeAgent({ model: "claude-opus-4-8" }));
    mockGetPreflight.mockResolvedValue(
      makePreflight({ restart: { supported: false, disabled_reason: "unavailable" } }),
    );

    await result.current("agent-1", { model: "claude-opus-4-8" });

    expect(mockStartLifecycle).not.toHaveBeenCalled();
    expect(mockToastSuccess).toHaveBeenCalledWith("Agent updated");
  });

  it("restart failure after save: keeps config without restart toast or throw", async () => {
    const { qc, result } = setup([makeAgent()]);
    mockUpdateAgent.mockResolvedValue(makeAgent({ model: "claude-opus-4-8" }));
    mockStartLifecycle.mockRejectedValue(new Error("daemon offline"));

    await expect(
      result.current("agent-1", { model: "claude-opus-4-8" }),
    ).resolves.toBeUndefined();

    // Config remains saved in cache.
    expect(cachedAgent(qc, "agent-1")!.model).toBe("claude-opus-4-8");
    expect(mockToastSuccess).toHaveBeenCalledWith("Agent updated");
    expect(mockShowErrorToast).not.toHaveBeenCalled();
  });
});
