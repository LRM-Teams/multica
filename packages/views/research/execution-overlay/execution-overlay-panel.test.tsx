import { fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { ExecutionOverlayPanel } from "./execution-overlay-panel";
import { ExecutionOverlaySyncIndicator } from "./execution-overlay-sync-indicator";
import type { ExecutionRow } from "./execution-adapter";

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (fn: (dict: Record<string, unknown>) => unknown, values?: { location?: string; name?: string; count?: number; time?: string; anomaly?: number; running?: number; queued?: number; total?: number }) =>
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
            task_objective: "Task",
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
            collapse_counts: "{{anomaly}} anomaly · {{running}} running · {{queued}} queued · {{total}} agents",
            collapsed_hint: "Expand the count bar to browse execution",
            status: { queued: "Queued", running: "Running", cancelling: "Cancelling", done: "Done", failed: "Failed", retrying: "Retrying", stale: "Stale", idle: "Idle", offline: "Offline", unknown: "Unknown" },
            action: { waiting: "Waiting for the current task to start", working: "Running the current task", cancelling: "Cancellation requested", recent_done: "Recent task completed", recent_failed: "Recent task failed", retrying: "Retrying the current task", stale: "Execution state is stale", idle: "No small task available", offline: "No live signal; not at post", unknown: "Execution state unknown" },
          },
        },
      }))
        .replace("{{location}}", values?.location ?? "")
        .replace("{{name}}", values?.name ?? "")
        .replace("{{count}}", String(values?.count ?? ""))
        .replace("{{time}}", values?.time ?? "")
        .replace("{{anomaly}}", String(values?.anomaly ?? ""))
        .replace("{{running}}", String(values?.running ?? ""))
        .replace("{{queued}}", String(values?.queued ?? ""))
        .replace("{{total}}", String(values?.total ?? "")),
  }),
}));

const T0 = 1_700_000_000_000;

function row(overrides: Partial<ExecutionRow> & { id: string; name: string }): ExecutionRow {
  return {
    role: "worker",
    initials: "AG",
    status: "queued",
    actionKey: "waiting",
    updatedAt: T0,
    ...overrides,
  } as ExecutionRow;
}

const tenStateRows: ExecutionRow[] = [
  row({ id: "q", name: "Quinn", status: "queued" }),
  row({ id: "r", name: "Ralph", status: "running", startedAt: T0 - 90_000, elapsedMs: 90_000, updatedAt: T0 - 5_000 }),
  row({ id: "cc", name: "Cecilia", status: "cancelling" }),
  row({ id: "d", name: "Dana", status: "done" }),
  row({ id: "f", name: "Felix", status: "failed", startedAt: T0 - 300_000, updatedAt: T0 - 200_000 }),
  row({ id: "rt", name: "Rita", status: "retrying", startedAt: T0 - 30_000, elapsedMs: 30_000 }),
  row({ id: "s", name: "Sam", status: "stale" }),
  row({ id: "i", name: "Iris", status: "idle" }),
  row({ id: "o", name: "Omar", status: "offline" }),
  row({ id: "u", name: "Uma", status: "unknown" }),
];

describe("ExecutionOverlayPanel — state matrix + time + result", () => {
  it("renders distinct, distinguishable statuses (icon + label + row data-status)", () => {
    render(<ExecutionOverlayPanel rows={tenStateRows} />);
    const rows = screen.getAllByTestId("execution-overlay-row");
    expect(rows).toHaveLength(10);
    const statuses = new Set(rows.map((r) => r.getAttribute("data-status")));
    expect(statuses).toEqual(new Set([
      "queued", "running", "cancelling", "done", "failed", "retrying", "stale", "idle", "offline", "unknown",
    ]));
    // Each badge label is visible (icon+color+text three-channel).
    for (const label of ["Queued", "Running", "Cancelling", "Done", "Failed", "Retrying", "Stale", "Idle", "Offline", "Unknown"]) {
      expect(screen.getAllByText(label).length).toBeGreaterThan(0);
    }
  });

  it("shows start time, elapsed duration and last update with tabular-nums", () => {
    render(<ExecutionOverlayPanel rows={tenStateRows} />);
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

  it("shows the task objective line on a running row", () => {
    const rows: ExecutionRow[] = [
      row({ id: "r", name: "Ralph", status: "running", taskObjective: "Verify supplier regional terms" }),
    ];
    render(<ExecutionOverlayPanel rows={rows} />);
    expect(withinRow("r").textContent).toContain("Verify supplier regional terms");
  });

  it("shows deck counts in the collapsed header", () => {
    render(<ExecutionOverlayPanel rows={tenStateRows} />);
    // anomaly = failed(1) + cancelling(1) + stale(1) = 3; 1 running, 1 queued, 10 total
    expect(screen.getByText(/3 anomaly · 1 running · 1 queued · 10 agents/)).toBeTruthy();
    fireEvent.click(screen.getByTestId("execution-overlay-collapse-toggle"));
    expect(screen.queryAllByTestId("execution-overlay-row")).toHaveLength(0);
  });
});

describe("ExecutionOverlayPanel — bidirectional locate + a11y + motion", () => {
  it("highlights an agent row when its node is selected (node → agent)", () => {
    render(<ExecutionOverlayPanel rows={tenStateRows} highlightAgentId="s" />);
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
    const rows = tenStateRows.map((r) => ({ ...r, currentNodeId: `node-${r.id}` }));
    render(<ExecutionOverlayPanel rows={rows} onLocate={() => {}} />);
    const running = screen.getByRole("button", { name: "Locate Ralph's current node" });
    running.focus();
    // Deck order: cancelling (Cecilia) before running (Ralph) before queued (Quinn)…
    await user.keyboard("{ArrowDown}");
    const next = screen.getByRole("button", { name: "Locate Quinn's current node" });
    expect(document.activeElement).toBe(next);
  });

  it("propagates locate through the button", () => {
    const onLocate = vi.fn();
    const rows = [{ ...tenStateRows[0]!, currentNodeId: "node-q" }];
    render(<ExecutionOverlayPanel rows={rows} onLocate={onLocate} />);
    fireEvent.click(screen.getByRole("button", { name: "Locate Quinn's current node" }));
    expect(onLocate).toHaveBeenCalledWith(expect.objectContaining({ id: "q" }));
  });

  it("lets a locatable failed row also expand to read the failure detail (LRM-1437)", () => {
    const onLocate = vi.fn();
    const failed = { ...tenStateRows[4]!, currentNodeId: "node-f", locationLabel: "node-f" };
    const { unmount } = render(<ExecutionOverlayPanel rows={[failed]} onLocate={onLocate} />);
    const rowButton = screen.getByRole("button", { name: "Locate Felix's current node" });

    // Before activation the detail is collapsed.
    expect(rowButton.getAttribute("aria-expanded")).toBe("false");

    // Clicking a locatable failed row locates once AND expands the detail.
    fireEvent.click(rowButton);
    expect(onLocate).toHaveBeenCalledTimes(1);
    expect(onLocate).toHaveBeenCalledWith(expect.objectContaining({ id: "f" }));
    expect(rowButton.getAttribute("aria-expanded")).toBe("true");
    // Failure reason is now readable.
    expect(screen.getByText("The task did not finish; review recent activity and retry.")).toBeTruthy();

    // Click again still locates and collapses (toggle).
    fireEvent.click(rowButton);
    expect(onLocate).toHaveBeenCalledTimes(2);
    expect(rowButton.getAttribute("aria-expanded")).toBe("false");
    unmount();
  });

  it("keeps reduced-motion guard on the running progress sweep", () => {
    render(<ExecutionOverlayPanel rows={tenStateRows} />);
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
    q: "Quinn", r: "Ralph", cc: "Cecilia", d: "Dana", f: "Felix",
    rt: "Rita", s: "Sam", i: "Iris", o: "Omar", u: "Uma",
  };
  return map[id] ?? id;
}
