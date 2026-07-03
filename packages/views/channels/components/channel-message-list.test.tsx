import { render, screen } from "@testing-library/react";
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
      React.useImperativeHandle(ref, () => ({ scrollToIndex: scrollToIndexMock }));

      const Header = components.Header;
      const List = components.List ?? "div";
      const Footer = components.Footer;
      const localTarget = Math.max(0, (initialTopMostItemIndex ?? firstItemIndex) - firstItemIndex);
      const targetIndex = Math.max(0, Math.min(localTarget, data.length - 1));
      const start = Math.max(0, Math.min(targetIndex - 1, data.length - 2));
      const windowedData = data.slice(start, start + 2);

      return (
        <div
          data-testid="virtuoso-scroller"
          data-initial-index={initialTopMostItemIndex ?? "unset"}
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

vi.mock("../../issues/components/comment-card", () => ({
  AttachmentList: () => null,
}));

vi.mock("@multica/core/workspace/hooks", () => ({
  useActorName: () => ({
    getActorAvatarUrl: () => null,
    getActorName: () => null,
  }),
}));

// ChannelMessageBubble overlays the shared presence dot (AgentStatusDot) on
// agent avatars, which reads presence via useAgentPresenceDetail and the
// current workspace via useCurrentWorkspace. Stub both so this viewport test
// stays free of QueryClient/workspace-provider wiring.
vi.mock("@multica/core/agents", () => ({
  useAgentPresenceDetail: () => "loading",
}));

vi.mock("@multica/core/paths", () => ({
  useCurrentWorkspace: () => ({ id: "ws-1" }),
}));

vi.mock("../../i18n", () => ({
  useT: () => ({
    t: (
      selector: (resources: {
        message: { add_reaction: string; agent_badge: string; feishu_badge: string };
        quote: { jump_to: string };
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
          jump_to: "Jump to original message",
        },
        thread: {
          reply: "Reply in thread",
          reply_count: "{{count}} replies",
        },
      }),
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

  it("opens thread lists at the root context when requested", () => {
    render(
      <MessageViewport
        messages={[
          makeMessage("m1", "First thread reply"),
          makeMessage("m2", "Second thread reply"),
          makeMessage("m3", "Later thread reply"),
        ]}
        currentUserId="user-1"
        emptyLabel="No replies"
        initialScroll="top"
        header={<div data-testid="thread-root-preview">Root preview</div>}
      />,
    );

    expect(screen.getByTestId("thread-root-preview")).toBeInTheDocument();
    expect(screen.getByTestId("virtuoso-scroller")).toHaveAttribute("data-initial-index", "0");
    expect(screen.getByText("First thread reply")).toBeInTheDocument();
    expect(screen.getByText("Second thread reply")).toBeInTheDocument();
    expect(screen.queryByText("Later thread reply")).not.toBeInTheDocument();
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

  it("offsets initialTopMostItemIndex and the highlight scrollToIndex call by firstItemIndex", () => {
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

    // firstItemIndex + local index (1) of the highlighted message.
    expect(screen.getByTestId("virtuoso-scroller")).toHaveAttribute(
      "data-initial-index",
      "999999",
    );
    expect(scrollToIndexMock).toHaveBeenCalledWith(
      expect.objectContaining({ index: 999_999 }),
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
