import { describe, expect, it } from "vitest";
import { useDisputeUiStore } from "./dispute-ui-store";

describe("useDisputeUiStore (LRM-1472 §5)", () => {
  it("defaults focus clear + overview tab", () => {
    expect(useDisputeUiStore.getState().focusNodeId).toBeNull();
    expect(useDisputeUiStore.getState().panelTab).toBe("overview");
  });

  it("sets a focused node (client-only display state)", () => {
    useDisputeUiStore.getState().setFocusNode("pos-2");
    expect(useDisputeUiStore.getState().focusNodeId).toBe("pos-2");
  });

  it("switches panel tab", () => {
    useDisputeUiStore.getState().setPanelTab("verdict");
    expect(useDisputeUiStore.getState().panelTab).toBe("verdict");
  });

  it("clears all display state without touching the graph", () => {
    useDisputeUiStore.getState().setFocusNode("ev-3");
    useDisputeUiStore.getState().setPanelTab("debate");
    useDisputeUiStore.getState().clear();
    expect(useDisputeUiStore.getState().focusNodeId).toBeNull();
    expect(useDisputeUiStore.getState().panelTab).toBe("overview");
  });
});
