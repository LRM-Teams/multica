import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { ChannelMessage } from "@multica/core/types";
import { ChannelMessageList, MessageViewport } from "./channel-message-list";

// jsdom doesn't implement scrollIntoView; only the highlight-scroll test below
// exercises this path (existing tests never set highlightMessageId).
Element.prototype.scrollIntoView = vi.fn();

const scrollToIndexMock = vi.fn();

vi.mock("react-virtuoso", async () => {
  const React = await import("react");

  const MockVirtuoso = React.forwardRef(
    (
      {
        components = {},
        data = [],
        itemContent,
        initialTopMostItemIndex,
        firstItemIndex = 0,
        startReached,
        scrollerRef,
      }: {
        components?: {
          Footer?: React.ComponentType;
          Header?: React.ComponentType;
          List?: React.ComponentType<React.HTMLAttributes<HTMLDivElement>>;
        };
        data?: ChannelMessage[];
        // Real Virtuoso accepts a number or an { index, align } location (#340
        // uses the object form to pin last-read with align:start).
        initialTopMostItemIndex?: number | { index: number; align?: string };
        firstItemIndex?: number;
        startReached?: () => void;
        // #325 phase 1: Virtuoso owns its scroller and reports it via scrollerRef.
        // Wire the mock's root so the mount-gated scroll effects still fire.
        scrollerRef?: (el: HTMLElement | Window | null) => void;
        itemContent: (index: number, item: ChannelMessage) => React.ReactNode;
      },
      ref: React.ForwardedRef<{ scrollToIndex: (...args: unknown[]) => void }>,
    ) => {
      React.useImperativeHandle(ref, () => ({ scrollToIndex: scrollToIndexMock }));

      const Header = components.Header;
      const List = components.List ?? "div";
      const Footer = components.Footer;
      const initialIndex =
        typeof initialTopMostItemIndex === "object" && initialTopMostItemIndex !== null
          ? initialTopMostItemIndex.index
          : initialTopMostItemIndex;
      // #689/#1189 index-contract fix: real react-virtuoso resolves
      // `initialTopMostItemIndex`/`scrollToIndex` against the LOCAL data
      // array (0..data.length-1) — it never reads `firstItemIndex` for this.
      // The mock previously subtracted `firstItemIndex` here, which silently
      // "corrected" a caller bug (adding the offset) back to the right local
      // index — masking that exact production bug from every test using this
      // mock. Treat `initialIndex` as already local, matching real Virtuoso.
      const localTarget = Math.max(0, initialIndex ?? 0);
      const targetIndex = Math.max(0, Math.min(localTarget, data.length - 1));
      const start = Math.max(0, Math.min(targetIndex - 1, data.length - 2));
      const windowedData = data.slice(start, start + 2);

      return (
        <div
          ref={scrollerRef}
          data-testid="virtuoso-scroller"
          data-initial-index={initialIndex ?? "unset"}
          data-first-item-index={firstItemIndex}
        >
          {startReached && (
            <button type="button" data-testid="start-reached" onClick={() => startReached()} />
          )}
          {Header ? <Header /> : null}
          <List>{windowedData.map((item, offset) => itemContent(start + offset, item))}</List>
          {Footer ? <Footer /> : null}
        </div>
      );
    },
  );
  MockVirtuoso.displayName = "MockVirtuoso";

  return {
    Virtuoso: MockVirtuoso,
  };
});

vi.mock("../../common/markdown", () => ({
  MemoizedMarkdown: ({ children }: { children: string }) => <span>{children}</span>,
}));


vi.mock("@multica/core/workspace/use-member-presence", () => ({
  useMemberOnline: () => false,
}));

vi.mock("@multica/core/workspace/hooks", () => ({
  useActorName: () => ({
    getActorAvatarUrl: () => null,
    getActorName: () => "Test Actor",
    getActorInitials: () => "TA",
    getMemberRole: () => null,
    getMemberHonor: () => undefined,
    getAgentFleetRank: () => undefined,
    getAgentHonorLevel: () => undefined,
  }),
}));

// The bubble reads the author avatar from the payload (#453/#574) via
// resolvePublicFileUrl, which needs api.getBaseUrl(); pass the raw value
// through so this viewport test doesn't touch the api base-url machinery.
vi.mock("@multica/core/workspace/avatar-url", () => ({
  resolvePublicFileUrl: (url: string | null | undefined) => url ?? null,
}));

// ChannelMessageBubble overlays the shared presence dot (AgentStatusDot) on
// agent avatars, which reads presence via useAgentPresenceDetail and the
// current workspace via useCurrentWorkspace. Stub both so this viewport test
// stays free of QueryClient/workspace-provider wiring.
vi.mock("@multica/core/agents", () => ({
  useAgentPresenceDetail: () => "loading",
  // #271 single-hook: summary + events fetched together.
  useAgentHealth: () => ({
    summary: undefined,
    events: undefined,
    isLoading: false,
    isError: false,
  }),
}));

// LRM-364: bubble reaction hover resolves names via member-profile queries.
// Viewport tests are not about reactor attribution — stub the hook so we stay
// free of QueryClientProvider (same rationale as agents/paths stubs above).
vi.mock("../../common/use-reaction-actor-name", () => ({
  useReactionActorName: () => (type: string, id: string) => id || type,
}));

// LRM-391: ActorAvatar may call useResolvedActorIdentity → useQuery(member-profiles).
// Viewport composition is not about identity resolution — stub to stay QC-free.
vi.mock("../../common/use-resolved-actor-identity", () => ({
  useResolvedActorIdentity: () => ({ displayName: "Test Actor", avatarUrl: null }),
  mentionTypeFromActorType: (actorType: string | null | undefined) => {
    if (actorType === "agent") return "agent";
    if (actorType === "member" || actorType === "human" || actorType === "user") return "member";
    return null;
  },
  resolvedActorLabel: (identity: { displayName: string | null }, actorId?: string) =>
    identity.displayName ?? actorId ?? "",
}));

vi.mock("@multica/core/paths", () => ({
  useCurrentWorkspace: () => ({ id: "ws-1", slug: "test" }),
  useWorkspaceSlug: () => "test",
  useRequiredWorkspaceSlug: () => "test",
  useWorkspacePaths: () => ({
    memberDetail: (id: string) => `/test/members/${id}`,
    agentDetail: (id: string) => `/test/agents/${id}`,
  }),
}));

vi.mock("../../i18n", () => ({
  useT: () => ({
    t: (
      selector: (resources: {
        message: { add_reaction: string; agent_badge: string; feishu_badge: string };
        quote: { jump_to: string; action: string };
        thread: { reply: string; reply_count: string };
      }) => string,
    ) =>
      selector({
        message: {
          add_reaction: "Add reaction",
          agent_badge: "Agent",
          feishu_badge: "Feishu",
        },
        quote: {
          action: "Quote",
          jump_to: "Jump to original message",
        },
        thread: {
          reply: "Reply in thread",
          reply_count: "{{count}} replies",
        },
      }),
  }),
}));

// The bubble (rendered inside every row) reads its labels from
// `../../i18n/use-t`; stub it so the edit/delete affordances have concrete
// accessible names to query by in the composition tests below.
vi.mock("../../common/use-viewing-timezone", () => ({
  useViewingTimezone: () => "UTC",
}));

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    // Interpolates {{count}} so the "N new messages" divider count is assertable.
    t: (
      selector: (resources: any) => string,
      params?: Record<string, unknown>,
    ) => {
      const raw = selector({
        message: {
          add_reaction: "Add reaction",
          agent_badge: "Agent",
          feishu_badge: "Feishu",
          copy_action: "Copy",
          expand_action: "See more",
          collapse_action: "See less",
          copied_toast: "Copied",
          copy_failed_toast: "Copy failed",
          edit_action: "Edit",
          delete_action: "Delete",
          edited_label: "(edited)",
          deleted_placeholder: "This message was deleted",
          choice_reselect_hint: "You can change your answer once",
          choice_locked: "Locked",
          choice_failed: "Couldn't select option",
          save_edit: "Save",
          cancel_edit: "Cancel",
        },
        quote: {
          action: "Quote",
          jump_to: "Jump to original message",
          cancel: "Cancel quote",
          unavailable_title: "Original message unavailable",
          unavailable_summary: "It may have been deleted or you may not have access.",
          type_user: "Message",
          type_agent: "Agent",
          type_lark: "Feishu",
          type_system: "System",
          type_unknown: "Message",
          attachment_summary: "Attachment",
          attachments_summary: "{{count}} attachments",
          image_summary: "Image",
          images_summary: "{{count}} images",
          empty_summary: "No preview available",
        },
        thread: { reply: "Reply in thread", reply_count: "2 replies" },
        time: { today: "Today", yesterday: "Yesterday", new_messages: "{{count}} new" },
      });
      return params
        ? raw.replace(/\{\{(\w+)\}\}/g, (_, k) => String(params[k] ?? ""))
        : raw;
    },
  }),
}));

function makeMessage(id: string, content: string): ChannelMessage {
  return {
    id,
    channel_id: "c1",
    workspace_id: "w1",
    seq: 1,
    type: "agent",
    author_id: "agent-1",
    author_name: "Research Agent",
    content,
    source: "multica",
    external_message_id: null,
    client_message_id: null,
    created_at: "2026-06-17T09:15:00Z",
  };
}

function makeRuntimeNotice(id: string): ChannelMessage {
  return {
    id,
    channel_id: "c1",
    workspace_id: "w1",
    seq: 1,
    type: "system",
    author_id: null,
    author_name: "System",
    content: "daemon_outdated",
    source: "multica",
    external_message_id: null,
    client_message_id: null,
    created_at: "2026-06-17T09:15:00Z",
  };
}

describe("MessageViewport", () => {
  it("renders the virtualized window when messages are present", async () => {
    render(
      <MessageViewport
        messages={[
          makeMessage("m1", "First visible message"),
          makeMessage("m2", "Second visible message"),
          makeMessage("m3", "Third visible message"),
        ]}
        currentUserId="user-1"
        emptyLabel="No messages"
      />,
    );

    expect(screen.getByText("Second visible message")).toBeInTheDocument();
    expect(screen.getByText("Third visible message")).toBeInTheDocument();
    expect(screen.queryByText("First visible message")).not.toBeInTheDocument();
    expect(screen.getAllByTestId("message-bubble")).toHaveLength(2);
    expect(screen.getAllByTestId("message-row")).toHaveLength(2);
    expect(screen.getByTestId("message-item-list").children).toHaveLength(2);
  });

  it("filters runtime notices out of the conversation row list", () => {
    render(
      <ChannelMessageList
        messages={[
          makeRuntimeNotice("n1"),
          makeMessage("m1", "First visible message"),
          makeRuntimeNotice("n2"),
          makeMessage("m2", "Second visible message"),
        ]}
        currentUserId="user-1"
        emptyLabel="No messages"
      />,
    );

    expect(screen.getAllByTestId("message-row")).toHaveLength(2);
    expect(screen.getByText("First visible message")).toBeInTheDocument();
    expect(screen.getByText("Second visible message")).toBeInTheDocument();
    expect(screen.queryByText("daemon_outdated")).not.toBeInTheDocument();
  });

  it("groups consecutive same-author messages into lead + compact rows (LRM-255)", () => {
    render(
      <MessageViewport
        messages={[
          makeMessage("m1", "First in group"),
          {
            ...makeMessage("m2", "Second in group"),
            created_at: "2026-06-17T09:16:00Z",
          },
        ]}
        currentUserId="user-1"
        emptyLabel="No messages"
      />,
    );

    const rows = screen.getAllByTestId("message-row");
    expect(rows).toHaveLength(2);
    expect(rows[0]).toHaveAttribute("data-message-group", "lead");
    expect(rows[1]).toHaveAttribute("data-message-group", "compact");
    expect(screen.getByTestId("message-gutter-time")).toHaveTextContent("09:16");
  });

  // LRM-1156 — a thread list (pinned root as `header`) must open on the LAST
  // reply, not the first. Frank: 「thread里读消息，能不能优先跳到最后一行？别老是
  // 让我滑动」. The root stays reachable by scrolling up; the landing belongs to
  // the newest reply, same as any chat surface.
  it("opens thread lists at the latest reply, not the root context (LRM-1156)", () => {
    render(
      <MessageViewport
        messages={[
          makeMessage("m1", "First thread reply"),
          makeMessage("m2", "Middle thread reply"),
          makeMessage("m3", "Latest thread reply"),
        ]}
        currentUserId="user-1"
        emptyLabel="No replies"
        header={<div data-testid="thread-root-preview">Root preview</div>}
        // #1194 regression guard: a large firstItemIndex must NOT leak into
        // the local landing index (here: 2, the last local row).
        firstItemIndex={1_000_000}
      />,
    );

    expect(screen.getByTestId("thread-root-preview")).toBeInTheDocument();
    expect(screen.getByTestId("virtuoso-scroller")).toHaveAttribute("data-initial-index", "2");
    expect(screen.getByText("Latest thread reply")).toBeInTheDocument();
    expect(screen.queryByText("First thread reply")).not.toBeInTheDocument();
  });

  it("pins a 'new messages' divider above the first unread but opens at latest (LRM-1068)", () => {
    // seq 5,6,7,8 with the read cursor at 6 → first unread is m7; latest is m8 (index 3).
    const messages = [
      { ...makeMessage("m5", "Read one"), seq: 5 },
      { ...makeMessage("m6", "Read two"), seq: 6 },
      { ...makeMessage("m7", "Unread one"), seq: 7 },
      { ...makeMessage("m8", "Unread two"), seq: 8 },
    ];
    render(
      <MessageViewport
        messages={messages}
        currentUserId="user-1"
        emptyLabel="No messages"
        lastReadSeq={6}
        firstItemIndex={1_000_000}
      />,
    );

    expect(screen.getByTestId("unread-divider")).toBeInTheDocument();
    // LRM-1068: cold open lands on the latest row (index 3), not first-unread.
    expect(screen.getByTestId("virtuoso-scroller")).toHaveAttribute("data-initial-index", "3");
  });

  it("#340: the divider shows the real unread count (frozen at entry), not the loaded-window count", () => {
    // Large-unread marquee case: only 2 unread are in the loaded around window,
    // but the conversation has 486 unread. The divider must say 486, not 2.
    const messages = [
      { ...makeMessage("m5", "Read"), seq: 5 },
      { ...makeMessage("m6", "Unread one"), seq: 6 },
      { ...makeMessage("m7", "Unread two"), seq: 7 },
    ];
    render(
      <MessageViewport
        messages={messages}
        currentUserId="user-1"
        emptyLabel="No messages"
        lastReadSeq={5}
        unreadCount={486}
      />,
    );
    expect(screen.getByTestId("unread-divider")).toHaveTextContent("486 new");
  });

  it("falls back to the loaded-window count when no entry unread count is provided", () => {
    const messages = [
      { ...makeMessage("m5", "Read"), seq: 5 },
      { ...makeMessage("m6", "Unread one"), seq: 6 },
      { ...makeMessage("m7", "Unread two"), seq: 7 },
    ];
    render(
      <MessageViewport
        messages={messages}
        currentUserId="user-1"
        emptyLabel="No messages"
        lastReadSeq={5}
      />,
    );
    // Two unread messages loaded, no frozen count → window count of 2.
    expect(screen.getByTestId("unread-divider")).toHaveTextContent("2 new");
  });


  it("renders no divider when the cursor is unknown (BE field absent)", () => {
    const messages = [
      { ...makeMessage("m1", "One"), seq: 1 },
      { ...makeMessage("m2", "Two"), seq: 2 },
    ];
    render(
      <MessageViewport messages={messages} currentUserId="user-1" emptyLabel="No messages" />,
    );

    expect(screen.queryByTestId("unread-divider")).not.toBeInTheDocument();
  });

  it("renders no divider when everything is already read", () => {
    const messages = [
      { ...makeMessage("m1", "One"), seq: 1 },
      { ...makeMessage("m2", "Two"), seq: 2 },
    ];
    render(
      <MessageViewport
        messages={messages}
        currentUserId="user-1"
        emptyLabel="No messages"
        lastReadSeq={2}
      />,
    );

    expect(screen.queryByTestId("unread-divider")).not.toBeInTheDocument();
  });

  it("does not render the full list while the custom scroller ref is being captured", () => {
    const messages = Array.from({ length: 700 }, (_, index) =>
      makeMessage(`m${index}`, `Message ${index}`),
    );
    const { container } = render(
      <MessageViewport
        messages={messages}
        currentUserId="user-1"
        emptyLabel="No messages"
      />,
    );

    expect(container.querySelectorAll('[data-testid="message-row"]').length).toBeLessThan(700);
  });

  it("keeps thread header content inside the message scroller for empty threads", () => {
    render(
      <MessageViewport
        messages={[]}
        currentUserId="user-1"
        emptyLabel="No replies"
        header={<div data-testid="thread-root-preview">Long root message</div>}
      />,
    );

    const scroller = screen.getByTestId("message-scroller");
    const header = screen.getByTestId("thread-root-preview");
    expect(scroller).toContainElement(header);
    expect(screen.getByText("No replies")).toBeInTheDocument();
  });

  // #689/#1189: `initialTopMostItemIndex` and `scrollToIndex` resolve against
  // the LOCAL data array (0..data.length-1) regardless of `firstItemIndex` —
  // confirmed against react-virtuoso's own prepend example
  // (`firstItemIndex=10000` + 20 items still uses `initialTopMostItemIndex=19`,
  // never `10019`) and its `scrollToIndexSystem` source, which never reads
  // `firstItemIndex`. A large `firstItemIndex` (this codebase's real
  // convention, `CHANNEL_MESSAGES_VIRTUOSO_BASE_INDEX = 1_000_000`) must NOT
  // change the index passed for highlight/unread-anchor/bottom-default
  // positioning — only Virtuoso's own prepend bookkeeping reads it.
  it("does not offset initialTopMostItemIndex or the highlight scrollToIndex call by firstItemIndex", () => {
    scrollToIndexMock.mockClear();
    render(
      <MessageViewport
        messages={[
          makeMessage("m1", "First visible message"),
          makeMessage("m2", "Second visible message"),
          makeMessage("m3", "Third visible message"),
        ]}
        currentUserId="user-1"
        emptyLabel="No messages"
        firstItemIndex={999_998}
        highlightMessageId="m2"
      />,
    );

    // Local index (1) of the highlighted message — firstItemIndex plays no part.
    expect(screen.getByTestId("virtuoso-scroller")).toHaveAttribute(
      "data-initial-index",
      "1",
    );
    expect(scrollToIndexMock).toHaveBeenCalledWith(expect.objectContaining({ index: 1 }));
  });

  it("lands on the local last-item index at the bottom default, unaffected by a large firstItemIndex", () => {
    render(
      <MessageViewport
        messages={[
          makeMessage("m1", "First visible message"),
          makeMessage("m2", "Second visible message"),
          makeMessage("m3", "Third visible message"),
        ]}
        currentUserId="user-1"
        emptyLabel="No messages"
        firstItemIndex={1_000_000}
      />,
    );

    expect(screen.getByTestId("virtuoso-scroller")).toHaveAttribute(
      "data-initial-index",
      "2",
    );
  });


  it("requests older history via startReached, but not while already loading or exhausted", () => {
    const onLoadOlder = vi.fn();
    const { rerender } = render(
      <MessageViewport
        messages={[makeMessage("m1", "Hello")]}
        currentUserId="user-1"
        emptyLabel="No messages"
        hasOlder
        onLoadOlder={onLoadOlder}
      />,
    );

    screen.getByTestId("start-reached").click();
    expect(onLoadOlder).toHaveBeenCalledTimes(1);

    rerender(
      <MessageViewport
        messages={[makeMessage("m1", "Hello")]}
        currentUserId="user-1"
        emptyLabel="No messages"
        hasOlder
        loadingOlder
        onLoadOlder={onLoadOlder}
      />,
    );
    screen.getByTestId("start-reached").click();
    expect(onLoadOlder).toHaveBeenCalledTimes(1);
  });
});

// A viewer-authored user message: only these expose edit/delete affordances.
function makeOwnUserMessage(
  overrides: Partial<ChannelMessage> = {},
): ChannelMessage {
  return {
    id: "own-1",
    channel_id: "c1",
    workspace_id: "w1",
    seq: 1,
    type: "user",
    author_id: "user-1",
    author_name: "Alice",
    content: "Original",
    source: "multica",
    external_message_id: null,
    client_message_id: null,
    created_at: "2026-06-17T09:15:00Z",
    ...overrides,
  };
}

// B3 (#241) parent→bubble composition: the prior unit tests passed onEdit /
// onDelete straight to the bubble, so they never caught that the message list
// (the bubble's real parent) forwarded neither — leaving the affordances dead
// on every live row. These render the real list → bubble and assert the
// composed DOM, so the wiring can't silently regress again.
describe("ChannelMessageList message edit / delete wiring", () => {
  // Edit unshipped 2026-07-05 (Frank/Miles): the Edit entry point is hidden
  // (canEdit=false) until rebuilt on the unified composer (#258). Delete stays,
  // so a wired own row surfaces Delete but never Edit.
  it("surfaces neither delete nor edit on the viewer's own message when wired (LRM-695)", () => {
    render(
      <ChannelMessageList
        messages={[makeOwnUserMessage()]}
        currentUserId="user-1"
        emptyLabel="No messages"
        onEditMessage={vi.fn()}
      />,
    );

    // LRM-695: delete removed from the UI (Frank: 只去删除); edit unshipped.
    expect(screen.queryByRole("button", { name: "Delete" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Edit" })).not.toBeInTheDocument();
  });

  it("hides edit and delete on a message the viewer does not own, even when wired", () => {
    render(
      <ChannelMessageList
        messages={[
          makeOwnUserMessage({
            id: "peer-1",
            author_id: "user-2",
            author_name: "Bob",
            content: "Not mine",
          }),
        ]}
        currentUserId="user-1"
        emptyLabel="No messages"
        onEditMessage={vi.fn()}
      />,
    );

    expect(screen.queryByRole("button", { name: "Edit" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Delete" })).not.toBeInTheDocument();
  });

  // Edit unshipped 2026-07-05 (Frank/Miles): the Edit entry point is hidden
  // (canEdit=false) so the inline editor is unreachable through the wired list.
  // onEditMessage wiring is kept dormant for the composer-parity rebuild (#258);
  // restore this PATCH/H5 flow test when Edit is re-enabled.
  it.skip("saves an inline edit through onEditMessage (PATCH) and never a send/dispatch path (H5)", async () => {
    const onEditMessage = vi.fn();
    const onReact = vi.fn();
    const onOpenThread = vi.fn();
    const message = makeOwnUserMessage();
    render(
      <ChannelMessageList
        messages={[message]}
        currentUserId="user-1"
        emptyLabel="No messages"
        onEditMessage={onEditMessage}
        onReact={onReact}
        onOpenThread={onOpenThread}
      />,
    );

    await userEvent.click(screen.getByRole("button", { name: "Edit" }));
    const editor = screen.getByRole("textbox", { name: "Edit" });
    await userEvent.clear(editor);
    await userEvent.type(editor, "Corrected");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    // Edit is a PATCH of the existing row — it must route only through
    // onEditMessage, never a reaction / thread (wake) path.
    expect(onEditMessage).toHaveBeenCalledTimes(1);
    expect(onEditMessage).toHaveBeenCalledWith(message, "Corrected");
    expect(onReact).not.toHaveBeenCalled();
    expect(onOpenThread).not.toHaveBeenCalled();
  });

  it("shows skeleton only on cold load (loading with no cached rows)", () => {
    const { rerender } = render(
      <ChannelMessageList
        messages={[]}
        currentUserId="user-1"
        emptyLabel="No messages"
        loading
      />,
    );
    expect(screen.getByTestId("message-rows-skeleton")).toBeInTheDocument();

    rerender(
      <ChannelMessageList
        messages={[makeMessage("m1", "Cached while refetching")]}
        currentUserId="user-1"
        emptyLabel="No messages"
        loading
      />,
    );
    expect(screen.queryByTestId("message-rows-skeleton")).not.toBeInTheDocument();
    expect(screen.getByTestId("message-bubble")).toBeInTheDocument();
  });

  it("LRM-357: empty thread primary copy uses text-foreground", () => {
    render(
      <MessageViewport
        messages={[]}
        currentUserId="user-1"
        emptyLabel="No messages"
      />,
    );
    const empty = screen.getByText("No messages");
    expect(empty.getAttribute("data-slot")).toBe("message-list-empty");
    expect(empty.className).toContain("text-foreground");
    expect(empty.className).not.toContain("text-muted-foreground");
  });

  it("renders a tombstone (non-empty row) for a server-deleted message (LRM-695)", () => {
    // LRM-695: the delete affordance is removed from the UI, but a message
    // deleted via API / another client still renders the tombstone placeholder.
    render(
      <ChannelMessageList
        messages={[
          makeOwnUserMessage({ deleted_at: "2026-06-17T09:25:00Z", content: "gone" }),
        ]}
        currentUserId="user-1"
        emptyLabel="No messages"
        onEditMessage={vi.fn()}
      />,
    );

    const tombstone = screen.getByTestId("message-tombstone");
    expect(tombstone).toHaveTextContent("This message was deleted");
    expect(screen.queryByText("gone")).not.toBeInTheDocument();
  });
});
