import { render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import { I18nProvider } from "@multica/core/i18n/react";
import type { ChannelMessage } from "@multica/core/types";
import type { DMItem } from "@multica/core/dm";
import enCommon from "../../locales/en/common.json";
import enChannels from "../../locales/en/channels.json";
import { DmConversation } from "./dm-conversation";

// #656 Reminder anchor `?thread=<root>&message=<reply>` deep-link — the DM
// counterpart of the channels-page reminder-anchor suite (now in
// channels-page.test.tsx). DMs render
// through this entirely separate component (own threadRoot/highlight state),
// so the group-channel fix in channels-page.tsx doesn't cover this path —
// this proves the equivalent DM wiring actually opens the thread and routes
// the highlight to the reply inside it, not the main DM timeline.

// jsdom doesn't implement scrollIntoView; ChannelMessageList's highlight
// effect calls it on the target ref once the deep-linked message mounts.
if (!Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = () => {};
}

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

vi.mock("react-virtuoso", async () => {
  const React = await import("react");
  const MockVirtuoso = React.forwardRef(
    (
      {
        components = {},
        data = [],
        itemContent,
      }: {
        components?: {
          Footer?: React.ComponentType;
          Header?: React.ComponentType;
          List?: React.ComponentType<React.HTMLAttributes<HTMLDivElement>>;
        };
        data?: ChannelMessage[];
        initialTopMostItemIndex?: number;
        firstItemIndex?: number;
        startReached?: () => void;
        itemContent: (index: number, item: ChannelMessage) => React.ReactNode;
      },
      ref: React.ForwardedRef<{ scrollToIndex: (...args: unknown[]) => void }>,
    ) => {
      React.useImperativeHandle(ref, () => ({ scrollToIndex: vi.fn() }));
      const Header = components.Header;
      const List = components.List ?? "div";
      const Footer = components.Footer;
      return (
        <div data-testid="virtuoso-scroller">
          {Header ? <Header /> : null}
          <List>{data.map((item, index) => itemContent(index, item))}</List>
          {Footer ? <Footer /> : null}
        </div>
      );
    },
  );
  MockVirtuoso.displayName = "MockVirtuoso";
  return { Virtuoso: MockVirtuoso };
});

const ROOT_ID = "dm-root-1";
const REPLY_ID = "dm-reply-1";
const SECOND_ROOT_ID = "dm-root-2";
const SECOND_REPLY_ID = "dm-reply-2";

function rootMessage(): ChannelMessage {
  return {
    id: ROOT_ID,
    channel_id: "dm-chan-1",
    workspace_id: "ws-1",
    seq: 1,
    type: "user",
    author_id: "peer-1",
    author_name: "Bob",
    content: "Root of the thread",
    source: "multica",
    external_message_id: null,
    client_message_id: null,
    thread_reply_count: 1,
    created_at: "2026-06-17T09:15:00Z",
  };
}

function replyMessage(): ChannelMessage {
  return {
    id: REPLY_ID,
    channel_id: "dm-chan-1",
    workspace_id: "ws-1",
    seq: 2,
    type: "user",
    author_id: "peer-1",
    author_name: "Bob",
    content: "The anchored reply",
    source: "multica",
    external_message_id: null,
    client_message_id: null,
    thread_root_message_id: ROOT_ID,
    created_at: "2026-07-21T01:00:00Z",
  };
}

function secondRootMessage(): ChannelMessage {
  return { ...rootMessage(), id: SECOND_ROOT_ID, seq: 3, content: "Second root of the thread" };
}

function secondReplyMessage(): ChannelMessage {
  return {
    ...replyMessage(),
    id: SECOND_REPLY_ID,
    seq: 4,
    content: "The second anchored reply",
    thread_root_message_id: SECOND_ROOT_ID,
    created_at: "2026-07-22T01:00:00Z",
  };
}

vi.mock("@multica/core/channels", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@multica/core/channels")>();
  return {
    ...actual,
    activeChannelTasksOptions: () => ({ queryKey: ["channel-tasks"], queryFn: async () => [] }),
    channelMessageThreadOptions: (_channelId: string, messageId: string) => ({
      queryKey: ["channel-thread", messageId],
      queryFn: async () => {
        // Lazily built (not at mock-factory-eval time, which is hoisted
        // above the top-level ROOT_ID/SECOND_ROOT_ID const declarations).
        const threads: Record<string, ChannelMessage[]> = {
          [ROOT_ID]: [rootMessage(), replyMessage()],
          [SECOND_ROOT_ID]: [secondRootMessage(), secondReplyMessage()],
        };
        return { messages: threads[messageId] ?? [] };
      },
    }),
    channelMessagesPageOptions: () => ({
      queryKey: ["dm-messages"],
      queryFn: async () => ({ messages: [rootMessage()], next_cursor: null }),
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

vi.mock("@multica/core/realtime", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/realtime")>()),
  useWSEvent: vi.fn(),
}));

vi.mock("@multica/core/hooks/use-file-upload", () => ({
  useFileUpload: () => ({ uploadWithToast: vi.fn() }),
}));

vi.mock("@multica/core/workspace/hooks", () => ({
  useActorName: () => ({
    getActorAvatarUrl: () => null,
    getActorName: () => null,
    getMemberHonor: () => undefined,
    getAgentFleetRank: () => undefined,
  }),
}));
vi.mock("@multica/core/agents", () => ({ useAgentPresenceDetail: () => "loading" }));
vi.mock("@multica/core/paths", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/paths")>()),
  useCurrentWorkspace: () => ({ id: "ws-1" }),
}));

vi.mock("@multica/ui/hooks/use-mobile", () => ({ useIsMobile: () => false }));

vi.mock("../../editor/content-editor", () => ({
  ContentEditor: () => <div data-testid="content-editor" />,
}));
vi.mock("../../common/markdown", () => ({
  MemoizedMarkdown: ({ children }: { children: string }) => <span>{children}</span>,
}));
vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: () => <div data-testid="actor-avatar" />,
}));
vi.mock("../../common/actor-profile-popover", () => ({
  ActorProfileTrigger: ({ children }: { children: ReactNode }) => <>{children}</>,
}));

const dm: DMItem = {
  id: "dm-chan-1",
  source: "dm_channel",
  peer: { type: "user", id: "peer-1", name: "Bob" },
  unread: 0,
  updated_at: "2026-06-17T09:00:00Z",
};

function renderDm(deepLink: { threadDeepLinkId?: string | null; deepLinkMessageId?: string | null }) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false } },
  });
  const result = render(
    <I18nProvider locale="en" resources={{ en: { common: enCommon, channels: enChannels } }}>
      <QueryClientProvider client={qc}>
        <DmConversation dm={dm} onBack={() => {}} {...deepLink} />
      </QueryClientProvider>
    </I18nProvider>,
  );
  return { ...result, qc };
}

describe("DmConversation — Reminder anchor ?thread=&message= deep-link (#656)", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("opens the DM's own thread reply list (not just the main timeline) and routes the highlight to the reply", async () => {
    renderDm({ threadDeepLinkId: ROOT_ID, deepLinkMessageId: REPLY_ID });

    // The thread panel's own header renders "Thread" — confirms it actually
    // opened, not just a main-timeline highlight of the root.
    await waitFor(() => {
      expect(screen.getByText("Thread")).toBeInTheDocument();
    });
    // LRM-873: the same reply text also appears in ThreadReplyPreview under
    // the parent, so resolve the highlighted row by message id.
    await waitFor(() => {
      expect(document.getElementById(`message-${REPLY_ID}`)).toBeTruthy();
    });
    const replyRow = document.getElementById(`message-${REPLY_ID}`);
    expect(replyRow?.textContent).toContain("The anchored reply");
    // Ring-highlight class applied by ChannelMessageList's `highlighted` prop.
    expect(replyRow?.className).toMatch(/ring/);
  });

  it("without a thread deep-link, a plain ?message= only highlights the main timeline (unchanged behavior)", async () => {
    renderDm({ deepLinkMessageId: ROOT_ID });

    await waitFor(() => {
      expect(screen.getByText("Root of the thread")).toBeInTheDocument();
    });
    expect(screen.queryByText("Thread")).not.toBeInTheDocument();
    const rootRow = screen.getByText("Root of the thread").closest("[id]");
    expect(rootRow).toHaveAttribute("id", `message-${ROOT_ID}`);
    expect(rootRow?.className).toMatch(/ring/);
  });

  it("opens a SECOND, different thread when new deep-link props arrive without remounting (same-page navigation, no full reload)", async () => {
    const { rerender, qc } = renderDm({ threadDeepLinkId: ROOT_ID, deepLinkMessageId: REPLY_ID });

    await waitFor(() => {
      expect(document.getElementById(`message-${REPLY_ID}`)).toBeTruthy();
    });

    // Same DmConversation instance, same QueryClient — only the deep-link
    // props change (as they would when channels-page.tsx's reactive
    // searchParams-derived state updates from an AppLink push while this DM
    // stays the active one).
    rerender(
      <I18nProvider locale="en" resources={{ en: { common: enCommon, channels: enChannels } }}>
        <QueryClientProvider client={qc}>
          <DmConversation
            dm={dm}
            onBack={() => {}}
            threadDeepLinkId={SECOND_ROOT_ID}
            deepLinkMessageId={SECOND_REPLY_ID}
          />
        </QueryClientProvider>
      </I18nProvider>,
    );

    await waitFor(() => {
      expect(document.getElementById(`message-${SECOND_REPLY_ID}`)).toBeTruthy();
    });
    const secondReplyRow = document.getElementById(`message-${SECOND_REPLY_ID}`);
    expect(secondReplyRow?.textContent).toContain("The second anchored reply");
    expect(secondReplyRow?.className).toMatch(/ring/);
  });
});
