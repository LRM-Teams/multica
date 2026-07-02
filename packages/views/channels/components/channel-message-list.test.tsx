import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { ChannelMessage } from "@multica/core/types";
import { MessageViewport } from "./channel-message-list";

vi.mock("react-virtuoso", async () => {
  const React = await import("react");

  const MockVirtuoso = React.forwardRef(
    (
      {
        components = {},
        data = [],
        itemContent,
        initialTopMostItemIndex,
      }: {
        components?: {
          Footer?: React.ComponentType;
          Header?: React.ComponentType;
          List?: React.ComponentType<React.HTMLAttributes<HTMLDivElement>>;
        };
        data?: ChannelMessage[];
        initialTopMostItemIndex?: number;
        itemContent: (index: number, item: ChannelMessage) => React.ReactNode;
      },
      ref: React.ForwardedRef<{ scrollToIndex: () => void }>,
    ) => {
      React.useImperativeHandle(ref, () => ({ scrollToIndex: vi.fn() }));

      const Header = components.Header;
      const List = components.List ?? "div";
      const Footer = components.Footer;
      const targetIndex = Math.max(0, Math.min(initialTopMostItemIndex ?? 0, data.length - 1));
      const start = Math.max(0, Math.min(targetIndex - 1, data.length - 2));
      const windowedData = data.slice(start, start + 2);

      return (
        <div data-testid="virtuoso-scroller">
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
    author_type: "agent",
    author_id: "agent-1",
    author_name: "Research Agent",
    content,
    source: "multica",
    external_message_id: null,
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
});
