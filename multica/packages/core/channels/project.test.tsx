/**
 * @vitest-environment jsdom
 *
 * #615 — the ORIGINATOR of a project bind/change/unbind must see the resulting
 * `channel_project_*` system row immediately, without depending on the WS echo.
 * The server does broadcast the row to channel members (incl. the originator)
 * and the realtime hook upserts it, but the originator must not rely solely on
 * that echo (reconnecting socket, backgrounded tab, …). So the mutation
 * invalidates the channel timeline on SUCCESS — exactly like sending a message
 * refreshes the sender's own list. A failed bind produces no system row, so the
 * timeline is left untouched.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";

import { setApiInstance } from "../api";
import type { ApiClient } from "../api/client";
import { channelKeys } from "./queries";
import { useSetChannelProject } from "./project";

function createWrapper(qc: QueryClient) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
  };
}

function makeApi() {
  return {
    setChannelProject: vi.fn().mockResolvedValue({ project_id: "proj-9" }),
  };
}

describe("useSetChannelProject refreshes the originator's channel timeline (#615)", () => {
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

  it("invalidates the channel messages timeline on a successful bind", async () => {
    const invalidateSpy = vi.spyOn(qc, "invalidateQueries");
    const { result } = renderHook(() => useSetChannelProject("ws-1", "channel-1"), {
      wrapper: createWrapper(qc),
    });

    await act(async () => {
      await result.current.mutateAsync("proj-9");
    });

    await waitFor(() =>
      expect(api.setChannelProject).toHaveBeenCalledWith("channel-1", "proj-9"),
    );

    // Both timeline query surfaces are invalidated so the `channel_project_bound`
    // row is fetched and shown to the originator immediately.
    expect(invalidateSpy).toHaveBeenCalledWith({
      queryKey: channelKeys.messages("channel-1"),
    });
    expect(invalidateSpy).toHaveBeenCalledWith({
      queryKey: channelKeys.messagesPage("channel-1"),
    });
  });

  it("also refreshes the timeline on an unbind (projectId null)", async () => {
    const invalidateSpy = vi.spyOn(qc, "invalidateQueries");
    const { result } = renderHook(() => useSetChannelProject("ws-1", "channel-1"), {
      wrapper: createWrapper(qc),
    });

    await act(async () => {
      await result.current.mutateAsync(null);
    });

    expect(api.setChannelProject).toHaveBeenCalledWith("channel-1", null);
    expect(invalidateSpy).toHaveBeenCalledWith({
      queryKey: channelKeys.messages("channel-1"),
    });
  });

  it("does NOT refresh the timeline when the bind fails (no system row produced)", async () => {
    api.setChannelProject.mockRejectedValueOnce(new Error("boom"));
    const invalidateSpy = vi.spyOn(qc, "invalidateQueries");
    const { result } = renderHook(() => useSetChannelProject("ws-1", "channel-1"), {
      wrapper: createWrapper(qc),
    });

    await act(async () => {
      await result.current.mutateAsync("proj-9").catch(() => {});
    });

    // onSettled still re-fetches the project binding itself, but a failed bind
    // emits no system row, so the message timeline is left untouched.
    expect(invalidateSpy).not.toHaveBeenCalledWith({
      queryKey: channelKeys.messages("channel-1"),
    });
  });
});
