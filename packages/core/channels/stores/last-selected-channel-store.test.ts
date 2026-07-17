import { beforeEach, describe, expect, it } from "vitest";
import { useLastSelectedChannelStore } from "./last-selected-channel-store";

describe("last selected channel store", () => {
  beforeEach(() => {
    useLastSelectedChannelStore.setState({ lastSelectedChannelId: null });
  });

  it("tracks one explicit group selection", () => {
    useLastSelectedChannelStore.getState().setLastSelectedChannelId("channel-1");

    expect(useLastSelectedChannelStore.getState().lastSelectedChannelId).toBe("channel-1");
  });

  it("can clear an invalidated selection", () => {
    useLastSelectedChannelStore.getState().setLastSelectedChannelId("channel-1");
    useLastSelectedChannelStore.getState().clearLastSelectedChannelId();

    expect(useLastSelectedChannelStore.getState().lastSelectedChannelId).toBeNull();
  });
});
