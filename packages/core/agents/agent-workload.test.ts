import { describe, expect, it } from "vitest";
import type { AgentTask } from "../types";
import { deriveWorkload, deriveWorkloadDetail } from "./agent-workload";

describe("Agent Workload", () => {
  it("classifies current Task counts without producing Presence", () => {
    expect(deriveWorkload({ runningCount: 1, queuedCount: 4 })).toBe("working");
    expect(deriveWorkload({ runningCount: 0, queuedCount: 2 })).toBe("queued");
    expect(deriveWorkload({ runningCount: 0, queuedCount: 0 })).toBe("idle");
  });

  it("ignores terminal Tasks", () => {
    const tasks = [
      { status: "completed" },
      { status: "failed" },
      { status: "running" },
    ] as AgentTask[];
    expect(deriveWorkloadDetail(tasks)).toEqual({
      workload: "working",
      runningCount: 1,
      queuedCount: 0,
    });
  });
});
