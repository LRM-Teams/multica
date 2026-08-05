import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import type { PropsWithChildren } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ChannelMessage } from "@multica/core/types";
import {
  THREAD_PREVIEW_PREFETCH_COUNT,
  THREAD_PREVIEW_QUERY_LIMIT,
  usePrefetchThreadPreviews,
} from "./use-prefetch-thread-previews";

const listThreadMock = vi.fn();

vi.mock("@multica/core/api", () => ({
  api: {
    listChannelMessageThread: (...args: unknown[]) => listThreadMock(...args),
  },
}));

function message(id: string, threadReplyCount = 0): ChannelMessage {
  return {
    id,
    channel_id: "channel-1",
    workspace_id: "workspace-1",
    seq: Number(id.replace(/\D/g, "")) || 1,
    type: "user",
    author_id: "user-1",
    author_name: "Frank",
    content: id,
    source: "multica",
    external_message_id: null,
    client_message_id: null,
    created_at: "2026-08-04T12:00:00.000Z",
    thread_reply_count: threadReplyCount,
  };
}

describe("usePrefetchThreadPreviews", () => {
  let queryClient: QueryClient;
  let wrapper: (props: PropsWithChildren) => React.ReactNode;

  beforeEach(() => {
    listThreadMock.mockReset();
    listThreadMock.mockResolvedValue({ messages: [], next_cursor: null });
    queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    wrapper = ({ children }) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    );
  });

  it("starts the newest three thread requests as soon as mainline messages arrive", async () => {
    const messages = [
      message("root-1", 1),
      message("plain-2"),
      message("root-3", 2),
      message("root-4", 1),
      message("root-5", 4),
    ];

    renderHook(() => usePrefetchThreadPreviews(messages), { wrapper });

    await waitFor(() =>
      expect(listThreadMock).toHaveBeenCalledTimes(THREAD_PREVIEW_PREFETCH_COUNT),
    );
    expect(listThreadMock.mock.calls.map((call) => call[1])).toEqual([
      "root-5",
      "root-4",
      "root-3",
    ]);
    for (const call of listThreadMock.mock.calls) {
      expect(call[2]).toMatchObject({ limit: THREAD_PREVIEW_QUERY_LIMIT });
    }
  });

  it("does not prefetch replies, empty roots, or deleted roots", () => {
    const reply = { ...message("reply-2", 2), thread_root_message_id: "root-1" };
    const deleted = {
      ...message("root-3", 2),
      deleted_at: "2026-08-04T12:01:00.000Z",
    };

    renderHook(
      () => usePrefetchThreadPreviews([message("root-1"), reply, deleted]),
      { wrapper },
    );

    expect(listThreadMock).not.toHaveBeenCalled();
  });

  it("keeps the mainline render independent when a prefetch fails", async () => {
    listThreadMock.mockRejectedValue(new Error("slow network failed"));

    const { result } = renderHook(
      () => {
        usePrefetchThreadPreviews([message("root-1", 1)]);
        return "mainline-ready";
      },
      { wrapper },
    );

    expect(result.current).toBe("mainline-ready");
    await waitFor(() => expect(listThreadMock).toHaveBeenCalledTimes(1));
  });
});
