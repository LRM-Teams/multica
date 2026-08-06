import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
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
        composer: Record<string, string>;
      }) => string,
      vars?: Record<string, string | number>,
    ) => {
      const dict = {
        thread: {
          preview_count: "{{count}} replies",
          preview_count_with_new: "{{count}} replies · {{newCount}} new",
          preview_view_all: "View all {{count}} →",
          preview_open: "View thread →",
          preview_image: "[Image]",
          preview_sticker: "[Sticker]",
          preview_attachment: "[Attachment]",
          load_failed: "Failed to load thread.",
        },
        composer: {
          retry: "Retry",
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

const useQueryMock = vi.fn();

vi.mock("@tanstack/react-query", () => ({
  useQuery: (...args: unknown[]) => useQueryMock(...args),
}));

vi.mock("@multica/core/channels", () => ({
  channelMessageThreadOptions: () => ({ queryKey: ["thread"] }),
}));

describe("ThreadReplyPreview", () => {
  beforeEach(() => {
    useQueryMock.mockReturnValue({
      data: { messages: replies, next_cursor: null },
      isLoading: false,
      isError: false,
      refetch: vi.fn(),
    });
  });

  it("shows HH:mm, top count, and View thread CTA when ≤3", () => {
    render(
      <ThreadReplyPreview
        message={replies[0]!}
        onOpenThread={() => undefined}
      />,
    );

    expect(screen.getByTestId("thread-reply-preview-count")).toHaveTextContent(
      "3 replies · 2 new",
    );
    expect(screen.getByTestId("thread-reply-preview-open")).toHaveTextContent(
      "View thread →",
    );
    const times = screen.getAllByTestId("thread-reply-preview-time");
    expect(times).toHaveLength(3);
    expect(times[0]).toHaveTextContent("09:03");
    expect(times[1]).toHaveTextContent("09:04");
    expect(times[2]).toHaveTextContent("09:05");
    expect(screen.queryByTestId("thread-reply-preview-view-all")).not.toBeInTheDocument();
  });

  it("shows brand View all CTA at bottom when reply count > 3", () => {
    const many = [
      ...replies,
      {
        id: "r-4",
        channel_id: "ch-1",
        workspace_id: "ws-1",
        seq: 5,
        type: "user" as const,
        author_id: "u-1",
        author_name: "Frank An",
        content: "more",
        source: "multica" as const,
        external_message_id: null,
        client_message_id: null,
        created_at: "2026-07-31T09:06:00.000Z",
        thread_root_message_id: "root-1",
      },
      {
        id: "r-5",
        channel_id: "ch-1",
        workspace_id: "ws-1",
        seq: 6,
        type: "user" as const,
        author_id: "u-1",
        author_name: "Frank An",
        content: "even more",
        source: "multica" as const,
        external_message_id: null,
        client_message_id: null,
        created_at: "2026-07-31T09:07:00.000Z",
        thread_root_message_id: "root-1",
      },
    ];
    useQueryMock.mockReturnValue({
      data: { messages: many, next_cursor: null },
      isLoading: false,
      isError: false,
      refetch: vi.fn(),
    });

    render(
      <ThreadReplyPreview
        message={{ ...replies[0]!, thread_reply_count: 5, thread_unread_count: 0 }}
        onOpenThread={() => undefined}
      />,
    );

    expect(screen.getByTestId("thread-reply-preview-count")).toHaveTextContent(
      "5 replies",
    );
    expect(screen.getByTestId("thread-reply-preview-view-all")).toHaveTextContent(
      "View all 5 →",
    );
    expect(screen.getAllByTestId("thread-reply-preview-time")).toHaveLength(3);
  });

  it("shows retryable error state when thread load fails", () => {
    const refetch = vi.fn();
    useQueryMock.mockReturnValue({
      data: undefined,
      isLoading: false,
      isError: true,
      refetch,
    });

    render(
      <ThreadReplyPreview
        message={replies[0]!}
        onOpenThread={() => undefined}
      />,
    );

    expect(screen.getByTestId("thread-reply-preview-error")).toHaveTextContent(
      "Failed to load thread.",
    );
    screen.getByTestId("thread-reply-preview-retry").click();
    expect(refetch).toHaveBeenCalled();
  });
});
