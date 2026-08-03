import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import type { ChannelMessage } from "@multica/core/types";
import { ThreadRootPreview } from "./thread-root-preview";

vi.mock("../../common/markdown", () => ({
  MemoizedMarkdown: ({ children }: { children: string }) => <span>{children}</span>,
}));


vi.mock("./message-parts-renderer", () => ({
  MessagePartsRenderer: ({
    parts,
  }: {
    parts: { type: string; text?: string; sticker_id?: string; alt?: string }[];
  }) => (
    <div data-testid="message-parts-renderer">
      {parts.map((part) =>
        part.type === "sticker" ? (
          <img
            key={`sticker:${part.sticker_id ?? part.alt ?? ""}`}
            data-testid="message-sticker"
            alt={part.alt ?? ""}
          />
        ) : part.type === "text" ? (
          <span key={`text:${part.text ?? ""}`}>{part.text}</span>
        ) : null,
      )}
    </div>
  ),
}));

vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: ({ actorId }: { actorId: string }) => (
    <span data-testid="actor-avatar">{actorId}</span>
  ),
}));

vi.mock("../../common/actor-profile-popover", () => ({
  ActorProfileTrigger: ({
    children,
    onClickCapture,
    memberType,
    memberId,
  }: {
    children: ReactNode;
    onClickCapture?: () => void;
    memberType?: string;
    memberId?: string;
  }) => (
    <span
      data-testid="actor-profile-trigger"
      data-member-type={memberType}
      data-member-id={memberId}
      onClickCapture={onClickCapture}
    >
      {children}
    </span>
  ),
}));

vi.mock("@multica/core/workspace/hooks", () => ({
  useActorName: () => ({
    getActorName: (_type: string, id: string, fallback?: string) =>
      id === "user-1"
        ? "Frank An"
        : id === "user-owner"
          ? "Frank An"
          : fallback,
    getMemberRole: (id: string) =>
      id === "user-owner" || id === "user-1" ? ("owner" as const) : null,
    getMemberHonor: (id: string) =>
      id === "user-1" ? { level: 42, name_style: "default" } : undefined,
    getAgentFleetRank: () => undefined,
    getAgentHonorLevel: () => 8,
  }),
}));

vi.mock("../../common/use-viewing-timezone", () => ({
  useViewingTimezone: () => "UTC",
}));

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (
      selector: (resources: {
        message: {
          agent_badge: string;
          voice_loading: string;
          voice_stop: string;
          voice_retry: string;
          voice_play: string;
          voice_hide_transcript: string;
          voice_show_transcript: string;
          voice_transcript_label: string;
          voice_input: string;
          voice_input_duration: string;
          expand_action: string;
          collapse_action: string;
        };
        profile_popover: {
          role: { owner: string; admin: string; member: string; agent: string };
        };
        thread: { collapse_message: string; show_full_message: string; view_parent: string };
        time: { today: string; yesterday: string };
      }) => string,
    ) =>
      selector({
        message: {
          agent_badge: "Agent",
          voice_loading: "Loading voice",
          voice_stop: "Stop voice",
          voice_retry: "Retry voice",
          voice_play: "Play voice",
          voice_hide_transcript: "Hide transcript",
          voice_show_transcript: "Show transcript",
          voice_transcript_label: "Voice transcript",
          voice_input: "Voice input",
          voice_input_duration: "Voice input duration",
          expand_action: "See more",
          collapse_action: "See less",
        },
        profile_popover: {
          role: { owner: "Owner", admin: "Admin", member: "Member", agent: "Agent" },
        },
        time: { today: "Today", yesterday: "Yesterday" },
        thread: {
          collapse_message: "Collapse message",
          show_full_message: "Show full message",
          view_parent: "View original message →",
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
    content: "Thread root content",
    source: "multica",
    external_message_id: null,
    client_message_id: null,
    created_at: "2026-06-17T09:15:00Z",
    ...overrides,
    seq,
  };
}

describe("ThreadRootPreview", () => {
  it("uses the permanent honor-level crest for an agent root author", () => {
    const { container } = render(
      <ThreadRootPreview message={makeMessage()} currentUserId="user-1" />,
    );

    expect(container.querySelector('[data-agent-honor-level="8"]')).toBeInTheDocument();
  });

  it("uses the user's armor level crest for a human root author", () => {
    const { container } = render(
      <ThreadRootPreview
        message={makeMessage({
          type: "user",
          author_id: "user-1",
          author_name: "Frank An",
        })}
        currentUserId="user-owner"
      />,
    );

    expect(container.querySelector('[data-user-honor-level="42"]')).toBeInTheDocument();
  });

  it("keeps parent preview lightweight without duplicate navigation actions", () => {
    render(
      <ThreadRootPreview
        message={makeMessage()}
        currentUserId="user-1"
      />,
    );

    expect(screen.getByText("Thread root content")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Show full message" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "View original message →" })).not.toBeInTheDocument();
  });

  it("uses a full root body with a local view-parent action (LRM-572)", async () => {
    const onViewParent = vi.fn();
    const { container } = render(
      <ThreadRootPreview
        message={makeMessage({
          content: ["line 1", "line 2", "line 3", "line 4", "line 5"].join("\n"),
        })}
        currentUserId="user-1"
        onViewParent={onViewParent}
      />,
    );

    // Full body (not compact line-clamp); long content may still get main-column See more.
    expect(container.querySelector(".line-clamp-3")).toBeNull();
    expect(screen.getByTestId("thread-root-body")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "View original message →" }));
    expect(onViewParent).toHaveBeenCalledTimes(1);
  });

  it("keeps border-b layering without brand spine/tint (LRM-572)", () => {
    const { container } = render(
      <ThreadRootPreview message={makeMessage()} currentUserId="user-1" />,
    );
    const root = container.firstElementChild as HTMLElement;
    expect(root.className).toMatch(/border-b/);
    expect(root.className).not.toMatch(/border-l-|bg-primary\//);
  });

  it("opens the agent side panel when the root agent author's avatar/name is clicked (#488)", () => {
    const onOpenAgent = vi.fn();
    render(
      <ThreadRootPreview
        message={makeMessage({ type: "agent", author_id: "agent-1" })}
        currentUserId="user-1"
        onOpenAgent={onOpenAgent}
      />,
    );

    // Both the avatar and the name are wrapped in a trigger; clicking either
    // fires the capture handler with the agent id (parity with the channel bubble).
    const [firstTrigger] = screen.getAllByTestId("actor-profile-trigger");
    fireEvent.click(firstTrigger!);
    expect(onOpenAgent).toHaveBeenCalledWith("agent-1", {
      display_name: "Research Agent",
      avatar_url: null,
    });
  });

  it("opens the member Profile dock when the root human author's avatar/name is clicked (LRM-619 parity)", () => {
    const onOpenMember = vi.fn();
    render(
      <ThreadRootPreview
        message={makeMessage({ type: "user", author_id: "user-9", author_name: "andong3" })}
        currentUserId="user-2"
        onOpenMember={onOpenMember}
      />,
    );

    const [firstTrigger] = screen.getAllByTestId("actor-profile-trigger");
    fireEvent.click(firstTrigger!);
    expect(onOpenMember).toHaveBeenCalledWith("user-9");
  });

  it("member avatar click stays a no-op when no onOpenMember is wired (hover card only)", () => {
    const onOpenAgent = vi.fn();
    render(
      <ThreadRootPreview
        message={makeMessage({ type: "user", author_id: "user-1", author_name: "andong3" })}
        currentUserId="user-2"
        onOpenAgent={onOpenAgent}
      />,
    );

    screen.getAllByTestId("actor-profile-trigger").forEach((el) => fireEvent.click(el));
    expect(onOpenAgent).not.toHaveBeenCalled();
  });

  it("omits Agent pill and Owner/Admin chrome on author rows (LRM-270)", () => {
    const { rerender } = render(
      <ThreadRootPreview message={makeMessage()} currentUserId="user-1" />,
    );
    expect(screen.getByText("Research Agent")).toBeInTheDocument();
    expect(screen.queryByText("Agent")).not.toBeInTheDocument();
    expect(screen.queryByTestId("message-author-role")).not.toBeInTheDocument();

    rerender(
      <ThreadRootPreview
        message={makeMessage({
          type: "user",
          author_id: "user-owner",
          author_name: "frank",
        })}
        currentUserId="user-2"
      />,
    );
    expect(screen.getByText("Frank An")).toBeInTheDocument();
    expect(screen.queryByTestId("message-author-role")).not.toBeInTheDocument();
    expect(screen.queryByText("Owner")).not.toBeInTheDocument();
  });

  it("uses the live display name for the root author label", () => {
    render(
      <ThreadRootPreview
        message={makeMessage({
          type: "user",
          author_id: "user-1",
          author_name: "andong3",
        })}
        currentUserId="user-1"
      />,
    );

    expect(screen.getByText("Frank An")).toBeInTheDocument();
    expect(screen.queryByText("andong3")).not.toBeInTheDocument();
  });

  // Sticker root previews must render the sticker as an IMAGE, matching the
  // channel message bubble (MessagePartsRenderer) — not the flattened
  // `[Sticker] …` label. This is what keeps channel and thread rendering
  // consistent for the same message.
  it("renders sticker parts as images in the root preview", () => {
    render(
      <ThreadRootPreview
        message={makeMessage({
          content: ":sticker:hi:",
          parts: [{ type: "sticker", sticker_id: "hi", alt: "Hi sticker" }],
        })}
        currentUserId="user-1"
      />,
    );

    expect(screen.getByTestId("message-sticker")).toBeInTheDocument();
    expect(screen.queryByText("[Sticker] Hi sticker")).not.toBeInTheDocument();
    expect(screen.queryByText(":sticker:hi:")).not.toBeInTheDocument();
  });

  it("keeps a recorded voice root as a playable bubble until its transcript is opened", () => {
    const transcript = "question spoken in the thread root";
    render(
      <ThreadRootPreview
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
            workspace_id: "w1",
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
    fireEvent.click(screen.getByRole("button", { name: "Show transcript" }));
    expect(screen.getByTestId("voice-reply-transcript")).toHaveTextContent(transcript);
  });

  // GAP 2 — load-bearing invariant. A structured-action envelope with no
  // renderable text parts and no output must NOT fall through to rendering the
  // raw envelope JSON as markdown in the thread root (LRM-572 full body path).
  it("never renders raw envelope JSON for a root with no renderable text", () => {
    const raw = '{"action":"message_send","parts":[{"type":"image","url":"x"}]}';
    const { container } = render(
      <ThreadRootPreview
        message={makeMessage({ content: raw, parts: [] })}
        currentUserId="user-1"
      />,
    );

    expect(container.textContent).not.toContain('"action"');
    expect(container.textContent).not.toContain("{");
    expect(container.textContent).not.toContain("image");
  });
});
