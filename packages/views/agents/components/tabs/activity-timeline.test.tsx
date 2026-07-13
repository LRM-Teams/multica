// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, cleanup, fireEvent } from "@testing-library/react";
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
              running_command: "Running command",
              writing_file: "Writing file",
              editing_file: "Editing file",
              reading_file: "Reading file",
              searching_files: "Searching files",
              searching_code: "Searching code",
              searching_web: "Searching web",
              sending_message: "Sending message",
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
  activity_kind: "thinking",
  detail_kind: "thinking",
  text: "Built the project.",
  target_ref: { kind: "agent", id: "agent-1" },
};
// A raft diagnostic-kind event — the mainline/diagnostic split is now driven by
// `activity_kind` (#389), not a `visibility` flag, so a diagnostic KIND is what
// keeps a row out of the default timeline.
const DIAG: ActivityEvent = {
  id: "d1",
  agent_id: "agent-1",
  occurred_at: "2026-07-06T09:36:10Z",
  activity_kind: "runtime_diagnostic",
  detail_kind: "runtime_diagnostic",
  reason_code: "freshness_check",
  target_ref: { kind: "agent", id: "agent-1" },
};
const WAKE: ActivityEvent = {
  id: "w1",
  agent_id: "agent-1",
  occurred_at: "2026-07-06T09:36:08Z",
  activity_kind: "wake_attempt",
  detail_kind: "wake_attempt",
  target_ref: { kind: "agent", id: "agent-1" },
};
const TOOL: ActivityEvent = {
  id: "tc1",
  agent_id: "agent-1",
  occurred_at: "2026-07-06T09:36:09Z",
  activity_kind: "tool_call",
  detail_kind: "tool_use",
  tool: "bash",
  tool_target: "bash",
  status: "running",
  target_ref: { kind: "agent", id: "agent-1" },
};
const EDIT: ActivityEvent = {
  id: "edit1",
  agent_id: "agent-1",
  occurred_at: "2026-07-06T09:36:10Z",
  activity_kind: "tool_call",
  detail_kind: "tool_use",
  tool: "edit_file",
  tool_target: "profile.go",
  status: "completed",
  target_ref: { kind: "agent", id: "agent-1" },
};
// #484 makes a file tool's tool_target a source-backed path (absolute when the
// runtime provides it) — long enough to blow out the row without the #385-FE
// basename-preserving path treatment.
const WRITE_LONGPATH: ActivityEvent = {
  id: "wlp1",
  agent_id: "agent-1",
  occurred_at: "2026-07-06T09:36:10Z",
  activity_kind: "tool_call",
  detail_kind: "tool_use",
  tool: "write_file",
  tool_target: "/Users/frank/multica_workspaces/7373de75/workdir/pathcheck.txt",
  status: "completed",
  target_ref: { kind: "agent", id: "agent-1" },
};
const TEXT: ActivityEvent = {
  id: "txt1",
  agent_id: "agent-1",
  occurred_at: "2026-07-06T09:36:11Z",
  activity_kind: "text",
  detail_kind: "text",
  text: "Done.",
  target_ref: { kind: "agent", id: "agent-1" },
};
const COMPACTION: ActivityEvent = {
  id: "cmp1",
  agent_id: "agent-1",
  occurred_at: "2026-07-06T09:36:12Z",
  activity_kind: "compaction_started",
  detail_kind: "compaction_started",
  target_ref: { kind: "agent", id: "agent-1" },
};
const TURN_END: ActivityEvent = {
  id: "done1",
  agent_id: "agent-1",
  occurred_at: "2026-07-06T09:36:13Z",
  activity_kind: "turn_end",
  detail_kind: "task_completed",
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

  it("shows a long file path with the basename always visible + full path on hover (#385)", () => {
    // A ~60-char source-backed path must not blow out the row: the basename
    // stays fully visible, the leading directories middle-ellipsis (a truncating
    // head span — never right-truncate the basename), and the full path is
    // exposed on hover via `title`.
    render(<ActivityTimeline events={[WRITE_LONGPATH]} />);
    const full = "/Users/frank/multica_workspaces/7373de75/workdir/pathcheck.txt";
    // Basename (with leading "/") is a discrete, non-truncating node.
    expect(screen.getByText("/pathcheck.txt")).toBeInTheDocument();
    // Leading directories live in a truncating head span (middle-ellipsis).
    const head = screen.getByText("/Users/frank/multica_workspaces/7373de75/workdir");
    expect(head).toHaveClass("truncate");
    // Full path is recoverable on hover.
    expect(screen.getByTitle(full)).toBeInTheDocument();
  });

  it("compact mode also gives a long file path the basename-preserving treatment (#385/#383)", () => {
    // Profile Recent (compact) shares the row, so a long path there must not
    // right-truncate the basename either — same head-truncate + tail-visible.
    render(<ActivityTimeline events={[WRITE_LONGPATH]} compact />);
    expect(screen.getByText("/pathcheck.txt")).toBeInTheDocument();
    expect(
      screen.getByText("/Users/frank/multica_workspaces/7373de75/workdir"),
    ).toHaveClass("truncate");
    expect(
      screen.getByTitle("/Users/frank/multica_workspaces/7373de75/workdir/pathcheck.txt"),
    ).toBeInTheDocument();
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

  it("renders Output/thinking full text as a collapsed click-to-expand block", () => {
    render(<ActivityTimeline events={[TEXT]} />);
    // The reply text is a collapsed, expandable control (§2.1: first line, click
    // for the full block) — not a fixed inline subtext.
    const block = screen.getByRole("button", { name: "Done." });
    expect(block).toHaveAttribute("aria-expanded", "false");
    fireEvent.click(block);
    expect(block).toHaveAttribute("aria-expanded", "true");
  });

  it("keeps a fixed subtext (Message received) inline, not an expandable block", () => {
    render(<ActivityTimeline events={[WAKE]} />);
    expect(screen.queryByRole("button", { name: "Message received" })).toBeNull();
    expect(screen.getByText("Message received")).toBeInTheDocument();
  });

  it("compact mode: shows only the most recent N narrative rows, never a click-to-expand", () => {
    // Profile Recent (#383): same projection, layout-only delta — last N rows,
    // single-line truncated subtext, no expand.
    const many: ActivityEvent[] = Array.from({ length: 7 }, (_, i) => ({
      id: `m${i}`,
      agent_id: "agent-1",
      occurred_at: `2026-07-06T09:3${i}:00Z`,
      activity_kind: "text",
      detail_kind: "text",
      text: `Reply ${i}`,
      target_ref: { kind: "agent", id: "agent-1" },
    }));
    render(<ActivityTimeline events={many} compact />);
    expect(screen.getAllByTestId("activity-row")).toHaveLength(5);
    expect(screen.queryByRole("button")).toBeNull();
    // most recent rows kept (m6 present, oldest m0/m1 trimmed)
    expect(screen.getByText("Reply 6")).toBeInTheDocument();
    expect(screen.queryByText("Reply 0")).toBeNull();
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
