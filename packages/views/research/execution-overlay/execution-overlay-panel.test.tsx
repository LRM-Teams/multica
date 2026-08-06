import { fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { ExecutionOverlayPanel } from "./execution-overlay-panel";
import { ExecutionOverlaySyncIndicator } from "./execution-overlay-sync-indicator";
import type { ExecutionRow } from "./execution-adapter";

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

const T0 = 1_700_000_000_000;

function row(overrides: Partial<ExecutionRow> & { id: string; name: string }): ExecutionRow {
  return {
    role: "worker",
    initials: "AG",
    status: "waiting",
    actionKey: "waiting",
    updatedAt: T0,
    ...overrides,
  } as ExecutionRow;
}

const eightStateRows: ExecutionRow[] = [
  row({ id: "w", name: "Wanda", status: "waiting" }),
  row({ id: "r", name: "Ralph", status: "running", startedAt: T0 - 90_000, elapsedMs: 90_000, updatedAt: T0 - 5_000 }),
  row({ id: "d", name: "Dana", status: "done" }),
  row({ id: "f", name: "Felix", status: "failed", startedAt: T0 - 300_000, updatedAt: T0 - 200_000 }),
  row({ id: "rt", name: "Rita", status: "retrying", startedAt: T0 - 30_000, elapsedMs: 30_000 }),
  row({ id: "s", name: "Sam", status: "stale" }),
  row({ id: "o", name: "Omar", status: "offline" }),
  row({ id: "u", name: "Uma", status: "unknown" }),
];

describe("ExecutionOverlayPanel — 8-state + time + result", () => {
  it("renders eight distinct, distinguishable statuses (icon + label + row data-status)", () => {
    render(<ExecutionOverlayPanel rows={eightStateRows} />);
    const rows = screen.getAllByTestId("execution-overlay-row");
    expect(rows).toHaveLength(8);
    const statuses = new Set(rows.map((r) => r.getAttribute("data-status")));
    expect(statuses).toEqual(new Set([
      "waiting", "running", "done", "failed", "retrying", "stale", "offline", "unknown",
    ]));
    // Each badge label is visible (icon+color+text three-channel).
    for (const label of ["Running", "Waiting", "Done", "Failed", "Retrying", "Stale", "Offline", "Unknown"]) {
      expect(screen.getAllByText(label).length).toBeGreaterThan(0);
    }
  });

  it("shows start time, elapsed duration and last update with tabular-nums", () => {
    render(<ExecutionOverlayPanel rows={eightStateRows} />);
    // Running row shows Started + Elapsed + Updated.
    const runningRow = withinRow("r");
    expect(runningRow.textContent).toContain("Started");
    expect(runningRow.textContent).toContain("Elapsed");
    expect(runningRow.textContent).toContain("Updated");
  });

  it("shows the most recent accepted output on a running row", () => {
    const withResult: ExecutionRow[] = [
      row({
        id: "r",
        name: "Ralph",
        status: "running",
        recentResult: { id: "c1", title: "Enterprise pricing caps", acceptedAt: T0 - 40_000 },
      }),
    ];
    render(<ExecutionOverlayPanel rows={withResult} />);
    expect(withinRow("r").textContent).toContain("Last accepted output");
    expect(withinRow("r").textContent).toContain("Enterprise pricing caps");
  });
});

describe("ExecutionOverlayPanel — bidirectional locate + a11y + motion", () => {
  it("highlights an agent row when its node is selected (node → agent)", () => {
    render(<ExecutionOverlayPanel rows={eightStateRows} highlightAgentId="s" />);
    const staleRow = Array.from(screen.getAllByTestId("execution-overlay-row")).find(
      (el) => el.getAttribute("data-status") === "stale",
    );
    expect(staleRow?.getAttribute("data-highlighted")).toBe("true");
    // Other rows are not highlighted.
    const running = Array.from(screen.getAllByTestId("execution-overlay-row")).find(
      (el) => el.getAttribute("data-status") === "running",
    );
    expect(running?.getAttribute("data-highlighted")).toBe("false");
  });

  it("locates agent → node and supports arrow-key navigation", async () => {
    const user = userEvent.setup();
    const rows = eightStateRows.map((r) => ({ ...r, currentNodeId: `node-${r.id}` }));
    render(<ExecutionOverlayPanel rows={rows} onLocate={() => {}} />);
    const running = screen.getByRole("button", { name: "Locate Ralph's current node" });
    running.focus();
    // Active group order: running (Ralph) then retrying (Rita); ArrowDown moves there.
    await user.keyboard("{ArrowDown}");
    const retrying = screen.getByRole("button", { name: "Locate Rita's current node" });
    expect(document.activeElement).toBe(retrying);
  });

  it("propagates locate through the button", () => {
    const onLocate = vi.fn();
    const rows = [{ ...eightStateRows[0]!, currentNodeId: "node-w" }];
    render(<ExecutionOverlayPanel rows={rows} onLocate={onLocate} />);
    fireEvent.click(screen.getByRole("button", { name: "Locate Wanda's current node" }));
    expect(onLocate).toHaveBeenCalledWith(expect.objectContaining({ id: "w" }));
  });

  it("keeps reduced-motion guard on the running progress sweep", () => {
    render(<ExecutionOverlayPanel rows={eightStateRows} />);
    const running = Array.from(screen.getAllByTestId("execution-overlay-row")).find(
      (el) => el.getAttribute("data-status") === "running",
    );
    expect(running?.querySelector(".animate-nav-progress-sweep")?.className).toContain("motion-reduce:hidden");
  });
});

describe("ExecutionOverlaySyncIndicator — disconnected / expired / ok", () => {
  it("renders an ok state when live", () => {
    const { container } = render(<ExecutionOverlaySyncIndicator />);
    expect(container.querySelector('[data-state="ok"]')).toBeTruthy();
  });

  it("renders a disconnected alert with retry", () => {
    const retry = vi.fn();
    const { container } = render(<ExecutionOverlaySyncIndicator disconnected onRetry={retry} />);
    expect(container.querySelector('[data-state="disconnected"]')).toBeTruthy();
    expect(retry).not.toHaveBeenCalled();
  });

  it("renders the data-expired state distinctly (offline ≠ expired)", () => {
    const { container } = render(<ExecutionOverlaySyncIndicator expired />);
    expect(container.querySelector('[data-state="expired"]')).toBeTruthy();
    expect(container.querySelector('[data-state="disconnected"]')).toBeNull();
  });
});

function withinRow(id: string): HTMLElement {
  const rows = screen.getAllByTestId("execution-overlay-row");
  const target = rows.find((r) => {
    // The row article wraps a button whose aria-label contains the agent name.
    const label = r.querySelector("button")?.getAttribute("aria-label") ?? "";
    return label.includes(idToName(id));
  });
  if (!target) throw new Error(`row ${id} not found`);
  return within(target).getByRole("button");
}

function idToName(id: string): string {
  const map: Record<string, string> = {
    w: "Wanda", r: "Ralph", d: "Dana", f: "Felix", rt: "Rita", s: "Sam", o: "Omar", u: "Uma",
  };
  return map[id] ?? id;
}
