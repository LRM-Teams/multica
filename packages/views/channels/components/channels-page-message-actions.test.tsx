import { act, render, screen, waitFor } from "@testing-library/react";
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
vi.mock("@multica/core/channels", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@multica/core/channels")>();
  const channel = {
    id: "chan-1",
    workspace_id: "ws-1",
    name: "general",
    kind: "group" as const,
    description: null,
    lark_chat_id: null,
    created_by: "user-1",
    created_at: "2026-06-17T09:00:00Z",
    updated_at: "2026-06-17T09:00:00Z",
  };
  const options = (queryKey: string[], data: unknown) => ({ queryKey, queryFn: async () => data });
  return {
    ...actual,
    channelsOptions: () => options(["channels"], [channel]),
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
  useWorkspacePaths: () => ({ channels: () => "/w/test/channels" }),
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

vi.mock("@multica/ui/hooks/use-mobile", () => ({ useIsMobile: () => false }));

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

vi.mock("./dm-conversation", () => ({ DmConversation: () => <div /> }));
vi.mock("./channel-files-panel", () => ({ ChannelFilesPanel: () => <div /> }));
vi.mock("./channel-stats-panel", () => ({ ChannelStatsPanel: () => <div /> }));

// The message list is mocked so we can capture the exact props ChannelsPage
// hands it — this is the parent→list contract the live bug broke.
const listProps = vi.hoisted(() => ({
  current: null as {
    onEditMessage?: (m: ChannelMessage, content: string) => void;
    onDeleteMessage?: (m: ChannelMessage) => void;
  } | null,
}));
vi.mock("./channel-message-list", () => ({
  ChannelMessageList: (props: {
    onEditMessage?: (m: ChannelMessage, content: string) => void;
    onDeleteMessage?: (m: ChannelMessage) => void;
  }) => {
    listProps.current = props;
    return <div data-testid="message-list" />;
  },
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

  it("supplies working onEditMessage / onDeleteMessage handlers to the message list", async () => {
    renderPage();
    await screen.findByTestId("message-list");
    await waitFor(() => {
      expect(typeof listProps.current?.onEditMessage).toBe("function");
      expect(typeof listProps.current?.onDeleteMessage).toBe("function");
    });
  });

  it("routes an edit through editChannelMessage (PATCH) and never a send (H5)", async () => {
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
});
