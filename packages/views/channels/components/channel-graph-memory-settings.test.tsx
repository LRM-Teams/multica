import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { api } from "@multica/core/api";
import { ChannelGraphMemorySettings } from "./channel-graph-memory-settings";

vi.mock("@multica/core/api", () => ({
  api: {
    getGraphMemoryChannelMode: vi.fn(),
    updateGraphMemoryChannelMode: vi.fn(),
    resetGraphMemoryChannelAgent: vi.fn(),
    getGraphMemoryChannelLineage: vi.fn(),
  },
}));

vi.mock("@multica/ui/lib/error-toast", () => ({
  showErrorToast: vi.fn(),
}));

vi.mock("../../i18n", () => ({
  useT: () => ({
    t: (selector: (root: any) => string, vars?: Record<string, string | number>) => {
      const value = selector({ graph_memory: {
        title: "Graph memory",
        description: "Override the workspace delivery mode for this channel.",
        inherit: "Inherit",
        agent: "Memory Agent",
        inject: "Inject",
        effective: "Effective mode",
        save: "Save",
        reset: "Reset state",
        update_failed: "Couldn't update graph memory mode",
        reset_failed: "Couldn't reset the Memory Agent",
        migration_title: "Memory migration",
        migration_phase: "Phase: {phase}",
        migration_generation: "Generation {generation}",
        migration_copied: "{count} atoms copied",
      } });
      return Object.entries(vars ?? {}).reduce((text, [key, item]) => text.replace(`{${key}}`, String(item)), value);
    },
  }),
}));

const channelMode = {
  workspace_id: "ws-1",
  channel_id: "c-1",
  override: "inherit" as const,
  effective_mode: "agent" as const,
  status: "active" as const,
  blocked_reason: "",
  agent_id: "agent-1",
  runtime_id: "runtime-1",
  memory_agent_runtime_id_override: "",
  memory_agent_model_override: "",
  memory_agent_thinking_override: "",
  effective_memory_agent_runtime_id: "",
  effective_memory_agent_model: "",
  effective_memory_agent_thinking: "",
};

function renderSettings() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <ChannelGraphMemorySettings wsId="ws-1" channelId="c-1" disabled={false} />
    </QueryClientProvider>,
  );
}

describe("ChannelGraphMemorySettings", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(api.getGraphMemoryChannelMode).mockResolvedValue(channelMode);
  });

  it("shows the migration phase row when a binding generation exists", async () => {
    vi.mocked(api.getGraphMemoryChannelLineage).mockResolvedValue({
      workspace_id: "ws-1",
      channel_id: "c-1",
      routing_mode: "project_lineage",
      current: null,
      lineage: [],
      migration: { binding_generation: 3, source_watermark: 1200, phase: "copying", copied_atoms: 42 },
    });

    renderSettings();

    expect(await screen.findByText("Memory migration")).toBeTruthy();
    expect(screen.getByText("Phase: copying")).toBeTruthy();
    expect(screen.getByText("Generation 3")).toBeTruthy();
    expect(screen.getByText("42 atoms copied")).toBeTruthy();
  });

  it("renders no migration row when the channel never rebound across projects", async () => {
    vi.mocked(api.getGraphMemoryChannelLineage).mockResolvedValue({
      workspace_id: "ws-1",
      channel_id: "c-1",
      routing_mode: "",
      current: null,
      lineage: [],
      migration: null,
    });

    renderSettings();

    await waitFor(() => expect(api.getGraphMemoryChannelLineage).toHaveBeenCalled());
    expect(screen.queryByText("Memory migration")).toBeNull();
    expect(screen.getByText("Graph memory")).toBeTruthy();
  });

  it("keeps the mode controls when lineage loading fails", async () => {
    vi.mocked(api.getGraphMemoryChannelLineage).mockRejectedValue(new Error("lineage unavailable"));

    renderSettings();

    expect(await screen.findByText("Graph memory")).toBeTruthy();
    await waitFor(() => expect(api.getGraphMemoryChannelLineage).toHaveBeenCalled());
    expect(screen.queryByText("Memory migration")).toBeNull();
    expect(screen.getByRole("button", { name: "Inherit" })).toBeEnabled();
    expect(screen.getByRole("button", { name: "Memory Agent" })).toBeEnabled();
  });
});
