"use client";

import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { cn } from "@multica/ui/lib/utils";
import { useWorkspaceId } from "@multica/core/hooks";
import { useAgentPresenceDetail } from "@multica/core/agents";
import { useT } from "../../i18n";
import { formatPresenceStatus, presenceStatusVisual } from "../presence";

/**
 * A compact live-presence line (status icon + localized word) for an agent —
 * the SAME token rule + word table + visual as the hover card / live-peek
 * "RECENT ACTIVITY" header (see presence.ts), so a DM/panel header can never
 * drift from the avatar dot or the popover. #371 fast layer: shows presence
 * (Starting up / Online / Idle / …); the realtime activity word
 * (Thinking / Writing…) lands with #302's event stream on the same source.
 *
 * Renders a subtle skeleton while presence is loading/unknown rather than
 * collapsing, so the header height stays stable.
 */
export function AgentPresenceStatusLine({
  agentId,
  className,
}: {
  agentId: string;
  className?: string;
}) {
  const { t } = useT("agents");
  const wsId = useWorkspaceId();
  const presence = useAgentPresenceDetail(wsId, agentId);
  const label = formatPresenceStatus(presence, t);
  const visual = presenceStatusVisual(presence);

  if (!label || !visual) {
    return <Skeleton className={cn("h-3 w-14", className)} />;
  }

  return (
    <span className={cn("inline-flex min-w-0 items-center gap-1.5", className)}>
      <visual.icon className={cn("h-3 w-3 shrink-0", visual.textClass)} />
      <span className={cn("truncate text-xs", visual.textClass)}>{label}</span>
    </span>
  );
}
