// PLACEHOLDER DATA — no backing API yet.
// Real now: spend (today's cost from the usage dashboard) and the pending-
// approval count + longest-wait (issues review-stats endpoint). What remains
// mock: the "vs yesterday" trend tags on the agent / success-rate cards (no
// day-over-day series for those). See
// docs/superpowers/specs/2026-06-17-overview-design.md.

export interface KpiTrend {
  delta: string;
  dir: "up" | "down";
}

// Only the cards that show a trend tag in the design carry one here.
export const MOCK_TRENDS: Partial<Record<string, KpiTrend>> = {
  active_agents: { delta: "+8", dir: "up" },
  success_rate: { delta: "-4%", dir: "down" },
};
