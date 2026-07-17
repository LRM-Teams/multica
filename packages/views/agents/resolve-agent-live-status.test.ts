import { describe, expect, it } from "vitest";
import type { TFunction } from "i18next";
import type { AgentPresenceDetail } from "@multica/core/agents";
import type { AgentTask } from "@multica/core/types";
import type { ActivityEvent } from "./components/tabs/activity-event";
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

// The header now projects the Activity row's OWN presentation, so the label
// mock is the `agents` activity-labels table (same one the timeline resolves).
const AGENTS = {
  workload: { working: "Working", queued: "Queued", idle: "Idle" },
  availability: { online: "Online", unstable: "Unstable", offline: "Offline", archived: "Archived" },
  tab_body: {
    activity: {
      labels: {
        thinking: "Thinking",
        output: "Output",
        running_command: "Running command",
        reading_file: "Reading file",
        writing_file: "Writing file",
        idle: "Idle",
        working: "Working",
      },
    },
  },
} as const;

const CHAT = {
  status_pill: {
    stages: {
      offline: "Offline",
      reconnecting: "Reconnecting",
      queued: "Queued",
    },
  },
} as const;

const tAgents = ((selector: (r: typeof AGENTS) => string) =>
  selector(AGENTS)) as unknown as TFunction<"agents">;
const tChat = ((selector: (r: typeof CHAT) => string) =>
  selector(CHAT)) as unknown as TFunction<"chat">;

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

describe("resolveAgentLiveStatus (header = Activity latest-row projection)", () => {
  const online = presence({ availability: "online", workload: "working", runningCount: 1 });

  it("returns null while presence is loading", () => {
    expect(
      resolveAgentLiveStatus({ presence: "loading", activeTask: null, latestActivity: null, tAgents, tChat }),
    ).toBeNull();
  });

  it("shows Online (not Idle) when online with no active task", () => {
    const view = resolveAgentLiveStatus({
      presence: presence({ availability: "online", workload: "idle" }),
      activeTask: null,
      latestActivity: null,
      tAgents,
      tChat,
    });
    expect(view?.label).toBe("Online");
  });

  it("shows Offline from presence when offline with no active task", () => {
    const view = resolveAgentLiveStatus({
      presence: presence({ availability: "offline", workload: "idle" }),
      activeTask: null,
      latestActivity: null,
      tAgents,
      tChat,
    });
    expect(view?.label).toBe("Offline");
  });

  it("offline availability overrides a residual active task + activity row → Offline (#571)", () => {
    // #571 regression: when the runtime is offline, the header must read
    // Offline even though a stale/residual task is still marked running AND an
    // Activity row is present. Connection state wins over the projected stage —
    // never a workload "Working" or a leftover activity word.
    const view = resolveAgentLiveStatus({
      presence: presence({ availability: "offline", workload: "working", runningCount: 1 }),
      activeTask: task({ id: "task-1", status: "running" }),
      latestActivity: evt({ activity_kind: "tool_call", tool: "bash" }),
      tAgents,
      tChat,
    });
    expect(view?.label).toBe("Offline");
    expect(view?.label).not.toBe("Working");
    expect(view?.label).not.toContain("Running command");
  });

  it("shows Thinking for a running task with no activity row yet", () => {
    const view = resolveAgentLiveStatus({
      presence: online,
      activeTask: task({ id: "task-1", status: "running" }),
      latestActivity: null,
      tAgents,
      tChat,
    });
    expect(view?.label).toBe("Thinking");
    // Thinking projects the neutral tone the timeline opens a round with — grey
    // dot + neutral text, never a blue "Working" dot.
    expect(view?.dotClass).toBe("bg-muted-foreground/40");
    expect(view?.textClass).toBe("text-foreground");
  });

  it("never invents 'Queued' — a queued task with no Activity row reads Thinking", () => {
    // Frank: the Activity timeline has no "queued" row, so the header must not
    // project one from the task snapshot status. A queued task with nothing
    // streamed falls back to the neutral Thinking word, never "Queued".
    const view = resolveAgentLiveStatus({
      presence: presence({ availability: "online", workload: "queued", queuedCount: 1 }),
      activeTask: task({ id: "task-1", status: "queued" }),
      latestActivity: null,
      tAgents,
      tChat,
    });
    expect(view?.label).toBe("Thinking");
    expect(view?.label).not.toBe("Queued");
  });

  it("projects a command row as 'Running command' with a NEUTRAL dot (Slack-style reduction)", () => {
    const view = resolveAgentLiveStatus({
      presence: online,
      activeTask: task({ id: "task-1", status: "running" }),
      latestActivity: evt({ activity_kind: "tool_call", tool: "bash" }),
      tAgents,
      tChat,
    });
    expect(view?.label).toContain("Running command");
    // Post-reduction every non-failure tone is neutral gray — the command dot no
    // longer carries an amber accent; "is it live" reads via the avatar pulse.
    expect(view?.dotClass).toBe("bg-muted-foreground/40");
    // The LABEL text stays neutral too, exactly like the timeline row.
    expect(view?.textClass).toBe("text-foreground");
  });

  it("projects a read tool row as 'Reading file'", () => {
    const view = resolveAgentLiveStatus({
      presence: online,
      activeTask: task({ id: "task-1", status: "running" }),
      latestActivity: evt({ activity_kind: "tool_call", tool: "read_file" }),
      tAgents,
      tChat,
    });
    expect(view?.label).toContain("Reading file");
  });

  it("projects a text row as 'Output', never a bare 'Working'", () => {
    const view = resolveAgentLiveStatus({
      presence: online,
      activeTask: task({ id: "task-1", status: "running" }),
      latestActivity: evt({ activity_kind: "text", text: "hello" }),
      tAgents,
      tChat,
    });
    expect(view?.label).toBe("Output");
    expect(view?.label).not.toBe("Working");
  });
});
