/**
 * @vitest-environment jsdom
 *
 * B3 (#241) — Edit + H5 FE guard. Editing a message must go through the
 * dedicated edit endpoint and MUST NOT call the message-send / agent-dispatch
 * endpoints (`sendChannelMessage` / `sendChannelThreadMessage`) — the only
 * wake-producing calls on this surface. The BE enforces no-new-wake (#235);
 * this locks the FE contract so a regression can never re-disturb already-read
 * agents by routing an edit through a send/dispatch.
 *
 * LRM-695: message delete is fully removed from the FE, so the delete half of
 * this guard is retired here too.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";

import { setApiInstance } from "../api";
import type { ApiClient } from "../api/client";
import type { ChannelMessage } from "../types";
import { useEditChannelMessage } from "./mutations";

vi.mock("../hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

function createWrapper(qc: QueryClient) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
  };
}

function channelMessage(overrides: Partial<ChannelMessage> = {}): ChannelMessage {
  return {
    id: "msg-1",
    channel_id: "channel-1",
    workspace_id: "ws-1",
    seq: 1,
    type: "user",
    author_id: "user-1",
    author_name: "Alice",
    content: "edited body",
    source: "multica",
    external_message_id: null,
    client_message_id: null,
    created_at: "2026-07-04T00:00:00Z",
    edited_at: "2026-07-04T00:01:00Z",
    ...overrides,
  };
}

function makeApi() {
  return {
    editChannelMessage: vi.fn().mockResolvedValue(channelMessage()),
    // The wake-producing calls — must never fire on an edit.
    sendChannelMessage: vi.fn(),
    sendChannelThreadMessage: vi.fn(),
  };
}

describe("channel message edit never wakes an agent (H5)", () => {
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

  it("edits a message through the edit endpoint, not a send/dispatch", async () => {
    const { result } = renderHook(() => useEditChannelMessage(), {
      wrapper: createWrapper(qc),
    });

    act(() => {
      result.current.mutate({
        channelId: "channel-1",
        messageId: "msg-1",
        content: "edited body",
      });
    });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    expect(api.editChannelMessage).toHaveBeenCalledWith(
      "channel-1",
      "msg-1",
      "edited body",
      undefined,
    );
    expect(api.sendChannelMessage).not.toHaveBeenCalled();
    expect(api.sendChannelThreadMessage).not.toHaveBeenCalled();
  });
});
