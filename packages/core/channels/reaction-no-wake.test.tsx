/**
 * @vitest-environment jsdom
 *
 * B4 (#242) — Reaction 4-carrier consistency: the "never wakes an agent"
 * invariant for the channel / dm_channel / thread carriers (all three send
 * their reactions through these two mutations). Reacting must hit the
 * dedicated reaction endpoints and MUST NOT call the message-send /
 * agent-dispatch endpoints (`sendChannelMessage` / `sendChannelThreadMessage`),
 * which are the only wake-producing calls on this surface. A regression that
 * routed a reaction through a send would silently wake every participant agent.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";

import { setApiInstance } from "../api";
import type { ApiClient } from "../api/client";
import type { ChannelReaction } from "../types";
import { useAddChannelReaction, useRemoveChannelReaction } from "./mutations";

vi.mock("../hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

function createWrapper(qc: QueryClient) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
  };
}

function channelReaction(overrides: Partial<ChannelReaction> = {}): ChannelReaction {
  return {
    id: "cr-1",
    channel_id: "channel-1",
    message_id: "msg-1",
    actor_type: "member",
    actor_id: "user-1",
    emoji: "👍",
    created_at: "2026-07-04T00:00:00Z",
    ...overrides,
  };
}

function makeApi() {
  return {
    addChannelReaction: vi.fn().mockResolvedValue(channelReaction()),
    removeChannelReaction: vi.fn().mockResolvedValue(undefined),
    // The wake-producing calls — must never fire on a reaction.
    sendChannelMessage: vi.fn(),
    sendChannelThreadMessage: vi.fn(),
  };
}

describe("channel reaction never wakes an agent", () => {
  let qc: QueryClient;
  let api: ReturnType<typeof makeApi>;

  beforeEach(() => {
    qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    api = makeApi();
    setApiInstance(api as unknown as ApiClient);
  });

  afterEach(() => {
    setApiInstance(undefined as unknown as ApiClient);
    vi.clearAllMocks();
  });

  it("adds a reaction through the reaction endpoint, not a send/dispatch", async () => {
    const { result } = renderHook(() => useAddChannelReaction(), {
      wrapper: createWrapper(qc),
    });

    act(() => {
      result.current.mutate({ channelId: "channel-1", messageId: "msg-1", emoji: "👍" });
    });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    expect(api.addChannelReaction).toHaveBeenCalledWith("channel-1", "msg-1", "👍");
    expect(api.sendChannelMessage).not.toHaveBeenCalled();
    expect(api.sendChannelThreadMessage).not.toHaveBeenCalled();
  });

  it("removes a reaction through the reaction endpoint, not a send/dispatch", async () => {
    const { result } = renderHook(() => useRemoveChannelReaction(), {
      wrapper: createWrapper(qc),
    });

    act(() => {
      result.current.mutate({ channelId: "channel-1", messageId: "msg-1", emoji: "👍" });
    });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    expect(api.removeChannelReaction).toHaveBeenCalledWith("channel-1", "msg-1", "👍");
    expect(api.sendChannelMessage).not.toHaveBeenCalled();
    expect(api.sendChannelThreadMessage).not.toHaveBeenCalled();
  });
});
