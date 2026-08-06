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

  it("returns waiting_local_directory on the daemon-emitted hold status", () => {
    // Daemon publishes this when it dequeues a task but another task owns the
    // local_directory's lock. The pill becomes static (no shimmer) because
    // nothing is actively happening from the user's point of view.
    expect(pickStageKeys("waiting_local_directory", [], "online")).toEqual({
      stageKey: "waiting_local_directory",
      static: true,
    });
  });

  it("waiting_local_directory wins over availability hints", () => {
    // Even if availability says reconnecting/offline, the directory-release
    // status is the more specific signal — surface it.
    expect(
      pickStageKeys("waiting_local_directory", [], "unstable"),
    ).toEqual({ stageKey: "waiting_local_directory", static: true });
    expect(
      pickStageKeys("waiting_local_directory", [], "offline"),
    ).toEqual({ stageKey: "waiting_local_directory", static: true });
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
