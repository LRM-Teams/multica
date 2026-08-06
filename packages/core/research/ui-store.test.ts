import { beforeEach, describe, expect, it } from "vitest";
import { useResearchUiStore } from "./ui-store";

describe("useResearchUiStore", () => {
  beforeEach(() => {
    useResearchUiStore.setState({ chatDrawerOpen: false });
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
});
