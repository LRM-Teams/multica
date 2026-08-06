// @vitest-environment node
import { describe, it, expect } from "vitest";
import { pickStageKeys } from "./task-status-pill";

describe("pickStageKeys", () => {
  it("returns queued when status is queued and agent is online", () => {
    expect(pickStageKeys("queued", [], "online")).toEqual({ stageKey: "queued" });
  });

  it("returns offline when status is queued and agent is offline", () => {
    expect(pickStageKeys("queued", [], "offline")).toEqual({
      stageKey: "offline",
      static: true,
    });
  });

  it("returns thinking for running with no messages", () => {
    expect(pickStageKeys("running", [], "online")).toEqual({ stageKey: "thinking" });
  });

  it("returns to thinking when the backend emits an empty phase status after a tool", () => {
    expect(
      pickStageKeys(
        "running",
        [
          { task_id: "task-1", issue_id: "", seq: 1, type: "tool_use", tool: "bash" },
          { task_id: "task-1", issue_id: "", seq: 2, type: "thinking", content: "" },
        ],
        "online",
      ),
    ).toEqual({ stageKey: "thinking" });
  });
});
