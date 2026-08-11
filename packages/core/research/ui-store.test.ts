import { beforeEach, describe, expect, it } from "vitest";
import { useResearchUiStore } from "./ui-store";

describe("useResearchUiStore", () => {
  beforeEach(() => {
    useResearchUiStore.setState({
      chatDrawerOpen: false,
      d5RailOpen: true,
      d5RailMode: "chat",
    });
  });

  it("defaults chat closed (LRM-1061)", () => {
    expect(useResearchUiStore.getState().chatDrawerOpen).toBe(false);
  });

  it("toggles chat drawer open state", () => {
    useResearchUiStore.getState().setChatDrawerOpen(true);
    expect(useResearchUiStore.getState().chatDrawerOpen).toBe(true);
    useResearchUiStore.getState().setChatDrawerOpen(false);
    expect(useResearchUiStore.getState().chatDrawerOpen).toBe(false);
  });

  it("persists D5 rail chrome defaults", () => {
    expect(useResearchUiStore.getState().d5RailOpen).toBe(true);
    expect(useResearchUiStore.getState().d5RailMode).toBe("chat");
    useResearchUiStore.getState().setD5RailOpen(false);
    useResearchUiStore.getState().setD5RailMode("detail");
    expect(useResearchUiStore.getState().d5RailOpen).toBe(false);
    expect(useResearchUiStore.getState().d5RailMode).toBe("detail");
  });
});
