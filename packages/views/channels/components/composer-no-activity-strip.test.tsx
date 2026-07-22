/**
 * LRM-228 — composer-adjacent activity chrome must stay gone.
 *
 * Guards against reintroducing ConversationActivityStrip /
 * ConversationAgentActivityLine above the input. The old strip showed
 * "is preparing a reply… / Stop" and "Searching code".
 */
import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import type { ChannelMessage } from "@multica/core/types";
import { ThreadPanel } from "./thread-panel";

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (picker: (keys: Record<string, unknown>) => string) =>
      picker({
        thread: {
          empty_replies: "No replies yet",
          load_failed: "Failed to load",
          follow: "Follow",
          unfollow: "Unfollow",
          following: "Following",
        },
        composer: { send: "Send" },
        quote: { cancel: "Cancel quote" },
      } as never),
  }),
}));

vi.mock("./channel-message-list", () => ({
  ChannelMessageList: ({ header }: { header?: ReactNode }) => (
    <div data-testid="message-list">{header}</div>
  ),
}));

vi.mock("./composer", () => ({
  Composer: ({ editor }: { editor?: ReactNode }) => (
    <div data-testid="composer">{editor}</div>
  ),
}));

vi.mock("./conversation-surface", () => ({
  ConversationHeader: ({ title }: { title?: ReactNode }) => <div>{title}</div>,
}));

vi.mock("./thread-follow-button", () => ({
  ThreadFollowButton: () => null,
}));

vi.mock("./thread-root-preview", () => ({
  ThreadRootPreview: () => <div>root</div>,
}));

vi.mock("./message-quote", () => ({
  ComposerQuotePreview: () => null,
}));

vi.mock("./read-only-conversation-banner", () => ({
  ReadOnlyConversationBanner: ({ children }: { children?: ReactNode }) => (
    <div>{children}</div>
  ),
}));

const root = {
  id: "msg-1",
  channel_id: "ch-1",
  content: "hello",
  created_at: "2026-07-22T00:00:00Z",
} as ChannelMessage;

describe("LRM-228 composer adjacency", () => {
  it("ThreadPanel has no activity strip above the composer", () => {
    const { container } = render(
      <ThreadPanel
        root={root}
        replies={[]}
        currentUserId="user-1"
        isMobile={false}
        onBack={() => {}}
        followed={false}
        onFollowChange={() => {}}
        editor={<textarea aria-label="composer" />}
        onSend={() => {}}
        sendDisabled={false}
      />,
    );

    expect(screen.getByTestId("composer")).toBeInTheDocument();
    expect(screen.getByTestId("message-list")).toBeInTheDocument();
    expect(container.querySelector('[data-testid="conversation-activity-strip"]')).toBeNull();
    expect(container.querySelector('[data-testid="conversation-agent-activity-line"]')).toBeNull();
    expect(screen.queryByText(/preparing a reply/i)).toBeNull();
    expect(screen.queryByText(/Searching code/i)).toBeNull();
  });
});
