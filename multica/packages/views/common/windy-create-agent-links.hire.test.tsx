// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, fireEvent, cleanup, waitFor } from "@testing-library/react";
import type { AgentCreationDraft } from "@multica/core/types";
import { I18nProvider } from "@multica/core/i18n/react";
import { WorkspaceSlugProvider } from "@multica/core/paths";
import enCommon from "../locales/en/common.json";
import enAgents from "../locales/en/agents.json";
import enModals from "../locales/en/modals.json";

const { listChannels, createAgent, createAgentDraft, createChannel, listRuntimes, listMembers } =
  vi.hoisted(() => ({
    listChannels: vi.fn(),
    createAgent: vi.fn(),
    createAgentDraft: vi.fn(),
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
    createAgentDraft: (...args: unknown[]) => createAgentDraft(...args),
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

function makeDraft(
  overrides: Partial<AgentCreationDraft> = {},
): AgentCreationDraft {
  return {
    id: "draft-1",
    workspace_id: "ws-1",
    target_user_id: "user-me",
    name: "Group Bot",
    description: "Hired for one group",
    instructions: "Be helpful",
    channel_id: "ch-home",
    project_id: null,
    can_execute_code: false,
    suggested_channels: [],
    recommended_tools: [],
    avatar_url: null,
    status: "draft",
    created_at: "2026-07-23T00:00:00Z",
    updated_at: "2026-07-23T00:00:00Z",
    ...overrides,
  };
}

function renderHire() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      <QueryClientProvider client={qc}>
        <WorkspaceSlugProvider slug="test-ws">
          <WindyCreateAgentLink href="multica://create-agent?name=Group%20Bot&visibility=channel&channel_id=ch-home">
            Hire
          </WindyCreateAgentLink>
        </WorkspaceSlugProvider>
      </QueryClientProvider>
    </I18nProvider>,
  );
}

describe("Windy hire card create path", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    listChannels.mockResolvedValue([
      {
        id: "ch-home",
        workspace_id: "ws-1",
        name: "Home Group",
        kind: "group",
        description: null,
        lark_chat_id: null,
        created_by: "user-me",
        created_at: "2026-01-01T00:00:00Z",
        updated_at: "2026-01-01T00:00:00Z",
      },
    ]);
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
    createAgentDraft.mockResolvedValue(makeDraft());
    createAgent.mockResolvedValue({
      id: "agent-1",
      display_name: "Group Bot",
      home_channel_id: "ch-home",
    });
  });

  afterEach(() => {
    cleanup();
    document.body.innerHTML = "";
  });

  // This is the only test of the hire card's create path — if it stops
  // asserting the field, nothing does.
  it("submits no visibility and no home_channel_id", async () => {
    renderHire();
    fireEvent.click(screen.getByRole("button", { name: /Hire/i }));
    await waitFor(() => {
      expect(screen.getByRole("button", { name: /^Create Agent$/i })).toBeTruthy();
    });
    // LRM-808 / LRM-914: wait for auto-selected model so Create is enabled.
    await waitFor(() => {
      expect(
        (screen.getByRole("button", { name: /^Create Agent$/i }) as HTMLButtonElement)
          .disabled,
      ).toBe(false);
    });
    fireEvent.click(screen.getByRole("button", { name: /^Create Agent$/i }));
    await waitFor(() => expect(createAgent).toHaveBeenCalled());

    const payload = createAgent.mock.calls[0]?.[0] as Record<string, unknown>;
    expect(payload.visibility).toBeUndefined();
    // Strictly undefined, not merely "not ch-home": the draft and the link both
    // name ch-home, so a weaker assertion would pass on any other channel too.
    expect(payload.home_channel_id).toBeUndefined();
    expect(payload.draft_id).toBe("draft-1");
    expect(payload.model).toBe("composer-1.5");
  });

  // The link carries `visibility=channel&channel_id=ch-home` and the draft
  // fixture says "channel" — both are now ignored rather than seeded, so this
  // also covers that a stale Windy link cannot resurrect the retired tier.
  it("renders no discoverability radios and ignores the link's visibility param", async () => {
    renderHire();
    fireEvent.click(screen.getByRole("button", { name: /Hire/i }));
    await waitFor(() => {
      expect(screen.getByRole("button", { name: /^Create Agent$/i })).toBeTruthy();
    });
    expect(screen.queryAllByRole("radio")).toHaveLength(0);
    expect(screen.queryByLabelText(/New group name/i)).toBeNull();

    expect(createAgentDraft).toHaveBeenCalled();
    expect(createAgentDraft.mock.calls[0]?.[0]).not.toHaveProperty("visibility");
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
        device_info: "locked-box",
        metadata: {},
        current_version: null,
        update_state: "idle",
        runtime_health: "ok",
        owner_id: "user-other",
        visibility: "private",
        last_seen_at: "2026-04-27T11:59:50Z",
        created_at: "2026-04-01T00:00:00Z",
        updated_at: "2026-04-01T00:00:00Z",
      },
      {
        id: "rt-mine",
        workspace_id: "ws-1",
        daemon_id: "daemon-mine",
        name: "My Runtime",
        display_name: "my-box",
        runtime_mode: "local",
        provider: "cursor",
        launch_header: "",
        status: "online",
        device_info: "my-box",
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

    renderHire();
    fireEvent.click(screen.getByRole("button", { name: /Hire/i }));
    await waitFor(() => {
      expect(screen.getByRole("button", { name: /^Create Agent$/i })).toBeTruthy();
    });

    // Default seeds to my-box (usable). Switch to locked-box — Create must
    // disable even though another machine still has a usable runtime.
    fireEvent.click(screen.getByText("my-box", { selector: "div.truncate" }));
    fireEvent.click(
      screen.getByText("locked-box", { selector: "div.truncate" }),
    );

    await waitFor(() => {
      expect(screen.getByTestId("hire-selected-runtime").textContent).toBe(
        "rt-locked",
      );
    });
    expect(
      (screen.getByRole("button", { name: /^Create Agent$/i }) as HTMLButtonElement)
        .disabled,
    ).toBe(true);
    expect(createAgent).not.toHaveBeenCalled();
  });
});
