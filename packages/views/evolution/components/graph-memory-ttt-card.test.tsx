// @vitest-environment jsdom

import type { ReactNode } from "react";
import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, waitFor, fireEvent } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import { api, ApiError } from "@multica/core/api";
import { EMPTY_GRAPH_MEMORY_PROFILE } from "@multica/core/api/schemas";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { toast } from "sonner";
import type { GraphMemoryProfile } from "@multica/core/types";
import enEvolution from "../../locales/en/evolution.json";
import { GraphMemoryTttCard } from "./graph-memory-cards";

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
    memory_type: "graph",
    explore_agents: 4,
    explore_max_rounds: 3,
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
    dive_model: "explore-v1",
    dive_provider: "openai",
    config_version: 5,
    updated_at: "2026-08-18T01:00:00Z",
    ...overrides,
  };
}

function expectedUpdatePayload(
  profile: GraphMemoryProfile,
  patch: { ttt_enabled: boolean; explore_agents: number },
) {
  return {
    memory_type: profile.memory_type,
    explore_agents: patch.explore_agents,
    explore_max_rounds: profile.explore_max_rounds,
    config_version: profile.config_version,
    ttt_enabled: patch.ttt_enabled,
    explore_nodes_per_expansion: profile.explore_nodes_per_expansion,
    max_hierarchy_fanout: profile.max_hierarchy_fanout,
    max_relation_edges_per_node: profile.max_relation_edges_per_node,
    dive_max_rounds: profile.dive_max_rounds,
    dive_max_viewed_nodes: profile.dive_max_viewed_nodes,
    dive_max_source_files: profile.dive_max_source_files,
    dive_timeout_seconds: profile.dive_timeout_seconds,
    w_round: profile.w_round,
    source_max_file_bytes: profile.source_max_file_bytes,
    source_max_total_bytes: profile.source_max_total_bytes,
    source_max_pdf_pages: profile.source_max_pdf_pages,
    source_max_av_seconds: profile.source_max_av_seconds,
    source_max_image_megapixels: profile.source_max_image_megapixels,
    dive_model: profile.dive_model,
    dive_provider: profile.dive_provider,
  };
}

function I18nWrapper({ children }: { children: ReactNode }) {
  return (
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      {children}
    </I18nProvider>
  );
}

function renderTttCard(options: {
  profile?: GraphMemoryProfile;
  isAdmin?: boolean;
}) {
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
        <GraphMemoryTttCard wsId="ws-1" isAdmin={isAdmin} />
      </QueryClientProvider>
    </I18nWrapper>,
  );

  return { queryClient, invalidate };
}

beforeEach(() => {
  vi.clearAllMocks();
});

describe("GraphMemoryTttCard", { timeout: 20000 }, () => {
  it("renders the TTT switch off by default with a disabled concurrency field showing the saved K and the effective-K-1 hint", async () => {
    renderTttCard({ profile: graphProfile({ ttt_enabled: false, explore_agents: 4 }) });

    const tttSwitch = await screen.findByRole("switch", { name: enEvolution.graphTtt });
    expect(tttSwitch).toHaveAttribute("aria-checked", "false");
    const concurrency = screen.getByLabelText(enEvolution.graphTttConcurrency);
    expect(concurrency).toBeDisabled();
    expect(concurrency).toHaveValue(4);
    expect(screen.getByText(enEvolution.graphTttEffectiveK)).toBeTruthy();
  });

  it("enables concurrency after toggling on and saves ttt_enabled, edited K, unchanged fields, and config_version", async () => {
    const profile = graphProfile({ ttt_enabled: false, explore_agents: 4, config_version: 5 });
    renderTttCard({ profile });
    const user = userEvent.setup();

    const tttSwitch = await screen.findByRole("switch", { name: enEvolution.graphTtt });
    fireEvent.click(tttSwitch);
    await waitFor(() => {
      expect(tttSwitch).toHaveAttribute("aria-checked", "true");
    });
    const concurrency = screen.getByLabelText(enEvolution.graphTttConcurrency);
    expect(concurrency).toBeEnabled();
    fireEvent.change(concurrency, { target: { value: "8" } });
    await user.click(screen.getByRole("button", { name: enEvolution.graphTttSave }));

    await waitFor(() => {
      expect(api.updateGraphMemoryProfile).toHaveBeenCalledWith(
        "ws-1",
        expectedUpdatePayload(profile, { ttt_enabled: true, explore_agents: 8 }),
      );
    });
    expect(toast.success).toHaveBeenCalledWith(enEvolution.graphTttSaved);
  });

  it("keeps the saved explore_agents when toggling TTT off rather than resetting K to 1", async () => {
    const profile = graphProfile({ ttt_enabled: true, explore_agents: 8, config_version: 5 });
    renderTttCard({ profile });
    const user = userEvent.setup();

    const tttSwitch = await screen.findByRole("switch", { name: enEvolution.graphTtt });
    expect(tttSwitch).toHaveAttribute("aria-checked", "true");
    fireEvent.click(tttSwitch);
    const concurrency = screen.getByLabelText(enEvolution.graphTttConcurrency);
    expect(concurrency).toBeDisabled();
    expect(concurrency).toHaveValue(8);
    await user.click(screen.getByRole("button", { name: enEvolution.graphTttSave }));

    await waitFor(() => {
      expect(api.updateGraphMemoryProfile).toHaveBeenCalledWith(
        "ws-1",
        expectedUpdatePayload(profile, { ttt_enabled: false, explore_agents: 8 }),
      );
    });
  });

  it("invalidates the profile query and shows a conflict toast on 409 without a silent overwrite", async () => {
    const profile = graphProfile();
    const { invalidate } = renderTttCard({ profile });
    vi.mocked(api.updateGraphMemoryProfile).mockRejectedValue(
      new ApiError("stale config_version", 409, "Conflict"),
    );
    const user = userEvent.setup();

    await screen.findByRole("switch", { name: enEvolution.graphTtt });
    await user.click(screen.getByRole("button", { name: enEvolution.graphTttSave }));

    await waitFor(() => {
      expect(showErrorToast).toHaveBeenCalledWith(enEvolution.graphTttConflict);
    });
    expect(invalidate).toHaveBeenCalledWith(
      expect.objectContaining({ queryKey: ["evolution", "ws-1", "graph-memory-profile"] }),
    );
    expect(toast.success).not.toHaveBeenCalled();
  });

  it("renders a parse-error state with retry and no editing controls when the profile cannot be parsed", async () => {
    renderTttCard({ profile: EMPTY_GRAPH_MEMORY_PROFILE });

    expect(await screen.findByText(enEvolution.graphTttParseError)).toBeTruthy();
    expect(screen.getByRole("button", { name: enEvolution.graphTttRetry })).toBeTruthy();
    expect(screen.queryByRole("switch", { name: enEvolution.graphTtt })).toBeNull();
    expect(screen.queryByLabelText(enEvolution.graphTttConcurrency)).toBeNull();
    expect(screen.queryByRole("button", { name: enEvolution.graphTttSave })).toBeNull();
  });

  it("disables TTT controls for non-admins", async () => {
    renderTttCard({
      profile: graphProfile({ ttt_enabled: true, explore_agents: 4 }),
      isAdmin: false,
    });

    const tttSwitch = await screen.findByRole("switch", { name: enEvolution.graphTtt });
    // Base UI Switch is a span: it surfaces disabled as aria-disabled, not the HTML disabled property.
    expect(tttSwitch).toHaveAttribute("aria-disabled", "true");
    expect(screen.getByLabelText(enEvolution.graphTttConcurrency)).toBeDisabled();
    expect(screen.getByRole("button", { name: enEvolution.graphTttSave })).toBeDisabled();
  });

  it("does not render TTT controls when memory_type is legacy", async () => {
    renderTttCard({ profile: graphProfile({ memory_type: "legacy" }) });

    await waitFor(() => {
      expect(api.getGraphMemoryProfile).toHaveBeenCalledWith("ws-1");
    });
    expect(screen.queryByRole("switch", { name: enEvolution.graphTtt })).toBeNull();
    expect(screen.queryByLabelText(enEvolution.graphTttConcurrency)).toBeNull();
    expect(screen.queryByText(enEvolution.graphTtt)).toBeNull();
  });
});
