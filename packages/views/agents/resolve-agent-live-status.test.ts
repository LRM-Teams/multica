import { describe, expect, it } from "vitest";
import type { TFunction } from "i18next";
import type { AgentPresenceDetail } from "@multica/core/agents";
import type { AgentTask, TaskMessagePayload } from "@multica/core/types";
import {
  pickPrimaryActiveTask,
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

function msg(
  over: Partial<TaskMessagePayload> & Pick<TaskMessagePayload, "type" | "seq">,
): TaskMessagePayload {
  return {
    task_id: "task-1",
    issue_id: "",
    ...over,
  };
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

const CHAT = {
  status_pill: {
    stages: {
      offline: "Offline",
      reconnecting: "Reconnecting",
      queued: "Queued",
      waiting_local_directory: "Waiting for local directory",
      starting_up: "Starting up",
      thinking: "Thinking",
      typing: "Typing",
    },
    tools: {
      running_command: "Running a command",
      reading_files: "Reading files",
      searching_code: "Searching the code",
      making_edits: "Making edits",
      searching_web: "Searching the web",
      fallback: "Working",
    },
  },
} as const;

const tAgents = ((selector: (r: typeof AGENTS) => string) =>
  selector(AGENTS)) as TFunction<"agents">;
const tChat = ((selector: (r: typeof CHAT) => string) =>
  selector(CHAT)) as TFunction<"chat">;

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

describe("resolveAgentLiveStatus", () => {
  it("returns null while presence is loading", () => {
    expect(
      resolveAgentLiveStatus({
        presence: "loading",
        activeTask: null,
        taskMessages: [],
        tAgents,
        tChat,
      }),
    ).toBeNull();
  });

  it("falls back to Idle when online with no active task", () => {
    const view = resolveAgentLiveStatus({
      presence: presence({ availability: "online", workload: "idle" }),
      activeTask: null,
      taskMessages: [],
      tAgents,
      tChat,
    });
    expect(view?.label).toBe("Idle");
  });

  it("shows Offline from presence when offline with no active task", () => {
    const view = resolveAgentLiveStatus({
      presence: presence({ availability: "offline", workload: "idle" }),
      activeTask: null,
      taskMessages: [],
      tAgents,
      tChat,
    });
    expect(view?.label).toBe("Offline");
  });

  it("shows Thinking for a running task with no stream yet", () => {
    const view = resolveAgentLiveStatus({
      presence: presence({ availability: "online", workload: "working", runningCount: 1 }),
      activeTask: task({ id: "task-1", status: "running" }),
      taskMessages: [],
      tAgents,
      tChat,
    });
    expect(view?.label).toBe("Thinking");
    expect(view?.textClass).toBe("text-brand");
  });

  it("shows Running a command from the latest tool_use stream event", () => {
    const view = resolveAgentLiveStatus({
      presence: presence({ availability: "online", workload: "working", runningCount: 1 }),
      activeTask: task({ id: "task-1", status: "running" }),
      taskMessages: [
        msg({ type: "thinking", seq: 1 }),
        msg({ type: "tool_use", seq: 2, tool: "bash" }),
      ],
      tAgents,
      tChat,
    });
    expect(view?.label).toBe("Running a command");
  });

  it("shows Reading files for read tool", () => {
    const view = resolveAgentLiveStatus({
      presence: presence({ availability: "online", workload: "working", runningCount: 1 }),
      activeTask: task({ id: "task-1", status: "running" }),
      taskMessages: [msg({ type: "tool_use", seq: 1, tool: "read" })],
      tAgents,
      tChat,
    });
    expect(view?.label).toBe("Reading files");
  });

  it("shows Typing when the latest stream is text", () => {
    const view = resolveAgentLiveStatus({
      presence: presence({ availability: "online", workload: "working", runningCount: 1 }),
      activeTask: task({ id: "task-1", status: "running" }),
      taskMessages: [msg({ type: "text", seq: 1, content: "hello" })],
      tAgents,
      tChat,
    });
    expect(view?.label).toBe("Typing");
  });

  it("shows Queued for a queued task while online", () => {
    const view = resolveAgentLiveStatus({
      presence: presence({ availability: "online", workload: "queued", queuedCount: 1 }),
      activeTask: task({ id: "task-1", status: "queued" }),
      taskMessages: [],
      tAgents,
      tChat,
    });
    expect(view?.label).toBe("Queued");
  });

  it("does not surface bare Working when a tool is running", () => {
    const view = resolveAgentLiveStatus({
      presence: presence({ availability: "online", workload: "working", runningCount: 1 }),
      activeTask: task({ id: "task-1", status: "running" }),
      taskMessages: [msg({ type: "tool_use", seq: 1, tool: "bash" })],
      tAgents,
      tChat,
    });
    expect(view?.label).not.toBe("Working");
    expect(view?.label).toBe("Running a command");
  });
});
