import { describe, expect, it } from "vitest";
import type { TFunction } from "i18next";
import type { AgentPresenceDetail } from "@multica/core/agents";
import type { AgentTask } from "@multica/core/types";
import type { ActivityEvent } from "./components/tabs/activity-event";
import {
  pickPrimaryActiveTask,
  resolveAgentActivityHeader,
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
    expect(resolveAgentLiveStatus({ presence: "loading", tAgents })).toBeNull();
  });

  it("shows Online for online and unstable; never Unstable/Working", () => {
    expect(
      resolveAgentLiveStatus({
        presence: presence({ availability: "online", workload: "working" }),
        tAgents,
      })?.label,
    ).toBe("Online");
    expect(
      resolveAgentLiveStatus({
        presence: presence({ availability: "unstable", workload: "idle" }),
        tAgents,
      })?.label,
    ).toBe("Online");
  });

  it("shows Offline when offline", () => {
    expect(
      resolveAgentLiveStatus({
        presence: presence({ availability: "offline", workload: "working" }),
        tAgents,
      })?.label,
    ).toBe("Offline");
  });

  it("returns null for archived (avatar grayscale, no live word)", () => {
    expect(
      resolveAgentLiveStatus({
        presence: presence({ availability: "archived" }),
        tAgents,
      }),
    ).toBeNull();
  });
});

describe("resolveAgentActivityHeader (Activity verbs, not live presence)", () => {
  const online = presence({ availability: "online", workload: "working", runningCount: 1 });

  it("returns null when idle or offline", () => {
    expect(
      resolveAgentActivityHeader({
        presence: presence({ availability: "online", workload: "idle" }),
        activeTask: null,
        latestActivity: null,
      }),
    ).toBeNull();
    expect(
      resolveAgentActivityHeader({
        presence: presence({ availability: "offline" }),
        activeTask: task({ id: "t", status: "running" }),
        latestActivity: evt({ activity_kind: "thinking" }),
      }),
    ).toBeNull();
  });

  it("treats unstable as online for activity projection", () => {
    const view = resolveAgentActivityHeader({
      presence: presence({ availability: "unstable", workload: "working", runningCount: 1 }),
      activeTask: task({ id: "task-1", status: "running" }),
      latestActivity: null,
    });
    expect(view?.label).toBe("Thinking");
  });

  it("projects a command row as Running command", () => {
    const view = resolveAgentActivityHeader({
      presence: online,
      activeTask: task({ id: "task-1", status: "running" }),
      latestActivity: evt({ activity_kind: "tool_call", tool: "bash" }),
    });
    expect(view?.label).toContain("Running command");
  });
});
