// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, cleanup } from "@testing-library/react";
import { ActivityTimeline } from "./activity-timeline";
import { formatActivityTime, type ActivityEvent } from "./activity-event";

vi.mock("../../../common/use-viewing-timezone", () => ({
  useViewingTimezone: () => "UTC",
}));

vi.mock("../../../i18n", () => ({
  useT: () => ({
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    t: (selector: (r: any) => string) =>
      selector({
        tab_body: {
          activity: {
            timeline_empty: "No activity yet",
            view_diagnostics: "View diagnostic details",
            hide_diagnostics: "Hide diagnostic details",
            labels: {
              thinking: "Thinking",
              output: "Output",
              working: "Working",
              failed: "Failed",
              waiting: "Waiting",
              running_command: "Running command…",
              writing_file: "Writing file…",
              editing_file: "Editing file…",
              reading_file: "Reading file…",
              searching_files: "Searching files…",
              searching_code: "Searching code…",
              searching_web: "Searching web…",
              sending_message: "Sending message…",
            },
            subtexts: {
              message_received: "Message received",
              compacting_context: "Compacting context",
              compaction_finished: "Compaction finished",
              subagent_activity: "Subagent activity",
            },
          },
        },
      }),
  }),
}));

const USER: ActivityEvent = {
  id: "u1",
  agent_id: "agent-1",
  occurred_at: "2026-07-06T09:36:05Z",
  kind: "thinking",
  event_type: "thinking",
  visibility: "user_facing",
  text: "Built the project.",
  target_ref: { kind: "agent", id: "agent-1" },
};
const DIAG: ActivityEvent = {
  id: "d1",
  agent_id: "agent-1",
  occurred_at: "2026-07-06T09:36:10Z",
  kind: "blocked",
  event_type: "blocked",
  visibility: "diagnostic_only",
  reason_code: "freshness_check",
  target_ref: { kind: "agent", id: "agent-1" },
};
const WAKE: ActivityEvent = {
  id: "w1",
  agent_id: "agent-1",
  occurred_at: "2026-07-06T09:36:08Z",
  kind: "wake_attempt",
  event_type: "wake_attempt",
  visibility: "user_facing",
  target_ref: { kind: "agent", id: "agent-1" },
};
const TOOL: ActivityEvent = {
  id: "tc1",
  agent_id: "agent-1",
  occurred_at: "2026-07-06T09:36:09Z",
  kind: "tool_call",
  event_type: "tool_use",
  visibility: "user_facing",
  tool: "bash",
  tool_target: "bash",
  status: "running",
  target_ref: { kind: "agent", id: "agent-1" },
};
const EDIT: ActivityEvent = {
  id: "edit1",
  agent_id: "agent-1",
  occurred_at: "2026-07-06T09:36:10Z",
  kind: "tool_call",
  event_type: "tool_use",
  visibility: "user_facing",
  tool: "edit_file",
  tool_target: "profile.go",
  status: "completed",
  target_ref: { kind: "agent", id: "agent-1" },
};
const TEXT: ActivityEvent = {
  id: "txt1",
  agent_id: "agent-1",
  occurred_at: "2026-07-06T09:36:11Z",
  kind: "text",
  event_type: "text",
  visibility: "user_facing",
  text: "Done.",
  target_ref: { kind: "agent", id: "agent-1" },
};
const COMPACTION: ActivityEvent = {
  id: "cmp1",
  agent_id: "agent-1",
  occurred_at: "2026-07-06T09:36:12Z",
  kind: "compaction_started",
  event_type: "compaction_started",
  visibility: "user_facing",
  target_ref: { kind: "agent", id: "agent-1" },
};
const TURN_END: ActivityEvent = {
  id: "done1",
  agent_id: "agent-1",
  occurred_at: "2026-07-06T09:36:13Z",
  kind: "turn_end",
  event_type: "task_completed",
  visibility: "user_facing",
  target_ref: { kind: "agent", id: "agent-1" },
};

describe("ActivityTimeline", () => {
  beforeEach(() => cleanup());

  it("renders user_facing events (projected label + subtext) and hides diagnostic_only by default", () => {
    render(<ActivityTimeline events={[USER, DIAG]} />);
    expect(screen.getByText("Thinking")).toBeInTheDocument();
    expect(screen.getByText("Built the project.")).toBeInTheDocument();
    expect(screen.queryByText("Waiting")).toBeNull();
    expect(screen.queryByText("View diagnostic details")).toBeNull();
  });

  it("compact mode: user_facing only, no diagnostics toggle", () => {
    render(<ActivityTimeline events={[USER, DIAG]} compact />);
    expect(screen.getByText("Thinking")).toBeInTheDocument();
    expect(screen.queryByText("Waiting · freshness check")).toBeNull();
    expect(screen.queryByText("View diagnostic details")).toBeNull();
  });

  it("shows the empty state when there are no user_facing events", () => {
    render(<ActivityTimeline events={[DIAG]} />);
    expect(screen.getByText("No activity yet")).toBeInTheDocument();
  });

  it("never renders raw command text — labels come from the read model", () => {
    // A diagnostic row's raw content is not exposed unless explicitly toggled;
    // and even then it's the BE-provided label, never a raw command string.
    render(<ActivityTimeline events={[TOOL, EDIT]} />);
    expect(screen.getByText("Running command…")).toBeInTheDocument();
    expect(screen.getByText("Editing file")).toBeInTheDocument();
    expect(screen.getByText("profile.go")).toBeInTheDocument();
    expect(screen.queryByText(/\/bin\/|--target|raft message/)).toBeNull();
    expect(screen.queryByText("Ran a command")).toBeNull();
  });

  it("projects Raft-style wake and reply labels without leaking old presentation copy", () => {
    render(<ActivityTimeline events={[WAKE, TEXT]} />);
    expect(screen.getByText("Working")).toBeInTheDocument();
    expect(screen.getByText("Message received")).toBeInTheDocument();
    expect(screen.getByText("Output")).toBeInTheDocument();
    expect(screen.queryByText("Woken")).toBeNull();
    expect(screen.queryByText("Sent a message")).toBeNull();
  });

  it("shows source-confirmed visible lifecycle and hides internal turn_end rows", () => {
    render(<ActivityTimeline events={[COMPACTION, TURN_END]} />);
    expect(screen.getByText("Compacting context")).toBeInTheDocument();
    expect(screen.queryByText("Done")).toBeNull();
  });
});

describe("formatActivityTime", () => {
  it("formats HH:MM:SS 24-hour in the given timezone", () => {
    expect(formatActivityTime("2026-07-06T09:36:05Z", "UTC")).toBe("09:36:05");
    expect(formatActivityTime("2026-07-06T09:36:05Z", "Asia/Shanghai")).toBe("17:36:05");
  });

  it("returns empty string for an invalid date", () => {
    expect(formatActivityTime("not-a-date", "UTC")).toBe("");
  });
});
