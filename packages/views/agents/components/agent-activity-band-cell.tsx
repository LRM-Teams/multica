import { Clock, Loader2 } from "lucide-react";
import type { AgentPresenceDetail } from "@multica/core/agents";
import {
  presentAgentActivityBand,
  resolveAgentActivityBand,
} from "../resolve-agent-live-status";

// task #7 (2026-07-31): was WorkloadCell / workloadConfig (Working/Queued/
// Idle) — replaced with the coarse Activity band (Idle/Working/Disconnected)
// shared with the rest of the product (resolveAgentActivityBand). The
// numeric running/queued counts are kept — they're real detail, not the
// vocabulary problem Frank flagged. `showDisconnected: false`: the adjacent
// Status column already owns connectivity, so a disconnected agent here
// renders "—", never the word "Disconnected" (would restate Status's "—").
//
// Split into its own file (react-doctor: only-export-components /
// no-multi-comp — agent-columns.tsx already held several inline cell
// components before this one).
export function AgentActivityBandCell({
  presence,
}: {
  presence: AgentPresenceDetail | null | undefined;
}) {
  if (!presence) {
    return (
      <span className="inline-flex h-3 w-20 animate-pulse rounded bg-muted/60" />
    );
  }
  const band = resolveAgentActivityBand(presence);
  if (!band) return <span className="text-xs text-muted-foreground">—</span>;
  const view = presentAgentActivityBand(band, false);
  const isWorking = band === "working";
  const isQueued = presence.workload === "queued";
  // Working: show running/capacity, optionally with +Nq when overflow.
  // Queued (folded into the "working" label above, but the counts still
  // distinguish it) — nothing running, things waiting: show the queued
  // count directly so the user sees "Working · 2 queued" instead of
  // misleading "Working 0/3 +2q". Idle/disconnected: no counts.
  const counts =
    isWorking && !isQueued
      ? presence.queuedCount > 0
        ? `${presence.runningCount}/${presence.capacity} +${presence.queuedCount}q`
        : `${presence.runningCount}/${presence.capacity}`
      : isQueued
        ? `${presence.queuedCount} queued`
        : null;
  return (
    <span className="inline-flex items-center gap-1 text-xs">
      {/* Genuinely running keeps the spinning loader; purely-queued (folded
          into the same "Working" label) keeps the static clock — nothing is
          actually in motion yet, a spinner there would misrepresent it. */}
      {isWorking && !isQueued && <Loader2 className="h-3 w-3 shrink-0 animate-spin text-running" />}
      {isWorking && isQueued && <Clock className="h-3 w-3 shrink-0 text-muted-foreground" />}
      <span className="shrink-0 text-foreground">{view.label}</span>
      {counts && (
        <span className="truncate text-muted-foreground">{counts}</span>
      )}
    </span>
  );
}
