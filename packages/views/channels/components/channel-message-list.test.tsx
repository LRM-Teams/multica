import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { ChannelMessage } from "@multica/core/types";
import { MessageViewport } from "./channel-message-list";

vi.mock("react-virtuoso", () => ({
  Virtuoso: ({
    data,
    itemContent,
    components,
  }: {
    data: ChannelMessage[];
    itemContent: (index: number, message: ChannelMessage) => React.ReactNode;
    components?: {
      Header?: () => React.ReactNode;
      Footer?: () => React.ReactNode;
    };
  }) => (
    <div data-testid="virtuoso-item-list">
      {components?.Header?.()}
      {data.map((message, index) => (
        <div key={message.id} data-testid="virtuoso-row">
          {itemContent(index, message)}
        </div>
      ))}
      {components?.Footer?.()}
    </div>
  ),
}));

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
        quote: { jump_to: string; reply: string; reply_aria: string; more_aria: string };
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
          reply: "Reply",
          reply_aria: "Reply to message",
          more_aria: "More actions",
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
  it("renders message items when messages are present", () => {
    render(
      <MessageViewport
        messages={[
          makeMessage("m1", "First visible message"),
          makeMessage("m2", "Second visible message"),
        ]}
        currentUserId="user-1"
        emptyLabel="No messages"
      />,
    );

    expect(screen.getAllByTestId("message-bubble")).toHaveLength(2);
    expect(screen.getByText("First visible message")).toBeInTheDocument();
    expect(screen.getByText("Second visible message")).toBeInTheDocument();
  });
});
