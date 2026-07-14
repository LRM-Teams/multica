import { describe, it, expect } from "vitest";
import type { ActivityEvent } from "./components/tabs/activity-event";
import { activityEventsToTaskMessages } from "./use-agent-live-status";

function evt(overrides: Partial<ActivityEvent>): ActivityEvent {
  return {
    id: overrides.id ?? "e",
    agent_id: "agent-1",
    occurred_at: overrides.occurred_at ?? "2026-07-13T00:00:00Z",
    activity_kind: overrides.activity_kind ?? "thinking",
    detail_kind: "text",
    ...overrides,
  } as ActivityEvent;
}

describe("activityEventsToTaskMessages (#414 — Activity stream → stage picker shape)", () => {
  it("maps the working kinds to the TaskMessagePayload types the picker reads", () => {
    const out = activityEventsToTaskMessages([
      evt({ activity_kind: "thinking" }),
      evt({ activity_kind: "text" }),
      evt({ activity_kind: "tool_call", tool: "bash" }),
    ]);
    expect(out.map((m) => m.type)).toEqual(["thinking", "text", "tool_use"]);
    expect(out[2]?.tool).toBe("bash");
    expect(out[0]?.tool).toBeUndefined();
  });

  it("drops non-stage kinds (status rows, wake, errors) so they never become the latest stage", () => {
    const out = activityEventsToTaskMessages([
      evt({ activity_kind: "custom", detail_kind: "agent_status_changed", status: "idle" }),
      evt({ activity_kind: "wake_attempt" }),
      evt({ activity_kind: "error" }),
      evt({ activity_kind: "tool_call", tool: "read_file" }),
    ]);
    // Only the tool_call survives — the picker will read it as the current stage.
    expect(out).toHaveLength(1);
    expect(out[0]).toMatchObject({ type: "tool_use", tool: "read_file" });
  });

  it("preserves chronological order and carries occurred_at through", () => {
    const out = activityEventsToTaskMessages([
      evt({ activity_kind: "thinking", occurred_at: "2026-07-13T00:00:01Z" }),
      evt({ activity_kind: "tool_call", tool: "grep", occurred_at: "2026-07-13T00:00:02Z" }),
    ]);
    expect(out.map((m) => m.created_at)).toEqual([
      "2026-07-13T00:00:01Z",
      "2026-07-13T00:00:02Z",
    ]);
  });
});
