"use client";

/**
 * LRM-1472 / UI-04 — dispute status label helper (pure, no JSX).
 * Lives in a non-component module so `panels.tsx` / `detail-sections.tsx`
 * can stay Fast-Refresh-friendly (react-doctor only-export-components).
 */

import { useT } from "../../i18n/use-t";

/** Known dispute-domain statuses mapped to the `dispute.status.*` label space. */
const DISPUTE_STATUS_KEYS = [
  "open",
  "investigating",
  "deadlocked",
  "escalated",
  "resolved",
  "conditionally_resolved",
  "irreducible",
  "reopened",
  "cancelled",
  "converged",
  "unresolved",
  "pending",
  "discussing",
] as const;

export type DisputeStatusKey = (typeof DISPUTE_STATUS_KEYS)[number];

export function disputeStatusLabel(
  status: string,
  t: ReturnType<typeof useT<"research">>["t"],
): string {
  const key = (status || "").toLowerCase().trim() as DisputeStatusKey;
  if ((DISPUTE_STATUS_KEYS as readonly string[]).includes(key)) {
    return t(($) => $.dispute.status[key]);
  }
  return status || "—";
}
