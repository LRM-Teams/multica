import { fireEvent, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enChannels from "../../locales/en/channels.json";
import { ChannelsPage } from "./channels-page";

// #568 follow-up — confirmed live bug: at a genuine 768px viewport (tablet),
// the two-pane desktop layout's detail header doesn't switch to any
// condensed pattern (MOBILE_BREAKPOINT is 768, and `useIsMobile` uses a
// strict `<`, so 768 itself stays on the desktop icon-row branch), but the
// detail pane at that width doesn't have room for the full action-icon row
// (member cluster + invite + search/share/stats/files/settings — all
// `shrink-0`, so they never yield space to the truncating title). The row
// overflowed past the viewport with 群设置 (Group Settings) physically
// unreachable — no scroll, no affordance to get to it (confirmed live via
// agent-browser: header clientWidth 224–264px vs a ~360–400px action row at
// 768px, actions rendered past x=768).
//
// Fix: `useIsNarrowerThan` (packages/ui/hooks/use-mobile.ts) gives the
// header its own HEADER_ACTIONS_COMPACT_BREAKPOINT (1200, empirically
// measured — the icon row doesn't reliably fit until well past 768, even at
// 1024/1100), independent of MOBILE_BREAKPOINT. Below it, the actions
// collapse into the same single "⋯" trigger + bottom Drawer the true-mobile
// path already uses (reused as-is — same menu, same settings panel), rather
// than inventing a second overflow UI. This mock drives `useIsMobile` and
// `useIsNarrowerThan` independently so a test can reproduce exactly the
// "768: not mobile, but the action row doesn't fit" condition without a real
// layout engine.
const responsive = vi.hoisted(() => ({ isMobile: false, headerActionsCompact: false }));
vi.mock("@multica/ui/hooks/use-mobile", () => ({
  useIsMobile: () => responsive.isMobile,
  useIsNarrowerThan: () => responsive.headerActionsCompact,
}));

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

const channelFixture = {
  id: "chan-1",
  workspace_id: "ws-1",
  name: "general",
  kind: "group" as const,
  description: null,
  lark_chat_id: null,
  created_by: "user-1",
  created_at: "2026-06-17T09:00:00Z",
  updated_at: "2026-06-17T09:00:00Z",
  archived_at: null as string | null,
};

vi.mock("@multica/core/channels", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@multica/core/channels")>();
  const options = (queryKey: string[], data: unknown) => ({ queryKey, queryFn: async () => data });
  return {
    ...actual,
    channelsOptions: () => options(["channels"], [channelFixture]),
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

// The real settings surface — asserting THIS renders (not just that a click
// handler fired) is what proves the compact "⋯" path actually reaches Group
// Settings, matching how #576's own tests prove reachability.
vi.mock("../../common/project-picker-button", () => ({
  ProjectPickerButton: (props: { disabled?: boolean }) => (
    <button type="button" disabled={props.disabled}>
      project
    </button>
  ),
}));

vi.mock("./dm-conversation", () => ({ DmConversation: () => <div /> }));
vi.mock("./channel-files-panel", () => ({ ChannelFilesPanel: () => <div /> }));
vi.mock("./channel-stats-panel", () => ({ ChannelStatsPanel: () => <div /> }));
vi.mock("./channel-message-list", () => ({
  ChannelMessageList: () => <div data-testid="message-list" />,
}));
vi.mock("./thread-panel", () => ({
  ThreadPanel: (props: { editor?: React.ReactNode }) => (
    <div data-testid="thread-panel">{props.editor}</div>
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

describe("ChannelsPage header actions — tablet overflow (#568)", () => {
  beforeEach(() => {
    responsive.isMobile = false;
    responsive.headerActionsCompact = false;
  });

  // The confirmed bug: not mobile, but too narrow for the full icon row.
  it("collapses the action row into the same '⋯' trigger the mobile path uses, and Group Settings is reachable through it", async () => {
    responsive.isMobile = false;
    responsive.headerActionsCompact = true;
    renderPage();
    await screen.findByTestId("message-list");

    // The raw per-icon "Group settings" button must NOT be in the desktop
    // icon row at all — that's the overflowing row this bug is about; if
    // it's present but merely visually clipped, that's still unreachable and
    // still the bug, so this asserts it doesn't render at all (only the
    // compact trigger does).
    expect(screen.queryByRole("button", { name: "Group settings" })).toBeNull();

    const trigger = screen.getByTestId("channel-header-actions-trigger");
    expect(trigger).toBeInTheDocument();
    fireEvent.click(trigger);

    // The bottom Drawer's action menu — same one true-mobile already uses —
    // lists "Group settings" as a row; picking it must open the real panel,
    // not just close the drawer.
    const settingsRow = await screen.findByRole("button", { name: "Group settings" });
    fireEvent.click(settingsRow);

    expect(await screen.findByRole("button", { name: "project" })).toBeInTheDocument();
  });

  // Desktop regression guard: wide enough that neither breakpoint applies —
  // the full icon row (and no compact trigger) must render exactly as
  // before this fix.
  it("still shows the full icon row directly when there's room (desktop, no regression)", async () => {
    responsive.isMobile = false;
    responsive.headerActionsCompact = false;
    renderPage();
    await screen.findByTestId("message-list");

    expect(screen.getByRole("button", { name: "Group settings" })).toBeInTheDocument();
    expect(screen.queryByTestId("channel-header-actions-trigger")).toBeNull();
  });

  // True-mobile regression guard: the existing <768 single-column pattern
  // (which also implies isHeaderActionsCompact, since 768 < 1200) keeps using
  // the same collapsed trigger + drawer it always has.
  it("keeps the existing mobile collapsed trigger untouched (<768, no regression)", async () => {
    responsive.isMobile = true;
    responsive.headerActionsCompact = true;
    renderPage(channelFixture.id);
    await screen.findByTestId("message-list");

    expect(screen.getByTestId("channel-header-actions-trigger")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Group settings" })).toBeNull();
  });
});
