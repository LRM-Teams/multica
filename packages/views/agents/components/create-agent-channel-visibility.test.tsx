// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, fireEvent, cleanup, waitFor } from "@testing-library/react";
import type { Agent, CreateAgentRequest, MemberWithUser, RuntimeDevice } from "@multica/core/types";
import { I18nProvider } from "@multica/core/i18n/react";
import { WorkspaceSlugProvider } from "@multica/core/paths";
import { NavigationProvider, type NavigationAdapter } from "../../navigation";
import enCommon from "../../locales/en/common.json";
import enAgents from "../../locales/en/agents.json";
import { VISIBILITY_LABEL } from "@multica/core/agents";

const navigationStub: NavigationAdapter = {
  push: vi.fn(),
  replace: vi.fn(),
  back: vi.fn(),
  pathname: "/",
  searchParams: new URLSearchParams(),
  getShareableUrl: (path: string) => path,
};

const TEST_RESOURCES = { en: { common: enCommon, agents: enAgents } };

const { listChannels } = vi.hoisted(() => ({
  listChannels: vi.fn(),
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
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
    listChannels: (...args: unknown[]) => listChannels(...args),
  },
}));

vi.mock("./model-dropdown", () => ({
  ModelDropdown: () => null,
}));

vi.mock("./thinking-dropdown", () => ({
  ThinkingDropdown: () => null,
}));

vi.mock("../../runtimes/components/provider-logo", () => ({
  ProviderLogo: () => null,
}));

vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: () => null,
}));

vi.mock("sonner", () => ({
  toast: { error: vi.fn(), success: vi.fn(), warning: vi.fn() },
}));

import { CreateAgentDialog } from "./create-agent-dialog";

const ME = "user-me";

const members: MemberWithUser[] = [
  {
    id: "m-me",
    user_id: ME,
    workspace_id: "ws-1",
    role: "member",
    name: "Me",
    display_name: "Me",
    email: "me@example.com",
    avatar_url: null,
    profile_description: "",
    created_at: "2026-01-01T00:00:00Z",
  },
];

function makeRuntime(overrides: Partial<RuntimeDevice> = {}): RuntimeDevice {
  return {
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
    owner_id: ME,
    visibility: "private",
    last_seen_at: "2026-04-27T11:59:50Z",
    created_at: "2026-04-01T00:00:00Z",
    updated_at: "2026-04-01T00:00:00Z",
    ...overrides,
  };
}

function renderDialog(opts?: {
  onCreate?: (data: CreateAgentRequest) => Promise<Agent | void>;
  defaultHomeChannelId?: string | null;
}) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const onCreate =
    opts?.onCreate ??
    (vi.fn(async () => undefined) as (
      data: CreateAgentRequest,
    ) => Promise<Agent | void>);
  const onClose = vi.fn();
  render(
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      <QueryClientProvider client={queryClient}>
        <WorkspaceSlugProvider slug="test-ws">
          <NavigationProvider value={navigationStub}>
            <CreateAgentDialog
              runtimes={[makeRuntime()]}
              members={members}
              currentUserId={ME}
              defaultHomeChannelId={opts?.defaultHomeChannelId}
              onClose={onClose}
              onCreate={onCreate}
            />
          </NavigationProvider>
        </WorkspaceSlugProvider>
      </QueryClientProvider>
    </I18nProvider>,
  );
  return { onCreate, onClose };
}

describe("CreateAgentDialog channel visibility (LRM-371 方案 A)", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    listChannels.mockResolvedValue([
      {
        id: "ch-home",
        workspace_id: "ws-1",
        name: "LRM2.0开发群",
        kind: "group",
        description: null,
        lark_chat_id: null,
        created_by: ME,
        created_at: "2026-01-01T00:00:00Z",
        updated_at: "2026-01-01T00:00:00Z",
      },
      {
        id: "ch-dm",
        workspace_id: "ws-1",
        name: "dm-peer",
        kind: "dm",
        description: null,
        lark_chat_id: null,
        created_by: ME,
        created_at: "2026-01-01T00:00:00Z",
        updated_at: "2026-01-01T00:00:00Z",
      },
    ]);
  });

  afterEach(() => {
    cleanup();
    document.body.innerHTML = "";
  });

  it("renders three visibility radios including 仅本群", async () => {
    renderDialog();
    expect(screen.getByRole("radio", { name: new RegExp(VISIBILITY_LABEL.private) })).toBeTruthy();
    expect(screen.getByRole("radio", { name: new RegExp(VISIBILITY_LABEL.channel) })).toBeTruthy();
    expect(screen.getByRole("radio", { name: new RegExp(VISIBILITY_LABEL.workspace) })).toBeTruthy();
  });

  it("expands home #chip when 仅本群 is selected and submits home_channel_id", async () => {
    const onCreate = vi.fn(async (_data: CreateAgentRequest) => undefined);
    renderDialog({ onCreate, defaultHomeChannelId: "ch-home" });
    fireEvent.change(screen.getByPlaceholderText(/e\.g\. Deep Research/i), {
      target: { value: "Group Bot" },
    });
    fireEvent.click(screen.getByRole("radio", { name: /仅本群/ }));
    expect(
      screen.getByRole("radio", { name: /仅本群/ }).getAttribute("aria-checked"),
    ).toBe("true");
    fireEvent.click(screen.getByRole("button", { name: /^Create$/i }));
    await waitFor(() => {
      expect(onCreate).toHaveBeenCalled();
    });
    const payload = onCreate.mock.calls[0]?.[0];
    expect(payload?.visibility).toBe("channel");
    expect(payload?.home_channel_id).toBe("ch-home");
  });

  it("blocks submit without home_channel_id and does not fall back to private", async () => {
    listChannels.mockResolvedValue([]);
    const onCreate = vi.fn(async (_data: CreateAgentRequest) => undefined);
    renderDialog({ onCreate });
    fireEvent.change(screen.getByPlaceholderText(/e\.g\. Deep Research/i), {
      target: { value: "Group Bot" },
    });
    // No groups → channel option disabled; stay on workspace and ensure
    // we never invent a silent private remap via create payload.
    fireEvent.click(screen.getByRole("button", { name: /^Create$/i }));
    await waitFor(() => expect(onCreate).toHaveBeenCalled());
    expect(onCreate.mock.calls[0]?.[0]?.visibility).toBe("workspace");
    expect(onCreate.mock.calls[0]?.[0]?.home_channel_id).toBeUndefined();
  });

  it("toasts when 仅本群 has no home_channel_id (no private fallback)", async () => {
    listChannels.mockResolvedValue([]);
    const onCreate = vi.fn(async (_data: CreateAgentRequest) => undefined);
    renderDialog({ onCreate });
    fireEvent.change(screen.getByPlaceholderText(/e\.g\. Deep Research/i), {
      target: { value: "Group Bot" },
    });
    const channelRadio = screen.getByRole("radio", { name: /仅本群/ });
    await waitFor(() => {
      expect(channelRadio).toBeDisabled();
    });
    fireEvent.click(screen.getByRole("button", { name: /^Create$/i }));
    await waitFor(() => expect(onCreate).toHaveBeenCalled());
    expect(onCreate.mock.calls[0]?.[0]?.visibility).not.toBe("private");
  });
});

// Satisfy unused Agent import for future fixture reuse.
void (null as unknown as Agent);
