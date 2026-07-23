import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enChannels from "../../locales/en/channels.json";
import { ChannelsPage } from "./channels-page";

// #576 — create-group dialog project field. The channels-page scope shipped
// the group-settings Project section (#800, ChannelProjectSettingsPanel) for
// binding an EXISTING channel to a project; this covers the remaining piece —
// picking a project AT CREATION time in the same inline create-channel
// popover (channels-page.tsx's sidebar "+" Popover), reusing the identical
// ProjectPickerButton + PropRow pattern instead of a bespoke picker. Leaving
// the field untouched must behave exactly like the pre-existing create flow
// (no `project_id` on the wire).

const apiMock = vi.hoisted(() => {
  const createChannel = vi.fn().mockResolvedValue({
    id: "chan-new",
    workspace_id: "ws-1",
    name: "New Group",
    kind: "group" as const,
    description: null,
    lark_chat_id: null,
    created_by: "user-1",
    created_at: "2026-07-21T09:00:00Z",
    updated_at: "2026-07-21T09:00:00Z",
  });
  const known: Record<string, unknown> = { createChannel };
  const proxy = new Proxy(known, {
    get(target, prop) {
      if (typeof prop !== "string") return undefined;
      if (!(prop in target)) target[prop] = vi.fn().mockResolvedValue(undefined);
      return target[prop];
    },
  });
  return { proxy, createChannel };
});
vi.mock("@multica/core/api", () => ({ api: apiMock.proxy }));

const CHANNELS = [
  {
    id: "chan-1",
    workspace_id: "ws-1",
    name: "general",
    kind: "group" as const,
    description: null,
    lark_chat_id: null,
    created_by: "user-1",
    created_at: "2026-06-17T09:00:00Z",
    updated_at: "2026-06-17T09:00:00Z",
  },
];

vi.mock("@multica/core/channels", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@multica/core/channels")>();
  const options = (queryKey: string[], data: unknown) => ({ queryKey, queryFn: async () => data });
  return {
    ...actual,
    channelsOptions: () => options(["channels"], CHANNELS),
    archivedChannelsOptions: () => options(["channels-archived"], []),
    channelMembersOptions: () => options(["channel-members"], []),
    channelProjectOptions: () => options(["channel-project"], ""),
    activeChannelTasksOptions: () => options(["channel-tasks"], []),
    channelMessageThreadOptions: () => options(["channel-thread"], { messages: [] }),
    channelMessagesPageOptions: () => ({
      queryKey: ["channel-messages"],
      queryFn: async () => ({ messages: [], next_cursor: null }),
      initialPageParam: null,
      getNextPageParam: () => undefined,
    }),
  };
});

const PROJECTS = [
  { id: "proj-1", title: "Apollo" },
  { id: "proj-2", title: "Zeus" },
];
vi.mock("@multica/core/projects/queries", () => ({
  projectListOptions: () => ({ queryKey: ["projects"], queryFn: async () => PROJECTS }),
}));

vi.mock("@multica/core/auth", () => ({
  useAuthStore: (selector: (s: { user: { id: string; name: string } }) => unknown) =>
    selector({ user: { id: "user-1", name: "Alice" } }),
}));

vi.mock("@multica/core/hooks", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/hooks")>()),
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@multica/core/paths", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/paths")>()),
  useWorkspacePaths: () => ({
    channels: () => "/w/test/channels",
    channelDetail: (id: string) => `/w/test/channels/${id}`,
  }),
}));

vi.mock("@multica/core/realtime", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/realtime")>()),
  useWSEvent: vi.fn(),
}));

vi.mock("@multica/core/hooks/use-file-upload", () => ({
  useFileUpload: () => ({ uploadWithToast: vi.fn() }),
}));

vi.mock("@multica/core/dm", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/dm")>()),
  dmListOptions: () => ({ queryKey: ["dm-list"], queryFn: async () => [] }),
}));

vi.mock("@multica/core/workspace/queries", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/workspace/queries")>()),
  memberListOptions: () => ({ queryKey: ["members"], queryFn: async () => [] }),
  agentListOptions: () => ({ queryKey: ["agents"], queryFn: async () => [] }),
}));

vi.mock("@multica/ui/hooks/use-mobile", () => ({
  useIsMobile: () => false,
  // #568 added container-driven header compaction after this branch was
  // created. This test is about the create popover, so keep the page in its
  // wide/direct layout while preserving the hook's tuple contract.
  useContainerNarrowerThan: () => [false, vi.fn()] as const,
}));

vi.mock("../../navigation/context", () => ({
  useNavigation: () => ({
    searchParams: new URLSearchParams(),
    replace: vi.fn(),
    getShareableUrl: (url: string) => url,
  }),
}));

vi.mock("../../editor/content-editor", () => ({
  ContentEditor: () => <div data-testid="content-editor" />,
}));

// Functional stub (unlike the other channels-page test files' dumb button):
// clicking toggles between "unset" and a fixed project id so tests can drive
// selection through `onChange`, and the rendered label echoes `value` so a
// test can assert what the create-popover currently holds.
vi.mock("../../common/project-picker-button", () => ({
  ProjectPickerButton: (props: { value: string | null; onChange: (id: string | null) => void }) => (
    <button
      type="button"
      aria-label="Project: pick"
      onClick={() => props.onChange(props.value === "proj-1" ? null : "proj-1")}
    >
      picker:{props.value ?? "none"}
    </button>
  ),
}));

vi.mock("./dm-conversation", () => ({ DmConversation: () => <div data-testid="dm-conversation" /> }));
vi.mock("./channel-files-panel", () => ({ ChannelFilesPanel: () => <div /> }));
vi.mock("./channel-stats-panel", () => ({ ChannelStatsPanel: () => <div /> }));
vi.mock("./channel-message-list", () => ({ ChannelMessageList: () => <div data-testid="message-list" /> }));

function renderPage() {
  const qc = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0 },
      mutations: { retry: false },
    },
  });
  return render(
    <I18nProvider locale="en" resources={{ en: { common: enCommon, channels: enChannels } }}>
      <QueryClientProvider client={qc}>
        <ChannelsPage channelId="chan-1" />
      </QueryClientProvider>
    </I18nProvider>,
  );
}

function openCreatePopover() {
  // The sidebar "+" trigger and the popover's submit button share the same
  // accessible name ("Create channel") once the popover is open, so grab the
  // trigger while it's still the only match.
  fireEvent.click(screen.getByRole("button", { name: "Create channel" }));
}

describe("ChannelsPage create-group popover — optional project field (#576)", () => {
  beforeEach(() => {
    apiMock.createChannel.mockClear();
  });

  it("renders an optional project field in the create-group popover, defaulted to unset", async () => {
    renderPage();
    openCreatePopover();

    await waitFor(() => {
      expect(screen.getByPlaceholderText("Channel name")).toBeInTheDocument();
    });
    // Reuses the same "Project" / "No project" copy as the group-settings panel.
    expect(screen.getByText("Project")).toBeInTheDocument();
    expect(screen.getByText("No project")).toBeInTheDocument();
    expect(screen.getByText("picker:none")).toBeInTheDocument();
  });

  it("includes the selected project id in the create submission", async () => {
    renderPage();
    openCreatePopover();

    const nameInput = await screen.findByPlaceholderText("Channel name");
    fireEvent.change(nameInput, { target: { value: "New Group" } });

    fireEvent.click(screen.getByLabelText("Project: pick"));
    await waitFor(() => expect(screen.getByText("Apollo")).toBeInTheDocument());

    fireEvent.keyDown(nameInput, { key: "Enter" });

    await waitFor(() => expect(apiMock.createChannel).toHaveBeenCalledTimes(1));
    expect(apiMock.createChannel).toHaveBeenCalledWith(
      expect.objectContaining({ name: "New Group", project_id: "proj-1" }),
    );
  });

  it("omits project_id when no project is picked — existing create flow unaffected", async () => {
    renderPage();
    openCreatePopover();

    const nameInput = await screen.findByPlaceholderText("Channel name");
    fireEvent.change(nameInput, { target: { value: "Plain Group" } });
    fireEvent.keyDown(nameInput, { key: "Enter" });

    await waitFor(() => expect(apiMock.createChannel).toHaveBeenCalledTimes(1));
    const payload = apiMock.createChannel.mock.calls[0]?.[0] as { project_id?: string | null };
    expect(payload).toMatchObject({ name: "Plain Group", lark_chat_id: undefined });
    // Not just falsy — genuinely absent from the wire payload (JSON.stringify
    // drops an `undefined` value), matching the pre-#576 create request shape.
    expect(payload.project_id).toBeUndefined();
  });

  it("LRM-399: create-group popover has no auto-Beckham / group-manager affordance", async () => {
    renderPage();
    openCreatePopover();

    await waitFor(() => {
      expect(screen.getByPlaceholderText("Channel name")).toBeInTheDocument();
    });
    const body = document.body.textContent ?? "";
    expect(body).not.toMatch(/自动带上|贝克汉姆|Beckham|group.?manager|auto.?provision/i);
    fireEvent.change(screen.getByPlaceholderText("Channel name"), {
      target: { value: "Solo Group" },
    });
    fireEvent.click(screen.getAllByRole("button", { name: "Create channel" }).at(-1)!);
    await waitFor(() => expect(apiMock.createChannel).toHaveBeenCalled());
    const createPayload = apiMock.createChannel.mock.calls[0]?.[0] as Record<string, unknown>;
    expect(createPayload).not.toHaveProperty("group_manager");
    expect(createPayload).not.toHaveProperty("provision_group_manager");
    expect(createPayload).not.toHaveProperty("with_beckham");
  });
});
