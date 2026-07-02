"use client";

import {
  formatActorHandleLabel,
  resolveActorDisplayName,
  resolveActorHandle,
  shouldShowActorHandleLabel,
  type ActorIdentityFields,
} from "@multica/core/identity";

export interface ActorIdentityRowProps {
  /** Actor record or explicit labels. */
  identity?: ActorIdentityFields | null;
  /** Used when `identity` is absent or empty. */
  displayName?: string;
  /** Override auto-derived handle (stable `name` field). */
  handle?: string | null;
  /** Force show/hide of the weak @handle row. Defaults to identity rules. */
  showHandle?: boolean;
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
      <span className={`block ${primaryClassName}`}>{displayName}</span>
      {showHandleLabel && handleLabel ? (
        <span className={`block ${secondaryClassName}`}>{handleLabel}</span>
      ) : null}
    </span>
  );
}