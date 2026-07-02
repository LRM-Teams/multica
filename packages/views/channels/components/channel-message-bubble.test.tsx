import { createEvent, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { toast } from "sonner";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ChannelMessage } from "@multica/core/types";
import { ChannelMessageBubble } from "./channel-message-bubble";

const copyTextMock = vi.fn();

vi.mock("@multica/ui/lib/clipboard", () => ({
  copyText: (text: string) => copyTextMock(text),
}));

vi.mock("sonner", () => ({
  toast: {
    success: vi.fn(),
    error: vi.fn(),
  },
}));

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
  useActorName: () => ({
    getActorAvatarUrl: () => null,
    getActorName: (_type: string, id: string) =>
      id === "user-1" ? "Alice" : id === "user-2" ? "Bob" : null,
  }),
}));

vi.mock("../../i18n", () => ({
  useT: () => ({
    t: (
      selector: (resources: {
        message: {
          add_reaction: string;
          agent_badge: string;
          feishu_badge: string;
          copy_action: string;
          copied_toast: string;
          copy_failed_toast: string;
        };
        quote: { jump_to: string };
        thread: { reply: string; reply_count: string };
      }) => string,
    ) =>
      selector({
        message: {
          add_reaction: "Add reaction",
          agent_badge: "Agent",
          feishu_badge: "Feishu",
          copy_action: "Copy",
          copied_toast: "Copied",
          copy_failed_toast: "Copy failed",
        },
        quote: {
          jump_to: "Jump to original message",
        },
        thread: {
          reply: "Reply in thread",
          reply_count: "2 replies",
        },
      }),
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
  beforeEach(() => {
    copyTextMock.mockReset();
    vi.mocked(toast.success).mockReset();
    vi.mocked(toast.error).mockReset();
  });

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

  it("allows native copy context menu when message body text is selected", () => {
    render(<ChannelMessageBubble message={makeMessage()} currentUserId="user-1" />);

    const body = screen.getByTestId("message-body");
    const text = screen.getByText("Here is the data.");
    vi.spyOn(window, "getSelection").mockReturnValue({
      isCollapsed: false,
      anchorNode: text.firstChild,
      focusNode: text.firstChild,
      toString: () => "data",
    } as Selection);

    const event = createEvent.contextMenu(text);
    fireEvent(text, event);

    expect(event.defaultPrevented).toBe(false);
    expect(body).toHaveClass("select-text");
  });

  it("copies the message content from the visible action button", async () => {
    copyTextMock.mockResolvedValue(true);

    render(<ChannelMessageBubble message={makeMessage()} currentUserId="user-1" />);

    await userEvent.click(screen.getByRole("button", { name: "Copy" }));

    await waitFor(() => expect(copyTextMock).toHaveBeenCalledWith("Here is the data."));
    expect(toast.success).toHaveBeenCalledWith("Copied");
  });

  it("does not open a custom message menu from the message body", () => {
    render(
      <ChannelMessageBubble
        message={makeMessage()}
        currentUserId="user-1"
        onOpenThread={vi.fn()}
      />,
    );

    vi.spyOn(window, "getSelection").mockReturnValue(null);
    const event = createEvent.contextMenu(screen.getByTestId("message-body"));
    fireEvent(screen.getByTestId("message-body"), event);

    expect(event.defaultPrevented).toBe(false);
  });

  it("does not open a custom message menu from row padding", () => {
    render(
      <ChannelMessageBubble
        message={makeMessage()}
        currentUserId="user-1"
        onOpenThread={vi.fn()}
      />,
    );

    vi.spyOn(window, "getSelection").mockReturnValue(null);
    const event = createEvent.contextMenu(screen.getByTestId("message-bubble"));
    fireEvent(screen.getByTestId("message-bubble"), event);

    expect(event.defaultPrevented).toBe(false);
  });

  it("only renders existing reaction chips in the footer", () => {
    render(
      <ChannelMessageBubble
        message={makeMessage({
          reactions: [
            {
              id: "reaction-1",
              channel_id: "c1",
              message_id: "m1",
              actor_type: "member",
              actor_id: "user-1",
              emoji: "👍",
              created_at: "2026-06-17T09:16:00Z",
            },
          ],
        })}
        currentUserId="user-1"
        onReact={vi.fn()}
      />,
    );

    expect(screen.getByRole("button", { name: "👍1" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "React with ❤️" })).not.toBeInTheDocument();
  });

  it("renders the thread chip before reaction chips in the footer", () => {
    render(
      <ChannelMessageBubble
        message={makeMessage({
          thread_reply_count: 2,
          reactions: [
            {
              id: "reaction-1",
              channel_id: "c1",
              message_id: "m1",
              actor_type: "member",
              actor_id: "user-2",
              emoji: "👍",
              created_at: "2026-06-17T09:16:00Z",
            },
          ],
        })}
        currentUserId="user-1"
        onOpenThread={vi.fn()}
        onReact={vi.fn()}
      />,
    );

    const footer = screen.getByRole("button", { name: /2 replies/ }).parentElement;
    expect(footer?.children[0]).toHaveTextContent("2 replies");
    expect(footer?.children[1]).toHaveTextContent("👍1");
  });

  it("labels the current user as You in reaction attribution", () => {
    render(
      <ChannelMessageBubble
        message={makeMessage({
          reactions: [
            {
              id: "reaction-1",
              channel_id: "c1",
              message_id: "m1",
              actor_type: "member",
              actor_id: "user-2",
              emoji: "👍",
              created_at: "2026-06-17T09:16:00Z",
            },
            {
              id: "reaction-2",
              channel_id: "c1",
              message_id: "m1",
              actor_type: "member",
              actor_id: "user-1",
              emoji: "👍",
              created_at: "2026-06-17T09:17:00Z",
            },
          ],
        })}
        currentUserId="user-1"
        onReact={vi.fn()}
      />,
    );

    expect(screen.getByRole("button", { name: "👍2" })).toHaveAttribute(
      "title",
      "You, Bob",
    );
  });

  it("keeps first-level actions on the visible action surface only", () => {
    render(
      <ChannelMessageBubble
        message={makeMessage()}
        currentUserId="user-1"
        onOpenThread={vi.fn()}
        onReact={vi.fn()}
      />,
    );

    expect(screen.getByRole("button", { name: "Add reaction" })).toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: "Copy" })).toHaveLength(1);
    expect(screen.getByRole("button", { name: "Reply in thread" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Quote reply" })).not.toBeInTheDocument();
  });
});
