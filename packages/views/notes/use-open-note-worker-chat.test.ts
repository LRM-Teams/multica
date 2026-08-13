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

  it("opens the Messages channel when channel_id is present", async () => {
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
