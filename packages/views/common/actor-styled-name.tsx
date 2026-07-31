"use client";

import type { AgentFleetRank } from "@multica/core/types/agent-fleet";
import type { HonorSnapshot } from "@multica/core/types/honor";
import { FleetRankBadge } from "@multica/ui/components/fleet/fleet-class-badge";
import { HonorBadgeIcon } from "@multica/ui/components/honor/honor-badge";
import { honorNameDisplayProps } from "@multica/ui/lib/honor-name-display";
import { cn } from "@multica/ui/lib/utils";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@multica/ui/components/ui/tooltip";

export interface ActorStyledNameProps {
  displayName: string;
  honor?: HonorSnapshot | null;
  fleet?: AgentFleetRank | null;
  honorSurface?: "inline" | "profile";
  /** Dense lists can keep earned name styling while omitting space-consuming badges. */
  showBadges?: boolean;
  className?: string;
  nameClassName?: string;
}

/** Inline actor display name with platform honor styling and/or agent fleet badge. */
export function ActorStyledName({
  displayName,
  honor,
  fleet,
  honorSurface = "inline",
  showBadges = true,
  className,
  nameClassName = "truncate",
}: ActorStyledNameProps) {
  const nameDisplay = honor
    ? honorNameDisplayProps({
        nameStyle: honor.name_style,
        level: honor.level,
        surface: honorSurface,
      })
    : null;

  return (
    <span className={cn("inline-flex min-w-0 max-w-full items-center gap-1.5", className)}>
      <span
        className={cn(nameClassName, nameDisplay?.className)}
        data-honor-glow-tier={nameDisplay?.["data-honor-glow-tier"]}
        data-honor-surface={nameDisplay?.["data-honor-surface"]}
        style={nameDisplay?.style}
      >
        {displayName}
      </span>
      {showBadges && honor?.equipped_badge ? (
        <Tooltip>
          <TooltipTrigger className="inline-flex shrink-0">
            <HonorBadgeIcon
              svgKey={honor.equipped_badge.svg_key}
              title={honor.equipped_badge.title}
              medal
            />
          </TooltipTrigger>
          <TooltipContent side="top">{honor.equipped_badge.title}</TooltipContent>
        </Tooltip>
      ) : null}
      {showBadges && fleet ? (
        <FleetRankBadge
          classId={fleet.class_id}
          classLabel={fleet.class_label}
          fleetRank={fleet.fleet_rank}
          frozen={fleet.frozen}
          medal
        />
      ) : null}
    </span>
  );
}
