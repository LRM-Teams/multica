// @vitest-environment jsdom

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { InboxItem } from "@multica/core/types";

const sendChannelMessage = vi.fn().mockResolvedValue({});
const markMutate = vi.fn();
const push = vi.fn();

vi.mock("@multica/core/api", () => ({
  api: { sendChannelMessage: (...a: unknown[]) => sendChannelMessage(...a) },
}));
vi.mock("@multica/core/inbox", () => ({
  useMarkInboxRead: () => ({ mutate: markMutate }),
  useMentionPopupStore: (sel: (s: unknown) => unknown) =>
    sel({ iconRect: null, bounceSignal: 0, setIconRect: vi.fn(), triggerBounce: vi.fn() }),
  inboxListOptions: () => ({ queryKey: ["inbox"], queryFn: async () => [] }),
}));
vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "ws" }));
vi.mock("@multica/core/paths", () => ({
  useWorkspacePaths: () => ({ channelDetail: (id: string) => `/w/channels/${id}` }),
}));
vi.mock("../navigation", () => ({ useNavigation: () => ({ push }) }));

const RESOURCES = {
  mention_popup: {
    title: "Mentioned",
    title_in: "Mentioned in {{channel}}",
    continue: "OK go ahead",
    delegate: "Delegate",
    later: "Later",
    manual: "Manual reply",
    send_failed: "send failed",
    no_manager: "no manager",
  },
};
vi.mock("../i18n", () => ({
  useT: () => ({ t: (sel: (r: typeof RESOURCES) => string) => sel(RESOURCES) }),
}));
vi.mock("motion/react", () => ({
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  motion: new Proxy({}, { get: () => (p: any) => <div {...p} /> }),
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  AnimatePresence: ({ children }: any) => children,
}));

import { MentionQuickReplyCard } from "./mention-quick-reply-popup";

function makeItem(): InboxItem {
  return {
    id: "inbox-1",
    workspace_id: "ws",
    recipient_type: "member",
    recipient_id: "me",
    actor_type: "agent",
    actor_id: "agent-1",
    type: "mentioned",
    severity: "info",
    issue_id: null,
    title: "Bot mentioned you",
    body: "@you please review this",
    issue_status: null,
    read: false,
    archived: false,
    created_at: "2026-01-01T00:00:00Z",
    details: {
      channel_id: "chan-1",
      channel_name: "ddz",
      message_id: "msg-1",
      actor_name: "Bot",
      group_manager_agent_id: "bh-1",
      group_manager_name: "Beckham",
    },
  };
}

describe("MentionQuickReplyCard", () => {
  afterEach(() => vi.clearAllMocks());

  it("可以继续吧: sends '可以，继续吧' @-mentioning the actor, marks read, dismisses", async () => {
    const onDismiss = vi.fn();
    render(<MentionQuickReplyCard item={makeItem()} onDismiss={onDismiss} />);
    fireEvent.click(screen.getByRole("button", { name: "OK go ahead" }));
    expect(sendChannelMessage).toHaveBeenCalledWith(
      "chan-1",
      expect.objectContaining({
        content: expect.stringContaining("mention://agent/agent-1"),
      }),
    );
    expect(sendChannelMessage.mock.calls[0][1].content).toContain("可以，继续吧");
    await waitFor(() => expect(markMutate).toHaveBeenCalledWith("inbox-1"));
    await waitFor(() => expect(onDismiss).toHaveBeenCalled());
  });

  it("@贝克汉姆: hands full authority to the group manager", async () => {
    const onDismiss = vi.fn();
    render(<MentionQuickReplyCard item={makeItem()} onDismiss={onDismiss} />);
    fireEvent.click(screen.getByRole("button", { name: "Delegate" }));
    const content = sendChannelMessage.mock.calls[0][1].content as string;
    expect(content).toContain("mention://agent/bh-1");
    expect(content).toContain("全权交给你处理");
    await waitFor(() => expect(markMutate).toHaveBeenCalledWith("inbox-1"));
  });

  it("手动回复: navigates to the channel and does not send", () => {
    const onDismiss = vi.fn();
    render(<MentionQuickReplyCard item={makeItem()} onDismiss={onDismiss} />);
    fireEvent.click(screen.getByRole("button", { name: "Manual reply" }));
    expect(push).toHaveBeenCalledWith("/w/channels/chan-1");
    expect(sendChannelMessage).not.toHaveBeenCalled();
    expect(onDismiss).toHaveBeenCalled();
  });

  it("稍后处理: dismisses without sending or marking read", () => {
    const onDismiss = vi.fn();
    render(<MentionQuickReplyCard item={makeItem()} onDismiss={onDismiss} />);
    const laterButtons = screen.getAllByRole("button", { name: "Later" });
    fireEvent.click(laterButtons[laterButtons.length - 1]);
    expect(onDismiss).toHaveBeenCalled();
    expect(sendChannelMessage).not.toHaveBeenCalled();
    expect(markMutate).not.toHaveBeenCalled();
  });
});
