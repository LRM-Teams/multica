import { describe, expect, it } from "vitest";
import type { AgentPresenceDetail } from "@multica/core/agents";
import type { AgentTask } from "@multica/core/types";
import type { ActivityEvent } from "../../agents/components/tabs/activity-event";
import { resolveAgentActivityProjection } from "../../agents/resolve-agent-live-status";

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

/**
 * LRM-650 — DM Compact Activity must use the shared EN Activity projection
 * (ACTIVITY_LABEL_EN), never i18n bubble short labels or command details.
 */
describe("DM Compact Activity projection (LRM-650)", () => {
  it("returns null when idle / no active task", () => {
    expect(
      resolveAgentActivityProjection({
        presence: presence(),
        activeTask: null,
        latestActivity: null,
      }),
    ).toBeNull();
  });

  it("returns null when offline even with an active task", () => {
    expect(
      resolveAgentActivityProjection({
        presence: presence({ availability: "offline", workload: "idle" }),
        activeTask: task({ id: "task-1", status: "running" }),
        latestActivity: activity({ activity_kind: "thinking" }),
      }),
    ).toBeNull();
  });

  it("shows Thinking while running with no activity yet", () => {
    expect(
      resolveAgentActivityProjection({
        presence: presence(),
        activeTask: task({ id: "task-1", status: "running" }),
        latestActivity: null,
      })?.label,
    ).toBe("Thinking");
  });

  it("shows Running command for bash — never the raw command / Shell short label", () => {
    const view = resolveAgentActivityProjection({
      presence: presence(),
      activeTask: task({ id: "task-1", status: "running" }),
      latestActivity: activity({
        activity_kind: "tool_call",
        tool: "bash",
        tool_target: "pnpm test",
      }),
    });
    expect(view?.label).toBe("Running command");
    expect(view?.label).not.toMatch(/pnpm|Shell/i);
  });

  it("shows EN tool type labels (Editing file…), not path details", () => {
    const view = resolveAgentActivityProjection({
      presence: presence(),
      activeTask: task({ id: "task-1", status: "running" }),
      latestActivity: activity({
        activity_kind: "tool_call",
        tool: "edit_file",
        tool_target: "packages/views/foo.tsx",
        status: "running",
      }),
    });
    expect(view?.label).toMatch(/^Editing file/);
    expect(view?.label).not.toContain("foo.tsx");
  });
});
