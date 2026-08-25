// @vitest-environment jsdom

import type { ReactNode } from "react";
import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import { api, ApiError } from "@multica/core/api";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { toast } from "sonner";
import type { GraphMemoryProfile } from "@multica/core/types";
import enEvolution from "../../locales/en/evolution.json";
import { MemoryTypeCard } from "./evolution-center-page";

const TEST_RESOURCES = { en: { evolution: enEvolution } };

vi.mock("@multica/core/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@multica/core/api")>();
  return {
    ...actual,
    api: {
      getGraphMemoryProfile: vi.fn(),
      updateGraphMemoryProfile: vi.fn(),
    },
  };
});

vi.mock("@multica/ui/lib/error-toast", () => ({
  showErrorToast: vi.fn(),
}));

vi.mock("sonner", () => ({
  toast: { success: vi.fn(), error: vi.fn() },
}));

function graphProfile(overrides: Partial<GraphMemoryProfile> = {}): GraphMemoryProfile {
  return {
    workspace_id: "ws-1",
    memory_type: "legacy",
    graph_memory_mode: "agent",
    memory_agent_runtime_id: "",
    memory_agent_model: "",
    memory_agent_thinking: "",
    recall_ttt_enabled: false,
    consolidation_ttt_enabled: false,
    memory_agent_idle_grace_seconds: 120,
    memory_agent_max_nodes_per_call: 4,
    memory_agent_max_nodes_per_minute: 30,
    memory_agent_max_continuous_turn_seconds: 600,
    memory_agent_max_tokens_per_hour: 200000,
    explore_agents: 4,
    explore_max_rounds: 6,
    ttt_enabled: false,
    explore_nodes_per_expansion: 1,
    max_hierarchy_fanout: 8,
    max_relation_edges_per_node: 8,
    dive_max_rounds: 6,
    dive_max_viewed_nodes: 24,
    dive_max_source_files: 4,
    dive_timeout_seconds: 600,
    w_round: 0.1,
    source_max_file_bytes: 20971520,
    source_max_total_bytes: 52428800,
    source_max_pdf_pages: 50,
    source_max_av_seconds: 600,
    source_max_image_megapixels: 40,
    dive_model: "",
    dive_provider: "",
    config_version: 7,
    updated_at: "2026-08-18T01:00:00Z",
    ...overrides,
  };
}

function I18nWrapper({ children }: { children: ReactNode }) {
  return (
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      {children}
    </I18nProvider>
  );
}

function renderMemoryTypeCard(options: { profile?: GraphMemoryProfile; isAdmin?: boolean }) {
  const { profile, isAdmin = true } = options;
  vi.mocked(api.getGraphMemoryProfile).mockResolvedValue(profile!);
  vi.mocked(api.updateGraphMemoryProfile).mockResolvedValue(profile ?? graphProfile());

  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const invalidate = vi.spyOn(queryClient, "invalidateQueries");

  render(
    <I18nWrapper>
      <QueryClientProvider client={queryClient}>
        <MemoryTypeCard wsId="ws-1" isAdmin={isAdmin} />
      </QueryClientProvider>
    </I18nWrapper>,
  );

  return { queryClient, invalidate };
}

// Base UI Select portals its popup onto document.body. userEvent.click
// dispatches the full pointer sequence, which Base UI needs to both highlight
// and commit the option; a bare fireEvent.click never highlights the option,
// so onValueChange would not fire.
async function pickMemoryType(user: ReturnType<typeof userEvent.setup>, name: string) {
  await user.click(screen.getByRole("combobox", { name: enEvolution.memoryType }));
  await user.click(await screen.findByRole("option", { name }));
}

beforeEach(() => {
  vi.clearAllMocks();
});

describe("MemoryTypeCard", { timeout: 20000 }, () => {
  it("sends config_version and confirm_empty_start when switching legacy -> graph", async () => {
    const profile = graphProfile({ memory_type: "legacy", config_version: 7 });
    renderMemoryTypeCard({ profile });
    const user = userEvent.setup();

    await screen.findByRole("combobox", { name: enEvolution.memoryType });
    await pickMemoryType(user, enEvolution.memoryTypeGraph);

    // The empty-start contract must be confirmed before the write fires.
    await user.click(await screen.findByRole("checkbox"));
    await user.click(screen.getByRole("button", { name: enEvolution.memoryTypeGraphConfirmApply }));

    await waitFor(() => {
      expect(api.updateGraphMemoryProfile).toHaveBeenCalledWith("ws-1", {
        memory_type: "graph",
        explore_agents: 4,
        explore_max_rounds: 6,
        config_version: 7,
        confirm_empty_start: true,
      });
    });
  });

  it("sends config_version and no confirm flag when switching graph -> legacy", async () => {
    const profile = graphProfile({ memory_type: "graph", config_version: 7 });
    renderMemoryTypeCard({ profile });
    const user = userEvent.setup();

    await screen.findByRole("combobox", { name: enEvolution.memoryType });
    await pickMemoryType(user, enEvolution.memoryTypeLegacy);

    await waitFor(() => {
      expect(api.updateGraphMemoryProfile).toHaveBeenCalledWith("ws-1", {
        memory_type: "legacy",
        explore_agents: 4,
        explore_max_rounds: 6,
        config_version: 7,
      });
    });
    expect(screen.queryByRole("checkbox")).toBeNull();
  });

  it("invalidates the profile query and shows a conflict toast on 409", async () => {
    const profile = graphProfile({ memory_type: "graph", config_version: 7 });
    const { invalidate } = renderMemoryTypeCard({ profile });
    vi.mocked(api.updateGraphMemoryProfile).mockRejectedValue(
      new ApiError("stale config_version", 409, "Conflict"),
    );
    const user = userEvent.setup();

    await screen.findByRole("combobox", { name: enEvolution.memoryType });
    await pickMemoryType(user, enEvolution.memoryTypeLegacy);

    await waitFor(() => {
      expect(showErrorToast).toHaveBeenCalledWith(enEvolution.memoryTypeConflict);
    });
    expect(invalidate).toHaveBeenCalledWith(
      expect.objectContaining({ queryKey: ["evolution", "ws-1", "graph-memory-profile"] }),
    );
    expect(toast.success).not.toHaveBeenCalled();
  });
});
