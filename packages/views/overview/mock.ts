// PLACEHOLDER DATA — no backing API yet.
// The "总览/Overview" design shows a daily spend figure with a fixed budget,
// "vs yesterday" deltas, and a longest-wait time. Multica has token usage but
// no dollar-cost rollup, no day-over-day comparison, and no approval-wait
// metric, so these stay mock until real endpoints exist. The widgets consume
// this module's shape and need no UI changes when swapped to live data.
// See docs/superpowers/specs/2026-06-17-overview-design.md.

export interface KpiTrend {
  delta: string;
  dir: "up" | "down";
}

// Only the cards that show a trend tag in the design carry one here.
export const MOCK_TRENDS: Partial<Record<string, KpiTrend>> = {
  active_agents: { delta: "+8", dir: "up" },
  success_rate: { delta: "-4%", dir: "down" },
  spend: { delta: "+6.23", dir: "up" },
};

export const MOCK_SPEND = "$ 23.24";
export const MOCK_BUDGET = "$ 200";
export const MOCK_LONGEST_WAIT = "42min";
