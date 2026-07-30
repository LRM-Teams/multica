// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { ChannelActiveTask } from "@multica/core/types";
import { listStoppableChannelTasks } from "./conversation-activity-tasks";

function task(over: Partial<ChannelActiveTask>): ChannelActiveTask {
  return {
    agent_id: "a1",
    agent_name: "Aria",
    task_id: "t1",
    status: "running",
    ...over,
  };
}

describe("listStoppableChannelTasks (LRM-405 / LRM-581)", () => {
  it("keeps every non-terminal inbox row, including issue-create kinds", () => {
    const running = [
      task({ kind: "quick_create", task_id: "qc-1", agent_name: "Wendy" }),
      task({ kind: "reply", reason: "mention", task_id: "t1", agent_name: "Aria" }),
      task({ outcome: "failed", task_id: "done-1", agent_name: "Bo" }),
    ];

    expect(listStoppableChannelTasks(running).map((row) => row.task_id)).toEqual([
      "qc-1",
      "t1",
    ]);
  });
});
