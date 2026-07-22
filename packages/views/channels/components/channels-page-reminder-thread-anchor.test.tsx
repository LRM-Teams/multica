import { render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import { useLastSelectedChannelStore } from "@multica/core/channels";
import enCommon from "../../locales/en/common.json";
import enChannels from "../../locales/en/channels.json";
import { ChannelsPage } from "./channels-page";

// #656 Reminder anchor `?thread=<root>&message=<reply>` deep-link: it must
// really open ThreadPanel and highlight the reply inside it — a plain
// `?message=` main-timeline highlight (what channels-page-routing.test.tsx
// already covers) is NOT the same thing and doesn't satisfy this.

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

const THREAD_ROOT_ID = "root-msg-1";
const THREAD_REPLY_ID = "reply-msg-1";

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
    channelMessageThreadOptions: (_channelId: string, messageId: string) =>
      options(["channel-thread", messageId], {
        messages:
          messageId === THREAD_ROOT_ID
            ? [
                {
                  id: THREAD_ROOT_ID,
                  channel_id: "chan-1",
                  workspace_id: "ws-1",
                  seq: 1,
                  type: "user",
                  author_id: "user-2",
                  author_name: "Bob",
                  content: "root",
                  source: "multica",
                  external_message_id: null,
                  client_message_id: null,
                  created_at: "2026-07-20T00:00:00Z",
                },
                {
                  id: THREAD_REPLY_ID,
                  channel_id: "chan-1",
                  workspace_id: "ws-1",
                  seq: 2,
                  type: "user",
                  author_id: "user-2",
                  author_name: "Bob",
                  content: "the anchored reply",
                  source: "multica",
                  external_message_id: null,
                  client_message_id: null,
                  thread_root_message_id: THREAD_ROOT_ID,
                  created_at: "2026-07-21T01:00:00Z",
                },
              ]
            : [],
      }),
    channelMessagesPageOptions: () => ({
      queryKey: ["channel-messages"],
      queryFn: async () => ({ messages: [], limit: 50, has_more: false, next_cursor: null }),
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

vi.mock("@multica/ui/hooks/use-mobile", () => ({
  useIsMobile: () => false,
  useContainerNarrowerThan: () => [false, () => {}] as const,
}));

vi.mock("../../navigation/context", () => ({
  useNavigation: () => ({
    searchParams: new URLSearchParams({ thread: THREAD_ROOT_ID, message: THREAD_REPLY_ID }),
    replace: vi.fn(),
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
// Unlike the plain routing test, this mock surfaces `highlightMessageId` so
// the assertions below can tell the main list apart from the thread list,
// and confirm exactly which one got the highlight target.
vi.mock("./channel-message-list", () => ({
  ChannelMessageList: ({
    messages,
    highlightMessageId,
  }: {
    messages?: { id: string }[];
    highlightMessageId?: string | null;
  }) => (
    <div data-testid="message-list" data-highlight={highlightMessageId ?? ""} data-count={(messages ?? []).length}>
      {(messages ?? []).filter(Boolean).map((m) => (
        <div key={m.id} data-testid={`msg-${m.id}`} />
      ))}
    </div>
  ),
}));

vi.mock("./conversation-surface", async (importOriginal) => ({
  ...(await importOriginal<typeof import("./conversation-surface")>()),
  ConversationHeader: ({ title, leading }: { title?: React.ReactNode; leading?: React.ReactNode }) => (
    <div data-testid="active-title">
      {leading}
      {title}
    </div>
  ),
}));

function renderPage(channelId?: string) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false } },
  });
  return render(
    <I18nProvider locale="en" resources={{ en: { common: enCommon, channels: enChannels } }}>
      <QueryClientProvider client={qc}>
        <ChannelsPage channelId={channelId} />
      </QueryClientProvider>
    </I18nProvider>,
  );
}

describe("ChannelsPage — Reminder anchor ?thread=&message= deep-link (#656)", () => {
  beforeEach(() => {
    window.sessionStorage.clear();
    useLastSelectedChannelStore.setState({ lastSelectedChannelId: null });
  });

  it("opens ThreadPanel (not just a main-timeline highlight) and routes the highlight to the reply inside it", async () => {
    renderPage("chan-1");

    // Both the main conversation header and ThreadPanel's own header render
    // through the same mocked ConversationHeader, so this now matches two —
    // the group's own title is enough to confirm the right channel resolved.
    await waitFor(() => {
      expect(
        screen.getAllByTestId("active-title").some((el) => el.textContent?.includes("general")),
      ).toBe(true);
    });

    // Two ChannelMessageList instances now exist: the main timeline and the
    // ThreadPanel's reply list. The reply-list one is the one carrying the
    // highlight target and the anchored reply.
    const lists = await screen.findAllByTestId("message-list");
    expect(lists.length).toBeGreaterThanOrEqual(2);
    const threadList = lists.find((el) => el.querySelector(`[data-testid="msg-${THREAD_REPLY_ID}"]`));
    expect(threadList).toBeTruthy();
    expect(threadList).toHaveAttribute("data-highlight", THREAD_REPLY_ID);

    // The main timeline must NOT have absorbed the highlight — it belongs to
    // a message that was never in the main list's page.
    const mainList = lists.find((el) => el !== threadList);
    expect(mainList).toHaveAttribute("data-highlight", "");
  });
});
