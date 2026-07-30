// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { TFunction } from "i18next";
import type { AgentPresenceDetail } from "@multica/core/agents";
import type { AgentTask } from "@multica/core/types";
import type { ActivityEvent } from "./components/tabs/activity-event";
import {
  pickPrimaryActiveTask,
  resolveAgentActivityProjection,
  resolveAgentLiveStatus,
} from "./resolve-agent-live-status";

function presence(over: Partial<AgentPresenceDetail> = {}): AgentPresenceDetail {
  return {
    availability: "online",
    workload: "idle",
    runningCount: 0,
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
    started_at: null,
    completed_at: null,
    result: null,
    error: null,
    created_at: "2026-07-09T10:00:00Z",
    ...over,
  };
}

function evt(
  over: Partial<ActivityEvent> & Pick<ActivityEvent, "activity_kind">,
): ActivityEvent {
  return {
    id: "e1",
    agent_id: "agent-1",
    occurred_at: "2026-07-09T10:00:00Z",
    detail_kind: "tool_use",
    ...over,
  } as ActivityEvent;
}

const AGENTS = {
  workload: { working: "Working", queued: "Queued", idle: "Idle" },
  availability: {
    online: "Online",
    unstable: "Unstable",
    offline: "Offline",
    archived: "Archived",
  },
} as const;

const tAgents = ((selector: (r: typeof AGENTS) => string) =>
  selector(AGENTS)) as unknown as TFunction<"agents">;

describe("pickPrimaryActiveTask", () => {
  it("prefers running over queued", () => {
    const snapshot = [
      task({ id: "q", status: "queued", created_at: "2026-07-09T11:00:00Z" }),
      task({ id: "r", status: "running", started_at: "2026-07-09T10:00:00Z" }),
    ];
    expect(pickPrimaryActiveTask(snapshot, "agent-1")?.id).toBe("r");
  });

  it("ignores other agents and terminal rows", () => {
    const snapshot = [
      task({ id: "other", agent_id: "agent-2", status: "running" }),
      task({ id: "done", status: "completed", completed_at: "2026-07-09T09:00:00Z" }),
      task({ id: "active", status: "queued" }),
    ];
    expect(pickPrimaryActiveTask(snapshot, "agent-1")?.id).toBe("active");
  });

  it("returns null when nothing is active", () => {
    expect(pickPrimaryActiveTask([task({ id: "d", status: "failed" })], "agent-1")).toBeNull();
  });
});

describe("resolveAgentLiveStatus (LRM-248 Online/Offline only)", () => {
  it("returns null while presence is loading", () => {
    expect(
      resolveAgentLiveStatus({ presence: "loading", tAgents }),
    ).toBeNull();
  });

  it("shows Online when online — never Working / Idle / activity verbs", () => {
    const view = resolveAgentLiveStatus({
      presence: presence({ availability: "online", workload: "working", runningCount: 1 }),
      activeTask: task({ id: "task-1", status: "running" }),
      latestActivity: evt({ activity_kind: "tool_call", tool: "bash" }),
      tAgents,
    });
    expect(view?.label).toBe("Online");
    expect(view?.dotClass).toBe("bg-success");
  });

  it("folds unstable → Online (never Unstable / Reconnecting)", () => {
    const view = resolveAgentLiveStatus({
      presence: presence({ availability: "unstable", workload: "working", runningCount: 1 }),
      activeTask: task({ id: "task-1", status: "running" }),
      latestActivity: evt({ activity_kind: "tool_call", tool: "bash" }),
      tAgents,
    });
    expect(view?.label).toBe("Online");
    expect(view?.label).not.toMatch(/Unstable|Reconnecting/i);
  });

  it("shows Offline when offline", () => {
    const view = resolveAgentLiveStatus({
      presence: presence({ availability: "offline", workload: "idle" }),
      tAgents,
    });
    expect(view?.label).toBe("Offline");
  });

  it("returns null for archived (not a live presence)", () => {
    expect(
      resolveAgentLiveStatus({
        presence: presence({ availability: "archived" }),
        tAgents,
      }),
    ).toBeNull();
  });
});

describe("resolveAgentActivityProjection (composer strip — non-live verbs)", () => {
  const online = presence({ availability: "online", workload: "working", runningCount: 1 });

  it("returns null when idle / offline / no active task", () => {
    expect(
      resolveAgentActivityProjection({
        presence: presence({ availability: "online", workload: "idle" }),
        activeTask: null,
        latestActivity: null,
      }),
    ).toBeNull();
    expect(
      resolveAgentActivityProjection({
        presence: presence({ availability: "offline", workload: "working" }),
        activeTask: task({ id: "t", status: "running" }),
        latestActivity: evt({ activity_kind: "tool_call", tool: "bash" }),
      }),
    ).toBeNull();
  });

  it("shows Thinking for a running task with no activity row yet", () => {
    const view = resolveAgentActivityProjection({
      presence: online,
      activeTask: task({ id: "task-1", status: "running" }),
      latestActivity: null,
    });
    expect(view?.label).toBe("Thinking");
  });

  it("projects a command row as 'Running command…'", () => {
    const view = resolveAgentActivityProjection({
      presence: online,
      activeTask: task({ id: "task-1", status: "running" }),
      latestActivity: evt({ activity_kind: "tool_call", tool: "bash" }),
    });
    expect(view?.label).toContain("Running command");
  });

  it("never invents Queued / Unstable / Reconnecting", () => {
    const view = resolveAgentActivityProjection({
      presence: presence({ availability: "unstable", workload: "queued", queuedCount: 1 }),
      activeTask: task({ id: "task-1", status: "queued" }),
      latestActivity: null,
    });
    expect(view?.label).toBe("Thinking");
    expect(view?.label).not.toMatch(/Queued|Unstable|Reconnecting/i);
  });
});
