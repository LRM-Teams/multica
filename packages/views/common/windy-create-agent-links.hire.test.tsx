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
import {
  buildAgentCreateActionHref,
  isAgentCreateActionLink,
  parseAgentCreateActionURL,
} from "./windy-create-agent-link-utils";

const {
  listChannels,
  createAgent,
  getAgentActionCard,
  dismissAgentActionCard,
  createChannel,
  listRuntimes,
  listMembers,
} = vi.hoisted(() => ({
  listChannels: vi.fn(),
  createAgent: vi.fn(),
  getAgentActionCard: vi.fn(),
  dismissAgentActionCard: vi.fn(),
  createChannel: vi.fn(),
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
    listChannels: (...args: unknown[]) => listChannels(...args),
    createAgent: (...args: unknown[]) => createAgent(...args),
    getAgentActionCard: (...args: unknown[]) => getAgentActionCard(...args),
    dismissAgentActionCard: (...args: unknown[]) => dismissAgentActionCard(...args),
    createChannel: (...args: unknown[]) => createChannel(...args),
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
    // Keep real isRuntimeUsableForUser so hire Create gates match create-agent-dialog.
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

import { WindyCreateAgentLink } from "./windy-create-agent-links";

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

function renderHire(href = buildAgentCreateActionHref(CARD_ID)) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      <QueryClientProvider client={qc}>
        <WorkspaceSlugProvider slug="test-ws">
          <WindyCreateAgentLink href={href}>Hire</WindyCreateAgentLink>
        </WorkspaceSlugProvider>
      </QueryClientProvider>
    </I18nProvider>,
  );
}

describe("parseAgentCreateActionURL", () => {
  it("parses canonical action-card links", () => {
    expect(parseAgentCreateActionURL(buildAgentCreateActionHref(CARD_ID))).toEqual({
      actionType: "agent:create",
      cardId: CARD_ID,
    });
    expect(
      parseAgentCreateActionURL(`multica://action-card?id=${CARD_ID}`),
    ).toEqual({ actionType: "agent:create", cardId: CARD_ID });
  });

  it("rejects retired draft hire links", () => {
    expect(
      parseAgentCreateActionURL("multica://create-agent?draft_id=draft-1"),
    ).toBeNull();
    expect(
      parseAgentCreateActionURL("multica://create-agent?name=Bot"),
    ).toBeNull();
    expect(isAgentCreateActionLink("multica://create-agent?draft_id=x")).toBe(
      false,
    );
  });
});

describe("Windy hire card create path (action_card_id)", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    listChannels.mockResolvedValue([]);
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
  });

  afterEach(() => {
    cleanup();
    document.body.innerHTML = "";
  });

  it("loads card by id and submits action_card_id without draft_id", async () => {
    renderHire();
    fireEvent.click(screen.getByRole("button", { name: /Hire/i }));
    await waitFor(() => {
      expect(getAgentActionCard).toHaveBeenCalledWith(CARD_ID);
    });
    await waitFor(() => {
      expect(screen.getByRole("button", { name: /^Create Agent$/i })).toBeTruthy();
    });
    await waitFor(() => {
      expect(
        (screen.getByRole("button", { name: /^Create Agent$/i }) as HTMLButtonElement)
          .disabled,
      ).toBe(false);
    });
    fireEvent.click(screen.getByRole("button", { name: /^Create Agent$/i }));
    await waitFor(() => expect(createAgent).toHaveBeenCalled());

    const payload = createAgent.mock.calls[0]?.[0] as Record<string, unknown>;
    expect(payload.action_card_id).toBe(CARD_ID);
    expect(payload.draft_id).toBeUndefined();
    expect(payload.display_name).toBe("Group Bot");
    expect(payload.description).toBe("Hired for one group");
    expect(payload.visibility).toBeUndefined();
    expect(payload.home_channel_id).toBeUndefined();
    expect(payload.model).toBe("composer-1.5");
  });

  it("renders no discoverability radios", async () => {
    renderHire();
    fireEvent.click(screen.getByRole("button", { name: /Hire/i }));
    await waitFor(() => {
      expect(screen.getByRole("button", { name: /^Create Agent$/i })).toBeTruthy();
    });
    expect(screen.queryAllByRole("radio")).toHaveLength(0);
  });

  it("disables Create when the selected computer only has a locked runtime", async () => {
    listRuntimes.mockResolvedValue([
      {
        id: "rt-locked",
        workspace_id: "ws-1",
        daemon_id: "daemon-locked",
        name: "Locked Private",
        display_name: "locked-box",
        runtime_mode: "local",
        provider: "claude",
        launch_header: "",
        status: "online",
        device_info: "locked.local",
        metadata: {},
        current_version: null,
        update_state: "idle",
        runtime_health: "ok",
        owner_id: "someone-else",
        visibility: "private",
        last_seen_at: "2026-04-27T11:59:50Z",
        created_at: "2026-04-01T00:00:00Z",
        updated_at: "2026-04-01T00:00:00Z",
      },
    ]);
    renderHire();
    fireEvent.click(screen.getByRole("button", { name: /Hire/i }));
    await waitFor(() => {
      expect(screen.getByRole("button", { name: /^Create Agent$/i })).toBeTruthy();
    });
    expect(
      (screen.getByRole("button", { name: /^Create Agent$/i }) as HTMLButtonElement)
        .disabled,
    ).toBe(true);
  });
});
