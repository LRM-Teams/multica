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

// This suite renders the FULL ChannelsPage with the real react-virtuoso message
// list (intentionally unmocked, to exercise real sidebar/selection wiring). That
// render is heavy in jsdom, so under full-suite PARALLEL CI load a single test's
// `findByTestId("message-list")` can exceed vitest's 5s default and flake — this
// has repeatedly reddened UNRELATED PRs (e.g. #1243, #1232, whose diffs don't
// touch views). The tests are correct and pass in isolation; give the render
// timeout headroom under load rather than mask a real failure.
vi.setConfig({ testTimeout: 20000 });

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
// #568 — `useContainerNarrowerThan` (ResizeObserver-driven) isn't relevant
// to what this file tests; keep it a no-op ("plenty of room", direct row)
// so pre-existing desktop-direct-row assumptions here are unaffected.
// jsdom's default `getBoundingClientRect` is 0x0 for every element and
// `ResizeObserver` isn't implemented at all, so leaving the real hook
// running here would default to "compact" instead.
vi.mock("@multica/ui/hooks/use-mobile", () => ({
  useIsMobile: () => mobileViewport.value,
  useContainerNarrowerThan: () => [false, () => {}] as const,
}));

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

  it("hides the Settings entry for the system channel in Channel details", async () => {
    renderPage("chan-general");
    await waitFor(() => {
      expect(screen.getByTestId("active-title")).toHaveTextContent("general");
    });
    fireEvent.click(screen.getByLabelText("Open channel details"));
    expect(await screen.findByTestId("channel-details-home")).toBeTruthy();
    expect(screen.queryByTestId("channel-details-settings")).toBeNull();
  });

  it("shows the Settings entry for a normal channel in Channel details", async () => {
    renderPage("chan-random");
    await waitFor(() => {
      expect(screen.getByTestId("active-title")).toHaveTextContent("random");
    });
    fireEvent.click(screen.getByLabelText("Open channel details"));
    expect(await screen.findByTestId("channel-details-settings")).toBeTruthy();
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

  it("drops the legacy per-member Remove for a normal group's member panel (owner-only menu now; #801)", async () => {
    renderPage("chan-random");
    await waitFor(() => {
      expect(screen.getByTestId("active-title")).toHaveTextContent("random");
    });
    // Presence stack opens the Members (browse) tab directly.
    fireEvent.click(screen.getByLabelText("View members"));
    await screen.findByText("Bob");
    // Ordinary groups route removal through the owner-only management menu (mock
    // to #801). The ungated legacy per-member Remove must NOT remain reachable —
    // it let a non-channel-owner (or the owner's own row) remove members outside
    // the gate. This viewer's channel role is fail-closed to member, so no ⋯ menu.
    expect(screen.queryByLabelText("Remove member")).toBeNull();
  });

  it("hides the mobile details Settings row for the system channel", async () => {
    mobileViewport.value = true;
    renderPage("chan-general");
    await waitFor(() => {
      expect(screen.getByTestId("active-title")).toHaveTextContent("general");
    });
    fireEvent.click(screen.getByLabelText("More"));
    expect(await screen.findByTestId("channel-details-home")).toBeTruthy();
    expect(screen.queryByTestId("channel-details-settings")).toBeNull();
    // Members stay reachable from the Slack members row.
    expect(screen.getByTestId("channel-details-members-row")).toBeTruthy();
  });

  it("keeps the mobile details Settings row for a normal channel", async () => {
    mobileViewport.value = true;
    renderPage("chan-random");
    await waitFor(() => {
      expect(screen.getByTestId("active-title")).toHaveTextContent("random");
    });
    fireEvent.click(screen.getByLabelText("More"));
    expect(await screen.findByTestId("channel-details-settings")).toBeTruthy();
  });

  // Slack-style header: faces + count open View-members (browse). System
  // #general has no Invite text button (read-only auto-managed roster).
  it("desktop header shows View-members presence trigger for the system channel, no Invite / +", async () => {
    renderPage("chan-general");
    await waitFor(() => {
      expect(screen.getByTestId("active-title")).toHaveTextContent("general");
    });
    const trigger = screen.getByLabelText("View members");
    expect(trigger).toBeTruthy();
    expect(screen.queryByLabelText("Invite people")).toBeNull();
    expect(screen.queryByLabelText("Manage members")).toBeNull();
    // Presence trigger itself must not carry a hollow "+" affordance.
    expect(trigger.querySelector("svg.lucide-plus")).toBeNull();
  });

  // LRM-447 — Invite left the wide header rail (Members · Search · Stop only).
  // Normal channels still reach Invite via Members dialog / overflow menu.
  it("desktop header keeps View-members without Invite on the action rail (LRM-447)", async () => {
    renderPage("chan-random");
    await waitFor(() => {
      expect(screen.getByTestId("active-title")).toHaveTextContent("random");
    });
    expect(screen.getByLabelText("View members")).toBeTruthy();
    expect(screen.queryByLabelText("Invite people")).toBeNull();
    expect(screen.queryByLabelText("Manage members")).toBeNull();
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
    expect(screen.getByLabelText("Open channel details")).toBeTruthy();
  });

  it("degrades an absent system_key to a normal, fully-mutable channel", async () => {
    channelsFixture.current = [{ ...DEFAULT_CHANNELS[0], id: "chan-no-key", name: "nokey" }];
    renderPage("chan-no-key");
    await waitFor(() => {
      expect(screen.getByTestId("active-title")).toHaveTextContent("nokey");
    });
    fireEvent.click(screen.getByLabelText("Open channel details"));
    expect(await screen.findByTestId("channel-details-settings")).toBeTruthy();
    fireEvent.click(screen.getByLabelText("View members"));
    await screen.findByText("Bob");
    // "Mutable" is evidenced by the Settings surface (asserted above); the system
    // channel hides it. The legacy per-member Remove is gone for ordinary groups
    // (owner-only menu; #801), so it must NOT be present here either.
    expect(screen.queryByLabelText("Remove member")).toBeNull();
  });
});
