"use client";

import type { HonorSnapshot } from "@multica/core/types/honor";
import { honorNameDisplayProps } from "@multica/ui/lib/honor-name-display";
import { cn } from "@multica/ui/lib/utils";
import { AgentHonorLevelIcon } from "../agents/components/agent-honor-level-icon";
import { UserHonorLevelIcon } from "../honor/user-honor-level-icon";
import { useT } from "../i18n";

export interface ActorStyledNameProps {
  displayName: string;
  honor?: HonorSnapshot | null;
  agentHonorLevel?: number | null;
  honorSurface?: "inline" | "profile";
  /** Dense lists can keep earned name styling while omitting space-consuming badges. */
  showBadges?: boolean;
  className?: string;
  nameClassName?: string;
}

/** Inline actor display name with the platform's human or Agent honor crest. */
export function ActorStyledName({
  displayName,
  honor,
  agentHonorLevel,
  honorSurface = "inline",
  showBadges = true,
  className,
  nameClassName = "truncate",
}: ActorStyledNameProps) {
  const { t } = useT("common");
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
      {showBadges && honor ? (
        <UserHonorLevelIcon
          level={honor.level}
          title={t(($) => $.honor_level_value, { level: honor.level })}
          className="size-6 drop-shadow-sm"
        />
      ) : null}
      {showBadges && agentHonorLevel ? (
        <AgentHonorLevelIcon
          level={agentHonorLevel}
          title={t(($) => $.honor_level_value, { level: agentHonorLevel })}
          className="size-6 drop-shadow-sm"
        />
      ) : null}
    </span>
  );
}
