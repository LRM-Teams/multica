import { render, screen } from "@testing-library/react";
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

vi.mock("../../i18n", () => ({
  useT: () => ({
    t: (
      selector: (resources: {
        message: { agent_badge: string };
        thread: { collapse_message: string; show_full_message: string; view_parent: string };
      }) => string,
    ) =>
      selector({
        message: { agent_badge: "Agent" },
        thread: {
          collapse_message: "Collapse message",
          show_full_message: "Show full message",
          view_parent: "Back to main chat",
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
    content: "Thread root content",
    source: "multica",
    external_message_id: null,
    created_at: "2026-06-17T09:15:00Z",
    ...overrides,
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
});
