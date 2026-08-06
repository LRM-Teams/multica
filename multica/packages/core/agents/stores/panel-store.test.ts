import { beforeEach, describe, expect, it } from "vitest";
import { useAgentPanelStore } from "./panel-store";

describe("useAgentPanelStore (LRM-877 Dock Stack)", () => {
  beforeEach(() => {
    useAgentPanelStore.getState().close();
  });

  it("stores returnToMemberId when opening from a human Profile", () => {
    useAgentPanelStore.getState().open(
      "agent-1",
      { display_name: "UI Designer" },
      { returnToMemberId: "user-frank" },
    );
    const state = useAgentPanelStore.getState();
    expect(state.selectedAgentId).toBe("agent-1");
    expect(state.returnToMemberId).toBe("user-frank");
    expect(state.identitySnapshot?.display_name).toBe("UI Designer");
  });

  it("clears returnToMemberId on close", () => {
    useAgentPanelStore
      .getState()
      .open("agent-1", undefined, { returnToMemberId: "user-frank" });
    useAgentPanelStore.getState().close();
    const state = useAgentPanelStore.getState();
    expect(state.selectedAgentId).toBeNull();
    expect(state.returnToMemberId).toBeNull();
  });
});
