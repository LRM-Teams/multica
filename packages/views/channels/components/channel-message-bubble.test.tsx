import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { ChannelMessage } from "@multica/core/types";
import { ChannelMessageBubble } from "./channel-message-bubble";

// The bubble delegates body rendering to the shared Markdown pipeline (tested
// separately). Stub it to a passthrough so these tests focus on bubble layout
// and identity styling, not markdown internals.
vi.mock("../../common/markdown", () => ({
  MemoizedMarkdown: ({
    children,
    highlightQuery,
  }: {
    children: string;
    highlightQuery?: string;
  }) => <span data-highlight-query={highlightQuery}>{children}</span>,
}));

vi.mock("../../issues/components/comment-card", () => ({
  AttachmentList: () => null,
}));

// The bubble resolves the author's live avatar from the members/agents cache.
// Stub it so these layout/identity tests don't need a QueryClient/workspace.
vi.mock("@multica/core/workspace/hooks", () => ({
  useActorName: () => ({ getActorAvatarUrl: () => null }),
}));

vi.mock("../../i18n", () => ({
  useT: () => ({
    t: (selector: (resources: { message: { agent_badge: string; feishu_badge: string } }) => string) =>
      selector({ message: { agent_badge: "Agent", feishu_badge: "Feishu" } }),
  }),
}));

function makeMessage(overrides: Partial<ChannelMessage> = {}): ChannelMessage {
  return {
    id: "m1",
    channel_id: "c1",
    workspace_id: "w1",
    author_type: "agent",
    author_id: "agent-1",
    author_name: "Research Agent",
    content: "Here is the data.",
    source: "multica",
    external_message_id: null,
    created_at: "2026-06-17T09:15:00Z",
    ...overrides,
  };
}

describe("ChannelMessageBubble", () => {
  it("renders an agent message left-aligned with an Agent pill, name and body", () => {
    render(<ChannelMessageBubble message={makeMessage()} currentUserId="user-1" />);

    expect(screen.getByText("Research Agent")).toBeInTheDocument();
    expect(screen.getByText("Agent")).toBeInTheDocument();
    expect(screen.getByText("Here is the data.")).toBeInTheDocument();
    expect(screen.getByTestId("message-bubble")).toHaveAttribute("data-own", "false");
  });

  it("renders the current user's own message right-aligned without an Agent pill", () => {
    const msg = makeMessage({
      author_type: "user",
      author_id: "user-1",
      author_name: "Alice",
      content: "Please summarize Q2.",
    });
    render(<ChannelMessageBubble message={msg} currentUserId="user-1" />);

    expect(screen.getByTestId("message-bubble")).toHaveAttribute("data-own", "true");
    expect(screen.queryByText("Agent")).not.toBeInTheDocument();
    expect(screen.getByText("Please summarize Q2.")).toBeInTheDocument();
  });

  it("treats another user's message as not own", () => {
    const msg = makeMessage({ author_type: "user", author_id: "user-2", author_name: "Bob" });
    render(<ChannelMessageBubble message={msg} currentUserId="user-1" />);

    expect(screen.getByTestId("message-bubble")).toHaveAttribute("data-own", "false");
    expect(screen.getByText("Bob")).toBeInTheDocument();
  });

  it("passes the search query to markdown only for search hits", () => {
    const { rerender } = render(
      <ChannelMessageBubble
        message={makeMessage()}
        currentUserId="user-1"
        searchHighlighted
        searchQuery="data"
      />,
    );

    expect(screen.getByText("Here is the data.")).toHaveAttribute("data-highlight-query", "data");

    rerender(
      <ChannelMessageBubble
        message={makeMessage()}
        currentUserId="user-1"
        searchHighlighted={false}
        searchQuery="data"
      />,
    );

    expect(screen.getByText("Here is the data.")).not.toHaveAttribute("data-highlight-query");
  });
});
