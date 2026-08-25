import { beforeEach, describe, expect, it } from "vitest";
import { useResearchUiStore } from "./ui-store";

describe("useResearchUiStore", () => {
  beforeEach(() => {
    useResearchUiStore.setState({
      chatDrawerOpen: false,
      d5RailOpen: true,
      d5RailMode: "chat",
      d5Lens: "agent",
      d5Overlay: null,
      goalCollapsedBySession: {},
      completionGuideDismissedBySession: {},
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

  it("owns a session-scoped transient D5 inspector surface", () => {
    useResearchUiStore.getState().setD5Overlay({
      sessionId: "session-a",
      kind: "agent",
      agentId: "agent-1",
    });
    expect(useResearchUiStore.getState().d5Overlay).toEqual({
      sessionId: "session-a",
      kind: "agent",
      agentId: "agent-1",
    });

    useResearchUiStore.getState().setD5Overlay({
      sessionId: "session-b",
      kind: "report",
    });
    expect(useResearchUiStore.getState().d5Overlay).toEqual({
      sessionId: "session-b",
      kind: "report",
    });
    useResearchUiStore.getState().setD5Overlay(null);
    expect(useResearchUiStore.getState().d5Overlay).toBeNull();
  });

  it("owns D5 rail chrome", () => {
    expect(useResearchUiStore.getState().d5RailOpen).toBe(true);
    expect(useResearchUiStore.getState().d5RailMode).toBe("chat");
    expect(useResearchUiStore.getState().d5Lens).toBe("agent");
    useResearchUiStore.getState().setD5RailOpen(false);
    useResearchUiStore.getState().setD5RailMode("detail");
    useResearchUiStore.getState().setD5Lens("lineage");
    expect(useResearchUiStore.getState().d5RailOpen).toBe(false);
    expect(useResearchUiStore.getState().d5RailMode).toBe("detail");
    expect(useResearchUiStore.getState().d5Lens).toBe("lineage");
  });

  it("owns bounded session preferences", () => {
    const store = useResearchUiStore.getState();
    store.setGoalCollapsed("session-a", true);
    store.dismissCompletionGuide("session-a");
    expect(useResearchUiStore.getState().goalCollapsedBySession).toEqual({
      "session-a": true,
    });
    expect(
      useResearchUiStore.getState().completionGuideDismissedBySession,
    ).toEqual({ "session-a": true });

    store.setGoalCollapsed("session-a", false);
    expect(useResearchUiStore.getState().goalCollapsedBySession).toEqual({});
  });
});
