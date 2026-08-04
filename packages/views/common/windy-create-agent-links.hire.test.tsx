// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, fireEvent, cleanup, waitFor } from "@testing-library/react";
import type { AgentActionCard } from "@multica/core/types";
import { I18nProvider } from "@multica/core/i18n/react";
import { WorkspaceSlugProvider } from "@multica/core/paths";
import enCommon from "../locales/en/common.json";
import enAgents from "../locales/en/agents.json";
import enModals from "../locales/en/modals.json";

const {
  createAgent,
  getAgentActionCard,
  dismissAgentActionCard,
  listRuntimes,
  listMembers,
} = vi.hoisted(() => ({
  createAgent: vi.fn(),
  getAgentActionCard: vi.fn(),
  dismissAgentActionCard: vi.fn(),
  listRuntimes: vi.fn(),
  listMembers: vi.fn(),
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@multica/core/auth", () => ({
  useAuthStore: (sel: (s: { user: { id: string } }) => unknown) =>
    sel({ user: { id: "user-me" } }),
}));

vi.mock("@multica/core/runtimes", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@multica/core/runtimes")>();
  return {
    ...actual,
    runtimeListOptions: () => ({
      queryKey: ["runtimes", "ws-1"],
      queryFn: () => listRuntimes(),
      enabled: true,
    }),
  };
});

vi.mock("@multica/core/workspace/queries", async (importOriginal) => {
  const actual =
    await importOriginal<typeof import("@multica/core/workspace/queries")>();
  return {
    ...actual,
    memberListOptions: () => ({
      queryKey: ["members", "ws-1"],
      queryFn: () => listMembers(),
      enabled: true,
    }),
  };
});

vi.mock("@multica/core/api", () => ({
  api: {
    createAgent: (...args: unknown[]) => createAgent(...args),
    getAgentActionCard: (...args: unknown[]) => getAgentActionCard(...args),
    dismissAgentActionCard: (...args: unknown[]) => dismissAgentActionCard(...args),
  },
}));

vi.mock("../agents/components/model-dropdown", () => ({
  ModelDropdown: ({
    onChange,
    value,
    autoSelectFirst,
  }: {
    onChange: (value: string) => void;
    value: string;
    autoSelectFirst?: boolean;
  }) => {
    if (autoSelectFirst && !value.trim()) {
      queueMicrotask(() => onChange("composer-1.5"));
    }
    return null;
  },
}));

vi.mock("../agents/components/thinking-dropdown", () => ({
  ThinkingDropdown: () => null,
}));

vi.mock("../agents/components/runtime-picker", async (importOriginal) => {
  const actual =
    await importOriginal<typeof import("../agents/components/runtime-picker")>();
  return {
    ...actual,
    RuntimePicker: ({
      runtimes,
      selectedRuntimeId,
      onSelect,
    }: {
      runtimes: { id: string; name: string }[];
      selectedRuntimeId: string;
      onSelect: (id: string) => void;
    }) => (
      <div>
        <span data-testid="hire-selected-runtime">{selectedRuntimeId}</span>
        {runtimes.map((runtime) => (
          <button
            key={runtime.id}
            type="button"
            onClick={() => onSelect(runtime.id)}
          >
            {runtime.name}
          </button>
        ))}
      </div>
    ),
  };
});

vi.mock("sonner", () => ({
  toast: { error: vi.fn(), success: vi.fn(), warning: vi.fn() },
}));

import { AgentCreateActionCard } from "./windy-create-agent-links";

const TEST_RESOURCES = {
  en: { common: enCommon, agents: enAgents, modals: enModals },
};

const CARD_ID = "card-uuid-1";

function makeCard(overrides: Partial<AgentActionCard> = {}): AgentActionCard {
  return {
    id: CARD_ID,
    action_type: "agent:create",
    status: "prepared",
    payload: {
      name: "Group Bot",
      description: "Hired for one group",
    },
    prepared_by_agent_id: "agent-wendy",
    channel_id: null,
    created_at: "2026-08-04T00:00:00Z",
    updated_at: "2026-08-04T00:00:00Z",
    ...overrides,
  };
}

function renderCard(label?: string) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      <QueryClientProvider client={qc}>
        <WorkspaceSlugProvider slug="test-ws">
          <AgentCreateActionCard cardId={CARD_ID} label={label} />
        </WorkspaceSlugProvider>
      </QueryClientProvider>
    </I18nProvider>,
  );
}

describe("AgentCreateActionCard (parts reference contract A)", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    listRuntimes.mockResolvedValue([
      {
        id: "rt-1",
        workspace_id: "ws-1",
        daemon_id: null,
        name: "My Runtime",
        runtime_mode: "local",
        provider: "claude",
        launch_header: "",
        status: "online",
        device_info: "host.local",
        metadata: {},
        current_version: null,
        update_state: "idle",
        runtime_health: "ok",
        owner_id: "user-me",
        visibility: "private",
        last_seen_at: "2026-04-27T11:59:50Z",
        created_at: "2026-04-01T00:00:00Z",
        updated_at: "2026-04-01T00:00:00Z",
      },
    ]);
    listMembers.mockResolvedValue([]);
    getAgentActionCard.mockResolvedValue(makeCard());
    createAgent.mockResolvedValue({
      id: "agent-1",
      display_name: "Group Bot",
    });
    dismissAgentActionCard.mockResolvedValue(
      makeCard({ status: "dismissed" }),
    );
  });

  afterEach(() => {
    cleanup();
    document.body.innerHTML = "";
  });

  it("loads card by ref_id and shows independent card chrome", async () => {
    renderCard("Fallback Label");
    await waitFor(() => {
      expect(getAgentActionCard).toHaveBeenCalledWith(CARD_ID);
    });
    await waitFor(() => {
      expect(screen.getByTestId("agent-create-action-card")).toBeTruthy();
    });
    expect(screen.getByText("Group Bot")).toBeTruthy();
    expect(screen.getByText("Hired for one group")).toBeTruthy();
    expect(screen.getByRole("button", { name: /^Create Agent$/i })).toBeTruthy();
    expect(screen.getByRole("button", { name: /^Cancel$/i })).toBeTruthy();
  });

  it("submits action_card_id without draft_id", async () => {
    renderCard();
    await waitFor(() => {
      expect(screen.getByRole("button", { name: /^Create Agent$/i })).toBeTruthy();
    });
    fireEvent.click(screen.getByRole("button", { name: /^Create Agent$/i }));
    await waitFor(() => {
      const dialogCreate = screen
        .getAllByRole("button", { name: /^Create Agent$/i })
        .find((b) => !(b as HTMLButtonElement).disabled);
      expect(dialogCreate).toBeTruthy();
    });
    const dialogCreate = screen
      .getAllByRole("button", { name: /^Create Agent$/i })
      .find((b) => !(b as HTMLButtonElement).disabled)!;
    fireEvent.click(dialogCreate);
    await waitFor(() => expect(createAgent).toHaveBeenCalled());

    const payload = createAgent.mock.calls[0]?.[0] as Record<string, unknown>;
    expect(payload.action_card_id).toBe(CARD_ID);
    expect(payload.draft_id).toBeUndefined();
    expect(payload.display_name).toBe("Group Bot");
  });

  it("dismisses prepared card", async () => {
    renderCard();
    await waitFor(() => {
      expect(screen.getByRole("button", { name: /^Cancel$/i })).toBeTruthy();
    });
    fireEvent.click(screen.getByRole("button", { name: /^Cancel$/i }));
    await waitFor(() => {
      expect(dismissAgentActionCard).toHaveBeenCalledWith(CARD_ID);
    });
    await waitFor(() => {
      expect(screen.getByText(/Dismissed/i)).toBeTruthy();
    });
  });
});
