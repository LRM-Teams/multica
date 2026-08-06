import { beforeEach, describe, expect, it } from "vitest";
import { useResearchUiStore } from "./ui-store";

describe("useResearchUiStore", () => {
  beforeEach(() => {
    useResearchUiStore.setState({ chatDrawerOpen: true });
  });

  it("toggles chat drawer open state", () => {
    useResearchUiStore.getState().setChatDrawerOpen(false);
    expect(useResearchUiStore.getState().chatDrawerOpen).toBe(false);
    useResearchUiStore.getState().setChatDrawerOpen(true);
    expect(useResearchUiStore.getState().chatDrawerOpen).toBe(true);
  });
});
