import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import type { ChannelMessage } from "@multica/core/types";
import { ThreadRootPreview } from "./thread-root-preview";

vi.mock("../../common/markdown", () => ({
  MemoizedMarkdown: ({ children }: { children: string }) => <span>{children}</span>,
}));

vi.mock("../../issues/components/comment-card", () => ({
  AttachmentList: () => null,
}));

vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: ({ actorId }: { actorId: string }) => (
    <span data-testid="actor-avatar">{actorId}</span>
  ),
}));

vi.mock("../../common/actor-profile-popover", () => ({
  ActorProfileTrigger: ({ children }: { children: ReactNode }) => children,
}));

vi.mock("@multica/core/workspace/hooks", () => ({
  useActorName: () => ({
    getActorName: (_type: string, id: string, fallback?: string) =>
      id === "user-1" ? "Frank An" : fallback,
  }),
}));

vi.mock("../../common/use-viewing-timezone", () => ({
  useViewingTimezone: () => "UTC",
}));

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (
      selector: (resources: {
        message: { agent_badge: string };
        thread: { collapse_message: string; show_full_message: string; view_parent: string };
        time: { today: string; yesterday: string };
      }) => string,
    ) =>
      selector({
        message: { agent_badge: "Agent" },
        time: { today: "Today", yesterday: "Yesterday" },
        thread: {
          collapse_message: "Collapse message",
          show_full_message: "Show full message",
          view_parent: "Back to main chat",
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
  it("keeps parent preview lightweight without duplicate navigation actions", () => {
    render(
      <ThreadRootPreview
        message={makeMessage()}
        currentUserId="user-1"
      />,
    );

    expect(screen.getByText("Thread root content")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Show full message" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Back to main chat" })).not.toBeInTheDocument();
  });

  it("uses a compact root preview with a local view-parent action", async () => {
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

    expect(container.querySelector(".line-clamp-3")).not.toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Back to main chat" }));
    expect(onViewParent).toHaveBeenCalledTimes(1);
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

  it("uses compact part text for sticker root previews", () => {
    render(
      <ThreadRootPreview
        message={makeMessage({
          content: ":sticker:hi:",
          parts: [{ type: "sticker", sticker_id: "hi", alt: "Hi sticker" }],
        })}
        currentUserId="user-1"
      />,
    );

    expect(screen.getByText("[Sticker] Hi sticker")).toBeInTheDocument();
    expect(screen.queryByText(":sticker:hi:")).not.toBeInTheDocument();
  });

  // GAP 2 — load-bearing invariant. A structured-action envelope with no
  // renderable text parts and no output must unwrap to the neutral "…"
  // placeholder, which keeps `compactBody` truthy so the compact body NEVER
  // falls through to rendering the raw envelope JSON as markdown. If a future
  // change made the placeholder empty, `compactBody` would become falsy and the
  // raw JSON would leak — this test would then fail.
  it("shows the neutral placeholder, never raw envelope JSON, for a root with no renderable text", () => {
    const raw = '{"action":"message_send","parts":[{"type":"image","url":"x"}]}';
    const { container } = render(
      <ThreadRootPreview
        message={makeMessage({ content: raw, parts: [] })}
        currentUserId="user-1"
      />,
    );

    expect(screen.getByText("…")).toBeInTheDocument();
    expect(container.textContent).not.toContain('"action"');
    expect(container.textContent).not.toContain("{");
    expect(container.textContent).not.toContain("image");
  });
});
