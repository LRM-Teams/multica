// @vitest-environment jsdom

import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import { useLastSelectedChannelStore } from "@multica/core/channels";
import enCommon from "../../locales/en/common.json";
import enChannels from "../../locales/en/channels.json";
import { ChannelsPage } from "./channels-page";

/**
 * LRM-263 / Frank A — channel sidebar rows are Slack-style: `#` + name
 * (+ unread). They must NOT show an `author: summary` last-message preview.
 * DM rows keep their preview (facet split).
 */

const PREVIEW_SNIPPET = "cli-target-probe";
const AUTHOR_LABEL = "后端工程师";

const CHANNELS = [
  {
    id: "chan-1",
    workspace_id: "ws-1",
    name: "LRM2.0开发群",
    kind: "group" as const,
    description: null,
    lark_chat_id: null,
    created_by: "user-1",
    created_at: "2026-06-17T09:00:00Z",
    updated_at: "2026-06-17T09:00:00Z",
    unread_count: 2,
    real_unread_count: 2,
    last_message: {
      type: "agent" as const,
      author_name: AUTHOR_LABEL,
      content: PREVIEW_SNIPPET,
      created_at: "2026-07-22T03:00:00Z",
    },
  },
  {
    id: "chan-2",
    workspace_id: "ws-1",
    name: "random",
    kind: "group" as const,
    description: null,
    lark_chat_id: null,
    created_by: "user-1",
    created_at: "2026-06-17T09:00:00Z",
    updated_at: "2026-06-17T09:00:00Z",
    last_message: {
      type: "user" as const,
      author_name: "Alice",
      content: "hello from channel",
      created_at: "2026-07-22T02:00:00Z",
    },
  },
];

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
vi.mock("@multica/ui/hooks/use-mobile", () => ({
  useIsMobile: () => mobileViewport.value,
  useContainerNarrowerThan: () => [false, () => {}] as const,
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
vi.mock("../../common/project-picker-button", () => ({
  ProjectPickerButton: () => <button type="button">project</button>,
}));
vi.mock("./dm-conversation", () => ({
  DmConversation: () => <div data-testid="dm-conversation" />,
}));
vi.mock("./channel-files-panel", () => ({ ChannelFilesPanel: () => <div /> }));
vi.mock("./channel-stats-panel", () => ({ ChannelStatsPanel: () => <div /> }));
vi.mock("./channel-message-list", () => ({
  ChannelMessageList: () => <div data-testid="message-list" />,
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

describe("ChannelsPage — sidebar last-message preview (LRM-263)", () => {
  beforeEach(() => {
    mobileViewport.value = false;
    window.sessionStorage.clear();
    useLastSelectedChannelStore.setState({ lastSelectedChannelId: null });
  });

  it("shows # + channel name without author:summary preview on channel rows", async () => {
    renderPage("chan-1");
    await screen.findByTestId("message-list");

    const rows = await screen.findAllByTestId("channel-sidebar-row");
    expect(rows.length).toBeGreaterThanOrEqual(2);

    const channelRow = rows.find((el) => el.getAttribute("data-channel-id") === "chan-1");
    expect(channelRow).toBeTruthy();
    expect(channelRow).toHaveTextContent("LRM2.0开发群");
    expect(channelRow!.querySelector('[data-testid="channel-hash-landmark"]')).toBeTruthy();

    // Distinctive last_message must not leak into the channel sidebar row.
    expect(channelRow).not.toHaveTextContent(PREVIEW_SNIPPET);
    expect(channelRow).not.toHaveTextContent(AUTHOR_LABEL);
    expect(channelRow).not.toHaveTextContent(`${AUTHOR_LABEL}:`);
    expect(screen.queryByText(new RegExp(`${AUTHOR_LABEL}:\\s*${PREVIEW_SNIPPET}`))).toBeNull();
  });
});
