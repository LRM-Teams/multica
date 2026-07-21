import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import { useLastSelectedChannelStore } from "@multica/core/channels";
import enCommon from "../../locales/en/common.json";
import enChannels from "../../locales/en/channels.json";
import { ChannelsPage } from "./channels-page";

// #642 — the workspace's immutable system #general channel. These tests
// cover what's reliably assertable through RTL: default-select priority
// (deep-link > remembered > #general > first channel), unpinned-list
// ordering, and the three gated affordances that are plain conditional
// DOM (not a floating Radix menu): the header Settings entry, the header
// member-management popover's per-member remove button, and the mobile
// Drawer's Settings row. The sidebar row's Archive item (inside a
// ContextMenu/DropdownMenu) is covered by direct code inspection in review
// rather than a jsdom floating-menu interaction test.

const apiMock = vi.hoisted(() => {
  const known: Record<string, unknown> = {};
  const proxy = new Proxy(known, {
    get(target, prop) {
      if (typeof prop !== "string") return undefined;
      if (!(prop in target)) target[prop] = vi.fn().mockResolvedValue(undefined);
      return target[prop];
    },
  });
  return { proxy };
});
vi.mock("@multica/core/api", () => ({ api: apiMock.proxy }));

// The system channel is deliberately NOT first in this array — ordering
// must come from `system_key`, never array/list position.
const DEFAULT_CHANNELS = [
  {
    id: "chan-random",
    workspace_id: "ws-1",
    name: "random",
    kind: "group" as const,
    description: null,
    lark_chat_id: null,
    created_by: "user-1",
    created_at: "2026-06-17T09:00:00Z",
    updated_at: "2026-06-17T09:00:00Z",
  },
  {
    id: "chan-general",
    workspace_id: "ws-1",
    name: "general",
    kind: "group" as const,
    description: null,
    lark_chat_id: null,
    created_by: "user-1",
    created_at: "2026-06-17T09:00:00Z",
    updated_at: "2026-06-17T09:00:00Z",
    system_key: "general",
  },
];

// Mutable per-test channel list — a handful of tests need a different
// workspace shape (no system channel at all; an unrecognized system_key)
// without duplicating the whole mock setup into a second file.
const channelsFixture = vi.hoisted(() => ({ current: [] as unknown[] }));

const membersByChannel: Record<string, unknown[]> = {
  "chan-general": [
    {
      member_type: "user",
      member_id: "user-2",
      name: "bob",
      display_name: "Bob",
      avatar_url: null,
    },
  ],
  "chan-random": [
    {
      member_type: "user",
      member_id: "user-2",
      name: "bob",
      display_name: "Bob",
      avatar_url: null,
    },
  ],
  "chan-no-key": [
    {
      member_type: "user",
      member_id: "user-2",
      name: "bob",
      display_name: "Bob",
      avatar_url: null,
    },
  ],
};

vi.mock("@multica/core/channels", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@multica/core/channels")>();
  const options = (queryKey: string[], data: unknown) => ({ queryKey, queryFn: async () => data });
  return {
    ...actual,
    channelsOptions: () => options(["channels"], channelsFixture.current),
    archivedChannelsOptions: () => options(["channels-archived"], []),
    channelMembersOptions: (channelId: string) =>
      options(["channel-members", channelId], membersByChannel[channelId] ?? []),
    channelProjectOptions: () => options(["channel-project"], ""),
    activeChannelTasksOptions: () => options(["channel-tasks"], []),
    channelMessageThreadOptions: () => options(["channel-thread"], { messages: [] }),
    channelMessagesPageOptions: () => ({
      queryKey: ["channel-messages"],
      queryFn: async () => ({ items: [], next_cursor: null }),
      initialPageParam: null,
      getNextPageParam: () => undefined,
    }),
  };
});

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

const mobileViewport = vi.hoisted(() => ({ value: false }));
vi.mock("@multica/ui/hooks/use-mobile", () => ({ useIsMobile: () => mobileViewport.value }));

const replaceSpy = vi.hoisted(() => vi.fn());
vi.mock("../../navigation/context", () => ({
  useNavigation: () => ({
    searchParams: new URLSearchParams(),
    replace: replaceSpy,
    getShareableUrl: (url: string) => url,
  }),
}));

vi.mock("../../editor/content-editor", () => ({
  ContentEditor: () => <div data-testid="content-editor" />,
}));
vi.mock("../../common/project-picker-button", () => ({
  ProjectPickerButton: () => <button type="button">project</button>,
}));
vi.mock("./dm-conversation", () => ({ DmConversation: () => <div data-testid="dm-conversation" /> }));
vi.mock("./channel-files-panel", () => ({ ChannelFilesPanel: () => <div /> }));
vi.mock("./channel-stats-panel", () => ({ ChannelStatsPanel: () => <div /> }));
vi.mock("./channel-message-list", () => ({ ChannelMessageList: () => <div data-testid="message-list" /> }));

vi.mock("./conversation-surface", async (importOriginal) => ({
  ...(await importOriginal<typeof import("./conversation-surface")>()),
  ConversationHeader: ({
    title,
    leading,
    actions,
  }: {
    title?: React.ReactNode;
    leading?: React.ReactNode;
    actions?: React.ReactNode;
  }) => (
    <div data-testid="active-title">
      {leading}
      {title}
      {actions}
    </div>
  ),
}));

function renderPage(channelId?: string) {
  const qc = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0 },
      mutations: { retry: false },
    },
  });
  return render(
    <I18nProvider locale="en" resources={{ en: { common: enCommon, channels: enChannels } }}>
      <QueryClientProvider client={qc}>
        <ChannelsPage channelId={channelId} />
      </QueryClientProvider>
    </I18nProvider>,
  );
}

describe("ChannelsPage — system #general channel (#642)", () => {
  beforeEach(() => {
    replaceSpy.mockReset();
    mobileViewport.value = false;
    window.sessionStorage.clear();
    useLastSelectedChannelStore.setState({ lastSelectedChannelId: null });
    channelsFixture.current = DEFAULT_CHANNELS;
  });

  it("sorts the system channel first in the sidebar regardless of API order", async () => {
    renderPage();
    await screen.findByTestId("message-list");
    const rows = screen
      .getAllByRole("button")
      .filter((el) => el.textContent?.includes("general") || el.textContent?.includes("random"));
    const generalIdx = rows.findIndex((el) => el.textContent?.includes("general"));
    const randomIdx = rows.findIndex((el) => el.textContent?.includes("random"));
    expect(generalIdx).toBeGreaterThanOrEqual(0);
    expect(randomIdx).toBeGreaterThanOrEqual(0);
    expect(generalIdx).toBeLessThan(randomIdx);
  });

  it("defaults to the system channel over the first channel when there's no deep-link/remembered target", async () => {
    renderPage();
    await waitFor(() => {
      expect(screen.getByTestId("active-title")).toHaveTextContent("general");
    });
  });

  it("still lets a deep-link to a non-system channel win over the system default", async () => {
    renderPage("chan-random");
    await waitFor(() => {
      expect(screen.getByTestId("active-title")).toHaveTextContent("random");
    });
  });

  it("still restores a remembered non-system channel over the system default", async () => {
    useLastSelectedChannelStore.setState({ lastSelectedChannelId: "chan-random" });
    renderPage();
    await waitFor(() => {
      expect(screen.getByTestId("active-title")).toHaveTextContent("random");
    });
  });

  it("hides the header Settings entry for the system channel", async () => {
    renderPage("chan-general");
    await waitFor(() => {
      expect(screen.getByTestId("active-title")).toHaveTextContent("general");
    });
    expect(screen.queryByLabelText("Group settings")).toBeNull();
  });

  it("shows the header Settings entry for a normal channel", async () => {
    renderPage("chan-random");
    await waitFor(() => {
      expect(screen.getByTestId("active-title")).toHaveTextContent("random");
    });
    expect(screen.getByLabelText("Group settings")).toBeTruthy();
  });

  it("hides the per-member remove button in the system channel's member panel", async () => {
    renderPage("chan-general");
    await waitFor(() => {
      expect(screen.getByTestId("active-title")).toHaveTextContent("general");
    });
    fireEvent.click(screen.getByLabelText("View members"));
    const panel = await screen.findByText("Bob");
    const row = panel.closest("div")!;
    expect(within(row.parentElement as HTMLElement).queryByLabelText("Remove member")).toBeNull();
    // Read-only roster: no Invite/Members tab switcher for the system channel.
    expect(screen.queryByText("Invite")).toBeNull();
  });

  it("keeps the per-member remove button for a normal channel's member panel", async () => {
    renderPage("chan-random");
    await waitFor(() => {
      expect(screen.getByTestId("active-title")).toHaveTextContent("random");
    });
    fireEvent.click(screen.getByLabelText("Manage members"));
    // Unlike the system channel (which skips straight to the read-only
    // list), a normal channel's panel defaults to the Invite tab.
    fireEvent.click(screen.getByText("Members", { exact: false }));
    await screen.findByText("Bob");
    expect(screen.getByLabelText("Remove member")).toBeTruthy();
  });

  it("hides the mobile Drawer's Settings row for the system channel", async () => {
    mobileViewport.value = true;
    renderPage("chan-general");
    await waitFor(() => {
      expect(screen.getByTestId("active-title")).toHaveTextContent("general");
    });
    fireEvent.click(screen.getByLabelText("More"));
    expect(screen.queryByText("Group settings")).toBeNull();
    // Normal behavior preserved: Members/Stats/Files stay reachable — but
    // #642 follow-up (Parker/Iris): the read-only auto-managed roster
    // must not say "Manage" (that implies add/remove that doesn't exist).
    expect(screen.getByText("View members")).toBeTruthy();
    expect(screen.queryByText("Manage members")).toBeNull();
  });

  it("keeps the mobile Drawer's Settings row for a normal channel", async () => {
    mobileViewport.value = true;
    renderPage("chan-random");
    await waitFor(() => {
      expect(screen.getByTestId("active-title")).toHaveTextContent("random");
    });
    fireEvent.click(screen.getByLabelText("More"));
    expect(screen.getByText("Group settings")).toBeTruthy();
  });

  // #642 follow-up (Parker/Iris served finding): the desktop header's "+"
  // member trigger implies an add affordance that doesn't exist on the
  // read-only auto-managed roster — swap to a neutral view-only trigger,
  // ordinary channels keep the existing add/manage semantics.
  it("desktop header shows a neutral View-members trigger for the system channel, no + icon", async () => {
    renderPage("chan-general");
    await waitFor(() => {
      expect(screen.getByTestId("active-title")).toHaveTextContent("general");
    });
    const trigger = screen.getByLabelText("View members");
    expect(trigger).toBeTruthy();
    expect(screen.queryByLabelText("Manage members")).toBeNull();
    // Iris: the label alone doesn't prove the icon actually swapped —
    // assert the real lucide-users/lucide-plus SVG classes.
    expect(trigger.querySelector("svg.lucide-users")).toBeTruthy();
    expect(trigger.querySelector("svg.lucide-plus")).toBeNull();
  });

  it("desktop header keeps the Manage-members + trigger for a normal channel", async () => {
    renderPage("chan-random");
    await waitFor(() => {
      expect(screen.getByTestId("active-title")).toHaveTextContent("random");
    });
    const trigger = screen.getByLabelText("Manage members");
    expect(trigger).toBeTruthy();
    expect(screen.queryByLabelText("View members")).toBeNull();
    expect(trigger.querySelector("svg.lucide-plus")).toBeTruthy();
    expect(trigger.querySelector("svg.lucide-users")).toBeNull();
  });

  // Iris/Parker review of PR #810's first head: code/design PASS on the
  // surface sweep, but flagged these 3 regressions as missing before code
  // GO. All three lock a #general edge case without touching the two
  // pre-existing auto-select effects' architecture (explicitly out of
  // scope for this PR).
  it("mobile cold-load with no deep-link/remembered stays list-first, not stolen by #general", async () => {
    mobileViewport.value = true;
    renderPage();
    // Give the auto-select effects a chance to run — mobile must never
    // land on a detail view (system channel or otherwise) on a bare load.
    await new Promise((r) => setTimeout(r, 30));
    expect(screen.queryByTestId("active-title")).not.toBeInTheDocument();
    expect(replaceSpy).not.toHaveBeenCalled();
  });

  it("desktop active → resize to mobile → back to list stays list, not re-grabbed by #general (Iris timing fix)", async () => {
    // The merged auto-select effect must sync its previous/current mobile
    // snapshot on EVERY run, not only when there's no active selection —
    // otherwise the snapshot goes stale while a channel is selected, and
    // clearing the selection afterward misreads as a fresh
    // desktop→mobile transition and wrongly re-grabs #general.
    const qc = new QueryClient({
      defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false } },
    });
    const page = (channelId?: string) => (
      <I18nProvider locale="en" resources={{ en: { common: enCommon, channels: enChannels } }}>
        <QueryClientProvider client={qc}>
          <ChannelsPage channelId={channelId} />
        </QueryClientProvider>
      </I18nProvider>
    );
    const { rerender } = render(page("chan-random"));
    await waitFor(() => {
      expect(screen.getByTestId("active-title")).toHaveTextContent("random");
    });

    // Resize to mobile while chan-random is still the active selection.
    mobileViewport.value = true;
    rerender(page("chan-random"));
    await waitFor(() => {
      expect(screen.getByTestId("active-title")).toHaveTextContent("random");
    });

    // "Back to list" — clears the selection client-side, same instance,
    // no remount (mobileBackToList).
    fireEvent.click(screen.getByLabelText("Back"));
    await new Promise((r) => setTimeout(r, 30));
    expect(screen.queryByTestId("active-title")).not.toBeInTheDocument();
  });

  it("desktop still falls back to the first channel when no system channel exists at all", async () => {
    channelsFixture.current = DEFAULT_CHANNELS.filter((c) => c.system_key !== "general");
    renderPage();
    await waitFor(() => {
      expect(screen.getByTestId("active-title")).toHaveTextContent("random");
    });
  });

  it("degrades an unrecognized system_key to a normal, fully-mutable channel", async () => {
    channelsFixture.current = [
      { ...DEFAULT_CHANNELS[0], id: "chan-unknown-key", name: "unknownkey", system_key: "future" },
    ];
    renderPage("chan-unknown-key");
    await waitFor(() => {
      expect(screen.getByTestId("active-title")).toHaveTextContent("unknownkey");
    });
    expect(screen.getByLabelText("Group settings")).toBeTruthy();
  });

  it("degrades an absent system_key to a normal, fully-mutable channel", async () => {
    channelsFixture.current = [{ ...DEFAULT_CHANNELS[0], id: "chan-no-key", name: "nokey" }];
    renderPage("chan-no-key");
    await waitFor(() => {
      expect(screen.getByTestId("active-title")).toHaveTextContent("nokey");
    });
    expect(screen.getByLabelText("Group settings")).toBeTruthy();
    fireEvent.click(screen.getByLabelText("Manage members"));
    fireEvent.click(screen.getByText("Members", { exact: false }));
    await screen.findByText("Bob");
    expect(screen.getByLabelText("Remove member")).toBeTruthy();
  });
});
