// @vitest-environment jsdom

import { act, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { type ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ChannelMessage, ChannelThreadMessagesPage } from "../types";
import { channelKeys } from "./queries";
import { useSetChannelThreadFollowed } from "./mutations";

const apiMock = vi.hoisted(() => ({
  followChannelThread: vi.fn(),
  unfollowChannelThread: vi.fn(),
}));

vi.mock("../api", () => ({ api: apiMock }));

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, resolve, reject };
}

function root(followed: boolean): ChannelMessage {
  return {
    id: "root-1",
    channel_id: "channel-1",
    workspace_id: "workspace-1",
    seq: 1,
    type: "user",
    author_id: "user-1",
    author_name: "Alice",
    content: "Root",
    source: "multica",
    external_message_id: null,
    client_message_id: null,
    thread_followed: followed,
    created_at: "2026-07-21T00:00:00Z",
  };
}

function createHarness(followed: boolean) {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  });
  const queryKey = [...channelKeys.messageThread("channel-1", "root-1"), undefined, undefined, undefined, undefined] as const;
  queryClient.setQueryData<ChannelThreadMessagesPage>(queryKey, {
    messages: [root(followed)],
    next_cursor: null,
  });
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  );
  return { queryClient, queryKey, wrapper };
}

describe("useSetChannelThreadFollowed", () => {
  beforeEach(() => {
    apiMock.followChannelThread.mockReset();
    apiMock.unfollowChannelThread.mockReset();
  });

  it("optimistically updates the root follow state while the request is pending", async () => {
    const request = deferred<void>();
    apiMock.followChannelThread.mockReturnValue(request.promise);
    const { queryClient, queryKey, wrapper } = createHarness(false);
    const { result } = renderHook(() => useSetChannelThreadFollowed(), { wrapper });

    act(() => {
      result.current.mutate({ channelId: "channel-1", messageId: "root-1", followed: true });
    });

    await waitFor(() => {
      expect(
        queryClient.getQueryData<ChannelThreadMessagesPage>(queryKey)?.messages[0]?.thread_followed,
      ).toBe(true);
    });
    expect(apiMock.followChannelThread).toHaveBeenCalledWith("channel-1", "root-1");

    request.resolve();
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
  });

  it("rolls the optimistic follow state back when the request fails", async () => {
    const request = deferred<void>();
    apiMock.unfollowChannelThread.mockReturnValue(request.promise);
    const { queryClient, queryKey, wrapper } = createHarness(true);
    const { result } = renderHook(() => useSetChannelThreadFollowed(), { wrapper });

    act(() => {
      result.current.mutate({ channelId: "channel-1", messageId: "root-1", followed: false });
    });

    await waitFor(() => {
      expect(
        queryClient.getQueryData<ChannelThreadMessagesPage>(queryKey)?.messages[0]?.thread_followed,
      ).toBe(false);
    });
    request.reject(new Error("network"));
    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(
      queryClient.getQueryData<ChannelThreadMessagesPage>(queryKey)?.messages[0]?.thread_followed,
    ).toBe(true);
  });
});
