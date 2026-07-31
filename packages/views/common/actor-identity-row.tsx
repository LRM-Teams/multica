"use client";

import type { AgentFleetRank } from "@multica/core/types/agent-fleet";
import type { HonorSnapshot } from "@multica/core/types/honor";
import {
  formatActorHandleLabel,
  resolveActorDisplayName,
  resolveActorHandle,
  shouldShowActorHandleLabel,
  type ActorIdentityFields,
} from "@multica/core/identity";
import { ActorStyledName } from "./actor-styled-name";

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
  /** Agent fleet rank badge for agents. */
  fleet?: AgentFleetRank | null;
  /** Inline surfaces cap glow at tier III; profile allows full VII. */
  honorSurface?: "inline" | "profile";
  /** Keep earned name styling but omit honor and fleet badges in dense lists. */
  showBadges?: boolean;
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
  fleet,
  honorSurface = "inline",
  showBadges = true,
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

  return (
    <span className={className}>
      <ActorStyledName
        displayName={displayName}
        honor={honor}
        fleet={fleet}
        honorSurface={honorSurface}
        showBadges={showBadges}
        className={primaryClassName}
      />
      {showHandleLabel && handleLabel ? (
        <span className={`block ${secondaryClassName}`}>{handleLabel}</span>
      ) : null}
    </span>
  );
}
