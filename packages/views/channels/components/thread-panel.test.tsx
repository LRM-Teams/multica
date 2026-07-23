import { fireEvent, render, screen, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import type { ChannelMessage } from "@multica/core/types";
import { TooltipProvider } from "@multica/ui/components/ui/tooltip";
import { ThreadPanel } from "./thread-panel";
import { deriveThreadParticipants } from "./thread-participants";

function renderPanel(ui: ReactNode) {
  return render(<TooltipProvider delay={0}>{ui}</TooltipProvider>);
}

// Capture the props ThreadPanel hands the reply list so the "no nesting"
// contract (a reply inside a thread gets no open-thread affordance) can be
// asserted structurally rather than by drilling into the virtualized list.
const messageListProps = vi.fn();

vi.mock("./channel-message-list", () => ({
  ChannelMessageList: (props: {
    messages: ChannelMessage[];
    header?: ReactNode;
    emptyLabel: string;
    onOpenThread?: unknown;
  }) => {
    messageListProps(props);
    return (
      <div data-testid="reply-list">
        {props.header}
        {props.messages.length === 0 ? (
          <div>{props.emptyLabel}</div>
        ) : (
          props.messages.map((m) => (
            <div key={m.id} data-testid="reply-row">
              {m.content}
            </div>
          ))
        )}
      </div>
    );
  },
}));

vi.mock("./thread-root-preview", () => ({
  ThreadRootPreview: ({
    message,
    onViewParent,
  }: {
    message: ChannelMessage;
    onViewParent?: () => void;
  }) => (
    <div data-testid="thread-root-preview">
      <span>{message.content}</span>
      {onViewParent ? (
        <button type="button" onClick={onViewParent}>
          view-parent
        </button>
      ) : null}
    </div>
  ),
}));

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (selector: (r: Record<string, Record<string, string>>) => string) =>
      selector(RESOURCES as never),
  }),
}));

const RESOURCES = {
  thread: {
    title: "Thread",
    meta_count: "{{count}} replies",
    empty_replies: "No replies yet.",
    load_failed: "Failed to load thread.",
    view_parent: "Back to main chat",
    close_aria: "Close thread",
    open_in_main_aria: "Open in main chat",
    back_to_conversation: "Back to conversation",
    follow: "Follow",
    following: "Following",
    follow_aria: "Follow thread",
    unfollow_aria: "Unfollow thread",
  },
  composer: { send: "Send" },
};

function makeMessage(overrides: Partial<ChannelMessage> = {}): ChannelMessage {
  return {
    id: "root",
    channel_id: "c1",
    workspace_id: "w1",
    seq: 1,
    type: "user",
    author_id: "user-a",
    author_name: "Ann",
    content: "Root message",
    source: "multica",
    external_message_id: null,
    client_message_id: null,
    created_at: "2026-07-04T09:00:00Z",
    ...overrides,
  };
}

function baseProps() {
  return {
    root: makeMessage(),
    replies: [
      makeMessage({ id: "r1", type: "agent", author_id: "agent-c", author_name: "Cy", content: "First reply" }),
    ],
    currentUserId: "user-a",
    isMobile: false,
    onBack: vi.fn(),
    followed: false,
    onFollowChange: vi.fn(),
    editor: <div data-testid="thread-editor">editor</div>,
    onSend: vi.fn(),
    sendDisabled: false,
  };
}

describe("deriveThreadParticipants", () => {
  it("unions started-root, @-mentioned, replied, and issue-assignee", () => {
    const root = makeMessage({
      author_id: "user-a",
      author_name: "Ann",
      content: "Kicking off [@Bea](mention://member/user-b) please look",
    });
    const replies = [
      makeMessage({ id: "r1", type: "agent", author_id: "agent-c", author_name: "Cy", content: "on it" }),
    ];
    const participants = deriveThreadParticipants(root, replies, {
      assignees: [{ memberType: "agent", memberId: "agent-d", displayName: "Dot" }],
    });

    const byKey = Object.fromEntries(participants.map((p) => [p.key, p]));
    expect(byKey["user:user-a"]?.sources).toContain("started");
    expect(byKey["user:user-b"]?.sources).toContain("mentioned");
    expect(byKey["agent:agent-c"]?.sources).toContain("replied");
    expect(byKey["agent:agent-d"]?.sources).toContain("assignee");
    expect(participants).toHaveLength(4);
  });

  it("merges sources for someone who both started and replied, without duplicating", () => {
    const root = makeMessage({ author_id: "user-a", author_name: "Ann", content: "hi" });
    const replies = [makeMessage({ id: "r1", type: "user", author_id: "user-a", author_name: "Ann", content: "more" })];
    const participants = deriveThreadParticipants(root, replies);

    expect(participants).toHaveLength(1);
    expect(participants[0]?.sources).toEqual(expect.arrayContaining(["started", "replied"]));
  });
});

describe("ThreadPanel", () => {
  it("renders the pinned root, flat replies, and a thread-surface composer with no open-thread affordance", () => {
    messageListProps.mockClear();
    const { container } = render(<ThreadPanel {...baseProps()} />);

    expect(within(screen.getByTestId("thread-root-preview")).getByText("Root message")).toBeInTheDocument();
    expect(screen.getByText("First reply")).toBeInTheDocument();
    expect(container.querySelector('[data-composer-surface="thread"]')).not.toBeNull();
    // No nesting: the reply list is never handed an open-thread callback.
    expect(messageListProps.mock.calls.at(-1)?.[0].onOpenThread).toBeUndefined();
  });

  it("forwards onOpenAgent to the reply list so agent avatars open the panel (#488)", () => {
    messageListProps.mockClear();
    const onOpenAgent = vi.fn();
    render(<ThreadPanel {...baseProps()} onOpenAgent={onOpenAgent} />);

    // Without this forward, thread reply avatars only show the hover card and
    // never open the agent side panel (the main channel passes the same handler).
    expect(messageListProps.mock.calls.at(-1)?.[0].onOpenAgent).toBe(onOpenAgent);
  });

  it("gives an explicit back-to-conversation control on mobile", () => {
    const onBack = vi.fn();
    render(<ThreadPanel {...baseProps()} isMobile onBack={onBack} />);

    fireEvent.click(screen.getByRole("button", { name: "Back to conversation" }));
    expect(onBack).toHaveBeenCalledTimes(1);
  });

  it("renders the follow state in the header and toggles it on desktop and mobile", () => {
    const desktopToggle = vi.fn();
    const { unmount } = render(
      <ThreadPanel {...baseProps()} followed onFollowChange={desktopToggle} />,
    );

    const following = screen.getByRole("button", { name: "Unfollow thread" });
    expect(desktopToggle).not.toHaveBeenCalled();
    expect(following).toHaveTextContent("Following");
    expect(following).toHaveAttribute("aria-pressed", "true");
    fireEvent.click(following);
    expect(desktopToggle).toHaveBeenCalledWith(false);

    unmount();
    const mobileToggle = vi.fn();
    render(
      <ThreadPanel
        {...baseProps()}
        isMobile
        followed={false}
        onFollowChange={mobileToggle}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: "Follow thread" }));
    expect(mobileToggle).toHaveBeenCalledWith(true);
  });

  // LRM-384 scheme A — header 28px ghost open-in-main; no dark float Maximize/Download.
  // LRM-389 — tooltip labels “open in main”; omitted when no handler.
  it("exposes a desktop header open-in-main control and hides it on mobile", async () => {
    const onViewParent = vi.fn();
    const { unmount } = renderPanel(
      <ThreadPanel {...baseProps()} onViewParent={onViewParent} />,
    );

    const openInMain = screen.getByRole("button", { name: "Open in main chat" });
    expect(openInMain.className).toMatch(/size-7/);
    expect(openInMain.className).toMatch(/text-muted-foreground/);
    expect(openInMain.className).toMatch(/hover:bg-muted/);
    // Focus opens the tooltip so Maximize2 is not misread as “expand”.
    fireEvent.focus(openInMain);
    expect(openInMain).toHaveAttribute("data-popup-open");
    expect(await screen.findByText("Open in main chat", { selector: "[data-slot=tooltip-content]" })).toBeInTheDocument();
    fireEvent.click(openInMain);
    expect(onViewParent).toHaveBeenCalledTimes(1);
    // Download stays out of the thread header main path.
    expect(screen.queryByRole("button", { name: /download/i })).toBeNull();

    unmount();
    renderPanel(<ThreadPanel {...baseProps()} isMobile onViewParent={onViewParent} />);
    expect(screen.queryByRole("button", { name: "Open in main chat" })).toBeNull();
  });

  it("omits the open-in-main control when onViewParent is not provided", () => {
    renderPanel(<ThreadPanel {...baseProps()} />);
    expect(screen.queryByRole("button", { name: "Open in main chat" })).toBeNull();
  });
});

describe("ThreadPanel composer rules", () => {
  it("does not offer a thread-to-channel projection control", () => {
    render(<ThreadPanel {...baseProps()} />);
    expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();
  });
});
