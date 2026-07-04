import { createEvent, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import userEvent from "@testing-library/user-event";
import { toast } from "sonner";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ChannelMessage } from "@multica/core/types";
import { stickerCatalogKeys } from "@multica/core/stickers";
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
    enableStickerShortcodes,
    highlightQuery,
  }: {
    children: string;
    enableStickerShortcodes?: boolean;
    highlightQuery?: string;
  }) => (
    <span
      data-highlight-query={highlightQuery}
      data-enable-sticker-shortcodes={String(enableStickerShortcodes ?? true)}
    >
      {children}
    </span>
  ),
}));

vi.mock("../../issues/components/comment-card", () => ({
  AttachmentList: () => null,
}));

// Agent avatars now overlay the shared presence dot (AgentStatusDot), which
// reads presence via useAgentPresenceDetail and the current workspace via
// useCurrentWorkspace. Default to a concrete "online + idle" detail so the
// dot renders in most tests; the dedicated presence test below overrides it
// per-case via the mock's return value.
const presenceDetailMock = vi.fn(() => ({
  availability: "online" as const,
  workload: "idle" as const,
  runningCount: 0,
  queuedCount: 0,
  capacity: 1,
}));

vi.mock("@multica/core/agents", () => ({
  useAgentPresenceDetail: () => presenceDetailMock(),
}));

vi.mock("@multica/core/paths", () => ({
  useCurrentWorkspace: () => ({ id: "ws-1" }),
}));

// The bubble resolves the author's live avatar from the members/agents cache.
// Stub it so these layout/identity tests don't need a QueryClient/workspace.
vi.mock("@multica/core/workspace/hooks", () => ({
  useActorName: () => ({
    getActorAvatarUrl: () => null,
    getActorName: (_type: string, id: string, fallback?: string) =>
      id === "user-1" ? "Alice Display" : id === "user-2" ? "Bob Display" : fallback,
  }),
}));

vi.mock("../../i18n/use-t", () => ({
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
          sticker_alt: string;
          sticker_loading: string;
          sticker_failed: string;
          sticker_unavailable: string;
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
          sticker_alt: "Sticker",
          sticker_loading: "Loading sticker",
          sticker_failed: "Sticker failed to load",
          sticker_unavailable: "Sticker unavailable",
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
  const seq = overrides.seq ?? 1;
  return {
    id: "m1",
    channel_id: "c1",
    workspace_id: "w1",
    type: "agent",
    author_id: "agent-1",
    author_name: "Research Agent",
    content: "Here is the data.",
    source: "multica",
    external_message_id: null,
    client_message_id: null,
    created_at: "2026-06-17T09:15:00Z",
    ...overrides,
    seq,
  };
}

function renderWithStickerCatalog(ui: ReactNode) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  queryClient.setQueryData(stickerCatalogKeys.catalog(), {
    stickers: [],
    license: "",
    source: "",
    packs: [
      {
        id: "builtin",
        name: "Built-in stickers",
        source: "",
        license: "",
        stickers: [
          {
            pack_id: "builtin",
            sticker_id: "hi",
            name: "Hi",
            name_en: "Hi",
            emotion: "greeting",
            asset_url: "/api/stickers/hi",
            mime_type: "image/jpeg",
            alt: "Hi sticker",
            tags: ["hi"],
            animated: false,
          },
        ],
      },
    ],
  });
  return render(<QueryClientProvider client={queryClient}>{ui}</QueryClientProvider>);
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
      type: "user",
      author_id: "user-1",
      author_name: "alice",
      content: "Please summarize Q2.",
    });
    render(<ChannelMessageBubble message={msg} currentUserId="user-1" />);

    expect(screen.getByTestId("message-bubble")).toHaveAttribute("data-own", "true");
    expect(screen.getByText("Alice Display")).toBeInTheDocument();
    expect(screen.queryByText("alice")).not.toBeInTheDocument();
    expect(screen.queryByText("Agent")).not.toBeInTheDocument();
    expect(screen.getByText("Please summarize Q2.")).toBeInTheDocument();
  });

  it("treats another user's message as not own", () => {
    const msg = makeMessage({ type: "user", author_id: "user-2", author_name: "bob" });
    render(<ChannelMessageBubble message={msg} currentUserId="user-1" />);

    expect(screen.getByTestId("message-bubble")).toHaveAttribute("data-own", "false");
    expect(screen.getByText("Bob Display")).toBeInTheDocument();
  });

  it("shows a presence status dot on agent message avatars only", () => {
    const { rerender } = render(
      <ChannelMessageBubble message={makeMessage()} currentUserId="user-1" />,
    );
    expect(screen.getByLabelText(/^Status:/)).toBeInTheDocument();

    rerender(
      <ChannelMessageBubble
        message={makeMessage({ type: "user", author_id: "user-1", author_name: "alice" })}
        currentUserId="user-1"
      />,
    );
    expect(screen.queryByLabelText(/^Status:/)).not.toBeInTheDocument();
  });

  it("resolves quoted reply author names through live identity", () => {
    render(
      <ChannelMessageBubble
        message={makeMessage({
          type: "user",
          author_id: "user-2",
          author_name: "bob",
          reply_to_message_id: "m0",
          reply_to: {
            id: "m0",
            type: "user",
            author_id: "user-1",
            author_name: "alice",
            content: "Earlier point",
            created_at: "2026-06-17T09:10:00Z",
          },
        })}
        currentUserId="user-2"
      />,
    );

    expect(screen.getByText("Alice Display")).toBeInTheDocument();
    expect(screen.queryByText("alice")).not.toBeInTheDocument();
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

  it("renders sticker parts from the catalog without showing content fallback", () => {
    renderWithStickerCatalog(
      <ChannelMessageBubble
        message={makeMessage({
          content: ":sticker:hi:",
          parts: [{ type: "sticker", sticker_id: "hi", alt: "Hi sticker" }],
        })}
        currentUserId="user-1"
      />,
    );

    const sticker = screen.getByTestId("message-sticker");
    expect(sticker).toHaveAttribute("src", "/api/stickers/hi");
    expect(sticker).toHaveAttribute("alt", "Hi sticker");
    expect(screen.queryByText(":sticker:hi:")).not.toBeInTheDocument();
  });

  it("does not enable content shortcode fallback for messages without parts", () => {
    render(
      <ChannelMessageBubble
        message={makeMessage({ content: ":sticker:hi:" })}
        currentUserId="user-1"
      />,
    );

    expect(screen.getByText(":sticker:hi:")).toHaveAttribute(
      "data-enable-sticker-shortcodes",
      "false",
    );
  });

  it("renders controlled unavailable state for unknown sticker parts", () => {
    renderWithStickerCatalog(
      <ChannelMessageBubble
        message={makeMessage({
          content: ":sticker:missing-sticker:",
          parts: [{ type: "sticker", sticker_id: "missing-sticker" }],
        })}
        currentUserId="user-1"
      />,
    );

    expect(screen.getByTestId("message-sticker-placeholder")).toHaveTextContent("Sticker unavailable");
    expect(screen.queryByText("missing-sticker")).not.toBeInTheDocument();
    expect(screen.queryByText(":sticker:missing-sticker:")).not.toBeInTheDocument();
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

  it("copies structured message parts instead of hidden fallback content", async () => {
    copyTextMock.mockResolvedValue(true);

    renderWithStickerCatalog(
      <ChannelMessageBubble
        message={makeMessage({
          content: ":sticker:hi:",
          parts: [
            { type: "text", text: "Done" },
            { type: "sticker", sticker_id: "hi", alt: "Hi sticker" },
          ],
        })}
        currentUserId="user-1"
      />,
    );

    await userEvent.click(screen.getByRole("button", { name: "Copy" }));

    await waitFor(() => expect(copyTextMock).toHaveBeenCalledWith("Done [Sticker] Hi sticker"));
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
      "You, Bob Display",
    );
  });

  // B4 (#242) — reaction 4-carrier consistency. Channel / dm_channel / thread
  // all render their reactions through this same bubble, so its picker→pill
  // aggregate (emoji + count, self-highlight) is the shared contract the issue
  // carrier (CommentCard) also delegates to via the same ReactionBar primitive.
  it("self-highlights the viewer's own reaction pill but not a peer-only pill", () => {
    render(
      <ChannelMessageBubble
        message={makeMessage({
          reactions: [
            {
              id: "reaction-own",
              channel_id: "c1",
              message_id: "m1",
              actor_type: "member",
              actor_id: "user-1",
              emoji: "👍",
              created_at: "2026-06-17T09:16:00Z",
            },
            {
              id: "reaction-peer",
              channel_id: "c1",
              message_id: "m1",
              actor_type: "member",
              actor_id: "user-2",
              emoji: "🎉",
              created_at: "2026-06-17T09:17:00Z",
            },
          ],
        })}
        currentUserId="user-1"
        onReact={vi.fn()}
      />,
    );

    // Own reaction → brand-highlighted; peer-only reaction → muted, not brand.
    expect(screen.getByRole("button", { name: "👍1" })).toHaveClass("text-brand");
    const peerPill = screen.getByRole("button", { name: "🎉1" });
    expect(peerPill).not.toHaveClass("text-brand");
    expect(peerPill).toHaveClass("text-muted-foreground");
  });

  it("toggles through onReact when an existing reaction pill is clicked (no send/dispatch path)", async () => {
    const onReact = vi.fn();
    const message = makeMessage({
      reactions: [
        {
          id: "reaction-own",
          channel_id: "c1",
          message_id: "m1",
          actor_type: "member",
          actor_id: "user-1",
          emoji: "👍",
          created_at: "2026-06-17T09:16:00Z",
        },
      ],
    });
    render(
      <ChannelMessageBubble message={message} currentUserId="user-1" onReact={onReact} />,
    );

    await userEvent.click(screen.getByRole("button", { name: "👍1" }));

    // The bubble surfaces reactions only through onReact — it has no send /
    // dispatch affordance, so a react can never produce a wake here.
    expect(onReact).toHaveBeenCalledTimes(1);
    expect(onReact).toHaveBeenCalledWith(message, "👍");
  });

  it("reacts through onReact when an emoji is chosen from the picker", async () => {
    const onReact = vi.fn();
    const message = makeMessage();
    render(
      <ChannelMessageBubble message={message} currentUserId="user-1" onReact={onReact} />,
    );

    await userEvent.click(screen.getByRole("button", { name: "Add reaction" }));
    await userEvent.click(screen.getByRole("button", { name: "🎉" }));

    expect(onReact).toHaveBeenCalledWith(message, "🎉");
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

  it("renders system messages as notice rows without chat bubble actions", () => {
    render(
      <ChannelMessageBubble
        message={makeMessage({
          type: "system",
          author_id: null,
          author_name: "System",
          content: "Barry archived this channel.",
          thread_reply_count: 2,
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
        onOpenThread={vi.fn()}
        onReact={vi.fn()}
      />,
    );

    expect(screen.getByTestId("system-message-row")).toHaveTextContent(
      "Barry archived this channel.",
    );
    expect(screen.queryByTestId("message-bubble")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Add reaction" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Copy" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Reply in thread" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /2 replies/ })).not.toBeInTheDocument();
  });

  it("does not render runtime state as a conversation system row", () => {
    const { container } = render(
      <ChannelMessageBubble
        message={makeMessage({
          type: "system",
          author_id: null,
          author_name: "System",
          content: "Local daemon is outdated.",
        })}
        currentUserId="user-1"
      />,
    );

    expect(container).toBeEmptyDOMElement();
    expect(screen.queryByTestId("system-message-row")).not.toBeInTheDocument();
  });

  it("does not leak raw runtime notice content in the conversation", () => {
    const { container } = render(
      <ChannelMessageBubble
        message={makeMessage({
          type: "system",
          author_id: null,
          author_name: "System",
          content: "daemon_outdated",
        })}
        currentUserId="user-1"
      />,
    );

    expect(container).toBeEmptyDOMElement();
    expect(screen.queryByText("daemon_outdated")).not.toBeInTheDocument();
  });
});
