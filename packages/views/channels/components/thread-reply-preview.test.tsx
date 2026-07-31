import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { ChannelMessage } from "@multica/core/types";
import { ThreadReplyPreview } from "./thread-reply-preview";

vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: ({ actorId }: { actorId: string }) => (
    <span data-testid="actor-avatar">{actorId}</span>
  ),
}));

vi.mock("../../common/use-viewing-timezone", () => ({
  useViewingTimezone: () => "UTC",
}));

vi.mock("../../i18n/time", () => ({
  Time: ({ value }: { value: string }) => {
    const d = new Date(value);
    const hh = String(d.getUTCHours()).padStart(2, "0");
    const mm = String(d.getUTCMinutes()).padStart(2, "0");
    return <time>{`${hh}:${mm}`}</time>;
  },
}));

vi.mock("../../i18n", () => ({
  useT: () => ({
    t: (
      selector: (resources: {
        thread: Record<string, string>;
      }) => string,
      vars?: Record<string, string | number>,
    ) => {
      const dict = {
        thread: {
          preview_count: "{{count}} replies",
          preview_count_with_new: "{{count}} replies · {{newCount}} new",
          preview_open: "View thread →",
          preview_image: "[Image]",
          preview_sticker: "[Sticker]",
          preview_attachment: "[Attachment]",
        },
      };
      let text = selector(dict);
      if (vars) {
        for (const [key, value] of Object.entries(vars)) {
          text = text.replaceAll(`{{${key}}}`, String(value));
        }
      }
      return text;
    },
    i18n: { language: "en" },
  }),
}));

const replies: ChannelMessage[] = [
  {
    id: "root-1",
    channel_id: "ch-1",
    workspace_id: "ws-1",
    seq: 1,
    type: "user",
    author_id: "u-1",
    author_name: "Frank An",
    content: "parent",
    source: "multica",
    external_message_id: null,
    client_message_id: null,
    created_at: "2026-07-31T08:00:00.000Z",
    thread_reply_count: 3,
    thread_unread_count: 2,
  },
  {
    id: "r-1",
    channel_id: "ch-1",
    workspace_id: "ws-1",
    seq: 2,
    type: "user",
    author_id: "u-1",
    author_name: "Frank An",
    content: "hi",
    source: "multica",
    external_message_id: null,
    client_message_id: null,
    created_at: "2026-07-31T09:03:00.000Z",
    thread_root_message_id: "root-1",
  },
  {
    id: "r-2",
    channel_id: "ch-1",
    workspace_id: "ws-1",
    seq: 3,
    type: "user",
    author_id: "u-1",
    author_name: "Frank An",
    content: "你好",
    source: "multica",
    external_message_id: null,
    client_message_id: null,
    created_at: "2026-07-31T09:04:00.000Z",
    thread_root_message_id: "root-1",
  },
  {
    id: "r-3",
    channel_id: "ch-1",
    workspace_id: "ws-1",
    seq: 4,
    type: "agent",
    author_id: "a-1",
    author_name: "Parker",
    content: "ok",
    source: "multica",
    external_message_id: null,
    client_message_id: null,
    created_at: "2026-07-31T09:05:00.000Z",
    thread_root_message_id: "root-1",
  },
];

vi.mock("@tanstack/react-query", () => ({
  useQuery: () => ({
    data: { messages: replies, next_cursor: null },
    isLoading: false,
    isError: false,
  }),
}));

vi.mock("@multica/core/channels", () => ({
  channelMessageThreadOptions: () => ({ queryKey: ["thread"] }),
}));

describe("ThreadReplyPreview", () => {
  it("shows HH:mm on each preview row and new-count in the header", () => {
    render(
      <ThreadReplyPreview
        message={replies[0]!}
        onOpenThread={() => undefined}
      />,
    );

    expect(screen.getByTestId("thread-reply-preview-count")).toHaveTextContent(
      "3 replies · 2 new >",
    );
    const times = screen.getAllByTestId("thread-reply-preview-time");
    expect(times).toHaveLength(3);
    expect(times[0]).toHaveTextContent("09:03");
    expect(times[1]).toHaveTextContent("09:04");
    expect(times[2]).toHaveTextContent("09:05");
    expect(screen.queryByText(/View thread/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/View all/i)).not.toBeInTheDocument();
  });
});
