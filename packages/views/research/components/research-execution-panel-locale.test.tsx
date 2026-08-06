import { fireEvent, render, screen, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import {
  researchExecutionPanelFixture,
  type ResearchExecutionAgent,
  type ResearchExecutionStatus,
} from "../lib/research-execution-panel-fixture";
import { ResearchExecutionPanel } from "./research-execution-panel";

// Localised render probe for LRM-1434: the Research execution panel must not
// leak Chinese chrome into en/ja/ko. This mirrors the real `en/research.json`
// `panel.execution` bundle (kept in sync by the locale parity test) and renders
// with live-activity-free agents so the view-model's semantic fallbacks run.
vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (fn: (dict: Record<string, unknown>) => unknown, values?: { location?: string; name?: string; count?: number; time?: string }) =>
      String(fn({
        panel: {
          execution: {
            title: "Execution activity",
            locatable: "Locate at {{location}}",
            locate: "Locate at {{location}}",
            unavailable: "No locatable node yet",
            load_failed: "Could not load execution status",
            retry: "Retry",
            empty: "No execution members",
            active_count: "{{count}} agents working",
            no_active: "No agents working",
            locate_aria: "Locate {{name}}'s current node",
            view_aria: "View {{name}}'s recent activity",
            expand_aria: "Expand {{name}}'s detail",
            recent_result: "Last accepted output",
            started: "Started",
            updated: "Updated",
            duration: "Elapsed",
            stage: "Stage",
            wait_reason: "Waiting reason",
            stale_reason: "Stale reason",
            task: "Task",
            attempt: "Attempt",
            waiting_reason: "Enqueued or waiting for a slot; no running signal yet.",
            offline_reason: "No live presence for this member; treat as not at post.",
            unknown_reason: "Cannot resolve an execution state from the projection.",
            failed_reason: "The task did not finish; review recent activity and retry.",
            clock_time: "{{time}}",
            elapsed_sec: "{{count}}s",
            elapsed_min: "{{count}}m",
            elapsed_hour: "{{count}}h",
            disconnected: "Connection lost · keeping last data",
            data_expired: "Data may be stale",
            synced: "Live",
            last_sync: "synced {{time}}",
            group_active: "Active",
            group_waiting: "Waiting",
            group_finished: "Finished",
            group_idle: "Idle",
            status: { waiting: "Waiting", running: "Running", done: "Done", failed: "Failed", retrying: "Retrying", stale: "Stale", offline: "Offline", unknown: "Unknown" },
            action: { waiting: "Waiting for the current task to start", working: "Running the current task", recent_done: "Recent task completed", recent_failed: "Recent task failed", retrying: "Retrying the current task", stale: "Execution state is stale", offline: "No live signal; not at post", unknown: "Execution state unknown" },
          },
        },
      }))
        .replace("{{location}}", values?.location ?? "")
        .replace("{{name}}", values?.name ?? "")
        .replace("{{count}}", String(values?.count ?? ""))
        .replace("{{time}}", values?.time ?? ""),
  }),
}));

// Semantic fallback agents: no live `signal.activity`, so the view-model emits
// `actionKey` / `timeKey` / `failureReasonKey` which the component must translate.
// We strip the fixture's Chinese chrome so the probe can only pass if the
// component-and-locale path produces English (no CJK fallback leakage).
const ACTION_KEY_BY_STATUS: Record<ResearchExecutionStatus, "waiting" | "working" | "recent_done" | "recent_failed" | "stale" | "idle"> = {
  queued: "waiting", running: "working", done: "recent_done", failed: "recent_failed", stale: "stale", idle: "idle",
};

const semanticAgents: ResearchExecutionAgent[] = researchExecutionPanelFixture.map((agent, index) => ({
  ...agent,
  name: `Agent ${index}`,
  role: "worker",
  initials: `A${index}`,
  action: undefined,
  actionKey: ACTION_KEY_BY_STATUS[agent.status],
  actionDetail: undefined,
  locationLabel: agent.locationLabel ? `node-${index}` : undefined,
}));

function hasCJK(text: string): boolean {
  return /[\u3400-\u9FFF\uF900-\uFAFF]/.test(text);
}

function collectVisibleText(container: HTMLElement): string {
  // Gather all non-empty element text plus accessible names from buttons.
  const texts: string[] = [];
  for (const el of Array.from(container.querySelectorAll("button, p, span, h2"))) {
    const own = el.childNodes.length === 0 ? el.textContent ?? "" : "";
    if (own.trim()) texts.push(own);
    const label = el.getAttribute("aria-label");
    if (label) texts.push(label);
    const title = el.getAttribute("title");
    if (title) texts.push(title);
  }
  return texts.join("\n");
}

describe("ResearchExecutionPanel · en locale no-CJK probe", () => {
  it("renders header, badge labels, fallback action/time, and failure reason without CJK", () => {
    const { container } = render(<ResearchExecutionPanel agents={semanticAgents} title="Execution activity" />);
    const text = collectVisibleText(container);

    // Header running counter is localised (no Chinese like “个智能体执行中”).
    expect(screen.getByText("1 agents working")).toBeTruthy();

    for (const row of screen.getAllByTestId("execution-overlay-row")) {
      const status = row.getAttribute("data-status") as ResearchExecutionStatus;
      // Every visible cell of the row (badge label, fallback action, time,
      // location) must be English — no CJK leakage from the view-model.
      expect(row.textContent).not.toMatch(/[\u3400-\u9FFF]/);
      void status;
    }
    expect(hasCJK(text)).toBe(false);
  });

  it("exposes English accessible names for locate and view actions", () => {
    render(<ResearchExecutionPanel agents={semanticAgents} onLocate={() => {}} title="Execution activity" />);
    for (const row of screen.getAllByTestId("execution-overlay-row")) {
      const button = within(row).getByRole("button");
      const label = button.getAttribute("aria-label") ?? "";
      expect(hasCJK(label)).toBe(false);
    }
  });

  it("renders the English failure reason for a failed member without CJK", () => {
    render(<ResearchExecutionPanel agents={semanticAgents} onLocate={() => {}} title="Execution activity" />);
    const failedRow = screen.getAllByTestId("execution-overlay-row").find((r) => r.dataset.status === "failed");
    const toggle = failedRow ? within(failedRow).getByRole("button") : null;
    if (toggle) fireEvent.click(toggle);
    expect(screen.getByText("The task did not finish; review recent activity and retry.")).toBeTruthy();
  });
});
