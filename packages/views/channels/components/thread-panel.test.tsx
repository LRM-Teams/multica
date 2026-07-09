import { fireEvent, render, screen, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import type { ChannelMessage } from "@multica/core/types";
import { ThreadPanel, ThreadOriginTag } from "./thread-panel";
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
    show_in_channel_label: "Also show in channel",
    from_thread_badge: "From thread",
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

  it("gives an explicit back-to-conversation control on mobile", () => {
    const onBack = vi.fn();
    render(<ThreadPanel {...baseProps()} isMobile onBack={onBack} />);

    fireEvent.click(screen.getByRole("button", { name: "Back to conversation" }));
    expect(onBack).toHaveBeenCalledTimes(1);
  });
});

describe("ThreadPanel composer rules", () => {
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
