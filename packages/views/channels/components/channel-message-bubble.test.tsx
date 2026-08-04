import { act, createEvent, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import userEvent from "@testing-library/user-event";
import { toast } from "sonner";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { AgentFleetRank, ChannelMessage } from "@multica/core/types";
import type { HonorSnapshot } from "@multica/core/types/honor";
import { stickerCatalogKeys } from "@multica/core/stickers";
import { __resetAuthorAvatarOkCacheForTests } from "./author-avatar-cache";
import { ChannelMessageBubble } from "./channel-message-bubble";

vi.mock("./thread-reply-preview", () => ({
  ThreadReplyPreview: ({ message }: { message: ChannelMessage }) => (
    <div data-testid="thread-reply-preview">preview:{message.thread_reply_count}</div>
  ),
}));

const copyTextMock = vi.fn();

vi.mock("../lib/voice-playback", () => ({
  claimVoiceAutoplay: () => false,
  prepareVoiceAudio: () => Promise.resolve({
    audio: new ArrayBuffer(4),
    durationMs: 3200,
  }),
  startPreparedVoicePlayback: () => Promise.resolve({
    durationMs: 3200,
    finished: Promise.resolve(),
    stop: vi.fn(),
  }),
  voicePlaybackScope: (channelId: string, threadRootMessageId?: string | null) =>
    `${channelId}:${threadRootMessageId ?? "main"}`,
}));

// The author avatar renders an ActorProfileTrigger; on mobile it navigates to
// the full-page profile (#586), which reads useNavigation. Stub the navigation
// context (the barrel re-exports it) so these layout/interaction tests don't
// each need a NavigationProvider.
vi.mock("../../navigation/context", () => ({
  useOptionalNavigation: () => null,
  useNavigation: () => ({
    push: vi.fn(),
    replace: vi.fn(),
    back: vi.fn(),
    pathname: "/acme/channels",
    searchParams: new URLSearchParams(),
    getShareableUrl: (path: string) => path,
  }),
  useIsNavigating: () => false,
  NavigationProvider: ({ children }: { children: ReactNode }) => children,
}));

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
  ActorMention: ({
    type,
    id,
    label,
  }: {
    type: string;
    id: string;
    label: string;
  }) => (
    <span data-testid="actor-mention" data-mention-type={type} data-mention-id={id}>
      {label}
    </span>
  ),
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
    actorProfile: (memberType: string, memberId: string) =>
      `/acme/profile/${memberType}/${memberId}`,
    agentDetail: (id: string) => `/acme/agents/${id}`,
    memberDetail: (id: string) => `/acme/members/${id}`,
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

// The bubble may fall back to the members/agents directory (profile-card source)
// when the message payload omits author_avatar_url (LRM-218). Stub the hook so
// layout tests don't need a QueryClient/workspace; override per-case when needed.
const getActorAvatarUrlMock = vi.fn(
  (_type: string, _id: string): string | null => null,
);
const getAgentHonorLevelMock = vi.fn((_agentId: string): number | undefined => undefined);
const getMemberHonorMock = vi.fn(
  (_userId: string): HonorSnapshot | undefined => undefined,
);
const getAgentFleetRankMock = vi.fn(
  (_agentId: string): AgentFleetRank | undefined => undefined,
);
// LRM-281 system rows may call useWorkspaceId + useQuery(member-profiles).
// Layout tests stub the actor directory; give them a stable workspace id so
// profile resolution does not throw outside a workspace route.
vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@multica/core/workspace/use-member-presence", () => ({
  useMemberOnline: () => false,
}));

vi.mock("@multica/core/workspace/hooks", () => ({
  useActorName: () => ({
    getActorAvatarUrl: getActorAvatarUrlMock,
    getActorInitials: (_type: string, id: string) => {
      if (id === "user-1") return "A";
      if (id === "user-2") return "B";
      if (id === "user-owner") return "F";
      if (id === "user-admin") return "Ad";
      if (id === "agent-1") return "R";
      return "?";
    },
    getActorName: (_type: string, id: string, fallback?: string) => {
      if (id === "user-1") return "Alice Display";
      if (id === "user-2") return "Bob Display";
      if (id === "user-owner") return "Frank An";
      if (id === "user-admin") return "Admin User";
      if (id === "agent-1") return "Research Agent";
      return fallback ?? id;
    },
    getMemberRole: (id: string) =>
      id === "user-owner"
        ? ("owner" as const)
        : id === "user-admin"
          ? ("admin" as const)
          : id === "user-1" || id === "user-2"
            ? ("member" as const)
            : null,
    getMemberHonor: getMemberHonorMock,
    getAgentFleetRank: getAgentFleetRankMock,
    getAgentHonorLevel: getAgentHonorLevelMock,
  }),
}));

// LRM-364: layout tests stub the reaction resolver so they don't need a live
// member-profile QueryClient path. Dedicated hook tests cover profile fetch.
vi.mock("../../common/use-reaction-actor-name", () => ({
  useReactionActorName: () => (_type: string, id: string, fallback?: string) => {
    if (id === "user-1") return "Alice Display";
    if (id === "user-2") return "Bob Display";
    if (id === "user-owner") return "Frank An";
    if (id === "user-admin") return "Admin User";
    if (id === "agent-1") return "Research Agent";
    if (id === "agent-beckham") return "贝克汉姆";
    return fallback ?? id;
  },
}));

// LRM-391: ActorAvatar resolves via member-profile on directory miss — stub so
// layout tests don't require a live profile QueryClient path.
vi.mock("../../common/use-resolved-actor-identity", () => ({
  mentionTypeFromActorType: (type: string) =>
    type === "agent" ? "agent" : type === "member" || type === "user" ? "member" : null,
  useResolvedActorIdentity: (actorId: string | undefined, mentionType: string | null) => {
    if (!actorId || !mentionType) return { displayName: null, avatarUrl: null };
    const names: Record<string, string> = {
      "user-1": "Alice Display",
      "user-2": "Bob Display",
      "user-owner": "Frank An",
      "user-admin": "Admin User",
      "agent-1": "Research Agent",
      "agent-beckham": "贝克汉姆",
    };
    return { displayName: names[actorId] ?? null, avatarUrl: null };
  },
  resolvedActorLabel: (
    identity: { displayName: string | null },
    actorId: string | undefined,
  ) => identity.displayName ?? actorId ?? "",
}));

vi.mock("@multica/core/agents/stores", () => ({
  useAgentPanelStore: (selector: (s: { open: (id: string) => void }) => unknown) =>
    selector({ open: vi.fn() }),
  useAgentXpBurstStore: (selector: (s: { bursts: Record<string, never> }) => unknown) =>
    selector({ bursts: {} }),
}));

vi.mock("../../common/agent-panel-context", () => ({
  useOpenAgentPanel: () => null,
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
          more_emojis: string;
          more_actions: string;
          agent_badge: string;
          feishu_badge: string;
          copy_action: string;
          expand_action: string;
          collapse_action: string;
          copied_toast: string;
          copy_failed_toast: string;
          sticker_alt: string;
          sticker_loading: string;
          sticker_failed: string;
          sticker_unavailable: string;
          attachment_unavailable: string;
          send_failed: string;
          retry_send: string;
          sending: string;
          voice_input: string;
          voice_input_duration: string;
          voice_play: string;
          voice_loading: string;
          voice_stop: string;
          voice_retry: string;
          voice_show_transcript: string;
          voice_hide_transcript: string;
          voice_transcript_label: string;
          edit_action: string;
          actions_menu: string;
          delete_action: string;
          edited_label: string;
          deleted_placeholder: string;
          choice_reselect_hint: string;
          choice_locked: string;
          choice_failed: string;
          save_edit: string;
          cancel_edit: string;
          system_event: {
            issue: {
              actor_system: string;
              created: string;
              assigned: string;
              assigned_unknown: string;
              in_progress: string;
              in_review: string;
              done: string;
              updated: string;
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
        honor_level_value: string;
        profile_popover: {
          role: { owner: string; admin: string; member: string; agent: string };
        };
      }) => string,
      options?: Record<string, unknown>,
    ) => {
      const raw = selector({
        message: {
          add_reaction: "Add reaction",
          more_emojis: "More emojis",
          more_actions: "More actions",
          agent_badge: "Agent",
          feishu_badge: "Feishu",
          actions_menu: "Message actions",
          copy_action: "Copy",
          expand_action: "See more",
          collapse_action: "See less",
          copied_toast: "Copied",
          copy_failed_toast: "Copy failed",
          sticker_alt: "Sticker",
          sticker_loading: "Loading sticker",
          sticker_failed: "Sticker failed to load",
          sticker_unavailable: "Sticker unavailable",
          attachment_unavailable: "Attachment unavailable",
          send_failed: "Couldn't send",
          retry_send: "Retry",
          sending: "Sending…",
          voice_input: "Voice input",
          voice_input_duration: "Voice input · {{seconds}}s",
          voice_play: "Play voice reply",
          voice_loading: "Preparing voice reply…",
          voice_stop: "Stop voice reply",
          voice_retry: "Voice playback failed · Retry",
          voice_show_transcript: "Show transcript",
          voice_hide_transcript: "Hide transcript",
          voice_transcript_label: "Voice transcript",
          edit_action: "Edit",
          delete_action: "Delete",
          edited_label: "(edited)",
          deleted_placeholder: "This message was deleted",
          choice_reselect_hint: "You can change your answer once",
          choice_locked: "Locked",
          choice_failed: "Couldn't select option",
          save_edit: "Save",
          cancel_edit: "Cancel",
          system_event: {
            issue: {
              actor_system: "Multica",
              created: "{actor} created {issue}",
              assigned: "{actor} assigned {issue} to {target}",
              assigned_unknown: "{actor} changed the assignee of {issue}",
              in_progress: "{actor} started {issue}",
              in_review: "{actor} sent {issue} for review",
              done: "{actor} completed {issue}",
              updated: "{actor} updated {issue}",
              status: "{actor} marked {issue} as {{status}}",
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
        honor_level_value: "Level {{level}}",
        profile_popover: {
          role: { owner: "Owner", admin: "Admin", member: "Member", agent: "Agent" },
        },
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

  // LRM-1174: a touch laptop in a narrow window is the dead-zone cell — the JS
  // gate sees `innerWidth < 768` and a touch pointerType, while the SAME device
  // reports `(pointer: fine)`. Only `(pointer: coarse)` is false here.
  const setMixedPointerNarrowViewport = () => {
    Object.defineProperty(window, "innerWidth", { configurable: true, value: 760 });
    Object.defineProperty(window, "matchMedia", {
      configurable: true,
      value: vi.fn((query: string) => ({
        matches: query.includes("max-width") || query.includes("pointer: fine"),
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
    getActorAvatarUrlMock.mockReset();
    getActorAvatarUrlMock.mockReturnValue(null);
    getAgentHonorLevelMock.mockReset();
    getAgentHonorLevelMock.mockReturnValue(undefined);
    getMemberHonorMock.mockReset();
    getMemberHonorMock.mockReturnValue(undefined);
    getAgentFleetRankMock.mockReset();
    getAgentFleetRankMock.mockReturnValue(undefined);
    vi.mocked(toast.success).mockReset();
    vi.mocked(toast.error).mockReset();
    __resetAuthorAvatarOkCacheForTests();
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

  it("renders an agent message with name and body but no Agent pill (LRM-232)", () => {
    render(<ChannelMessageBubble message={makeMessage()} currentUserId="user-1" />);

    expect(screen.getByText("Research Agent")).toBeInTheDocument();
    expect(screen.queryByText("Agent")).not.toBeInTheDocument();
    expect(screen.queryByTestId("message-author-role")).not.toBeInTheDocument();
    expect(screen.getByText("Here is the data.")).toBeInTheDocument();
    expect(screen.getByTestId("message-bubble")).toHaveAttribute("data-own", "false");
  });

  it("uses the permanent honor-level crest beside an agent author name", () => {
    getAgentHonorLevelMock.mockReturnValue(8);
    getAgentFleetRankMock.mockReturnValue({
      agent_id: "agent-1",
      fleet_score: 0,
      class_id: "reserve",
      class_label: "Reserve",
      fleet_rank: 0,
      fleet_size: 1,
      sample_tasks: 0,
      sample_sufficient: false,
      frozen: false,
      pillars: { delivery: 0, evolution: 0, growth: 0, efficiency: 0 },
    });

    const { container } = render(
      <ChannelMessageBubble message={makeMessage()} currentUserId="user-1" />,
    );

    expect(container.querySelector('[data-agent-honor-level="8"]')).toBeInTheDocument();
    expect(screen.queryByTitle("Reserve")).not.toBeInTheDocument();
  });

  it("uses the user's armor level crest beside a group-message author name", () => {
    getMemberHonorMock.mockReturnValue({ level: 42, name_style: "default" });

    const { container } = render(
      <ChannelMessageBubble
        message={makeMessage({
          type: "user",
          author_id: "user-2",
          author_name: "Bob Display",
        })}
        currentUserId="user-1"
      />,
    );

    expect(container.querySelector('[data-user-honor-level="42"]')).toBeInTheDocument();
  });

  it("centers the quiet timestamp with the author name and earned badge row", () => {
    render(<ChannelMessageBubble message={makeMessage()} currentUserId="user-1" />);

    expect(screen.getByTestId("message-author-row")).toHaveClass("items-center");
    expect(screen.getByText("Research Agent").closest("button")).toHaveClass(
      "self-center",
    );
    expect(screen.getByTestId("message-author-time")).toHaveClass(
      "h-5",
      "items-center",
      "text-[10px]",
      "text-muted-foreground/50",
    );
  });

  it("lets message content fill the conversation column (LRM-400)", () => {
    render(<ChannelMessageBubble message={makeMessage()} currentUserId="user-1" />);
    const body = screen.getByTestId("message-body");
    const contentCol = body.parentElement;
    expect(contentCol).not.toBeNull();
    expect(contentCol!.className).toMatch(/\bmax-w-full\b/);
    expect(contentCol!.className).not.toMatch(/760px/);
  });

  it("LRM-1331: drops shell gutter; author row reserves; bar chrome stays overlay", () => {
    render(
      <ChannelMessageBubble
        message={makeMessage()}
        currentUserId="user-1"
        onReact={vi.fn()}
        onOpenThread={vi.fn()}
      />,
    );

    const shell = screen.getByTestId("message-shell");
    expect(shell.className).not.toMatch(/pr-\[136px\]/);
    expect(shell.className).not.toMatch(/pointer:fine.*pr-/);

    const authorRow = screen.getByTestId("message-author-row");
    expect(authorRow.className).toMatch(
      /\[@media\(pointer:fine\)_and_\(min-width:640px\)\]:pr-\[162px\]/,
    );

    const actionBar = screen.getByTestId("message-action-bar");
    expect(actionBar).toHaveClass("bg-popover");
    expect(actionBar).toHaveClass("border-line-strong");
    expect(actionBar).toHaveClass("shadow-sm");
    expect(actionBar).toHaveClass("rounded-lg");
    expect(actionBar).toHaveClass("top-1");
    expect(actionBar.className).toMatch(
      /\[@media\(pointer:fine\)_and_\(min-width:640px\)\]:flex/,
    );
    expect(actionBar.className).not.toMatch(/\[@media\(pointer:fine\)\]:flex\b/);

    // Author name: never wrap (role/time shrink first on narrow).
    expect(screen.getByText("Research Agent").className).toMatch(/whitespace-nowrap/);
  });

  it("LRM-1126: compact rows align the action bar to the first body line", () => {
    render(
      <ChannelMessageBubble
        message={makeMessage({ content: "follow-up" })}
        currentUserId="user-1"
        compact
        onReact={vi.fn()}
      />,
    );
    expect(screen.getByTestId("message-action-bar")).toHaveClass("top-0.5");
  });

  it("LRM-1331: compact text rows use a first-line float safety zone", () => {
    render(
      <ChannelMessageBubble
        message={makeMessage({ content: "follow-up line that wraps" })}
        currentUserId="user-1"
        compact
        onReact={vi.fn()}
      />,
    );
    const body = screen.getByTestId("message-body");
    const gate = "[@media(pointer:fine)_and_(min-width:640px)]";
    expect(body.className).toContain(`${gate}:before:float-right`);
    expect(body.className).toContain(`${gate}:before:h-[36px]`);
    expect(body.className).toContain(`${gate}:before:w-[158px]`);
    expect(screen.getByTestId("message-shell").className).not.toMatch(/pr-\[136px\]/);
  });

  it("LRM-1331: compact leading quote uses card inset instead of body float", () => {
    render(
      <ChannelMessageBubble
        message={makeMessage({
          content: "reply with quote",
          quote_message_id: "q-1",
          quote: {
            messageId: "q-1",
            status: "active",
            snapshot: {
              type: "user",
              authorId: "user-a",
              authorName: "Alice",
              content: "quoted",
              createdAt: "2026-06-17T09:00:00Z",
            },
          },
        })}
        currentUserId="user-1"
        compact
        onReact={vi.fn()}
      />,
    );
    const body = screen.getByTestId("message-body");
    const quote = screen.getByTestId("message-quote-card");
    const gate = "[@media(pointer:fine)_and_(min-width:640px)]";
    expect(body.className).not.toContain(`${gate}:before:float-right`);
    expect(quote.className).toContain(`${gate}:pr-[158px]`);
  });

  describe("LRM-1227 bubble shell (① + G + C2 + D2, frozen in LRM-1233)", () => {
    /** Every utility LRM-1346 (Frank lock ①) forbids on the shell / its segments. */
    const FORBIDDEN_SHELL_EDGE =
      /\bborder(-line|-line-strong|-x|-y|-t|-b|-l|-r)?\b|\brounded(-[tbrl]{1,2})?-lg\b|\bring-1\b|\bdivide-y\b|\bshadow-/;

    it("paints a borderless shell on a standalone message (LRM-1346 lock ①)", () => {
      render(<ChannelMessageBubble message={makeMessage()} currentUserId="user-1" />);
      const shell = screen.getByTestId("message-shell");

      // Shell is the content column itself — no extra wrapper node.
      expect(shell).toBe(screen.getByTestId("message-body").parentElement);
      expect(shell).toHaveAttribute("data-group-start", "true");
      expect(shell).toHaveAttribute("data-group-end", "true");
      // Surface = message pane (`bg-background`); no grey slab, and — since
      // LRM-1346 — no 1px frame or card corners on any of the four sides.
      expect(shell).toHaveClass("bg-background");
      expect(shell.className).not.toMatch(/\bbg-muted\b/);
      expect(shell.className).not.toMatch(FORBIDDEN_SHELL_EDGE);
    });

    it("keeps group boundaries as data attributes only — no head/middle/tail edges", () => {
      const { rerender } = render(
        <ChannelMessageBubble
          message={makeMessage()}
          currentUserId="user-1"
          groupEnd={false}
        />,
      );
      // Group head: boundary is readable to tests / geometry, invisible to the eye.
      let shell = screen.getByTestId("message-shell");
      expect(shell).toHaveAttribute("data-group-start", "true");
      expect(shell).not.toHaveAttribute("data-group-end");
      expect(shell.className).not.toMatch(FORBIDDEN_SHELL_EDGE);

      // Middle continuation.
      rerender(
        <ChannelMessageBubble
          message={makeMessage({ content: "middle" })}
          currentUserId="user-1"
          compact
          groupEnd={false}
        />,
      );
      shell = screen.getByTestId("message-shell");
      expect(shell).not.toHaveAttribute("data-group-start");
      expect(shell).not.toHaveAttribute("data-group-end");
      expect(shell.className).not.toMatch(FORBIDDEN_SHELL_EDGE);

      // Group tail.
      rerender(
        <ChannelMessageBubble
          message={makeMessage({ content: "last" })}
          currentUserId="user-1"
          compact
          groupEnd
        />,
      );
      shell = screen.getByTestId("message-shell");
      expect(shell).toHaveAttribute("data-group-end", "true");
      expect(shell.className).not.toMatch(FORBIDDEN_SHELL_EDGE);
    });

    it("keeps the row wash off and no longer strengthens a shell edge on hover", () => {
      render(<ChannelMessageBubble message={makeMessage()} currentUserId="user-1" />);
      // The old 1.02:1 wash stays gone (LRM-1227) …
      expect(screen.getByTestId("message-bubble").className).not.toMatch(
        /hover:bg-muted\/35|focus-within:bg-muted\/35/,
      );
      // … and LRM-1346 removed the edge that replaced it, so nothing on the
      // shell may re-add a hover / focus-within border. The floating action bar
      // is the fine-pointer affordance.
      const shell = screen.getByTestId("message-shell");
      expect(shell.className).not.toMatch(/group-hover:border/);
      expect(shell.className).not.toMatch(/group-focus-within:border/);
      expect(screen.getByTestId("message-shell").className).not.toMatch(FORBIDDEN_SHELL_EDGE);
    });

    it("lets the self-mention wash replace the shell fill, never stack under it", () => {
      const msg = makeMessage({
        type: "user",
        author_id: "user-2",
        author_name: "bob",
        content: "hey [@Alice](mention://member/user-1) look",
      });
      render(<ChannelMessageBubble message={msg} currentUserId="user-1" />);

      expect(screen.getByTestId("message-bubble").className).toContain("bg-[#fef9e8]");
      const shell = screen.getByTestId("message-shell");
      // Shell drops its own surface so the row's wash is what the viewer sees;
      // since LRM-1346 it has no silhouette to keep either.
      expect(shell.className).not.toMatch(/\bbg-background\b/);
      expect(shell.className).not.toMatch(/\bbg-muted\b/);
      expect(shell.className).not.toMatch(FORBIDDEN_SHELL_EDGE);
    });

    it("lets the deep-link highlight replace the shell fill too", () => {
      render(
        <ChannelMessageBubble message={makeMessage()} currentUserId="user-1" highlighted />,
      );
      expect(screen.getByTestId("message-bubble").className).toContain("bg-primary/10");
      const shell = screen.getByTestId("message-shell");
      expect(shell.className).not.toMatch(/\bbg-background\b/);
      expect(shell.className).not.toMatch(/\bbg-muted\b/);
    });

    it("D2: bar rides the shell top edge on a lead row and sits inside a compact row", () => {
      const { rerender } = render(
        <ChannelMessageBubble
          message={makeMessage()}
          currentUserId="user-1"
          onReact={vi.fn()}
        />,
      );
      const bar = screen.getByTestId("message-action-bar");
      // right-2 == the row's own px-2, so bar right edge == shell right edge.
      expect(bar).toHaveClass("right-2");
      expect(bar).toHaveClass("top-1");
      expect(bar).toHaveClass("-translate-y-1/2");

      rerender(
        <ChannelMessageBubble
          message={makeMessage({ content: "follow-up" })}
          currentUserId="user-1"
          compact
          onReact={vi.fn()}
        />,
      );
      const compactBar = screen.getByTestId("message-action-bar");
      expect(compactBar).toHaveClass("top-0.5");
      expect(compactBar.className).not.toMatch(/-translate-y-1\/2/);
    });

    it("D2: 124x34 bar geometry — four size-7 controls, each with its own focus ring", () => {
      render(
        <ChannelMessageBubble
          message={makeMessage()}
          currentUserId="user-1"
          onReact={vi.fn()}
          onQuote={vi.fn()}
          onOpenThread={vi.fn()}
        />,
      );
      const bar = screen.getByTestId("message-action-bar");
      const controls = Array.from(bar.querySelectorAll("button"));
      // 4 × 28 + 3 × 2 (gap-0.5) + 2 × 2 (p-0.5) + 2 × 1 (edge) = 124; 28+4+2 = 34.
      expect(controls).toHaveLength(4);
      expect(bar).toHaveClass("gap-0.5");
      expect(bar).toHaveClass("p-0.5");
      for (const control of controls) {
        expect(control.className).toMatch(/\bsize-7\b/);
        expect(control.className).toMatch(/focus-visible:ring-1/);
        expect(control.className).toMatch(/focus-visible:ring-ring/);
      }
    });

    it("LRM-1331: bar + reserves share fine+≥640 gate (no shell-wide pr gutter)", () => {
      render(
        <ChannelMessageBubble
          message={makeMessage()}
          currentUserId="user-1"
          onReact={vi.fn()}
        />,
      );
      const shell = screen.getByTestId("message-shell");
      const bar = screen.getByTestId("message-action-bar");
      const authorRow = screen.getByTestId("message-author-row");
      const gate = "[@media(pointer:fine)_and_(min-width:640px)]";
      expect(shell.className).not.toMatch(/pr-\[136px\]/);
      expect(authorRow.className).toContain(`${gate}:pr-[162px]`);
      expect(bar.className).toContain(`${gate}:flex`);
      // Narrow fine (<640) must not get the hover bar (falls back to long-press).
      expect(bar.className).not.toMatch(/\[@media\(pointer:fine\)\]:flex\b/);
    });

    it("a11y: the shell is decoration only — no role, no aria, no new tab stop", () => {
      render(<ChannelMessageBubble message={makeMessage()} currentUserId="user-1" />);
      const shell = screen.getByTestId("message-shell");
      expect(shell.getAttribute("role")).toBeNull();
      expect(shell.getAttribute("tabindex")).toBeNull();
      expect(
        Array.from(shell.attributes).filter((attr) => attr.name.startsWith("aria-")),
      ).toHaveLength(0);
    });
  });

  it("renders compact continuations without author chrome but keeps body and gutter time (LRM-255)", () => {
    render(
      <ChannelMessageBubble
        message={makeMessage({
          type: "user",
          author_id: "user-a",
          author_name: "Alice",
          content: "follow-up",
          created_at: "2026-06-17T09:16:00Z",
        })}
        currentUserId="user-1"
        compact
      />,
    );

    expect(screen.getByTestId("message-bubble")).toHaveAttribute("data-message-group", "compact");
    expect(screen.queryByText("Alice")).not.toBeInTheDocument();
    expect(screen.getByTestId("message-gutter-time")).toHaveTextContent("09:16");
    expect(screen.getByText("follow-up")).toBeInTheDocument();
    expect(screen.getByTestId("message-action-bar")).toBeInTheDocument();
  });

  it("does not show Owner/Admin chrome on message author rows (LRM-270)", () => {
    const { rerender } = render(
      <ChannelMessageBubble
        message={makeMessage({
          type: "user",
          author_id: "user-owner",
          author_name: "frank",
          content: "hello from owner",
        })}
        currentUserId="user-1"
      />,
    );

    expect(screen.getByText("Frank An")).toBeInTheDocument();
    expect(screen.queryByTestId("message-author-role")).not.toBeInTheDocument();
    expect(screen.queryByText("Owner")).not.toBeInTheDocument();

    rerender(
      <ChannelMessageBubble
        message={makeMessage({
          type: "user",
          author_id: "user-admin",
          author_name: "admin",
          content: "admin note",
        })}
        currentUserId="user-1"
      />,
    );
    expect(screen.getByText("Admin User")).toBeInTheDocument();
    expect(screen.queryByTestId("message-author-role")).not.toBeInTheDocument();
    expect(screen.queryByText("Admin")).not.toBeInTheDocument();

    rerender(
      <ChannelMessageBubble
        message={makeMessage({
          type: "user",
          author_id: "user-2",
          author_name: "bob",
          content: "member note",
        })}
        currentUserId="user-1"
      />,
    );
    expect(screen.getByText("Bob Display")).toBeInTheDocument();
    expect(screen.queryByTestId("message-author-role")).not.toBeInTheDocument();
  });

  it("opens the agent side panel when an agent author's avatar/name is clicked (#488)", () => {
    const onOpenAgent = vi.fn();
    renderWithStickerCatalog(
      <ChannelMessageBubble
        message={makeMessage()}
        currentUserId="user-1"
        onOpenAgent={onOpenAgent}
      />,
    );

    fireEvent.click(screen.getAllByText("Research Agent")[0]!);
    expect(onOpenAgent).toHaveBeenCalledWith("agent-1", {
      display_name: "Research Agent",
      avatar_url: null,
    });
  });

  it("opens the member Profile dock when a human author's avatar/name is clicked (LRM-619 parity)", () => {
    const onOpenMember = vi.fn();
    renderWithStickerCatalog(
      <ChannelMessageBubble
        message={makeMessage({ type: "user", author_id: "user-9", author_name: "andong3" })}
        currentUserId="user-1"
        onOpenMember={onOpenMember}
      />,
    );

    fireEvent.click(screen.getAllByText("andong3")[0]!);
    expect(onOpenMember).toHaveBeenCalledWith("user-9");
  });

  it("renders the author avatar straight from the message payload (#453), not a viewer-scoped lookup", () => {
    render(
      <ChannelMessageBubble
        message={makeMessage({ author_avatar_url: "/uploads/agent-avatar.png" })}
        currentUserId="user-1"
      />,
    );
    // The avatar image comes from the payload's `author_avatar_url` (aggregated
    // by the BE for every viewer, #574). Directory fallback is only used when
    // payload + sticky cache both miss.
    const img = screen.getByRole("img", { name: /Research Agent/i });
    expect(img).toHaveAttribute("src", "/uploads/agent-avatar.png");
  });

  it("LRM-202: reuses a prior same-author avatar when a later message omits author_avatar_url", () => {
    const { rerender } = render(
      <ChannelMessageBubble
        message={makeMessage({
          id: "msg-1",
          author_avatar_url: "/uploads/agent-avatar.png",
          content: "first",
        })}
        currentUserId="user-1"
      />,
    );
    expect(screen.getByRole("img", { name: /Research Agent/i })).toHaveAttribute(
      "src",
      "/uploads/agent-avatar.png",
    );

    rerender(
      <ChannelMessageBubble
        message={makeMessage({
          id: "msg-2",
          author_avatar_url: null,
          content: "second",
        })}
        currentUserId="user-1"
      />,
    );
    expect(screen.getByRole("img", { name: /Research Agent/i })).toHaveAttribute(
      "src",
      "/uploads/agent-avatar.png",
    );
  });

  it("LRM-218: falls back to actor-directory avatar when payload omits URL", () => {
    getActorAvatarUrlMock.mockImplementation((type, id) =>
      type === "agent" && id === "agent-1" ? "/agent-avatars/human-02.jpg" : null,
    );
    render(
      <ChannelMessageBubble
        message={makeMessage({
          author_id: "agent-1",
          author_avatar_url: null,
          content: "no payload avatar",
        })}
        currentUserId="user-1"
      />,
    );
    expect(screen.getByRole("img", { name: /Research Agent/i })).toHaveAttribute(
      "src",
      "/agent-avatars/human-02.jpg",
    );
  });

  it("LRM-218: directory fallback seeds sticky cache for consecutive same-author bubbles", () => {
    getActorAvatarUrlMock.mockImplementation((type, id) =>
      type === "agent" && id === "agent-1" ? "/agent-avatars/human-02.jpg" : null,
    );
    const { rerender } = render(
      <ChannelMessageBubble
        message={makeMessage({
          id: "msg-1",
          author_id: "agent-1",
          author_avatar_url: null,
          content: "first",
        })}
        currentUserId="user-1"
      />,
    );
    expect(screen.getByRole("img", { name: /Research Agent/i })).toHaveAttribute(
      "src",
      "/agent-avatars/human-02.jpg",
    );

    getActorAvatarUrlMock.mockReturnValue(null);
    rerender(
      <ChannelMessageBubble
        message={makeMessage({
          id: "msg-2",
          author_id: "agent-1",
          author_avatar_url: null,
          content: "second",
        })}
        currentUserId="user-1"
      />,
    );
    expect(screen.getByRole("img", { name: /Research Agent/i })).toHaveAttribute(
      "src",
      "/agent-avatars/human-02.jpg",
    );
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
    expect(bubble.className).toContain("bg-[#fef9e8]");
  });

  it("applies dark self-mention row tokens for agent @-mentions of the viewer", () => {
    const msg = makeMessage({
      type: "agent",
      author_id: "agent-1",
      author_name: "Bot",
      content: "ping [@Alice](mention://member/user-1) check this",
    });
    render(<ChannelMessageBubble message={msg} currentUserId="user-1" />);

    const bubble = screen.getByTestId("message-bubble");
    expect(bubble).toHaveAttribute("data-self-mentioned", "true");
    expect(bubble.className).toContain("dark:border-brand");
    expect(bubble.className).toContain("dark:bg-brand/[0.06]");
    expect(bubble.className).toContain("[&_.mention]:dark:bg-transparent");
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
      "bg-[#fef9e8]",
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
    expect(bubble.className).not.toContain("bg-[#fef9e8]");
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

  it("LRM-224/LRM-462: agent and member message avatars show status dots", () => {
    // LRM-223 option B keeps agent dots; LRM-462 adds human-member presence
    // dots backed by workspace WS sessions.
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
    expect(screen.getByLabelText(/^Status:/)).toBeInTheDocument();
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

    // #1637 dropped the separate type-label badge (was "Message"/"Agent"/etc)
    // from the quote card — the new `> author: summary` format has no badge
    // to assert on; the author-resolves-to-live-identity behavior (the
    // subject of this test) is unaffected and still checked below.
    expect(screen.getByText("Alice Display")).toBeInTheDocument();
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

  it("keeps long messages as full DOM content behind a Slack-height collapsed preview", async () => {
    render(
      <ChannelMessageBubble
        message={makeMessage({ content: Array.from({ length: 20 }, (_, index) => `Line ${index}`).join("\n") })}
        currentUserId="user-1"
      />,
    );

    const body = screen.getByTestId("message-body");
    Object.defineProperties(body, {
      scrollHeight: { configurable: true, value: 420 },
      clientHeight: { configurable: true, value: 320 },
    });
    fireEvent(window, new Event("resize"));

    await waitFor(() => {
      expect(body).toHaveAttribute("data-collapsed", "true");
    });
    expect(body).toHaveClass("max-h-[320px]");
    expect(body).toHaveTextContent("Line 19");
    expect(screen.getByTestId("message-collapse-fade")).toBeInTheDocument();

    const expand = screen.getByRole("button", { name: "See more" });
    // LRM-302: text link, not centered pill covering the body.
    expect(expand.className).toMatch(/text-sm/);
    expect(expand.className).not.toMatch(/rounded-full/);
    expect(expand.className).not.toMatch(/min-h-11/);
    expect(expand.className).not.toMatch(/shadow-sm/);
    expect(screen.getByTestId("message-collapse-fade").className).toMatch(
      /justify-start/,
    );
    expect(screen.getByTestId("message-collapse-fade").className).toMatch(
      /from-background/,
    );
    expect(screen.getByTestId("message-collapse-fade").className).toMatch(/from-35%/);

    await userEvent.click(expand);

    expect(body).not.toHaveAttribute("data-collapsed");
    expect(screen.queryByRole("button", { name: "See more" })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "See less" })).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "See less" }));
    expect(body).toHaveAttribute("data-collapsed", "true");
    expect(screen.getByRole("button", { name: "See more" })).toBeInTheDocument();
  });

  it("uses self-mention row tokens on the collapse fade (LRM-368)", async () => {
    const longBody = Array.from({ length: 20 }, (_, index) => `Line ${index}`).join("\n");
    const msg = makeMessage({
      type: "user",
      author_id: "user-2",
      author_name: "bob",
      content: `hey [@Alice](mention://member/user-1)\n${longBody}`,
    });
    render(<ChannelMessageBubble message={msg} currentUserId="user-1" />);

    const body = screen.getByTestId("message-body");
    Object.defineProperties(body, {
      scrollHeight: { configurable: true, value: 420 },
      clientHeight: { configurable: true, value: 320 },
    });
    fireEvent(window, new Event("resize"));

    await waitFor(() => {
      expect(body).toHaveAttribute("data-collapsed", "true");
    });

    const fade = screen.getByTestId("message-collapse-fade");
    expect(fade.className).toContain("from-[#fef9e8]");
    expect(fade.className).not.toContain("from-background");
  });

  it("#689: does not construct a per-row ResizeObserver — window resize alone drives re-measurement", () => {
    // A per-row ResizeObserver used to exist to catch late content growth
    // and re-apply the collapse cap. Two late-growth paths still exist
    // (markdown inline images without forwarded dimensions, sticker
    // placeholder→loaded-box swaps) — the tradeoff is accepted as
    // cosmetic-only (see the component's #689 comment for the full
    // reasoning): removing this observer stops it from double-firing
    // Virtuoso's own per-item re-settle on every one of those events, which
    // is the actual mid-scroll jank source. Assert it stays gone.
    const OriginalResizeObserver = globalThis.ResizeObserver;
    const ctorSpy = vi.fn();
    class SpyingResizeObserver extends OriginalResizeObserver {
      constructor(callback: ResizeObserverCallback) {
        super(callback);
        ctorSpy();
      }
    }
    vi.stubGlobal("ResizeObserver", SpyingResizeObserver);

    render(
      <ChannelMessageBubble
        message={makeMessage({ content: Array.from({ length: 20 }, (_, index) => `Line ${index}`).join("\n") })}
        currentUserId="user-1"
      />,
    );

    expect(ctorSpy).not.toHaveBeenCalled();
    vi.stubGlobal("ResizeObserver", OriginalResizeObserver);
  });

  it("does not show the collapse affordance for short messages", () => {
    render(
      <ChannelMessageBubble
        message={makeMessage({ content: "Short historical answer" })}
        currentUserId="user-1"
      />,
    );

    const body = screen.getByTestId("message-body");
    Object.defineProperties(body, {
      scrollHeight: { configurable: true, value: 48 },
      clientHeight: { configurable: true, value: 48 },
    });
    fireEvent(window, new Event("resize"));

    expect(body).not.toHaveAttribute("data-collapsed");
    expect(screen.queryByRole("button", { name: "See more" })).not.toBeInTheDocument();
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

  it("renders thread reply preview when the message has replies (LRM-873)", () => {
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

    expect(screen.getByTestId("thread-reply-preview")).toHaveTextContent("preview:2");
    expect(screen.getByRole("button", { name: "👍1" })).toBeInTheDocument();
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

  it("LRM-364: reaction hover shows group-manager display name, never Unknown Agent", async () => {
    const user = userEvent.setup();
    render(
      <ChannelMessageBubble
        message={makeMessage({
          reactions: [
            {
              id: "reaction-beckham",
              channel_id: "c1",
              message_id: "m1",
              actor_type: "agent",
              actor_id: "agent-beckham",
              emoji: "👍",
              created_at: "2026-06-17T09:16:00Z",
            },
          ],
        })}
        currentUserId="user-1"
        onReact={vi.fn()}
      />,
    );

    const pill = screen.getByRole("button", { name: "👍1" });
    await user.hover(pill);
    expect(await screen.findByText("贝克汉姆")).toBeInTheDocument();
    expect(screen.queryByText("Unknown Agent")).not.toBeInTheDocument();
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
    expect(actionBar).toHaveClass(
      "[@media(pointer:fine)_and_(min-width:640px)]:flex",
    );
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

  // LRM-1174 (LRM-1173 freeze B): the JS gate (pointerType === "touch" +
  // isMobileActionViewport) can open the sheet on a touch laptop in a narrow
  // window, but the dialog also carried `[@media(pointer:fine)]:hidden` — the
  // same device reports `pointer: fine`, so showModal() put an INVISIBLE modal
  // in the top layer and the page went inert (cellA-mixed-760-DEAD). Visibility
  // and modality must come from one source: the JS gate only.
  it("keeps the mobile action sheet visible on a mixed-pointer narrow window (LRM-1174)", async () => {
    setMixedPointerNarrowViewport();
    render(
      <ChannelMessageBubble message={makeMessage()} currentUserId="user-1" onReact={vi.fn()} />,
    );

    const bubble = screen.getByTestId("message-bubble");
    fireEvent.pointerDown(bubble, { pointerType: "touch", clientX: 0, clientY: 0 });

    const menu = await screen.findByRole("dialog", { name: "Message actions" });
    expect(menu.className).not.toMatch(/\[@media\(pointer:fine\)\]:hidden/);

    await userEvent.click(within(menu).getByRole("button", { name: "Add reaction" }));
    const reactionSheet = await screen.findByRole("dialog", { name: "Add reaction" });
    expect(reactionSheet.className).not.toMatch(/\[@media\(pointer:fine\)\]:hidden/);

    // LRM-1331: hover action bar is fine-pointer + ≥640 only.
    expect(screen.getByTestId("message-action-bar")).toHaveClass(
      "[@media(pointer:fine)_and_(min-width:640px)]:flex",
    );
  });

  it("returns focus to the triggering bubble when the mobile action sheet closes (LRM-1174)", async () => {
    setMobileViewport();
    copyTextMock.mockResolvedValue(true);
    render(<ChannelMessageBubble message={makeMessage()} currentUserId="user-1" />);

    const bubble = screen.getByTestId("message-bubble");
    fireEvent.pointerDown(bubble, { pointerType: "touch", clientX: 0, clientY: 0 });
    fireEvent.pointerUp(bubble, { pointerType: "touch", clientX: 0, clientY: 0 });

    const menu = await screen.findByRole("dialog", { name: "Message actions" });
    await userEvent.click(within(menu).getByRole("button", { name: "Copy" }));

    await waitFor(() =>
      expect(screen.queryByRole("dialog", { name: "Message actions" })).not.toBeInTheDocument(),
    );
    // Removing a top-layer <dialog> drops activeElement to <body>; the bubble
    // that opened the sheet must get focus back so the next action/keyboard
    // step continues from the same message.
    await waitFor(() => expect(document.activeElement).toBe(bubble));
  });

  it("returns focus to the triggering bubble when the mobile reaction sheet closes (LRM-1174)", async () => {
    setMobileViewport();
    const onReact = vi.fn();
    render(
      <ChannelMessageBubble message={makeMessage()} currentUserId="user-1" onReact={onReact} />,
    );

    const bubble = screen.getByTestId("message-bubble");
    fireEvent.pointerDown(bubble, { pointerType: "touch", clientX: 0, clientY: 0 });
    fireEvent.pointerUp(bubble, { pointerType: "touch", clientX: 0, clientY: 0 });

    const menu = await screen.findByRole("dialog", { name: "Message actions" });
    await userEvent.click(within(menu).getByRole("button", { name: "Add reaction" }));
    const reactionSheet = await screen.findByRole("dialog", { name: "Add reaction" });

    // Handing the primary sheet over to the reaction sheet is NOT a dismissal —
    // focus must stay inside the overlay stack, not snap back to the bubble.
    expect(document.activeElement).not.toBe(bubble);

    await userEvent.click(within(reactionSheet).getByRole("button", { name: "🎉" }));

    await waitFor(() =>
      expect(screen.queryByRole("dialog", { name: "Add reaction" })).not.toBeInTheDocument(),
    );
    await waitFor(() => expect(document.activeElement).toBe(bubble));
  });

  it("hides the permanent mobile More trigger; long-press and left-swipe open the action sheet (LRM-495)", async () => {
    setMobileViewport();
    render(
      <ChannelMessageBubble
        message={makeMessage()}
        currentUserId="user-1"
        onReact={vi.fn()}
        onQuote={vi.fn()}
      />,
    );

    expect(screen.queryByRole("button", { name: "More actions" })).not.toBeInTheDocument();
    expect(screen.queryByTestId("message-mobile-more-trigger")).not.toBeInTheDocument();
    expect(screen.getByTestId("message-bubble").className).toMatch(/\bpy-1\b/);
    expect(screen.getByTestId("message-bubble").className).not.toMatch(/_44px/);
    expect(screen.queryByRole("dialog", { name: "Message actions" })).not.toBeInTheDocument();

    const bubble = screen.getByTestId("message-bubble");
    fireEvent.pointerDown(bubble, { pointerType: "touch", clientX: 80, clientY: 40 });
    fireEvent.pointerMove(bubble, { pointerType: "touch", clientX: 20, clientY: 42 });
    fireEvent.pointerUp(bubble, { pointerType: "touch", clientX: 20, clientY: 42 });

    expect(await screen.findByRole("dialog", { name: "Message actions" })).toBeInTheDocument();
  });

  it("opens the reaction sheet in place of the action sheet, never both at once (#568)", async () => {
    setMobileViewport();
    const onReact = vi.fn();
    render(
      <ChannelMessageBubble message={makeMessage()} currentUserId="user-1" onReact={onReact} />,
    );

    const bubble = screen.getByTestId("message-bubble");
    fireEvent.pointerDown(bubble, { pointerType: "touch", clientX: 0, clientY: 0 });
    fireEvent.pointerUp(bubble, { pointerType: "touch", clientX: 0, clientY: 0 });

    const menu = await screen.findByRole("dialog", { name: "Message actions" });
    await userEvent.click(within(menu).getByRole("button", { name: "Add reaction" }));

    // The action sheet must be gone before the reaction sheet appears — the two
    // mobile overlays are never mounted together (Iris #568: was a real
    // double-panel bug when the QuickEmojiPicker popover opened on top of the
    // still-open action <dialog>).
    await waitFor(() =>
      expect(screen.queryByRole("dialog", { name: "Message actions" })).not.toBeInTheDocument(),
    );
    const reactionSheet = await screen.findByRole("dialog", { name: "Add reaction" });
    expect(reactionSheet).toBeInTheDocument();

    await userEvent.click(within(reactionSheet).getByRole("button", { name: "🎉" }));

    expect(onReact).toHaveBeenCalledWith(expect.anything(), "🎉");
    await waitFor(() =>
      expect(screen.queryByRole("dialog", { name: "Add reaction" })).not.toBeInTheDocument(),
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
    // LRM-609 SoT A': left-align, no ClipboardList, no side hairlines, no capsule.
    expect(within(systemRow).queryByTestId("system-message-icon")).toBeNull();
    expect(within(systemRow).queryByTestId("system-message-rule")).toBeNull();
    expect(systemRow.className).toContain("pl-10");
    expect(systemRow.className).toContain("text-xs");
    expect(systemRow.className).not.toMatch(/rounded-full/);
  });

  it("projects an issue-lifecycle status change into the item #7 row with a simple inline time (#497)", () => {
    // QueryClient required: LRM-281 may enable member-profiles useQuery when
    // the directory misses; Alice is in the stubbed directory so the query
    // stays disabled, but useQuery still needs a provider.
    renderWithStickerCatalog(
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
    expect(systemRow).toHaveTextContent("@Alice Display started");
    expect(systemRow.textContent).not.toContain("moved");
    expect(systemRow.textContent).not.toContain("in_progress");

    // A typed actor is projected through the same interactive @mention path as
    // message prose. The profile card owns its avatar; the system row does not
    // invent an inline avatar or parse the fallback sentence.
    expect(within(systemRow).getByTestId("actor-mention")).toHaveAttribute(
      "data-mention-type",
      "member",
    );
    expect(within(systemRow).getByTestId("actor-mention")).toHaveAttribute(
      "data-mention-id",
      "user-1",
    );

    // No issue_title on the event → honest interim identifier as the sole link.
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
    expect(within(systemRow).queryByTestId("system-message-icon")).toBeNull();
    expect(within(systemRow).queryByTestId("system-message-rule")).toBeNull();
    expect(systemRow.className).toContain("pl-10");
    expect(systemRow.className).toContain("text-muted-foreground/85");
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
  it("shows neither delete nor edit on the viewer's own message (LRM-695)", () => {
    const { rerender } = render(
      <ChannelMessageBubble
        message={ownMessage()}
        currentUserId="user-1"
        onEdit={vi.fn()}
      />,
    );

    // LRM-695: message delete is removed from the UI (Frank: 只去删除). Edit is
    // unshipped. Neither entry point renders, even on the viewer's own message.
    expect(screen.queryByRole("button", { name: "Delete" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Edit" })).not.toBeInTheDocument();

    // A peer / agent message from another author exposes no edit or delete.
    rerender(
      <ChannelMessageBubble
        message={makeMessage({ type: "user", author_id: "user-2", author_name: "bob" })}
        currentUserId="user-1"
        onEdit={vi.fn()}
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

  it("does not surface a delete affordance (LRM-695 removed it)", () => {
    // LRM-695: message delete is fully removed from the UI (Frank/Wendy seq 4555 —
    // entry code + calls deleted, not dormant). No button renders; the tombstone
    // still renders for server-deleted messages (covered in the list test).
    render(
      <ChannelMessageBubble
        message={ownMessage()}
        currentUserId="user-1"
        onEdit={vi.fn()}
        onReact={vi.fn()}
      />,
    );

    expect(screen.queryByRole("button", { name: "Delete" })).not.toBeInTheDocument();
  });

  it("renders an optimistic pending bubble silently (no Sending…)", () => {
    render(
      <ChannelMessageBubble
        message={makeMessage({
          type: "user",
          author_id: "user-1",
          author_name: "Alice",
          id: "client-1",
          client_message_id: "client-1",
          local_send_status: "pending",
          content: "optimistic pending",
        })}
        currentUserId="user-1"
        onReact={vi.fn()}
        onQuote={vi.fn()}
        onOpenThread={vi.fn()}
      />,
    );

    expect(screen.getByTestId("message-bubble")).toHaveAttribute("data-local-send", "pending");
    expect(screen.getByText("optimistic pending")).toBeInTheDocument();
    // Slack / LRM-271/273/280: no Sending… chrome on pending optimistic rows.
    expect(screen.queryByTestId("message-send-pending")).not.toBeInTheDocument();
    expect(screen.queryByText(/Sending/i)).not.toBeInTheDocument();
    expect(screen.queryByTestId("message-action-bar")).not.toBeInTheDocument();
  });

  it("shows a failed bar with one-click retry for optimistic send failures", async () => {
    const onRetrySend = vi.fn();
    const message = makeMessage({
      type: "user",
      author_id: "user-1",
      author_name: "Alice",
      id: "client-2",
      client_message_id: "client-2",
      local_send_status: "failed",
      content: "optimistic failed",
    });
    render(
      <ChannelMessageBubble
        message={message}
        currentUserId="user-1"
        onRetrySend={onRetrySend}
        onReact={vi.fn()}
      />,
    );

    expect(screen.getByTestId("message-send-failed")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(onRetrySend).toHaveBeenCalledWith(message);
  });

  it("labels transcribed human voice input without inventing stored audio", () => {
    render(
      <ChannelMessageBubble
        message={makeMessage({
          type: "user",
          author_id: "user-1",
          author_name: "Alice",
          content: "spoken question",
          parts: [
            { type: "text", text: "spoken question" },
            { type: "voice", duration_ms: 2400 },
          ],
        })}
        currentUserId="user-1"
      />,
    );

    expect(screen.getByTestId("voice-input-label")).toHaveTextContent("Voice input · 2s");
    expect(screen.queryByTestId("voice-reply-control")).not.toBeInTheDocument();
  });

  it("hides a recorded human voice transcript until the user asks for it", async () => {
    const transcript = "question spoken by Alice";
    render(
      <ChannelMessageBubble
        message={makeMessage({
          type: "user",
          author_id: "user-1",
          author_name: "Alice",
          content: transcript,
          parts: [
            { type: "text", text: transcript },
            {
              type: "voice",
              duration_ms: 2400,
              attachment_id: "recording-1",
              filename: "voice-recording.wav",
              content_type: "audio/wav",
              size_bytes: 48,
            },
          ],
          attachments: [{
            id: "recording-1",
            workspace_id: "workspace-1",
            issue_id: null,
            comment_id: null,
            chat_session_id: null,
            chat_message_id: null,
            uploader_type: "member",
            uploader_id: "user-1",
            filename: "voice-recording.wav",
            url: "/uploads/voice-recording.wav",
            download_url: "/api/attachments/recording-1/download",
            markdown_url: "/api/attachments/recording-1/download",
            content_type: "audio/wav",
            size_bytes: 48,
            created_at: "2026-07-23T00:00:00Z",
          }],
        })}
        currentUserId="user-1"
      />,
    );

    expect(screen.queryByText(transcript)).not.toBeInTheDocument();
    expect(screen.getByTestId("voice-reply-control")).toHaveTextContent('2″');
    await userEvent.click(screen.getByRole("button", { name: "Show transcript" }));
    expect(screen.getByTestId("voice-reply-transcript")).toHaveTextContent(transcript);
  });

  it("hides an Agent voice transcript until its attached text control is opened", async () => {
    const transcript = "One day, the teacher asked what echo means.";
    render(
      <ChannelMessageBubble
        message={makeMessage({
          content: transcript,
          parts: [
            { type: "text", text: transcript },
            { type: "voice" },
          ],
        })}
        currentUserId="user-1"
      />,
    );

    expect(screen.queryByText(transcript)).not.toBeInTheDocument();
    const toggle = screen.getByRole("button", { name: "Show transcript" });
    expect(toggle).toHaveAttribute("aria-expanded", "false");

    await userEvent.click(toggle);

    const panel = screen.getByTestId("voice-reply-transcript");
    expect(screen.getByRole("region", { name: "Voice transcript" })).toBe(panel);
    expect(toggle).toHaveAttribute("aria-expanded", "true");
    expect(toggle).toHaveAttribute("aria-controls", panel.id);
    expect(within(panel).getByText("Voice transcript")).toBeInTheDocument();
    expect(within(panel).getByText(transcript)).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Hide transcript" }));
    expect(screen.queryByTestId("voice-reply-transcript")).not.toBeInTheDocument();
  });

  it("keeps the Agent voice replay control in a compact message group", async () => {
    render(
      <ChannelMessageBubble
        message={makeMessage({
          content: "spoken answer",
          parts: [
            { type: "text", text: "spoken answer" },
            { type: "voice" },
          ],
        })}
        currentUserId="user-1"
        compact
      />,
    );

    expect(screen.getByTestId("message-bubble")).toHaveAttribute("data-message-group", "compact");
    expect(screen.getByRole("button", { name: "Play voice reply" })).toBeInTheDocument();
    await waitFor(() => expect(screen.getByTestId("voice-reply-control")).toHaveTextContent('3″'));
  });
});

describe("ChannelMessageBubble — #1276 INV-3 in-flight status", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  function pending() {
    return makeMessage({
      type: "user",
      author_id: "user-1",
      author_name: "Alice",
      id: "c-1",
      client_message_id: "c-1",
      local_send_status: "pending",
      content: "hi",
    });
  }

  it("stays silent while pending under 1.0s (LRM-271/273 no-flash preserved)", () => {
    render(
      <ChannelMessageBubble
        message={pending()}
        currentUserId="user-1"
        onReact={vi.fn()}
      />,
    );
    act(() => vi.advanceTimersByTime(999));
    expect(screen.queryByTestId("message-sending")).not.toBeInTheDocument();
    expect(screen.queryByText(/Sending/i)).not.toBeInTheDocument();
  });

  it("reveals a low-emphasis Sending… in a role=status region once pending crosses 1.0s", () => {
    render(
      <ChannelMessageBubble
        message={pending()}
        currentUserId="user-1"
        onReact={vi.fn()}
      />,
    );
    act(() => vi.advanceTimersByTime(1000));
    expect(screen.getByTestId("message-sending")).toHaveTextContent("Sending…");
    // <output> is an implicit role=status live region (announced politely).
    expect(screen.getByRole("status")).toHaveAttribute(
      "data-testid",
      "message-send-status",
    );
  });

  it("replaces Sending… with Failed in the same status region on failure (never dual sent/sending)", () => {
    const msg = pending();
    const { rerender } = render(
      <ChannelMessageBubble
        message={msg}
        currentUserId="user-1"
        onRetrySend={vi.fn()}
        onReact={vi.fn()}
      />,
    );
    act(() => vi.advanceTimersByTime(1000));
    expect(screen.getByTestId("message-sending")).toBeInTheDocument();
    rerender(
      <ChannelMessageBubble
        message={{ ...msg, local_send_status: "failed" }}
        currentUserId="user-1"
        onRetrySend={vi.fn()}
        onReact={vi.fn()}
      />,
    );
    expect(screen.queryByTestId("message-sending")).not.toBeInTheDocument();
    expect(screen.getByTestId("message-send-failed")).toBeInTheDocument();
    expect(screen.getByTestId("message-send-status")).toBeInTheDocument();
  });
});
