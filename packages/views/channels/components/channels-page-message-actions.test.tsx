import { fireEvent, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import type { ChannelMessage } from "@multica/core/types";
import enCommon from "../../locales/en/common.json";
import enChannels from "../../locales/en/channels.json";
import { ChannelsPage } from "./channels-page";

// B3 (#241) — the live edit bug lived in the PARENT wiring: the bubble fully
// supported edit, but ChannelsPage rendered the message list without
// `onEditMessage`, so the affordance was dead on every real row. The bubble/list
// unit tests hand the callback in directly, so none of them exercised the
// parent that must supply it. This test renders the real ChannelsPage and
// asserts that an edit is a PATCH (editChannelMessage) — never a re-send (H5).

// The api client is what the real edit/send mutation hooks call; spy on it so
// we can assert edit == PATCH and never a send.
const apiMock = vi.hoisted(() => {
  const editChannelMessage = vi.fn();
  const sendChannelMessage = vi.fn();
  const known: Record<string, unknown> = {
    editChannelMessage,
    sendChannelMessage,
  };
  const proxy = new Proxy(known, {
    get(target, prop) {
      if (typeof prop !== "string") return undefined;
      if (!(prop in target)) target[prop] = vi.fn().mockResolvedValue(undefined);
      return target[prop];
    },
  });
  return { proxy, editChannelMessage, sendChannelMessage };
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

vi.mock("@multica/core/auth", async () => {
  const { authMock } = await import("./__fixtures/channels-page-mocks");
  return authMock();
});

vi.mock("@multica/core/hooks", async (importOriginal) => {
  const { hooksMock } = await import("./__fixtures/channels-page-mocks");
  return hooksMock(importOriginal);
});

vi.mock("@multica/core/paths", async (importOriginal) => {
  const { pathsMock } = await import("./__fixtures/channels-page-mocks");
  return pathsMock(importOriginal);
});

vi.mock("@multica/core/realtime", async (importOriginal) => {
  const { realtimeMock } = await import("./__fixtures/channels-page-mocks");
  return realtimeMock(importOriginal);
});

vi.mock("@multica/core/hooks/use-file-upload", async () => {
  const { fileUploadMock } = await import("./__fixtures/channels-page-mocks");
  return fileUploadMock();
});

vi.mock("@multica/core/dm", async (importOriginal) => {
  const { dmMock } = await import("./__fixtures/channels-page-mocks");
  return dmMock(importOriginal);
});

vi.mock("@multica/core/conversations", async (importOriginal) => {
  const { conversationsMock } = await import("./__fixtures/channels-page-mocks");
  return conversationsMock(importOriginal, () => [channelFixture.current]);
});

vi.mock("@multica/core/workspace/queries", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/workspace/queries")>()),
  memberListOptions: () => ({ queryKey: ["members"], queryFn: async () => memberFixture.current }),
  agentListOptions: () => ({ queryKey: ["agents"], queryFn: async () => [] }),
}));

// #568 — `useContainerNarrowerThan` (ResizeObserver-driven) isn't relevant
// to what this file tests; keep it a no-op ("plenty of room", direct row)
// so pre-existing desktop-direct-row assumptions here are unaffected.
// jsdom's default `getBoundingClientRect` is 0x0 for every element and
// `ResizeObserver` isn't implemented at all, so leaving the real hook
// running here would default to "compact" instead.
vi.mock("@multica/ui/hooks/use-mobile", () => ({
  useIsMobile: () => false,
  useContainerNarrowerThan: () => [false, () => {}] as const,
}));

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
vi.mock("../../editor/lazy-content-editor", () => ({
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

// The message list is mocked so we can capture the exact props ChannelsPage
// hands it — this is the parent→list contract the live bug broke.
const listProps = vi.hoisted(() => ({
  current: null as {
    onEditMessage?: (m: ChannelMessage, content: string) => void;
    onOpenThread?: (m: ChannelMessage) => void;
  } | null,
}));
vi.mock("./channel-message-list", () => ({
  ChannelMessageList: (props: {
    onEditMessage?: (m: ChannelMessage, content: string) => void;
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

  it("reveals the project picker inside Channel details → Settings", async () => {
    renderPage();
    await screen.findByTestId("message-list");
    fireEvent.click(screen.getByRole("button", { name: "Open channel details" }));
    fireEvent.click(await screen.findByTestId("channel-details-settings"));
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
    fireEvent.click(screen.getByRole("button", { name: "Open channel details" }));
    fireEvent.click(await screen.findByTestId("channel-details-settings"));
    expect(await screen.findByRole("button", { name: "project" })).toBeEnabled();
  });

  it("enables the project picker for a workspace admin who didn't create the channel", async () => {
    channelFixture.current = { ...channelFixture.current, created_by: "user-2" };
    memberFixture.current = [{ user_id: "user-1", role: "admin" }];
    renderPage();
    await screen.findByTestId("message-list");
    fireEvent.click(screen.getByRole("button", { name: "Open channel details" }));
    fireEvent.click(await screen.findByTestId("channel-details-settings"));
    expect(await screen.findByRole("button", { name: "project" })).toBeEnabled();
  });

  it("disables the project picker for a plain member", async () => {
    channelFixture.current = { ...channelFixture.current, created_by: "user-2" };
    memberFixture.current = [{ user_id: "user-1", role: "member" }];
    renderPage();
    await screen.findByTestId("message-list");
    fireEvent.click(screen.getByRole("button", { name: "Open channel details" }));
    fireEvent.click(await screen.findByTestId("channel-details-settings"));
    expect(await screen.findByRole("button", { name: "project" })).toBeDisabled();
  });

  it("disables the project picker for an archived channel, even for its creator", async () => {
    channelFixture.current = { ...channelFixture.current, archived_at: "2026-07-01T00:00:00Z" };
    renderPage();
    await screen.findByTestId("message-list");
    fireEvent.click(screen.getByRole("button", { name: "Open channel details" }));
    fireEvent.click(await screen.findByTestId("channel-details-settings"));
    expect(await screen.findByRole("button", { name: "project" })).toBeDisabled();
  });
});

describe("ChannelsPage — Channel details shares the exclusive thread/agent slot (#645)", () => {
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

  // Details and the Thread panel both route through the same exclusive
  // sidePanel union that gates the Agent panel too.



  it("keeps conversation full-width when no side dock is open (LRM-400)", async () => {
    renderPage();
    await screen.findByTestId("message-list");
    expect(screen.getByTestId("channel-conversation-column")).toBeInTheDocument();
    expect(screen.queryByTestId("channel-details-side-slot")).toBeNull();
    expect(screen.queryByTestId("thread-side-slot")).toBeNull();
    expect(screen.queryByTestId("agent-side-slot")).toBeNull();
    expect(screen.queryByTestId("channel-detail-side-resize")).toBeNull();

    fireEvent.click(screen.getByRole("button", { name: "Open channel details" }));
    expect(await screen.findByTestId("channel-details-side-slot")).toBeInTheDocument();
    expect(screen.getByTestId("channel-detail-side-resize")).toBeInTheDocument();
    // Conversation column stays mounted beside the dock (no remount blank shell).
    expect(screen.getByTestId("channel-conversation-column")).toBeInTheDocument();
  });
});
