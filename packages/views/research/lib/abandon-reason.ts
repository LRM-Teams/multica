/**
 * LRM-1333 / LRM-1315 — abandoned surface + abandon reason projection.
 *
 * Gate: only `status === "abandoned"` (exact). Reason reads the BE-projected
 * top-level field, then payload `abandon_reason` / `deprecate_reason`.
 * Never fall back to `reason`, `dead_end_reason`, assessment, or edge color.
 */

import type { ResearchGraphNode } from "@multica/core/types";

export function isAbandonedStatus(status: string | null | undefined): boolean {
  return (status || "").toLowerCase().trim() === "abandoned";
}

function trimReason(value: unknown): string | null {
  if (typeof value !== "string") return null;
  const trimmed = value.trim();
  return trimmed ? trimmed : null;
}

/** Formal abandon reason text, or null when missing/empty/illegal. */
export function readAbandonReason(node: ResearchGraphNode): string | null {
  const top = trimReason(node.abandon_reason);
  if (top) return top;

  const payload = node.payload;
  if (!payload || typeof payload !== "object" || Array.isArray(payload)) {
    return null;
  }
  const record = payload as Record<string, unknown>;
  return (
    trimReason(record.abandon_reason) ??
    trimReason(record.deprecate_reason) ??
    null
  );
}
