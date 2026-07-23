// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, fireEvent, cleanup, waitFor } from "@testing-library/react";
import { I18nProvider } from "@multica/core/i18n/react";
import { WorkspaceSlugProvider } from "@multica/core/paths";
import { VISIBILITY_LABEL } from "@multica/core/agents";
import type { AgentCreationDraft } from "@multica/core/types";
import { toast } from "sonner";
import enCommon from "../locales/en/common.json";
import enAgents from "../locales/en/agents.json";
import enModals from "../locales/en/modals.json";

const { createAgent, createAgentDraft, listChannels } = vi.hoisted(() => ({
  createAgent: vi.fn(),
  createAgentDraft: vi.fn(),
  listChannels: vi.fn(),
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@multica/core/auth", () => ({
  useAuthStore: (sel: (s: { user: { id: string } }) => unknown) =>
    sel({ user: { id: "user-me" } }),
}));

vi.mock("@multica/core/channels", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@multica/core/channels")>();
  return {
    ...actual,
    channelsOptions: () => ({
      queryKey: ["channels", "ws-1", "list"],
      queryFn: () => listChannels(),
      enabled: true,
    }),
  };
});

vi.mock("@multica/core/api", () => ({
  api: {
    createAgent: (...args: unknown[]) => createAgent(...args),
    createAgentDraft: (...args: unknown[]) => createAgentDraft(...args),
    listChannels: (...args: unknown[]) => listChannels(...args),
  },
}));

vi.mock("@multica/core/runtimes", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@multica/core/runtimes")>();
  return {
    ...actual,
    runtimeListOptions: () => ({
      queryKey: ["runtimes", "ws-1"],
      queryFn: async () => [
        {
          id: "rt-1",
          workspace_id: "ws-1",
          name: "Local",
          status: "online",
          visibility: "private",
          owner_id: "user-me",
        },
      ],
    }),
  };
});

vi.mock("@multica/core/workspace/queries", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@multica/core/workspace/queries")>();
  return {
    ...actual,
    memberListOptions: () => ({
      queryKey: ["members", "ws-1"],
      queryFn: async () => [
        {
          id: "m-me",
          user_id: "user-me",
          workspace_id: "ws-1",
          role: "member",
          name: "Me",
          email: "me@example.com",
        },
      ],
    }),
  };
});

vi.mock("../agents/components/model-dropdown", () => ({
  ModelDropdown: () => null,
}));

vi.mock("../agents/components/thinking-dropdown", () => ({
  ThinkingDropdown: () => null,
}));

vi.mock("../runtimes/components/provider-logo", () => ({
  ProviderLogo: () => null,
}));

vi.mock("../common/actor-avatar", () => ({
  ActorAvatar: () => null,
}));

vi.mock("sonner", () => ({
  toast: { error: vi.fn(), success: vi.fn(), warning: vi.fn() },
}));

import { WindyCreateAgentLink } from "./windy-create-agent-links";

const TEST_RESOURCES = {
  en: { common: enCommon, agents: enAgents, modals: enModals },
};

const draft: AgentCreationDraft = {
  id: "draft-1",
  workspace_id: "ws-1",
  target_user_id: "user-me",
  name: "Hiree",
  description: "A hired agent",
  instructions: "Do the work",
  visibility: "channel",
  channel_id: "ch-home",
  project_id: null,
  can_execute_code: false,
  suggested_channels: [],
  recommended_tools: [],
  avatar_url: null,
  status: "draft",
  created_at: "2026-07-23T00:00:00Z",
  updated_at: "2026-07-23T00:00:00Z",
};

describe("Windy hire form discoverability (LRM-399)", () => {
  beforeEach(() => {
    createAgent.mockReset();
    createAgentDraft.mockReset();
    listChannels.mockReset();
    listChannels.mockResolvedValue([
      {
        id: "ch-home",
        workspace_id: "ws-1",
        name: "Home Group",
        kind: "group",
        archived_at: null,
      },
    ]);
    createAgentDraft.mockResolvedValue(draft);
    createAgent.mockResolvedValue({
      id: "agent-1",
      display_name: "Hiree",
      visibility: "channel",
      home_channel_id: "ch-home",
    });
    vi.mocked(toast.error).mockClear();
  });

  afterEach(() => cleanup());

  async function openHireDialog() {
    const qc = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    render(
      <QueryClientProvider client={qc}>
        <I18nProvider resources={TEST_RESOURCES} locale="en">
          <WorkspaceSlugProvider slug="ws">
            <WindyCreateAgentLink href="multica://create-agent?name=Hiree&visibility=channel&channel_id=ch-home">
              Hire Hiree
            </WindyCreateAgentLink>
          </WorkspaceSlugProvider>
        </I18nProvider>
      </QueryClientProvider>,
    );
    fireEvent.click(screen.getByRole("button", { name: /Hire Hiree/i }));
    await waitFor(() => {
      expect(screen.getByRole("radiogroup")).toBeInTheDocument();
    });
  }

  it("shows discoverability radios and expands home chip for 仅本群", async () => {
    await openHireDialog();
    expect(screen.getByRole("radio", { name: new RegExp(VISIBILITY_LABEL.private) })).toBeTruthy();
    expect(screen.getByRole("radio", { name: new RegExp(VISIBILITY_LABEL.channel) })).toBeTruthy();
    expect(screen.getByRole("radio", { name: new RegExp(VISIBILITY_LABEL.workspace) })).toBeTruthy();
    expect(screen.getByRole("radio", { name: new RegExp(VISIBILITY_LABEL.channel) })).toHaveAttribute(
      "aria-checked",
      "true",
    );
    expect(screen.getByText("Home Group")).toBeTruthy();
  });

  it("submits visibility=channel with home_channel_id", async () => {
    await openHireDialog();
    fireEvent.click(screen.getByRole("button", { name: /create agent|创建/i }));
    await waitFor(() => expect(createAgent).toHaveBeenCalledTimes(1));
    expect(createAgent.mock.calls[0]?.[0]).toMatchObject({
      visibility: "channel",
      home_channel_id: "ch-home",
      draft_id: "draft-1",
    });
  });

  it("blocks submit when 仅本群 has no home (no silent private fallback)", async () => {
    createAgentDraft.mockResolvedValueOnce({ ...draft, channel_id: null });
    await openHireDialog();
    // Chip may show pick label; clear home by selecting channel without bind is
    // already the case when channel_id is null — submit must toast + skip API.
    fireEvent.click(screen.getByRole("button", { name: /create agent|创建/i }));
    await waitFor(() => expect(toast.error).toHaveBeenCalled());
    expect(createAgent).not.toHaveBeenCalled();
  });
});
