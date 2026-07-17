import { createEvent, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
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
  // A message carrying reference parts renders its body through the projector,
  // which reaches for ActorMention — stub it so these tests stay on the bubble.
  ActorMention: ({ label }: { label: string }) => <span>{label}</span>,
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
  // Dot COLOR source (#266). No summary in tests → the dot falls back to the
  // availability color, keeping presence assertions stable.
  useAgentHealth: () => ({
    summary: undefined,
    events: undefined,
    isLoading: false,
    isError: false,
  }),
}));

vi.mock("@multica/core/paths", () => ({
  useCurrentWorkspace: () => ({ id: "ws-1" }),
  // Needed once a system row projects anchored issue refs into linked tokens.
  useWorkspacePaths: () => ({
    issueDetail: (issueId: string) => `/acme/issues/${issueId}`,
  }),
}));

// A projected issue token resolves the LIVE issue for its peek (#504), which would
// drag a QueryClient into these layout tests. Unresolved → plain link token, which
// is exactly what the system-row projection test below asserts.
vi.mock("../../issues/components/issue-chip", () => ({
  useResolvedIssue: () => undefined,
}));

// Projected issue tokens render through AppLink, which needs a NavigationProvider.
vi.mock("../../navigation/app-link", () => ({
  AppLink: ({
    href,
    children,
    className,
  }: {
    href: string;
    children: React.ReactNode;
    className?: string;
  }) => (
    <a href={href} className={className}>
      {children}
    </a>
  ),
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

// The bubble reads the author avatar straight from the payload (#453/#574) via
// resolvePublicFileUrl, which needs api.getBaseUrl(); stub it to pass the raw
// value through so tests don't touch the api base-url machinery.
vi.mock("@multica/core/workspace/avatar-url", () => ({
  resolvePublicFileUrl: (url: string | null | undefined) => url ?? null,
}));

vi.mock("../../common/use-viewing-timezone", () => ({
  useViewingTimezone: () => "UTC",
}));

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (
      selector: (resources: {
        message: {
          add_reaction: string;
          agent_badge: string;
          radar_badge: string;
          feishu_badge: string;
          copy_action: string;
          expand_action: string;
          copied_toast: string;
          copy_failed_toast: string;
          sticker_alt: string;
          sticker_loading: string;
          sticker_failed: string;
          sticker_unavailable: string;
          edit_action: string;
          actions_menu: string;
          delete_action: string;
          edited_label: string;
          deleted_placeholder: string;
          save_edit: string;
          cancel_edit: string;
          system_event: {
            issue: {
              actor_system: string;
              assigned: string;
              assigned_unknown: string;
              in_progress: string;
              in_review: string;
              done: string;
              status: string;
            };
            issue_status: {
              backlog: string;
              todo: string;
              in_progress: string;
              in_review: string;
              done: string;
              blocked: string;
              cancelled: string;
            };
          };
        };
        quote: {
          action: string;
          jump_to: string;
          cancel: string;
          unavailable_title: string;
          unavailable_summary: string;
          type_user: string;
          type_agent: string;
          type_lark: string;
          type_system: string;
          type_unknown: string;
          attachment_summary: string;
          attachments_summary: string;
          image_summary: string;
          images_summary: string;
          empty_summary: string;
        };
        thread: { reply: string; reply_count: string };
        time: { today: string; yesterday: string };
      }) => string,
      options?: Record<string, unknown>,
    ) => {
      const raw = selector({
        message: {
          add_reaction: "Add reaction",
          agent_badge: "Agent",
          radar_badge: "Project Radar",
          feishu_badge: "Feishu",
          actions_menu: "Message actions",
          copy_action: "Copy",
          expand_action: "Show full message",
          copied_toast: "Copied",
          copy_failed_toast: "Copy failed",
          sticker_alt: "Sticker",
          sticker_loading: "Loading sticker",
          sticker_failed: "Sticker failed to load",
          sticker_unavailable: "Sticker unavailable",
          edit_action: "Edit",
          delete_action: "Delete",
          edited_label: "(edited)",
          deleted_placeholder: "This message was deleted",
          save_edit: "Save",
          cancel_edit: "Cancel",
          system_event: {
            issue: {
              actor_system: "Multica",
              assigned: "{{actor}} assigned {issue} to {{target}}",
              assigned_unknown: "{{actor}} changed the assignee of {issue}",
              in_progress: "{{actor}} started {issue}",
              in_review: "{{actor}} sent {issue} for review",
              done: "{{actor}} completed {issue}",
              status: "{{actor}} marked {issue} as {{status}}",
            },
            issue_status: {
              backlog: "Backlog",
              todo: "To do",
              in_progress: "In progress",
              in_review: "In review",
              done: "Done",
              blocked: "Blocked",
              cancelled: "Cancelled",
            },
          },
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
          attachments_summary: "Attachments",
          image_summary: "Image",
          images_summary: "Images",
          empty_summary: "No preview available",
        },
        thread: {
          reply: "Reply in thread",
          reply_count: "2 replies",
        },
        time: { today: "Today", yesterday: "Yesterday" },
      });
      return options
        ? raw.replace(/\{\{(\w+)\}\}/g, (_, key: string) => String(options[key] ?? ""))
        : raw;
    },
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
  const setMobileViewport = () => {
    Object.defineProperty(window, "innerWidth", { configurable: true, value: 390 });
    Object.defineProperty(window, "matchMedia", {
      configurable: true,
      value: vi.fn((query: string) => ({
        matches: query.includes("max-width") || query.includes("pointer"),
        media: query,
        onchange: null,
        addListener: vi.fn(),
        removeListener: vi.fn(),
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
        dispatchEvent: vi.fn(),
      })),
    });
  };

  beforeEach(() => {
    copyTextMock.mockReset();
    vi.mocked(toast.success).mockReset();
    vi.mocked(toast.error).mockReset();
    Object.defineProperty(window, "innerWidth", { configurable: true, value: 1024 });
    Object.defineProperty(window, "matchMedia", {
      configurable: true,
      value: vi.fn((query: string) => ({
        matches: false,
        media: query,
        onchange: null,
        addListener: vi.fn(),
        removeListener: vi.fn(),
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
        dispatchEvent: vi.fn(),
      })),
    });
  });

  it("renders an agent message left-aligned with an Agent pill, name and body", () => {
    render(<ChannelMessageBubble message={makeMessage()} currentUserId="user-1" />);

    expect(screen.getByText("Research Agent")).toBeInTheDocument();
    expect(screen.getByText("Agent")).toBeInTheDocument();
    expect(screen.getByText("Here is the data.")).toBeInTheDocument();
    expect(screen.getByTestId("message-bubble")).toHaveAttribute("data-own", "false");
  });

  it("renders the author avatar straight from the message payload (#453), not a viewer-scoped lookup", () => {
    render(
      <ChannelMessageBubble
        message={makeMessage({ author_avatar_url: "/uploads/agent-avatar.png" })}
        currentUserId="user-1"
      />,
    );
    // The avatar image comes from the payload's `author_avatar_url` (aggregated
    // by the BE for every viewer, #574) — so a group member sees the author's
    // real avatar instead of the default bot; no `getActorAvatarUrl` guess.
    const img = screen.getByRole("img", { name: /Research Agent/i });
    expect(img).toHaveAttribute("src", "/uploads/agent-avatar.png");
  });

  it("marks proactive radar messages with a Project Radar pill", () => {
    render(
      <ChannelMessageBubble
        message={makeMessage({ content: "主动发现：CI has failed twice." })}
        currentUserId="user-1"
      />,
    );

    expect(screen.getByText("Project Radar")).toBeInTheDocument();
  });

  it("scopes the message body as message-surface for Slack-aligned image caps", () => {
    render(<ChannelMessageBubble message={makeMessage()} currentUserId="user-1" />);
    expect(screen.getByTestId("message-body")).toHaveClass("message-surface");
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

  it("applies a cool self-mention wash when someone else @-mentions the viewer", () => {
    const msg = makeMessage({
      type: "user",
      author_id: "user-2",
      author_name: "bob",
      content: "hey [@Alice](mention://member/user-1) please look",
    });
    render(<ChannelMessageBubble message={msg} currentUserId="user-1" />);

    const bubble = screen.getByTestId("message-bubble");
    expect(bubble).toHaveAttribute("data-self-mentioned", "true");
    expect(bubble.className).toContain("bg-brand/[0.04]");
  });

  it("applies the self-mention wash for @all from another author", () => {
    const msg = makeMessage({
      type: "user",
      author_id: "user-2",
      author_name: "bob",
      content: "[@all](mention://all/all) standup in 5",
    });
    render(<ChannelMessageBubble message={msg} currentUserId="user-1" />);

    expect(screen.getByTestId("message-bubble")).toHaveAttribute(
      "data-self-mentioned",
      "true",
    );
  });

  it("does not wash the viewer's own messages even when they @ themselves or @all", () => {
    const selfPing = makeMessage({
      type: "user",
      author_id: "user-1",
      author_name: "alice",
      content: "note to self [@Alice](mention://member/user-1)",
    });
    const { rerender } = render(
      <ChannelMessageBubble message={selfPing} currentUserId="user-1" />,
    );
    expect(screen.getByTestId("message-bubble")).not.toHaveAttribute(
      "data-self-mentioned",
    );
    expect(screen.getByTestId("message-bubble").className).not.toContain(
      "bg-brand/[0.04]",
    );

    const ownAll = makeMessage({
      type: "user",
      author_id: "user-1",
      author_name: "alice",
      content: "[@all](mention://all/all) heads up",
    });
    rerender(<ChannelMessageBubble message={ownAll} currentUserId="user-1" />);
    expect(screen.getByTestId("message-bubble")).not.toHaveAttribute(
      "data-self-mentioned",
    );
  });

  it("does not self-mention-wash messages that only mention others", () => {
    const msg = makeMessage({
      type: "user",
      author_id: "user-2",
      author_name: "bob",
      content: "hey [@Carol](mention://member/user-3) and [@Bot](mention://agent/agent-1)",
    });
    render(<ChannelMessageBubble message={msg} currentUserId="user-1" />);

    const bubble = screen.getByTestId("message-bubble");
    expect(bubble).not.toHaveAttribute("data-self-mentioned");
    expect(bubble.className).not.toContain("bg-brand/[0.04]");
  });

  it("lets deep-link highlight take visual priority over the self-mention wash", () => {
    const msg = makeMessage({
      type: "user",
      author_id: "user-2",
      author_name: "bob",
      content: "hey [@Alice](mention://member/user-1)",
    });
    render(
      <ChannelMessageBubble message={msg} currentUserId="user-1" highlighted />,
    );

    const bubble = screen.getByTestId("message-bubble");
    expect(bubble).toHaveAttribute("data-self-mentioned", "true");
    expect(bubble.className).toContain("bg-primary/10");
    expect(bubble.className).toContain("ring-primary/25");
  });

  it("never shows a live presence dot on message avatars (presence 不进消息历史, #477)", () => {
    // A message is history — pinning "online right now" onto a historical row is
    // both the noisiest column in the view and semantically wrong. Presence now
    // lives only on directory surfaces (sidebar / member list) and the header
    // status word, never the message stream (Parker/Iris final principle).
    const { rerender } = render(
      <ChannelMessageBubble message={makeMessage()} currentUserId="user-1" />,
    );
    expect(screen.queryByLabelText(/^Status:/)).not.toBeInTheDocument();

    rerender(
      <ChannelMessageBubble
        message={makeMessage({ type: "user", author_id: "user-1", author_name: "alice" })}
        currentUserId="user-1"
      />,
    );
    expect(screen.queryByLabelText(/^Status:/)).not.toBeInTheDocument();
  });

  it("resolves quoted snapshot author names through live identity", () => {
    render(
      <ChannelMessageBubble
        message={makeMessage({
          type: "user",
          author_id: "user-2",
          author_name: "bob",
          quote_message_id: "m0",
          quote: {
            messageId: "m0",
            status: "active",
            snapshot: {
              type: "user",
              authorId: "user-1",
              authorName: "alice",
              content: "Earlier point",
              createdAt: "2026-06-17T09:10:00Z",
            },
          },
        })}
        currentUserId="user-2"
      />,
    );

    expect(screen.getByText("Alice Display")).toBeInTheDocument();
    expect(screen.getByText("Message")).toBeInTheDocument();
    expect(screen.queryByText("alice")).not.toBeInTheDocument();
  });

  it("starts quoting from the desktop message context menu", async () => {
    const onQuote = vi.fn();
    render(<ChannelMessageBubble message={makeMessage()} currentUserId="user-1" onQuote={onQuote} />);

    fireEvent.contextMenu(screen.getByTestId("message-bubble"));
    await userEvent.click(await screen.findByRole("menuitem", { name: /Quote/ }));

    expect(onQuote).toHaveBeenCalledWith(expect.objectContaining({ id: "m1" }));
  });

  it("renders deleted and inaccessible quote fallbacks", () => {
    const { rerender } = render(
      <ChannelMessageBubble
        message={makeMessage({
          quote_message_id: "m0",
          quote: { messageId: "m0", status: "deleted" },
        })}
        currentUserId="user-2"
      />,
    );

    expect(screen.getByText("Original message unavailable")).toBeInTheDocument();

    rerender(
      <ChannelMessageBubble
        message={makeMessage({ quote_message_id: "missing", quote: null })}
        currentUserId="user-2"
      />,
    );

    expect(screen.getByText("It may have been deleted or you may not have access.")).toBeInTheDocument();
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

  it("keeps long history as full DOM content behind a readable collapsed preview", async () => {
    render(
      <ChannelMessageBubble
        message={makeMessage({ content: Array.from({ length: 13 }, (_, index) => `Line ${index}`).join("\n") })}
        currentUserId="user-1"
        collapseLongContent
      />,
    );

    const body = screen.getByTestId("message-body");
    expect(body).toHaveAttribute("data-collapsed", "true");
    expect(body).toHaveClass("max-h-[min(260px,55vh)]");
    expect(body).toHaveTextContent("Line 12");

    await userEvent.click(screen.getByRole("button", { name: "Show full message" }));

    expect(body).not.toHaveAttribute("data-collapsed");
    expect(screen.queryByRole("button", { name: "Show full message" })).not.toBeInTheDocument();
  });

  it("does not show the history collapse affordance for short messages", () => {
    render(
      <ChannelMessageBubble
        message={makeMessage({ content: "Short historical answer" })}
        currentUserId="user-1"
        collapseLongContent
      />,
    );

    expect(screen.getByTestId("message-body")).not.toHaveAttribute("data-collapsed");
    expect(screen.queryByRole("button", { name: "Show full message" })).not.toBeInTheDocument();
  });

  it("copies the message content from the visible action button", async () => {
    copyTextMock.mockResolvedValue(true);

    render(<ChannelMessageBubble message={makeMessage()} currentUserId="user-1" />);

    await userEvent.click(screen.getByRole("button", { name: "Copy" }));

    await waitFor(() => expect(copyTextMock).toHaveBeenCalledWith("Here is the data."));
    expect(toast.success).toHaveBeenCalledWith("Copied");
  });

  // #250 — historical agent messages whose denormalized `parts` were never
  // backfilled carry the structured-action envelope JSON in `content`. The body
  // must unwrap it to its REAL parts (never render raw JSON) and copy the real
  // text, while ordinary non-envelope content stays completely unchanged.
  const ENVELOPE_CONTENT =
    '{"action":"message_send","output":"hi","parts":[{"type":"text","text":"hi there"}]}';

  it("renders unwrapped envelope parts for a historical message, never raw JSON", () => {
    render(
      <ChannelMessageBubble
        message={makeMessage({ content: ENVELOPE_CONTENT, parts: [] })}
        currentUserId="user-1"
      />,
    );

    const body = screen.getByTestId("message-body");
    expect(body).toHaveTextContent("hi there");
    expect(body.textContent).not.toContain('"action"');
    expect(body.textContent).not.toContain("{");
    expect(screen.queryByText(ENVELOPE_CONTENT)).not.toBeInTheDocument();
  });

  it("copies the unwrapped envelope text for a historical message, not raw JSON", async () => {
    copyTextMock.mockResolvedValue(true);

    render(
      <ChannelMessageBubble
        message={makeMessage({ content: ENVELOPE_CONTENT, parts: [] })}
        currentUserId="user-1"
      />,
    );

    await userEvent.click(screen.getByRole("button", { name: "Copy" }));

    await waitFor(() => expect(copyTextMock).toHaveBeenCalledWith("hi there"));
    const copied = copyTextMock.mock.calls[0]?.[0] as string;
    expect(copied).not.toContain('"action"');
    expect(copied).not.toContain("{");
  });

  it("leaves body and copy unchanged for ordinary non-envelope content", async () => {
    copyTextMock.mockResolvedValue(true);

    render(
      <ChannelMessageBubble
        message={makeMessage({ content: "just a plain message" })}
        currentUserId="user-1"
      />,
    );

    expect(screen.getByText("just a plain message")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Copy" }));
    await waitFor(() => expect(copyTextMock).toHaveBeenCalledWith("just a plain message"));
  });

  it("copies the display name, not the internal handle (#530 — clipboard must match the screen)", async () => {
    // Iris's ruling: copy = take away what I can see. The screen says "Alice
    // Display"; a clipboard holding "@user_raw" disagrees with it, and that is its
    // own kind of lying.
    //
    // This asserts the WIRING at this surface, not the projection —
    // projectReferencesToText is covered in message-preview.test.ts. What can break
    // here silently is the call itself: delete it and copy falls back to raw
    // content, the leak returns, and CI stays green. (The preceding test is the
    // control: an ordinary message must still copy verbatim.)
    copyTextMock.mockResolvedValue(true);

    render(
      <ChannelMessageBubble
        message={makeMessage({
          content: "ping @user_raw now",
          parts: [
            {
              type: "reference",
              ref_type: "mention",
              ref_subtype: "member",
              ref_id: "user-1",
              label: "@user_raw",
              content_start_utf16: 5,
              content_end_utf16: 14,
            },
          ],
        } as never)}
        currentUserId="user-2"
      />,
    );

    await userEvent.click(screen.getByRole("button", { name: "Copy" }));

    await waitFor(() => expect(copyTextMock).toHaveBeenCalled());
    const copied = copyTextMock.mock.calls[0]?.[0] as string;
    expect(copied).toBe("ping @Alice Display now");
    expect(copied).not.toContain("user_raw");
  });

  // GAP 3 — legit user-pasted JSON with a `parts` array but no top-level
  // `action` key must NOT be intercepted; it renders as normal markdown text.
  it("does not intercept legit JSON content lacking an action key", () => {
    const jsonish = '{"parts":["a","b"]}';
    render(
      <ChannelMessageBubble
        message={makeMessage({ type: "user", author_id: "user-1", content: jsonish })}
        currentUserId="user-1"
      />,
    );

    expect(screen.getByText(jsonish)).toBeInTheDocument();
  });

  // #250 (round 3) — the real leaking historical content is reasoning-text
  // prefix + the envelope JSON concatenated, so a whole-string parse throws and
  // the raw string (prefix + JSON) would otherwise render. The body must unwrap
  // the embedded envelope's real parts; copy must yield the real text.
  const EMBEDDED_ENVELOPE_CONTENT =
    'Repo isn\'t checked out this turn either — consistent with prior. {"action":"message_send","output":"x","parts":[{"type":"text","text":"the real message"}]}';

  it("renders the embedded envelope's real message, dropping the reasoning prefix and raw JSON", () => {
    render(
      <ChannelMessageBubble
        message={makeMessage({ content: EMBEDDED_ENVELOPE_CONTENT, parts: [] })}
        currentUserId="user-1"
      />,
    );

    const body = screen.getByTestId("message-body");
    expect(body).toHaveTextContent("the real message");
    expect(body.textContent).not.toContain('"action"');
    expect(body.textContent).not.toContain("{");
    expect(body.textContent).not.toContain("Repo isn't checked out");
  });

  it("copies only the embedded envelope's real text, not the prefix or JSON", async () => {
    copyTextMock.mockResolvedValue(true);

    render(
      <ChannelMessageBubble
        message={makeMessage({ content: EMBEDDED_ENVELOPE_CONTENT, parts: [] })}
        currentUserId="user-1"
      />,
    );

    await userEvent.click(screen.getByRole("button", { name: "Copy" }));

    await waitFor(() => expect(copyTextMock).toHaveBeenCalledWith("the real message"));
    const copied = copyTextMock.mock.calls[0]?.[0] as string;
    expect(copied).not.toContain('"action"');
    expect(copied).not.toContain("Repo isn't checked out");
  });

  it("extracts an embedded envelope even when a part text value contains braces", () => {
    const raw =
      'thinking… {"action":"message_send","parts":[{"type":"text","text":"use {curly} braces"}]}';
    render(
      <ChannelMessageBubble
        message={makeMessage({ content: raw, parts: [] })}
        currentUserId="user-1"
      />,
    );

    const body = screen.getByTestId("message-body");
    expect(body).toHaveTextContent("use {curly} braces");
    expect(body.textContent).not.toContain('"action"');
    expect(body.textContent).not.toContain("thinking");
  });

  // GAP 3 — prose that merely mentions a stray JSON object (no action+parts
  // envelope) must render intact, never blanked or mangled.
  it("leaves prose that mentions a stray JSON object intact", () => {
    const prose = 'here is {"foo":1} in my note';
    render(
      <ChannelMessageBubble
        message={makeMessage({ type: "user", author_id: "user-1", content: prose })}
        currentUserId="user-1"
      />,
    );

    expect(screen.getByText(prose)).toBeInTheDocument();
  });

  // #250 — embedded envelope with no renderable text parts: body must fall back
  // to the envelope output, never the raw JSON or reasoning prefix.
  it("renders the embedded envelope output when it carries no text parts", () => {
    const raw =
      'some reasoning here {"action":"message_send","output":"fallback summary","parts":[{"type":"image","url":"x"}]}';
    render(
      <ChannelMessageBubble
        message={makeMessage({ content: raw, parts: [] })}
        currentUserId="user-1"
      />,
    );

    const body = screen.getByTestId("message-body");
    expect(body.textContent).not.toContain('"action"');
    expect(body.textContent).not.toContain("some reasoning here");
    expect(body.textContent).not.toContain("{");
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

  it("labels the current user as You in reaction attribution via HoverCard only", async () => {
    const user = userEvent.setup();
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

    const pill = screen.getByRole("button", { name: "👍2" });
    // No native title — that double-rendered the same name under HoverCard.
    expect(pill).not.toHaveAttribute("title");

    await user.hover(pill);
    expect(await screen.findByText("You, Bob Display")).toBeInTheDocument();
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

  it("opens the thread page from a mobile message tap with border feedback", async () => {
    setMobileViewport();
    const onOpenThread = vi.fn();
    const message = makeMessage();
    render(
      <ChannelMessageBubble
        message={message}
        currentUserId="user-1"
        onOpenThread={onOpenThread}
        onReact={vi.fn()}
      />,
    );

    const bubble = screen.getByTestId("message-bubble");
    const actionBar = screen.getByTestId("message-action-bar");
    expect(actionBar).toHaveClass("hidden");
    expect(actionBar).toHaveClass("md:flex");
    expect(actionBar).toHaveClass("opacity-0");
    expect(screen.queryByRole("dialog", { name: "Message actions" })).not.toBeInTheDocument();

    fireEvent.pointerDown(bubble, { pointerType: "touch", clientX: 0, clientY: 0 });
    fireEvent.pointerUp(bubble, { pointerType: "touch", clientX: 0, clientY: 0 });

    expect(bubble).toHaveClass("ring-primary/45");
    expect(screen.queryByRole("dialog", { name: "Message actions" })).not.toBeInTheDocument();
    await waitFor(() => expect(onOpenThread).toHaveBeenCalledWith(message));
  });

  it("opens mobile message actions from a long press without a thread menu item", async () => {
    setMobileViewport();
    render(
      <ChannelMessageBubble
        message={makeMessage()}
        currentUserId="user-1"
        onOpenThread={vi.fn()}
        onReact={vi.fn()}
      />,
    );

    const bubble = screen.getByTestId("message-bubble");
    fireEvent.pointerDown(bubble, { pointerType: "touch", clientX: 0, clientY: 0 });

    const menu = await screen.findByRole("dialog", { name: "Message actions" });
    expect(menu).toBeInTheDocument();
    expect(within(menu).getByRole("button", { name: "Add reaction" })).toBeInTheDocument();
    expect(within(menu).getByRole("button", { name: "Copy" })).toBeInTheDocument();
    expect(within(menu).queryByRole("button", { name: "Reply in thread" })).not.toBeInTheDocument();
    expect(within(menu).queryByRole("button", { name: "Quote reply" })).not.toBeInTheDocument();
  });

  it("closes the mobile action sheet after a copy action", async () => {
    setMobileViewport();
    copyTextMock.mockResolvedValue(true);
    render(<ChannelMessageBubble message={makeMessage()} currentUserId="user-1" />);

    const bubble = screen.getByTestId("message-bubble");
    fireEvent.pointerDown(bubble, { pointerType: "touch", clientX: 0, clientY: 0 });
    fireEvent.pointerUp(bubble, { pointerType: "touch", clientX: 0, clientY: 0 });

    const copyButtons = await screen.findAllByRole("button", { name: "Copy" });
    await userEvent.click(copyButtons.at(-1)!);

    await waitFor(() => expect(copyTextMock).toHaveBeenCalledWith("Here is the data."));
    await waitFor(() =>
      expect(screen.queryByRole("dialog", { name: "Message actions" })).not.toBeInTheDocument(),
    );
  });

  it("projects anchored issue refs in a backflow system row instead of dumping raw text (#469/#497)", () => {
    render(
      <ChannelMessageBubble
        message={makeMessage({
          type: "system",
          author_id: null,
          author_name: "System",
          content: "MUL-7 assigned to Felix",
          parts: [
            {
              type: "reference",
              ref_type: "issue-ref",
              ref_subtype: "issue",
              ref_id: "issue-uuid",
              label: "MUL-7",
              content_start_utf16: 0,
              content_end_utf16: 5,
            },
          ],
        } as never)}
        currentUserId="user-1"
        onOpenThread={vi.fn()}
        onReact={vi.fn()}
      />,
    );

    // Pre-fix the system row rendered `message.content` as a bare string, so a
    // backflow "MUL-7 assigned to …" could never become a token even once the
    // server anchored it.
    const systemRow = screen.getByTestId("system-message-row");
    expect(within(systemRow).getByText("MUL-7").closest("a")).not.toBeNull();
  });

  it("projects an issue-lifecycle status change into the item #7 row with a simple inline time (#497)", () => {
    render(
      <ChannelMessageBubble
        message={makeMessage({
          type: "system",
          author_id: null,
          author_name: "System",
          created_at: "2026-06-17T09:15:00Z",
          // Pre-#7 the backend fallback string leaked the internal enum wording.
          content: "LRM-137 moved from Todo to In Progress",
          parts: [
            {
              type: "system_event",
              event: "issue_status_changed",
              event_params: {
                issue_id: "issue-uuid",
                issue_identifier: "LRM-137",
                issue_status: "in_progress",
                previous_status: "todo",
                actor_id: "user-1",
                actor_type: "human",
              },
            },
            {
              type: "reference",
              ref_type: "issue-ref",
              ref_subtype: "issue",
              ref_id: "issue-uuid",
              label: "LRM-137",
            },
          ],
        } as never)}
        currentUserId="user-2"
        onOpenThread={vi.fn()}
        onReact={vi.fn()}
      />,
    );

    const systemRow = screen.getByTestId("system-message-row");
    // The projected action verb replaces the raw "moved from Todo to In Progress".
    expect(systemRow).toHaveTextContent("Alice Display started");
    expect(systemRow.textContent).not.toContain("moved");
    expect(systemRow.textContent).not.toContain("in_progress");

    // The issue identifier is the sole link in the row.
    const links = within(systemRow).getAllByRole("link");
    expect(links).toHaveLength(1);
    expect(links[0]).toHaveTextContent("LRM-137");

    // A simple inline time ("· <time>"), never the full hover timestamp (no title).
    expect(systemRow).toHaveTextContent("·");
    expect(systemRow).toHaveTextContent("09:15");
    expect(systemRow.getAttribute("title")).toBeNull();
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

    const systemRow = screen.getByTestId("system-message-row");
    expect(systemRow).toHaveTextContent("Barry archived this channel.");
    // The absolute local time is revealed on hover (Frank: "系统事件 hover 出时间") —
    // rendered inline (not just the native title), so it's in the row's text too.
    const fullTime = systemRow.getAttribute("title");
    expect(fullTime).toBeTruthy();
    expect(systemRow).toHaveTextContent(fullTime!);
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

  // B3 (#241) — Edit / Delete + H5 FE guard.
  const ownMessage = () =>
    makeMessage({ type: "user", author_id: "user-1", author_name: "alice", content: "Original" });

  // Edit unshipped 2026-07-05 (Frank/Miles): the Edit entry point is hidden
  // (canEdit=false) until the inline editor is rebuilt on the unified composer
  // (#258). Delete stays. This asserts Delete renders on the viewer's own
  // message while Edit never does.
  it("shows delete but never edit on the viewer's own message", () => {
    const { rerender } = render(
      <ChannelMessageBubble
        message={ownMessage()}
        currentUserId="user-1"
        onEdit={vi.fn()}
        onDelete={vi.fn()}
      />,
    );

    expect(screen.getByRole("button", { name: "Delete" })).toBeInTheDocument();
    // Edit is unshipped — the entry point must not render even on own messages.
    expect(screen.queryByRole("button", { name: "Edit" })).not.toBeInTheDocument();

    // A peer / agent message from another author exposes no edit or delete.
    rerender(
      <ChannelMessageBubble
        message={makeMessage({ type: "user", author_id: "user-2", author_name: "bob" })}
        currentUserId="user-1"
        onEdit={vi.fn()}
        onDelete={vi.fn()}
      />,
    );
    expect(screen.queryByRole("button", { name: "Edit" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Delete" })).not.toBeInTheDocument();
  });

  // Edit unshipped 2026-07-05 (Frank/Miles): the Edit entry point is hidden
  // (canEdit=false) so the inline editor is unreachable. MessageInlineEditor /
  // onEdit are kept dormant for the composer-parity rebuild (#258); restore
  // these two flow tests when Edit is re-enabled on the unified composer.
  it.skip("edits inline and saves through onEdit without a send/dispatch path", async () => {
    const onEdit = vi.fn();
    const onReact = vi.fn();
    const onOpenThread = vi.fn();
    const message = ownMessage();
    render(
      <ChannelMessageBubble
        message={message}
        currentUserId="user-1"
        onEdit={onEdit}
        onDelete={vi.fn()}
        onReact={onReact}
        onOpenThread={onOpenThread}
      />,
    );

    await userEvent.click(screen.getByRole("button", { name: "Edit" }));

    const editor = screen.getByRole("textbox", { name: "Edit" });
    expect(editor).toHaveValue("Original");

    await userEvent.clear(editor);
    await userEvent.type(editor, "Corrected");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    // Edit routes only through onEdit — never a reaction / thread (wake) path.
    expect(onEdit).toHaveBeenCalledTimes(1);
    expect(onEdit).toHaveBeenCalledWith(message, "Corrected");
    expect(onReact).not.toHaveBeenCalled();
    expect(onOpenThread).not.toHaveBeenCalled();

    // Editor closes back to the read view after saving.
    expect(screen.queryByRole("textbox", { name: "Edit" })).not.toBeInTheDocument();
  });

  // Edit unshipped 2026-07-05 (Frank/Miles): unreachable while canEdit=false;
  // restore alongside the composer-parity rebuild (#258).
  it.skip("cancels an inline edit without calling onEdit", async () => {
    const onEdit = vi.fn();
    render(
      <ChannelMessageBubble
        message={ownMessage()}
        currentUserId="user-1"
        onEdit={onEdit}
        onDelete={vi.fn()}
      />,
    );

    await userEvent.click(screen.getByRole("button", { name: "Edit" }));
    await userEvent.type(screen.getByRole("textbox", { name: "Edit" }), " changed");
    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));

    expect(onEdit).not.toHaveBeenCalled();
    expect(screen.queryByRole("textbox", { name: "Edit" })).not.toBeInTheDocument();
    expect(screen.getByText("Original")).toBeInTheDocument();
  });

  it("marks an edited message with an edited label", () => {
    render(
      <ChannelMessageBubble
        message={makeMessage({
          type: "user",
          author_id: "user-1",
          author_name: "alice",
          content: "Fixed typo",
          edited_at: "2026-06-17T09:20:00Z",
        })}
        currentUserId="user-1"
      />,
    );

    expect(screen.getByText("(edited)")).toBeInTheDocument();
  });

  it("renders a tombstone placeholder for a deleted message, not an empty row", () => {
    render(
      <ChannelMessageBubble
        message={makeMessage({
          type: "user",
          author_id: "user-1",
          author_name: "alice",
          content: "secret text",
          deleted_at: "2026-06-17T09:25:00Z",
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
        onEdit={vi.fn()}
        onDelete={vi.fn()}
        onReact={vi.fn()}
        onOpenThread={vi.fn()}
      />,
    );

    const tombstone = screen.getByTestId("message-tombstone");
    expect(tombstone).toHaveTextContent("This message was deleted");
    // Original content is gone and no message actions survive a delete.
    expect(screen.queryByText("secret text")).not.toBeInTheDocument();
    expect(screen.queryByTestId("message-body")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Edit" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "👍1" })).not.toBeInTheDocument();
  });

  it("deletes through onDelete without a send/dispatch path", async () => {
    const onDelete = vi.fn();
    const onReact = vi.fn();
    const message = ownMessage();
    render(
      <ChannelMessageBubble
        message={message}
        currentUserId="user-1"
        onEdit={vi.fn()}
        onDelete={onDelete}
        onReact={onReact}
      />,
    );

    await userEvent.click(screen.getByRole("button", { name: "Delete" }));

    expect(onDelete).toHaveBeenCalledTimes(1);
    expect(onDelete).toHaveBeenCalledWith(message);
    expect(onReact).not.toHaveBeenCalled();
  });
});
