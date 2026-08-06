import { RUNNER_ACTIVITY_LABEL_EN } from "../../agents/runner-activity-labels";

/**
 * LRM-650 / LRM-647 — Compact Activity allows concrete EN state types only.
 * Never "Working" / "Idle" (presence stays on the avatar dot).
 */
export function isCompactActivityLabel(label: string | null | undefined): boolean {
  if (!label) return false;
  const base = label.replace(/…$/u, "").trim();
  return base !== RUNNER_ACTIVITY_LABEL_EN.working && base !== RUNNER_ACTIVITY_LABEL_EN.idle;
}
