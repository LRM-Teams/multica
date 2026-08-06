// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { AgentPresenceDetail } from "@multica/core/agents";
import type { AgentTask } from "@multica/core/types";
import type { ActivityEvent } from "../../agents/components/tabs/activity-event";
import { resolveDmShortWorkingLabel } from "./dm-agent-working-label";

function presence(over: Partial<AgentPresenceDetail> = {}): AgentPresenceDetail {
  return {
    availability: "online",
    workload: "working",
    runningCount: 1,
    queuedCount: 0,
    capacity: 1,
    ...over,
  };
}

function task(over: Partial<AgentTask> & Pick<AgentTask, "id" | "status">): AgentTask {
  return {
    agent_id: "agent-1",
    runtime_id: "rt-1",
    issue_id: "",
    priority: 0,
    dispatched_at: null,
    started_at: "2026-07-26T00:00:01Z",
    completed_at: null,
    result: null,
    error: null,
    created_at: "2026-07-26T00:00:00Z",
    ...over,
  };
}

function activity(
  over: Partial<ActivityEvent> & Pick<ActivityEvent, "activity_kind">,
): ActivityEvent {
  return {
    id: "ev-1",
    agent_id: "agent-1",
    detail_kind: over.activity_kind,
    occurred_at: "2026-07-26T00:00:02Z",
    target_ref: { type: "none" },
    ...over,
  } as ActivityEvent;
}

describe("resolveDmShortWorkingLabel", () => {
  const labels = {
    thinkingLabel: "思考中",
    queuedLabel: "排队中",
    startingLabel: "处理中",
  };

  it("returns null when idle / no active task", () => {
    expect(
      resolveDmShortWorkingLabel({
        presence: presence(),
        activeTask: null,
        latestActivity: null,
        ...labels,
      }),
    ).toBeNull();
  });

  it("returns null when offline even with an active task", () => {
    expect(
      resolveDmShortWorkingLabel({
        presence: presence({ availability: "offline", workload: "idle" }),
        activeTask: task({ id: "task-1", status: "running" }),
        latestActivity: activity({ activity_kind: "thinking" }),
        ...labels,
      }),
    ).toBeNull();
  });

  it("shows 思考中 while running with no activity yet", () => {
    expect(
      resolveDmShortWorkingLabel({
        presence: presence(),
        activeTask: task({ id: "task-1", status: "running" }),
        latestActivity: null,
        ...labels,
      }),
    ).toBe("思考中");
  });

  it("shows short bubble tool labels (Edit / Shell), not path details", () => {
    expect(
      resolveDmShortWorkingLabel({
        presence: presence(),
        activeTask: task({ id: "task-1", status: "running" }),
        latestActivity: activity({
          activity_kind: "tool_call",
          tool: "edit_file",
          tool_target: "packages/views/foo.tsx",
        }),
        ...labels,
      }),
    ).toBe("Edit");

    expect(
      resolveDmShortWorkingLabel({
        presence: presence(),
        activeTask: task({ id: "task-1", status: "running" }),
        latestActivity: activity({
          activity_kind: "tool_call",
          tool: "bash",
          tool_target: "pnpm test",
        }),
        ...labels,
      }),
    ).toBe("Shell");
  });

  it("shows thinking / queued / starting stage labels", () => {
    expect(
      resolveDmShortWorkingLabel({
        presence: presence(),
        activeTask: task({ id: "task-1", status: "running" }),
        latestActivity: activity({ activity_kind: "thinking" }),
        ...labels,
      }),
    ).toBe("思考中");

    expect(
      resolveDmShortWorkingLabel({
        presence: presence(),
        activeTask: task({ id: "task-1", status: "queued", started_at: null }),
        latestActivity: null,
        ...labels,
      }),
    ).toBe("排队中");

    expect(
      resolveDmShortWorkingLabel({
        presence: presence(),
        activeTask: task({ id: "task-1", status: "dispatched", started_at: null }),
        latestActivity: null,
        ...labels,
      }),
    ).toBe("处理中");
  });
});
