/**
 * @vitest-environment happy-dom
 */
import { renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const openDM = vi.fn();
const push = vi.fn();

vi.mock("../common/use-open-dm", () => ({
  useOpenDM: () => ({ openDM, isPending: false }),
}));

vi.mock("@multica/core/paths", () => ({
  appendQueryParams: (href: string, params: Record<string, string>) => {
    const query = new URLSearchParams(params).toString();
    return query ? `${href}?${query}` : href;
  },
  useWorkspacePaths: () => ({
    channelDetail: (id: string) => `/acme/channels/${id}`,
  }),
}));

vi.mock("../navigation", () => ({
  useNavigation: () => ({ push }),
}));

import { useOpenNoteWorkerChat } from "./use-open-note-worker-chat";

describe("useOpenNoteWorkerChat", () => {
  beforeEach(() => {
    openDM.mockReset();
    push.mockReset();
    openDM.mockResolvedValue({ id: "dm-1" });
  });

  it("opens the Messages thread when channel_id and channel_message_id are present", async () => {
    const { result } = renderHook(() => useOpenNoteWorkerChat());
    await result.current.openNoteWorkerChat({ agent_id: "a1", channel_id: "ch-9", channel_message_id: "msg-9" });
    await waitFor(() => {
      expect(push).toHaveBeenCalledWith("/acme/channels/ch-9?thread=msg-9");
    });
    expect(openDM).not.toHaveBeenCalled();
  });

  it("opens the Messages channel when channel_id is present without a thread", async () => {
    const { result } = renderHook(() => useOpenNoteWorkerChat());
    await result.current.openNoteWorkerChat({ agent_id: "a1", channel_id: "ch-9" });
    await waitFor(() => {
      expect(push).toHaveBeenCalledWith("/acme/channels/ch-9");
    });
    expect(openDM).not.toHaveBeenCalled();
  });

  it("falls back to agent DM when channel_id is missing", async () => {
    const { result } = renderHook(() => useOpenNoteWorkerChat());
    await result.current.openNoteWorkerChat({ agent_id: "a1" });
    await waitFor(() => {
      expect(openDM).toHaveBeenCalledWith({ peer_type: "agent", peer_id: "a1" });
    });
  });
});
