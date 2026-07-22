"use client";

import { Skeleton } from "@multica/ui/components/ui/skeleton";
import type { AgentPresenceDetail } from "@multica/core/agents";
import {
  availabilityConfig,
  toLiveAvailability,
} from "../presence";
import { useT } from "../../i18n";

interface PresenceIndicatorProps {
  // null/undefined = still loading. Caller passes the detail computed at
  // the page level (or via the useAgentPresenceDetail hook for single-agent
  // views). Keeping this as a prop avoids per-row hook subscriptions in
  // long lists.
  detail: AgentPresenceDetail | null | undefined;
  // Compact = dot only, no label. Used in dense rows.
  compact?: boolean;
}

/**
 * Renders live Online/Offline presence (LRM-248).
 *
 * Compact mode collapses to dot-only. Full mode is dot + Online/Offline
 * word — never Unstable / Working / Queued / Idle as live labels.
 * Archived returns null (caller grays the avatar separately).
 */
export function AgentPresenceIndicator({
  detail,
  compact,
}: PresenceIndicatorProps) {
  const { t } = useT("agents");
  if (!detail) {
    return compact ? (
      <Skeleton className="h-1.5 w-1.5 rounded-full" />
    ) : (
      <Skeleton className="h-3 w-24 rounded" />
    );
  }

  const live = toLiveAvailability(detail.availability);
  if (!live) return null;

  const av = availabilityConfig[live];
  const availabilityLabel = t(($) => $.availability[live]);

  if (compact) {
    return (
      <span className="inline-flex items-center" title={availabilityLabel}>
        <span className={`h-1.5 w-1.5 shrink-0 rounded-full ${av.dotClass}`} />
      </span>
    );
  }

  return (
    <span className="inline-flex items-center gap-1.5">
      <span className={`h-1.5 w-1.5 shrink-0 rounded-full ${av.dotClass}`} />
      <span className={`text-xs ${av.textClass}`}>{availabilityLabel}</span>
    </span>
  );
}
