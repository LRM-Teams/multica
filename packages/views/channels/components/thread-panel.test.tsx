import { fireEvent, render, screen, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import type { ChannelMessage } from "@multica/core/types";
import { ThreadPanel, ThreadOriginTag, type ThreadWakeAnnotation } from "./thread-panel";
import { deriveThreadParticipants } from "./thread-participants";

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
    back_to_conversation: "Back to conversation",
    participants_label: "Participants",
    follow: "Follow thread",
    following: "Following",
    show_in_channel_label: "Also show in channel",
    from_thread_badge: "From thread",
    wake_pending: "Awaiting reply",
    wake_replied: "Replied",
    wake_acked: "Acknowledged",
    wake_delivered: "Delivered",
    wake_no_reply: "No reply",
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
    participants: deriveThreadParticipants(makeMessage(), []),
    followed: false,
    onToggleFollow: vi.fn(),
    showInChannel: false,
    onShowInChannelChange: vi.fn(),
    isMobile: false,
    onBack: vi.fn(),
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

  it("shows participant chips for the union and toggles thread follow explicitly", () => {
    const root = makeMessage({ content: "start [@Bea](mention://member/user-b)" });
    const replies = [makeMessage({ id: "r1", type: "agent", author_id: "agent-c", author_name: "Cy", content: "hi" })];
    const props = baseProps();
    const onToggleFollow = vi.fn();
    const { rerender } = render(
      <ThreadPanel
        {...props}
        root={root}
        replies={replies}
        participants={deriveThreadParticipants(root, replies)}
        followed={false}
        onToggleFollow={onToggleFollow}
      />,
    );

    const chips = screen.getByTestId("thread-participants");
    expect(within(chips).getByText("Ann")).toBeInTheDocument();
    expect(within(chips).getByText("Bea")).toBeInTheDocument();
    expect(within(chips).getByText("Cy")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Follow thread" }));
    expect(onToggleFollow).toHaveBeenCalledWith(true);

    rerender(
      <ThreadPanel
        {...props}
        root={root}
        replies={replies}
        participants={deriveThreadParticipants(root, replies)}
        followed
        onToggleFollow={onToggleFollow}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: "Following" }));
    expect(onToggleFollow).toHaveBeenCalledWith(false);
  });

  it("defaults show-in-channel off and reports explicit toggles; the main-timeline surface is marked from-thread", () => {
    const onShowInChannelChange = vi.fn();
    render(<ThreadPanel {...baseProps()} onShowInChannelChange={onShowInChannelChange} />);

    const checkbox = screen.getByRole("checkbox", { name: "Also show in channel" });
    expect(checkbox).not.toBeChecked();
    fireEvent.click(checkbox);
    expect(onShowInChannelChange).toHaveBeenCalledWith(true);

    render(<ThreadOriginTag />);
    expect(screen.getByText("From thread")).toBeInTheDocument();
  });

  it("renders the wake state for each participant and never surfaces a non-participant", () => {
    const annotations: ThreadWakeAnnotation[] = [
      { key: "agent:agent-c", displayName: "Cy", memberType: "agent", state: "pending" },
      { key: "agent:agent-e", displayName: "Eve", memberType: "agent", state: "no_reply", reason: "not mentioned" },
    ];
    render(<ThreadPanel {...baseProps()} wakeAnnotations={annotations} />);

    const strip = screen.getByTestId("thread-wake-strip");
    expect(within(strip).getByText("Awaiting reply")).toBeInTheDocument();
    expect(within(strip).getByText("No reply")).toBeInTheDocument();
    expect(within(strip).getByText(/not mentioned/)).toBeInTheDocument();
    // A member who was never a participant must not appear as woken.
    expect(within(strip).queryByText("Zed")).not.toBeInTheDocument();
  });

  it("gives an explicit back-to-conversation control on mobile", () => {
    const onBack = vi.fn();
    render(<ThreadPanel {...baseProps()} isMobile onBack={onBack} />);

    fireEvent.click(screen.getByRole("button", { name: "Back to conversation" }));
    expect(onBack).toHaveBeenCalledTimes(1);
  });
});

describe("ThreadPanel wake strip render rules (#196)", () => {
  it("is agent-only and drops an unknown/future state (low-noise, never raw)", () => {
    const annotations: ThreadWakeAnnotation[] = [
      { key: "agent:agent-c", displayName: "Cy", memberType: "agent", state: "delivered" },
      // A human is never woken — a stray record must not read as woken.
      { key: "user:user-a", displayName: "Ann", memberType: "user", state: "pending" },
      // No vetted copy for an unknown state → dropped, not shown as a raw token.
      { key: "agent:agent-z", displayName: "Zed", memberType: "agent", state: "escalated" },
    ];
    render(<ThreadPanel {...baseProps()} wakeAnnotations={annotations} />);

    const strip = screen.getByTestId("thread-wake-strip");
    expect(within(strip).getByText("Delivered")).toBeInTheDocument();
    expect(within(strip).queryByText("Ann")).not.toBeInTheDocument();
    expect(within(strip).queryByText("Zed")).not.toBeInTheDocument();
    expect(within(strip).queryByText("escalated")).not.toBeInTheDocument();
  });

  it("presents no_reply neutrally (received, no reply needed — not a refusal)", () => {
    const annotations: ThreadWakeAnnotation[] = [
      { key: "agent:agent-c", displayName: "Cy", memberType: "agent", state: "no_reply", reason: "nothing to add" },
    ];
    render(<ThreadPanel {...baseProps()} wakeAnnotations={annotations} />);

    const row = screen
      .getByTestId("thread-wake-strip")
      .querySelector('[data-wake-state="no_reply"]');
    expect(row).not.toBeNull();
    const chip = within(row as HTMLElement).getByText("No reply");
    // Muted, not primary/emphatic — reads as informational, never an error.
    expect(chip.className).toContain("bg-muted");
    expect(chip.className).not.toContain("bg-primary");
    expect(within(row as HTMLElement).getByText(/nothing to add/)).toBeInTheDocument();
  });

  it("renders no strip at all when every record is filtered out", () => {
    const annotations: ThreadWakeAnnotation[] = [
      { key: "user:user-a", displayName: "Ann", memberType: "user", state: "pending" },
    ];
    render(<ThreadPanel {...baseProps()} wakeAnnotations={annotations} />);
    expect(screen.queryByTestId("thread-wake-strip")).not.toBeInTheDocument();
  });

  it("hides the show-in-channel checkbox when no change handler is supplied (#256 cut)", () => {
    render(
      <ThreadPanel
        {...baseProps()}
        showInChannel={undefined}
        onShowInChannelChange={undefined}
      />,
    );
    expect(
      screen.queryByRole("checkbox", { name: "Also show in channel" }),
    ).not.toBeInTheDocument();
  });
});
