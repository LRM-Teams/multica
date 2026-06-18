// PLACEHOLDER DATA — no backing API yet.
// Spend is now real (today's cost from the usage dashboard). What remains mock:
// the "vs yesterday" trend tags on the agent / success-rate cards (no
// day-over-day series for those) and the approval longest-wait metric. The
// widgets consume this module's shape and need no UI changes when a real source
// appears. See docs/superpowers/specs/2026-06-17-overview-design.md.

export interface KpiTrend {
  delta: string;
  dir: "up" | "down";
}

// Only the cards that show a trend tag in the design carry one here.
export const MOCK_TRENDS: Partial<Record<string, KpiTrend>> = {
  active_agents: { delta: "+8", dir: "up" },
  success_rate: { delta: "-4%", dir: "down" },
};

export const MOCK_LONGEST_WAIT = "42min";
