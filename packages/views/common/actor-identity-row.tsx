"use client";

import type { HonorSnapshot } from "@multica/core/types/honor";
import {
  formatActorHandleLabel,
  resolveActorDisplayName,
  resolveActorHandle,
  shouldShowActorHandleLabel,
  type ActorIdentityFields,
} from "@multica/core/identity";
import { HonorBadgeIcon, honorNameDisplayProps } from "@multica/ui/components/honor/honor-badge";
import { cn } from "@multica/ui/lib/utils";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@multica/ui/components/ui/tooltip";

export interface ActorIdentityRowProps {
  /** Actor record or explicit labels. */
  identity?: ActorIdentityFields | null;
  /** Used when `identity` is absent or empty. */
  displayName?: string;
  /** Override auto-derived handle (stable `name` field). */
  handle?: string | null;
  /** Force show/hide of the weak @handle row. Defaults to identity rules. */
  showHandle?: boolean;
  /** Platform honor styling for human users. */
  honor?: HonorSnapshot | null;
  /** Inline surfaces cap glow at tier III; profile allows full VII. */
  honorSurface?: "inline" | "profile";
  primaryClassName?: string;
  secondaryClassName?: string;
  className?: string;
}

/**
 * Unified actor identity text stack: display_name primary, @handle weak secondary.
 * Squads and other non-handle actors can pass `displayName` directly.
 */
export function ActorIdentityRow({
  identity,
  displayName: displayNameProp,
  handle: handleProp,
  showHandle,
  honor,
  honorSurface = "inline",
  primaryClassName = "truncate",
  secondaryClassName = "truncate text-xs text-muted-foreground",
  className = "min-w-0 flex-1",
}: ActorIdentityRowProps) {
  const displayName =
    displayNameProp ?? (identity ? resolveActorDisplayName(identity, "") : "");
  const handle = handleProp ?? (identity ? resolveActorHandle(identity) : "");
  const handleLabel = formatActorHandleLabel(handle);
  const showHandleLabel =
    showHandle ?? (handleLabel !== null && shouldShowActorHandleLabel(displayName, handle));
  const nameDisplay = honor
    ? honorNameDisplayProps({
        nameStyle: honor.name_style,
        level: honor.level,
        surface: honorSurface,
      })
    : null;

  return (
    <span className={className}>
      <span className={`flex min-w-0 items-center gap-1 ${primaryClassName}`}>
        <span
          className={cn("truncate", nameDisplay?.className)}
          data-honor-glow-tier={nameDisplay?.["data-honor-glow-tier"]}
          style={nameDisplay?.style}
        >
          {displayName}
        </span>
        {honor?.equipped_badge ? (
          <Tooltip>
            <TooltipTrigger className="inline-flex shrink-0">
              <HonorBadgeIcon
                svgKey={honor.equipped_badge.svg_key}
                title={honor.equipped_badge.title}
              />
            </TooltipTrigger>
            <TooltipContent side="top">{honor.equipped_badge.title}</TooltipContent>
          </Tooltip>
        ) : null}
      </span>
      {showHandleLabel && handleLabel ? (
        <span className={`block ${secondaryClassName}`}>{handleLabel}</span>
      ) : null}
    </span>
  );
}