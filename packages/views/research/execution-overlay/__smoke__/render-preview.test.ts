// @vitest-environment node
import { describe, expect, it, vi } from "vitest";
import { renderToString } from "react-dom/server";
import { createElement } from "react";
import * as fs from "node:fs";
import { ExecutionOverlayPanel } from "../execution-overlay-panel";
import type { ExecutionRow } from "../execution-adapter";

// Display copy mirrored from `en/research.json` `panel.execution` (the SSR
// renderer never touches i18n providers; it uses the same dict path the panel
// reads through `useT` so the string set matches production).
vi.mock("../../../i18n/use-t", () => ({
  useT: () => ({
    t: (fn: (dict: Record<string, unknown>) => unknown, values?: { location?: string; name?: string; count?: number; time?: string; anomaly?: number; running?: number; queued?: number; total?: number }) =>
      String(fn({
        panel: { execution: {
          title: "Execution activity",
          locatable: "Locate at {{location}}",
          locate: "Locate at {{location}}",
          unavailable: "No locatable node yet",
          load_failed: "Could not load execution status",
          retry: "Retry",
          empty: "No execution members",
          active_count: "{{count}} active",
          no_active: "No agents working",
          recent_result: "Last accepted output",
          started: "Started",
          updated: "Updated",
          duration: "Elapsed",
          stage: "Stage",
          wait_reason: "Waiting",
          stale_reason: "Stale",
          task: "Task",
          attempt: "Attempt",
          waiting_reason: "Enqueued / waiting for a slot; no running signal yet.",
          offline_reason: "No live presence; treated as not at post.",
          unknown_reason: "State cannot be resolved from the projection.",
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
          task_objective: "Task",
          collapse_counts: "{{anomaly}} anomaly · {{running}} running · {{queued}} queued · {{total}} agents",
          collapsed_hint: "Expand the count bar to browse execution",
          status: { queued: "Queued", waiting: "Waiting", running: "Running", cancelling: "Cancelling", done: "Done", failed: "Failed", retrying: "Retrying", stale: "Stale", idle: "Idle", offline: "Offline", unknown: "Unknown" },
          action: { waiting: "Waiting for the current task to start", working: "Working on the current task", cancelling: "Cancellation requested", recent_done: "Recent task completed", recent_failed: "Recent task failed", retrying: "Retrying the current task", stale: "Execution state is stale", idle: "No small task available", offline: "No live signal; not at post", unknown: "Execution state unknown" },
        } },
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

const ROWS: ExecutionRow[] = [
  { id: "r", name: "Ralph", role: "scout", initials: "RA", status: "running", actionKey: "working", action: "Pulling ANN benchmark results", startedAt: T0 - 90_000, elapsedMs: 90_000, updatedAt: T0 - 5_000, recentResult: { id: "c1", title: "Enterprise pricing caps under renewal", acceptedAt: T0 - 40_000 }, currentNodeId: "n1", locationLabel: "Pricing branch" },
  { id: "rt", name: "Rita", role: "domain", initials: "RI", status: "retrying", actionKey: "retrying", action: "Retrying evidence retrieval after timeout", startedAt: T0 - 30_000, elapsedMs: 30_000, updatedAt: T0 - 3_000, currentNodeId: "n2", locationLabel: "Compliance branch" },
  { id: "w", name: "Wanda", role: "lead", initials: "WA", status: "queued", actionKey: "waiting", action: "Waiting for a retrieval slot", updatedAt: T0 - 20_000, stage: "exploration", currentNodeId: "n3", locationLabel: "Discovery branch" },
  { id: "d", name: "Dana", role: "reporter", initials: "DA", status: "done", actionKey: "recent_done", action: "Consolidated 14 user interviews", updatedAt: T0 - 400_000, recentResult: { id: "c2", title: "Migration friction themes", acceptedAt: T0 - 400_000 }, currentNodeId: "n4", locationLabel: "Insight branch" },
  { id: "f", name: "Felix", role: "analyst", initials: "FE", status: "failed", actionKey: "recent_failed", action: "Computing retention confidence interval", updatedAt: T0 - 200_000, currentNodeId: "n5", locationLabel: "Analysis node 7" },
  { id: "s", name: "Sam", role: "web", initials: "SA", status: "stale", actionKey: "stale", action: "Reviewing compliance documentation", updatedAt: T0 - 3_600_000, currentNodeId: "n6", locationLabel: "Compliance branch" },
  { id: "o", name: "Omar", role: "reviewer", initials: "OM", status: "offline", actionKey: "offline", action: "", updatedAt: T0 - 9_000_000 },
  { id: "u", name: "Uma", role: "advisor", initials: "UM", status: "unknown", actionKey: "unknown", action: "", updatedAt: T0 - 1_000 },
];

describe("execution-overlay SSR preview renderer (node)", () => {
  it("renders the panel to static HTML (desktop overlay + narrow sidebar) and writes an artifact", () => {
    const desktop = renderToString(
      createElement(ExecutionOverlayPanel, {
        rows: ROWS,
        sync: { disconnected: false, lastSyncedAt: T0 },
      }),
    );
    const narrow = renderToString(
      createElement(ExecutionOverlayPanel, {
        rows: ROWS,
        sync: { disconnected: true, lastSyncedAt: T0 },
        highlightAgentId: "s",
        onLocate: () => {},
      }),
    );

    expect(desktop).toContain("execution-overlay-panel");
    expect(desktop).toContain("8"); // all eight status rows
    expect(desktop).toContain("Running");
    expect(desktop).toContain("Retrying");
    expect(desktop).toContain("Offline");
    expect(desktop).toContain("Unknown");
    expect(desktop).toContain("Last accepted output");
    expect(desktop).toContain("Enterprise pricing caps under renewal");
    // Narrow sidebar carries the disconnected sync banner + highlight.
    expect(narrow).toContain("disconnected");

    if (process.env.RENDER_OVERLAY_PREVIEW === "1") {
      fs.mkdirSync("artifacts", { recursive: true });
      fs.writeFileSync(
        "artifacts/execution-overlay-preview.html",
        fullPage(desktop, narrow),
      );
    }
  });
});

function fullPage(desktop: string, narrow: string): string {
  return `<!doctype html>
<html><head><meta charset="utf-8"/><title>Execution overlay preview</title>
<style>
  :root { color-scheme: light; }
  body { margin: 0; background: #f5f5f7; font-family: system-ui, sans-serif; padding: 16px; box-sizing: border-box; }
  .row { display: flex; gap: 40px; align-items: flex-start; flex-wrap: wrap; }
  .stage { position: relative; border: 1px dashed #b8b8c4; border-radius: 12px; padding: 16px; background: #ebeaf0; }
  .stage-title { font-size: 11px; font-weight: 600; color: #6b6b76; margin: 0 0 8px; text-transform: uppercase; letter-spacing: .05em; }
  .overlay { width: 22rem; }
  .sidebar { width: min(22rem, 100%); }
  /* Minimal token mapping so the SSR markup is legible in the capture. */
  .bg-card { background:#fff; } .border-border { border-color:#d8d7e0; }
  .text-foreground { color:#1c1b22; } .text-muted-foreground { color:#6b6b76; }
  .text-brand { color:#2f6fe4; } .text-success-strong { color:#18794e; }
  .text-warning { color:#ad5700; } .text-destructive-strong { color:#cd2b31; }
</style></head>
<body>
  <div class="row">
    <section><h2 class="stage-title">Desktop floating overlay (right-top, 22rem)</h2><div class="stage overlay">${desktop}</div></section>
    <section><h2 class="stage-title">Narrow sidebar panel (full width)</h2><div class="stage sidebar">${narrow}</div></section>
  </div>
</body></html>`;
}
