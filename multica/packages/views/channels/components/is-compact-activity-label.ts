import { ACTIVITY_LABEL_EN } from "../../agents/components/tabs/activity-event";

/**
 * LRM-650 / LRM-647 — Compact Activity allows concrete EN state types only.
 * Never "Working" / "Idle" (presence stays on the avatar dot).
 */
export function isCompactActivityLabel(label: string | null | undefined): boolean {
  if (!label) return false;
  const base = label.replace(/…$/u, "").trim();
  return base !== ACTIVITY_LABEL_EN.working && base !== ACTIVITY_LABEL_EN.idle;
}
