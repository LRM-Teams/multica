// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, cleanup, fireEvent, waitFor } from "@testing-library/react";
import { copyText } from "@multica/ui/lib/clipboard";
import { ActivityTimeline } from "./activity-timeline";
import { formatActivityTime, formatActivityRelativeTime, type ActivityEvent } from "./activity-event";

vi.mock("../../../common/use-viewing-timezone", () => ({
  useViewingTimezone: () => "UTC",
}));

vi.mock("../../../i18n", () => ({
  useT: () => ({
    t: (selector: (r: any) => string) =>
      selector({
        tab_body: {
          activity: {
            timeline_empty: "No activity yet",
            view_diagnostics: "View diagnostic details",
            hide_diagnostics: "Hide diagnostic details",
            copy_command: "Copy",
            command_copied: "Copied",
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
              compaction_finished: "Context compaction finished",
              subagent_activity: "Subagent activity",
            },
          },
        },
      }),
  }),
}));

vi.mock("../../../common/markdown", () => ({
  MemoizedMarkdown: ({
    children,
    enableStickerShortcodes,
  }: {
    children: string;
    enableStickerShortcodes?: boolean;
  }) => (
    <div
      data-testid="activity-markdown"
      data-sticker-shortcodes={String(enableStickerShortcodes)}
      data-source={children}
    >
      {children}
    </div>
  ),
}));

vi.mock("@multica/ui/lib/clipboard", () => ({
  copyText: vi.fn().mockResolvedValue(true),
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
const IDLE_STATUS: ActivityEvent = {
  id: "idle1",
  agent_id: "agent-1",
  occurred_at: "2026-07-06T09:36:14Z",
  activity_kind: "custom",
  detail_kind: "agent_status_changed",
  status: "idle",
  target_ref: { kind: "agent", id: "agent-1" },
};
function errorEvent(overrides: Partial<ActivityEvent> = {}): ActivityEvent {
  return {
    id: "err1",
    agent_id: "agent-1",
    occurred_at: new Date().toISOString(),
    activity_kind: "error",
    detail_kind: "error",
    text: "cursor-agent exited with error: exit status 1",
    target_ref: { kind: "agent", id: "agent-1" },
    ...overrides,
  };
}

describe("ActivityTimeline", () => {
  beforeEach(() => {
    cleanup();
    vi.mocked(copyText).mockClear();
  });

  it("renders mainline events (projected label + subtext) and hides diagnostic kinds by default", () => {
    render(<ActivityTimeline events={[USER, DIAG]} />);
    expect(screen.getByText("Thinking")).toBeInTheDocument();
    expect(screen.getByText("Built the project.")).toBeInTheDocument();
    expect(screen.queryByText("Waiting")).toBeNull();
    expect(screen.queryByText("View diagnostic details")).toBeNull();
  });

  it("compact mode: mainline only, no diagnostics toggle", () => {
    render(<ActivityTimeline events={[USER, DIAG]} compact />);
    expect(screen.getByText("Thinking")).toBeInTheDocument();
    expect(screen.queryByText("Waiting · freshness check")).toBeNull();
    expect(screen.queryByText("View diagnostic details")).toBeNull();
  });

  it("compact mode drops settled Idle status rows; full timeline keeps them (#465②)", () => {
    // Compact peek: the action row shows, the settled Idle status row is noise → dropped.
    const { rerender } = render(<ActivityTimeline events={[TEXT, IDLE_STATUS]} compact />);
    expect(screen.getByText("Output")).toBeInTheDocument();
    expect(screen.queryByText("Idle")).toBeNull();
    // Full timeline keeps the historical "went idle" fact.
    rerender(<ActivityTimeline events={[TEXT, IDLE_STATUS]} />);
    expect(screen.getByText("Idle")).toBeInTheDocument();
  });

  it("merges consecutive Idle rows into one de-emphasized `Idle · N` line (LRM-566 方案 B)", () => {
    // SoT LRM-567 BEFORE: three bare `Idle · <time>` rows stack a middle-gap
    // void. AFTER: one collapsed row, latest timestamp, dimmed + tight.
    const idle14: ActivityEvent = { ...IDLE_STATUS, id: "idle-14", occurred_at: "2026-07-06T09:36:14Z" };
    const idle30: ActivityEvent = { ...IDLE_STATUS, id: "idle-30", occurred_at: "2026-07-06T09:36:30Z" };
    const idle48: ActivityEvent = { ...IDLE_STATUS, id: "idle-48", occurred_at: "2026-07-06T09:36:48Z" };
    render(<ActivityTimeline events={[TEXT, idle14, idle30, idle48]} />);

    // Exactly one merged idle row carries the whole run.
    const idleRow = screen.getByTestId("activity-idle-row");
    expect(idleRow).toHaveAttribute("data-idle-count", "3");
    expect(screen.getAllByTestId("activity-idle-row")).toHaveLength(1);
    // Label is `Idle · 3` (the merged count), not three separate `Idle` rows.
    expect(screen.getByText("Idle · 3")).toBeInTheDocument();
    // Latest timestamp wins (09:36:48), not the first — task #13: the visible
    // text is now relative ("Xd ago"), the exact clock time lives in the
    // hover title so precision isn't lost.
    const idleTimeTitle = idleRow.querySelector("[title]")?.getAttribute("title");
    expect(idleTimeTitle).toContain("09:36:48");
    expect(idleTimeTitle).not.toContain("09:36:14");

    // De-emphasized per SoT: tight row + dimmed medium label.
    expect(idleRow.className).toContain("py-0.5");
    const label = screen.getByText("Idle · 3");
    expect(label.className).toContain("text-[12px]");
    expect(label.className).toContain("font-medium");
    expect(label.className).toContain("text-muted-foreground");
  });

  it("a lone Idle row stays de-emphasized but shows plain `Idle` (no count suffix)", () => {
    render(<ActivityTimeline events={[TEXT, IDLE_STATUS]} />);
    const idleRow = screen.getByTestId("activity-idle-row");
    expect(idleRow).toHaveAttribute("data-idle-count", "1");
    // No `· 1` suffix for a single idle — same dimmed styling as a merged run.
    expect(screen.getByText("Idle")).toBeInTheDocument();
    expect(screen.queryByText("Idle · 1")).toBeNull();
    expect(idleRow.className).toContain("py-0.5");
    const label = screen.getByText("Idle");
    expect(label.className).toContain("text-[12px]");
    expect(label.className).toContain("text-muted-foreground");
  });

  it("keeps separate (non-adjacent) Idle runs as separate merged rows", () => {
    const a1: ActivityEvent = { ...IDLE_STATUS, id: "a1", occurred_at: "2026-07-06T09:00:00Z" };
    const a2: ActivityEvent = { ...IDLE_STATUS, id: "a2", occurred_at: "2026-07-06T09:00:10Z" };
    const b1: ActivityEvent = { ...IDLE_STATUS, id: "b1", occurred_at: "2026-07-06T09:01:00Z" };
    render(<ActivityTimeline events={[a1, a2, TEXT, b1]} />);
    const idleRows = screen.getAllByTestId("activity-idle-row");
    expect(idleRows).toHaveLength(2);
    expect(idleRows[0]).toHaveAttribute("data-idle-count", "2");
    expect(idleRows[0]!.querySelector("[title]")?.getAttribute("title")).toContain("09:00:10");
    expect(idleRows[1]).toHaveAttribute("data-idle-count", "1");
    expect(idleRows[1]!.querySelector("[title]")?.getAttribute("title")).toContain("09:01:00");
  });

  it("shows the empty state when there are no mainline events", () => {
    render(<ActivityTimeline events={[DIAG]} />);
    expect(screen.getByTestId("activity-timeline-empty")).toBeInTheDocument();
    expect(screen.getByText("No activity yet")).toBeInTheDocument();
    expect(screen.queryByTestId("activity-timeline-spine")).toBeNull();
  });

  it("renders loading skeleton bars without a spine (LRM-563)", () => {
    const { container } = render(<ActivityTimeline events={[]} isLoading />);
    expect(screen.getByTestId("activity-timeline-loading")).toBeInTheDocument();
    expect(screen.queryByTestId("activity-timeline-spine")).toBeNull();
    expect(container.querySelectorAll("[data-slot='skeleton']").length).toBeGreaterThanOrEqual(3);
  });

  it("renders error state with retry and no red wash (LRM-563)", () => {
    const onRetry = vi.fn();
    render(<ActivityTimeline events={[]} isError onRetry={onRetry} />);
    expect(screen.getByTestId("activity-timeline-error")).toBeInTheDocument();
    expect(screen.getByText("Couldn't load activity")).toBeInTheDocument();
    expect(screen.queryByTestId("activity-timeline-spine")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(onRetry).toHaveBeenCalledTimes(1);
  });

  it("keeps compact empty as a one-line italic (profile peek)", () => {
    render(<ActivityTimeline events={[DIAG]} compact />);
    expect(screen.getByText("No activity yet")).toBeInTheDocument();
    expect(screen.queryByTestId("activity-timeline-empty")).toBeNull();
  });

  it("never renders raw command text — labels come from the read model", () => {
    // A diagnostic row's raw content is not exposed unless explicitly toggled;
    // and even then it's the BE-provided label, never a raw command string.
    render(<ActivityTimeline events={[TOOL, EDIT]} />);
    // Command rows are amber `running` tone (not `active`), so no trailing "…" (#404).
    expect(screen.getByText("Running command")).toBeInTheDocument();
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

  it("compact mode shows state type only — no path/command detail (LRM-650)", () => {
    // Profile Recent compact: EN label only; path/command stay on Expanded.
    render(<ActivityTimeline events={[WRITE_LONGPATH]} compact />);
    expect(screen.getByText("Writing file")).toBeInTheDocument();
    expect(screen.queryByText("/pathcheck.txt")).toBeNull();
    expect(
      screen.queryByTitle("/Users/frank/multica_workspaces/7373de75/workdir/pathcheck.txt"),
    ).toBeNull();
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

  it("expands authored output through the message Markdown renderer in a sibling detail region", () => {
    const markdownOutput: ActivityEvent = {
      ...TEXT,
      text: "## Verification\n\n[@Frank](mention://member/frank-1) confirmed it.",
    };
    render(<ActivityTimeline events={[markdownOutput]} />);

    const header = screen.getByRole("button", { name: /Output/ });
    expect(header).toHaveAttribute("aria-expanded", "false");
    expect(header).not.toHaveAttribute("aria-controls");
    expect(header).toHaveClass("min-h-11");

    fireEvent.click(header);
    const detail = screen.getByTestId("activity-expanded-detail");
    expect(header).toHaveAttribute("aria-expanded", "true");
    expect(header).toHaveAttribute("aria-controls");
    expect(detail).toHaveAttribute("id", header.getAttribute("aria-controls"));
    expect(header.contains(detail)).toBe(false);
    // Expanded content replaces the compact preview in the same text column;
    // it is not rendered as a second bordered card below the row.
    expect(header).not.toHaveTextContent(markdownOutput.text!);
    expect(detail).toHaveClass("mt-1", "text-xs", "text-foreground");
    // LRM-560: expanded body sits in muted surface + brand bar (not old left-time indent).
    expect(screen.getByTestId("activity-expanded-surface")).toHaveClass(
      "border-l-2",
      "border-brand",
      "bg-muted",
    );
    const markdown = screen.getByTestId("activity-markdown");
    expect(markdown).toHaveAttribute("data-source", markdownOutput.text!);
    expect(markdown).toHaveAttribute("data-sticker-shortcodes", "false");
  });

  it("paints a continuous spine and tokenized hang-off nodes (LRM-560)", () => {
    const READ_RUNNING: ActivityEvent = {
      ...EDIT,
      id: "rd-run",
      tool: "read_file",
      status: "running",
    };
    render(<ActivityTimeline events={[TOOL, READ_RUNNING]} />);
    expect(screen.getByTestId("activity-timeline-spine")).toHaveClass("w-[1.5px]", "bg-border");
    const rows = screen.getAllByTestId("activity-row");
    expect(rows).toHaveLength(2);
    // Command → bg-running; in-progress file tool → bg-brand
    expect(rows[0]!.querySelector(".bg-running")).not.toBeNull();
    expect(rows[1]!.querySelector(".bg-brand")).not.toBeNull();
  });

  it("normalizes blank lines in expanded thinking/output (LRM-554 / LRM-560)", () => {
    const sparse: ActivityEvent = {
      ...TEXT,
      text: "line1\n\n\n\nline2\n",
    };
    render(<ActivityTimeline events={[sparse]} />);
    fireEvent.click(screen.getByRole("button", { name: /Output/ }));
    expect(screen.getByTestId("activity-markdown")).toHaveAttribute(
      "data-source",
      "line1\n\nline2",
    );
  });

  it("keeps expanded detail bounded only when it truly overflows, with a fade until scrolled to the end", () => {
    const longOutput: ActivityEvent = {
      ...TEXT,
      text: "Long authored output that exceeds the reused history-message height boundary.",
    };
    render(<ActivityTimeline events={[longOutput]} />);
    fireEvent.click(screen.getByRole("button", { name: /Output/ }));

    const detail = screen.getByTestId("activity-expanded-detail");
    // Short/default content remains a plain sibling — no empty tab stop or fade.
    expect(detail).toHaveClass("max-h-[min(260px,55vh)]", "md:max-h-[360px]");
    expect(detail).not.toHaveAttribute("role");
    expect(screen.queryByTestId("activity-detail-scroll-fade")).toBeNull();

    Object.defineProperties(detail, {
      clientHeight: { configurable: true, value: 260 },
      scrollHeight: { configurable: true, value: 520 },
      scrollTop: { configurable: true, value: 0, writable: true },
    });
    fireEvent(window, new Event("resize"));

    expect(detail).toHaveClass("overflow-y-auto", "overscroll-contain");
    expect(detail).toHaveAttribute("role", "region");
    expect(detail).toHaveAttribute("tabindex", "0");
    expect(detail).toHaveAttribute("aria-label", "Expanded details, scrollable");
    expect(screen.getByTestId("activity-detail-scroll-fade")).toHaveClass(
      "pointer-events-none",
      "bg-gradient-to-t",
    );

    detail.scrollTop = 260;
    fireEvent.scroll(detail);
    expect(screen.queryByTestId("activity-detail-scroll-fade")).toBeNull();
  });

  it("keeps Tier3 fixed facts inline without an empty expand affordance", () => {
    render(<ActivityTimeline events={[WAKE]} />);
    expect(screen.queryByRole("button")).toBeNull();
    expect(screen.getByText("Message received")).toBeInTheDocument();
  });

  it("compact mode: shows only the most recent N narrative rows, never a click-to-expand", () => {
    // Profile Recent (#383 / LRM-650): last N rows, label-only (no reply body).
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
    expect(screen.getAllByText("Output")).toHaveLength(5);
    expect(screen.queryByText("Reply 6")).toBeNull();
    expect(screen.queryByText("Reply 0")).toBeNull();
  });

  it("renders a shell command as a full-width hanging block (not path-mangled) (#v0/#404)", () => {
    const full = 'cd /a/b && multica send --target "#c"';
    const CMD: ActivityEvent = {
      id: "cmd1",
      agent_id: "agent-1",
      occurred_at: "2026-07-06T09:36:05Z",
      activity_kind: "tool_call",
      detail_kind: "tool_use",
      tool: "bash",
      tool_target: "cd /a/b && multica send…",
      status: "completed",
      entries: [{ kind: "tool_call", tool: "bash", command: full }],
      target_ref: { kind: "agent", id: "agent-1" },
    };
    // An actively-running non-command tool — its dot must NOT pulse either (#404).
    const READ_RUNNING: ActivityEvent = {
      id: "rd1",
      agent_id: "agent-1",
      occurred_at: "2026-07-06T09:36:06Z",
      activity_kind: "tool_call",
      detail_kind: "tool_use",
      tool: "read_file",
      tool_target: "app.ts",
      status: "running",
      target_ref: { kind: "agent", id: "agent-1" },
    };
    const { container } = render(<ActivityTimeline events={[CMD, READ_RUNNING]} />);
    expect(screen.getByText("Running command")).toBeInTheDocument();
    // Command is one plain node (never path head/tail); full entries[].command.
    expect(screen.getByText(full)).toBeInTheDocument();
    // Collapsed: muted command surface under the header, clamp-2 + hover Copy.
    const toggle = screen.getByRole("button", { expanded: false });
    expect(toggle).toHaveTextContent("Running command");
    expect(toggle).not.toHaveTextContent(full);
    const cmdBlock = screen.getByTestId("activity-command-block");
    expect(cmdBlock).toHaveClass("bg-muted", "rounded-md");
    expect(cmdBlock.querySelector(".line-clamp-2")).not.toBeNull();
    expect(screen.getByRole("button", { name: "Copy" })).toBeInTheDocument();
    // No dot pulses — all static (#404).
    expect(container.querySelector(".animate-pulse")).toBeNull();
  });

  it("expands a command into a bounded sibling code detail with a usable Copy control", async () => {
    const full =
      'multica message send --message \'能看到，截图里是我的状态显示"Idle"\' --output text';
    const CMD: ActivityEvent = {
      id: "cmd-expand",
      agent_id: "agent-1",
      occurred_at: "2026-07-06T09:36:05Z",
      activity_kind: "tool_call",
      detail_kind: "tool_use",
      tool: "bash",
      status: "completed",
      entries: [{ kind: "tool_call", tool: "bash", command: full }],
      target_ref: { kind: "agent", id: "agent-1" },
    };
    render(<ActivityTimeline events={[CMD]} />);
    const toggle = screen.getByRole("button", { expanded: false });
    expect(screen.getByTestId("activity-command-block").querySelector(".line-clamp-2")).not.toBeNull();
    fireEvent.click(toggle);

    const open = screen.getByRole("button", { expanded: true });
    const detail = screen.getByTestId("activity-expanded-detail");
    expect(open.contains(detail)).toBe(false);
    // Tier2 command details share the same bounded vertical-detail baseline,
    // while their pre keeps its independent horizontal handling.
    expect(detail).toHaveClass("max-h-[min(260px,55vh)]", "md:max-h-[360px]");
    expect(detail.querySelector("pre")).toHaveClass("overflow-x-auto");
    expect(detail.querySelector("pre")).not.toHaveClass("line-clamp-2");
    expect(detail.querySelector("pre code")).toHaveTextContent(full);
    const copy = screen.getByRole("button", { name: "Copy" });

    Object.defineProperties(detail, {
      clientHeight: { configurable: true, value: 260 },
      scrollHeight: { configurable: true, value: 520 },
      scrollTop: { configurable: true, value: 0, writable: true },
    });
    fireEvent(window, new Event("resize"));

    // Tier2 command source takes the same real-overflow path as Tier1 Markdown.
    expect(detail).toHaveClass("overflow-y-auto", "overscroll-contain");
    expect(detail).toHaveAttribute("role", "region");
    expect(detail).toHaveAttribute("tabindex", "0");
    expect(screen.getByTestId("activity-detail-scroll-fade")).toBeInTheDocument();

    fireEvent.click(copy);
    await waitFor(() => expect(copyText).toHaveBeenCalledWith(full));
    expect(screen.getByRole("button", { name: "Copied" })).toBeInTheDocument();

    detail.scrollTop = 260;
    fireEvent.scroll(detail);
    expect(screen.queryByTestId("activity-detail-scroll-fade")).toBeNull();

    // Collapse again — clamp + hover Copy return on the command surface.
    fireEvent.click(open);
    expect(screen.getByTestId("activity-command-block").querySelector(".line-clamp-2")).not.toBeNull();
    expect(screen.getByRole("button", { name: /Copy|Copied/ })).toBeInTheDocument();
  });

  it("compact mode drops command detail — state type only, non-interactive (LRM-650)", () => {
    const CMD: ActivityEvent = {
      id: "cmd2",
      agent_id: "agent-1",
      occurred_at: "2026-07-06T09:36:05Z",
      activity_kind: "tool_call",
      detail_kind: "tool_use",
      tool: "bash",
      tool_target: "ls /a/b",
      status: "completed",
      entries: [{ kind: "tool_call", tool: "bash", command: "ls /a/b" }],
      target_ref: { kind: "agent", id: "agent-1" },
    };
    render(<ActivityTimeline events={[CMD]} compact />);
    expect(screen.getByText("Running command")).toBeInTheDocument();
    expect(screen.queryByText("ls /a/b")).toBeNull();
    expect(screen.queryByRole("button")).toBeNull();
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

describe("formatActivityRelativeTime", () => {
  it("formats compact English relative times", () => {
    const now = Date.parse("2026-07-06T10:00:00Z");
    expect(formatActivityRelativeTime("2026-07-06T09:59:30Z", now)).toBe("just now");
    expect(formatActivityRelativeTime("2026-07-06T09:55:00Z", now)).toBe("5m ago");
    expect(formatActivityRelativeTime("2026-07-06T08:00:00Z", now)).toBe("2h ago");
    expect(formatActivityRelativeTime("2026-07-04T10:00:00Z", now)).toBe("2d ago");
  });
});

// task #13 (Frank, 2026-08-01): a resolved failure from 55 minutes ago read as
// "broken right now" — no time context, full-strength red forever. These lock
// in the fix: the row decays (dims) once it's old or superseded, and the
// visible time is relative with the exact clock time preserved on hover.
describe("ActivityTimeline failure row decay (task #13)", () => {
  it("shows a fresh failure at full strength", () => {
    render(<ActivityTimeline events={[errorEvent()]} />);
    const label = screen.getByText("Failed");
    expect(label.className).toContain("font-semibold");
    expect(label.className).toContain("text-foreground");
  });

  it("dims a failure once it's older than the decay threshold", () => {
    const old = errorEvent({
      occurred_at: new Date(Date.now() - 20 * 60_000).toISOString(),
    });
    render(<ActivityTimeline events={[old]} />);
    const label = screen.getByText("Failed");
    expect(label.className).toContain("font-medium");
    expect(label.className).toContain("text-muted-foreground");
    expect(label.className).not.toContain("font-semibold");
  });

  it("dims a failure once a later event exists, even if it's recent", () => {
    const recentError = errorEvent({
      id: "err-recent",
      occurred_at: new Date(Date.now() - 60_000).toISOString(),
    });
    const laterText: ActivityEvent = {
      ...TEXT,
      id: "later-text",
      occurred_at: new Date().toISOString(),
    };
    render(<ActivityTimeline events={[recentError, laterText]} />);
    const label = screen.getByText("Failed");
    expect(label.className).toContain("font-medium");
    expect(label.className).toContain("text-muted-foreground");
  });

  it("shows the exact clock time on hover via title, and a relative time as the visible text", () => {
    const old = errorEvent({ occurred_at: "2026-07-06T09:36:48Z" });
    render(<ActivityTimeline events={[old]} />);
    const row = screen.getByTestId("activity-row");
    const timeEl = row.querySelector("[title]");
    expect(timeEl?.getAttribute("title")).toContain("09:36:48");
    // Visible text is relative, not the raw clock time.
    expect(timeEl?.textContent).not.toContain("09:36:48");
  });
});
