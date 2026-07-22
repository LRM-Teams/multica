"use client";

import { Skeleton } from "@multica/ui/components/ui/skeleton";
import type { AgentPresenceDetail } from "@multica/core/agents";
import { availabilityConfig, toLivePresence } from "../presence";
import { useT } from "../../i18n";

interface PresenceIndicatorProps {
  detail: AgentPresenceDetail | null | undefined;
  /** Compact = dot only. Used in dense rows. */
  compact?: boolean;
}

/**
 * Live Online/Offline indicator (LRM-248). No Unstable / Working / Queued chips.
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

  const live = toLivePresence(detail.availability);
  if (live === "archived") {
    return compact ? null : (
      <span className="text-xs text-muted-foreground">
        {t(($) => $.availability.archived)}
      </span>
    );
  }

  const av = live === "online" ? availabilityConfig.online : availabilityConfig.offline;
  const availabilityLabel =
    live === "online"
      ? t(($) => $.availability.online)
      : t(($) => $.availability.offline);

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
