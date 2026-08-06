import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enChannels from "../../locales/en/channels.json";
import { ChannelsPage } from "./channels-page";

// LRM-263 — channel sidebar rows are Slack-style (# + name + unread only).
// Last-message author/content preview and timestamp must not render on channel
// rows; DMs keep their preview (covered in dm-list.test.tsx).

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

const previewFixture = vi.hoisted(() => ({
  author: "Preview Author",
  body: "LRM-263 preview body must not appear",
}));

const channelsFixture = vi.hoisted(() => ({
  current: [
    {
      id: "chan-with-preview",
      workspace_id: "ws-1",
      name: "engineering",
      kind: "group" as const,
      description: null,
      lark_chat_id: null,
      created_by: "user-1",
      created_at: "2026-06-17T09:00:00Z",
      updated_at: "2026-06-17T09:00:00Z",
      unread_count: 3,
      real_unread_count: 3,
      last_message: {
        type: "user" as const,
        author_name: previewFixture.author,
        content: previewFixture.body,
        created_at: "2026-07-22T03:00:00.000Z",
      },
    },
  ],
}));

vi.mock("@multica/core/channels", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@multica/core/channels")>();
  const options = (queryKey: string[], data: unknown) => ({ queryKey, queryFn: async () => data });
  return {
    ...actual,
    channelsOptions: () => options(["channels"], channelsFixture.current),
    archivedChannelsOptions: () => options(["channels-archived"], []),
    channelMembersOptions: () => options(["channel-members"], []),
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
  return conversationsMock(importOriginal, () => channelsFixture.current);
});

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
    searchParams: new URLSearchParams(),
    replace: vi.fn(),
    getShareableUrl: (url: string) => url,
  }),
}));

vi.mock("../../editor/lazy-content-editor", () => ({
  ContentEditor: () => <div data-testid="content-editor" />,
}));
vi.mock("../../common/project-picker-button", () => ({
  ProjectPickerButton: () => <button type="button">project</button>,
}));
vi.mock("./dm-conversation", () => ({ DmConversation: () => <div data-testid="dm-conversation" /> }));
vi.mock("./channel-files-panel", () => ({ ChannelFilesPanel: () => <div /> }));
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

describe("ChannelsPage — channel sidebar preview (LRM-263)", () => {
  beforeEach(() => {
    window.sessionStorage.clear();
    channelsFixture.current = [
      {
        id: "chan-with-preview",
        workspace_id: "ws-1",
        name: "engineering",
        kind: "group" as const,
        description: null,
        lark_chat_id: null,
        created_by: "user-1",
        created_at: "2026-06-17T09:00:00Z",
        updated_at: "2026-06-17T09:00:00Z",
        unread_count: 3,
        real_unread_count: 3,
        last_message: {
          type: "user" as const,
          author_name: previewFixture.author,
          content: previewFixture.body,
          created_at: "2026-07-22T03:00:00.000Z",
        },
      },
    ];
  });

  it("shows # + channel name but not last-message author/content preview on channel rows", async () => {
    renderPage("chan-with-preview");
    await screen.findByTestId("message-list");

    const row = screen.getByRole("button", { name: /engineering/i });
    expect(row).toBeInTheDocument();
    expect(row.querySelector('[data-testid="channel-hash-landmark"]')).not.toBeNull();

    expect(screen.queryByText(previewFixture.body)).not.toBeInTheDocument();
    expect(screen.queryByText(new RegExp(`${previewFixture.author}:`))).not.toBeInTheDocument();
    expect(
      screen.queryByText(new RegExp(`${previewFixture.author}: ${previewFixture.body}`)),
    ).not.toBeInTheDocument();
  });

  it("keeps unread affordance on the channel row when last_message is present", async () => {
    renderPage("chan-with-preview");
    await screen.findByTestId("message-list");

    const row = screen.getByRole("button", { name: /engineering/i });
    // LRM-767 (Slack-aligned): unread is bold name + a neutral numeric badge.
    expect(row.querySelector(".font-semibold")).not.toBeNull();
    expect(row.querySelector(".rounded-full.bg-muted")).not.toBeNull();
  });
});
