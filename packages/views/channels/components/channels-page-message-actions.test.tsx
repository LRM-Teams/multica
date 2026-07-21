import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import type { ChannelMessage } from "@multica/core/types";
import enCommon from "../../locales/en/common.json";
import enChannels from "../../locales/en/channels.json";
import { ChannelsPage } from "./channels-page";

// B3 (#241) — the live edit/delete bug lived in the PARENT wiring: the bubble
// fully supported edit/delete, but ChannelsPage rendered the message list
// without `onEditMessage` / `onDeleteMessage`, so the affordances were dead on
// every real row. The bubble/list unit tests all hand the callbacks in
// directly, so none of them exercised the parent that must supply them. This
// test renders the real ChannelsPage and asserts the message region actually
// receives working edit/delete handlers, and that an edit is a PATCH
// (editChannelMessage) — never a re-send (H5).

// The api client is what the real edit/delete/send mutation hooks call; spy on
// it so we can assert edit == PATCH and never a send.
const apiMock = vi.hoisted(() => {
  const editChannelMessage = vi.fn();
  const deleteChannelMessage = vi.fn();
  const sendChannelMessage = vi.fn();
  const known: Record<string, unknown> = {
    editChannelMessage,
    deleteChannelMessage,
    sendChannelMessage,
  };
  const proxy = new Proxy(known, {
    get(target, prop) {
      if (typeof prop !== "string") return undefined;
      if (!(prop in target)) target[prop] = vi.fn().mockResolvedValue(undefined);
      return target[prop];
    },
  });
  return { proxy, editChannelMessage, deleteChannelMessage, sendChannelMessage };
});
vi.mock("@multica/core/api", () => ({ api: apiMock.proxy }));

// Keep the real mutation hooks (so edit really routes through
// api.editChannelMessage), but stub the query options to fixtures so the page
// resolves a single active channel without any network.
// Mutable per-test channel fixture (#576 tri-state tests need to flip
// `created_by`/`archived_at` between tests without re-declaring the whole
// mock factory).
const channelFixture = vi.hoisted(() => ({
  current: {
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
  },
}));

// Mutable per-test workspace-member fixture, so a test can grant the current
// user an "admin" role without being the channel's creator.
const memberFixture = vi.hoisted(() => ({
  current: [] as Array<{ user_id: string; role: string }>,
}));

vi.mock("@multica/core/channels", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@multica/core/channels")>();
  const options = (queryKey: string[], data: unknown) => ({ queryKey, queryFn: async () => data });
  return {
    ...actual,
    channelsOptions: () => options(["channels"], [channelFixture.current]),
    archivedChannelsOptions: () => options(["channels-archived"], []),
    channelMembersOptions: () => options(["channel-members"], []),
    channelProjectOptions: () => options(["channel-project"], ""),
    activeChannelTasksOptions: () => options(["channel-tasks"], []),
    channelMessageThreadOptions: () => options(["channel-thread"], { messages: [] }),
    channelMessagesPageOptions: () => ({
      queryKey: ["channel-messages"],
      // `messages` is the shape flattenChannelMessagePages reads; an empty
      // array (not the wrong `items` key) keeps the flattened list a valid
      // `[]` so opening a thread resolves threadRoot without crashing.
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
  memberListOptions: () => ({ queryKey: ["members"], queryFn: async () => memberFixture.current }),
  agentListOptions: () => ({ queryKey: ["agents"], queryFn: async () => [] }),
}));

vi.mock("@multica/ui/hooks/use-mobile", () => ({ useIsMobile: () => false }));

vi.mock("../../navigation/context", () => ({
  useNavigation: () => ({
    searchParams: new URLSearchParams(),
    replace: vi.fn(),
    getShareableUrl: (url: string) => url,
  }),
}));

// Expose `plainUrls` so a test can assert the channel composer opts into
// plain-text URLs (#542) — the miss-surface root cause was this prop never
// reaching the web channel composer.
vi.mock("../../editor/content-editor", () => ({
  ContentEditor: (props: { plainUrls?: boolean }) => (
    <div data-testid="content-editor" data-plain-urls={String(!!props.plainUrls)} />
  ),
}));

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

// The message list is mocked so we can capture the exact props ChannelsPage
// hands it — this is the parent→list contract the live bug broke.
const listProps = vi.hoisted(() => ({
  current: null as {
    onEditMessage?: (m: ChannelMessage, content: string) => void;
    onDeleteMessage?: (m: ChannelMessage) => void;
    onOpenThread?: (m: ChannelMessage) => void;
  } | null,
}));
vi.mock("./channel-message-list", () => ({
  ChannelMessageList: (props: {
    onEditMessage?: (m: ChannelMessage, content: string) => void;
    onDeleteMessage?: (m: ChannelMessage) => void;
    onOpenThread?: (m: ChannelMessage) => void;
  }) => {
    listProps.current = props;
    return <div data-testid="message-list" />;
  },
}));

// Render only the composer the page hands ThreadPanel via `editor`, so opening
// a thread exercises the thread composer's `plainUrls` wiring without pulling
// in ThreadPanel's own render dependencies (zero-flaky, per review).
vi.mock("./thread-panel", () => ({
  ThreadPanel: (props: { editor?: React.ReactNode }) => (
    <div data-testid="thread-panel">{props.editor}</div>
  ),
}));

function ownMessage(): ChannelMessage {
  return {
    id: "m-1",
    channel_id: "chan-1",
    workspace_id: "ws-1",
    seq: 1,
    type: "user",
    author_id: "user-1",
    author_name: "Alice",
    content: "Original",
    source: "multica",
    external_message_id: null,
    client_message_id: null,
    created_at: "2026-06-17T09:15:00Z",
  };
}

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
        <ChannelsPage />
      </QueryClientProvider>
    </I18nProvider>,
  );
}

describe("ChannelsPage message edit / delete wiring (#241 B3)", () => {
  beforeEach(() => {
    listProps.current = null;
    apiMock.editChannelMessage.mockReset().mockResolvedValue({
      ...ownMessage(),
      content: "Corrected",
      edited_at: "2026-06-17T09:20:00Z",
    });
    apiMock.deleteChannelMessage.mockReset().mockResolvedValue(undefined);
    apiMock.sendChannelMessage.mockReset().mockResolvedValue(ownMessage());
  });

  // Edit unshipped 2026-07-05 (Frank/Miles): the Edit entry point is hidden in
  // the bubble (canEdit=false) until rebuilt on the unified composer (#258). The
  // onEditMessage wiring is kept dormant, so we assert only the live delete
  // handler here; the dormant edit wiring is covered by the skipped PATCH test.
  it("supplies a working onDeleteMessage handler to the message list", async () => {
    renderPage();
    await screen.findByTestId("message-list");
    await waitFor(() => {
      expect(typeof listProps.current?.onDeleteMessage).toBe("function");
    });
  });

  // Edit unshipped 2026-07-05 (Frank/Miles): the Edit entry point is hidden
  // (canEdit=false) so an edit can't be triggered from the UI. The dormant
  // onEditMessage → editChannelMessage (PATCH) wiring is kept for the
  // composer-parity rebuild (#258); restore this H5 PATCH test when re-enabled.
  it.skip("routes an edit through editChannelMessage (PATCH) and never a send (H5)", async () => {
    renderPage();
    await screen.findByTestId("message-list");
    await waitFor(() => expect(listProps.current?.onEditMessage).toBeTypeOf("function"));

    await act(async () => {
      listProps.current?.onEditMessage?.(ownMessage(), "Corrected");
    });

    await waitFor(() =>
      expect(apiMock.editChannelMessage).toHaveBeenCalledWith("chan-1", "m-1", "Corrected", undefined),
    );
    expect(apiMock.sendChannelMessage).not.toHaveBeenCalled();
  });

  it("routes a delete through deleteChannelMessage (soft-delete)", async () => {
    renderPage();
    await screen.findByTestId("message-list");
    await waitFor(() => expect(listProps.current?.onDeleteMessage).toBeTypeOf("function"));

    await act(async () => {
      listProps.current?.onDeleteMessage?.(ownMessage());
    });

    await waitFor(() => expect(apiMock.deleteChannelMessage).toHaveBeenCalledWith("chan-1", "m-1"));
    expect(apiMock.sendChannelMessage).not.toHaveBeenCalled();
  });

  // #542 — both channel composers (main + thread) must opt into plain-text
  // URLs so a typed URL isn't auto-linkified in the input. Per-call-site
  // regression guard: the miss-surface bug was `plainUrls` reaching one
  // surface but not another, which exact-head review alone can't keep out.
  it("channel main + thread composers each pass plainUrls (#542)", async () => {
    renderPage();
    await screen.findByTestId("message-list");

    const main = await screen.findByTestId("content-editor");
    expect(main.getAttribute("data-plain-urls")).toBe("true");

    // Open a thread → the thread composer (channels-page.tsx:2174) renders via
    // ThreadPanel's `editor` prop, a distinct call site.
    await waitFor(() => expect(listProps.current?.onOpenThread).toBeTypeOf("function"));
    await act(async () => {
      listProps.current?.onOpenThread?.(ownMessage());
    });
    await screen.findByTestId("thread-panel");

    const composers = screen.getAllByTestId("content-editor");
    expect(composers).toHaveLength(2);
    for (const composer of composers) {
      expect(composer.getAttribute("data-plain-urls")).toBe("true");
    }
  });
});

describe("ChannelsPage — project picker relocated to group settings (#576)", () => {
  beforeEach(() => {
    listProps.current = null;
    channelFixture.current = {
      id: "chan-1",
      workspace_id: "ws-1",
      name: "general",
      kind: "group" as const,
      description: null,
      lark_chat_id: null,
      created_by: "user-1",
      created_at: "2026-06-17T09:00:00Z",
      updated_at: "2026-06-17T09:00:00Z",
      archived_at: null,
    };
    memberFixture.current = [];
  });

  it("does not render the project picker in the composer", async () => {
    renderPage();
    await screen.findByTestId("message-list");
    // The mocked ProjectPickerButton renders as a plain button labeled
    // "project" — it must not appear until the settings surface is opened.
    expect(screen.queryByRole("button", { name: "project" })).toBeNull();
  });

  it("reveals the project picker inside the header's Group settings popover", async () => {
    renderPage();
    await screen.findByTestId("message-list");
    fireEvent.click(screen.getByRole("button", { name: "Group settings" }));
    expect(await screen.findByRole("button", { name: "project" })).toBeTruthy();
  });

  // #576 blocker (Iris): the picker must be gated by the same permission
  // canArchive() already uses, plus the archived state — a plain member or
  // anyone viewing an archived channel could otherwise fire a mutation that
  // 403s server-side. Tri-state: creator/admin editable, plain member
  // disabled, archived disabled even for the creator.
  it("enables the project picker for the channel's creator", async () => {
    // Fixture default: chan-1.created_by === "user-1" === the signed-in user.
    renderPage();
    await screen.findByTestId("message-list");
    fireEvent.click(screen.getByRole("button", { name: "Group settings" }));
    expect(await screen.findByRole("button", { name: "project" })).toBeEnabled();
  });

  it("enables the project picker for a workspace admin who didn't create the channel", async () => {
    channelFixture.current = { ...channelFixture.current, created_by: "user-2" };
    memberFixture.current = [{ user_id: "user-1", role: "admin" }];
    renderPage();
    await screen.findByTestId("message-list");
    fireEvent.click(screen.getByRole("button", { name: "Group settings" }));
    expect(await screen.findByRole("button", { name: "project" })).toBeEnabled();
  });

  it("disables the project picker for a plain member", async () => {
    channelFixture.current = { ...channelFixture.current, created_by: "user-2" };
    memberFixture.current = [{ user_id: "user-1", role: "member" }];
    renderPage();
    await screen.findByTestId("message-list");
    fireEvent.click(screen.getByRole("button", { name: "Group settings" }));
    expect(await screen.findByRole("button", { name: "project" })).toBeDisabled();
  });

  it("disables the project picker for an archived channel, even for its creator", async () => {
    channelFixture.current = { ...channelFixture.current, archived_at: "2026-07-01T00:00:00Z" };
    renderPage();
    await screen.findByTestId("message-list");
    fireEvent.click(screen.getByRole("button", { name: "Group settings" }));
    expect(await screen.findByRole("button", { name: "project" })).toBeDisabled();
  });
});

describe("ChannelsPage — Group Settings shares the exclusive thread/agent slot (#645)", () => {
  beforeEach(() => {
    listProps.current = null;
    channelFixture.current = {
      id: "chan-1",
      workspace_id: "ws-1",
      name: "general",
      kind: "group" as const,
      description: null,
      lark_chat_id: null,
      created_by: "user-1",
      created_at: "2026-06-17T09:00:00Z",
      updated_at: "2026-06-17T09:00:00Z",
      archived_at: null,
    };
    memberFixture.current = [];
  });

  // Settings and the Thread panel both route through the same
  // setOpenThreadRoot/setChannelSettingsOpen exclusion logic that gates the
  // Agent panel too (handleOpenAgentPanel is symmetric with handleOpenThread
  // — both clear channelSettingsOpen) — this exercises the real shared code
  // path, not a duplicate per-panel test.
  it("opening a thread closes an already-open Group settings panel", async () => {
    renderPage();
    await screen.findByTestId("message-list");
    fireEvent.click(screen.getByRole("button", { name: "Group settings" }));
    expect(await screen.findByRole("button", { name: "project" })).toBeTruthy();

    await waitFor(() => expect(listProps.current?.onOpenThread).toBeTypeOf("function"));
    await act(async () => {
      listProps.current?.onOpenThread?.(ownMessage());
    });
    await screen.findByTestId("thread-panel");
    expect(screen.queryByRole("button", { name: "project" })).toBeNull();
  });

  it("opening Group settings closes an already-open thread", async () => {
    renderPage();
    await screen.findByTestId("message-list");
    await waitFor(() => expect(listProps.current?.onOpenThread).toBeTypeOf("function"));
    await act(async () => {
      listProps.current?.onOpenThread?.(ownMessage());
    });
    await screen.findByTestId("thread-panel");

    fireEvent.click(screen.getByRole("button", { name: "Group settings" }));
    expect(await screen.findByRole("button", { name: "project" })).toBeTruthy();
    expect(screen.queryByTestId("thread-panel")).toBeNull();
  });

  it("clicking the Group settings toggle again closes it", async () => {
    renderPage();
    await screen.findByTestId("message-list");
    const toggle = screen.getByRole("button", { name: "Group settings" });
    fireEvent.click(toggle);
    expect(await screen.findByRole("button", { name: "project" })).toBeTruthy();
    fireEvent.click(toggle);
    await waitFor(() => {
      expect(screen.queryByRole("button", { name: "project" })).toBeNull();
    });
  });
});
