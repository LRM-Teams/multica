import { describe, expect, it } from "vitest";
import type { ChannelActiveTask } from "@multica/core/types";
import {
  filterComposerStripTasks,
  isComposerStripReplyTask,
  listStoppableChannelTasks,
} from "./conversation-activity-tasks";

function task(over: Partial<ChannelActiveTask>): ChannelActiveTask {
  return {
    agent_id: "a1",
    agent_name: "Aria",
    task_id: "t1",
    status: "running",
    ...over,
  };
}

describe("filterComposerStripTasks (LRM-287)", () => {
  it("keeps normal reply/running inbox tasks", () => {
    expect(
      isComposerStripReplyTask(task({ kind: "reply", reason: "mention" })),
    ).toBe(true);
    expect(
      filterComposerStripTasks([
        task({ kind: "reply", reason: "channel_message" }),
        task({ kind: "reply", reason: "thread_reply" }),
      ]),
    ).toHaveLength(2);
  });

  it("drops quick_create and issue_create kinds (including a single row)", () => {
    expect(isComposerStripReplyTask(task({ kind: "quick_create" }))).toBe(false);
    expect(isComposerStripReplyTask(task({ kind: "issue_create" }))).toBe(false);
    expect(
      filterComposerStripTasks([
        task({ kind: "quick_create", agent_name: "Wendy" }),
        task({ kind: "reply", reason: "mention", agent_name: "Aria" }),
      ]),
    ).toEqual([task({ kind: "reply", reason: "mention", agent_name: "Aria" })]);
  });

  it("drops ambient / channel_onboarding inbox wakes", () => {
    expect(isComposerStripReplyTask(task({ reason: "ambient" }))).toBe(false);
    expect(isComposerStripReplyTask(task({ reason: "channel_onboarding" }))).toBe(false);
  });
});

describe("listStoppableChannelTasks (LRM-405)", () => {
  it("keeps every non-terminal running task, including issue-create kinds", () => {
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
