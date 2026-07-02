import type { ActorIdentityFields, ActorIdentityPresentation } from "./types";

/** Strip leading `@` and surrounding whitespace from a handle string. */
export function normalizeActorHandle(handle: string | null | undefined): string {
  return handle?.trim().replace(/^@+/, "") ?? "";
}

/**
 * Resolve the primary display label for an actor.
 * Order: `display_name` → `name` → `fallback`.
 */
export function resolveActorDisplayName(
  actor: ActorIdentityFields | null | undefined,
  fallback: string,
): string {
  const display = actor?.display_name?.trim();
  if (display) return display;
  const handle = actor?.name?.trim();
  if (handle) return handle;
  return fallback;
}

/** Resolve the stable handle from an actor record (`name` field), then fallback. */
export function resolveActorHandle(
  actor: ActorIdentityFields | null | undefined,
  fallback = "",
): string {
  return normalizeActorHandle(actor?.name) || normalizeActorHandle(fallback);
}

/** Format a handle as a weak secondary label (`@handle`). */
export function formatActorHandleLabel(handle: string | null | undefined): string | null {
  const normalized = normalizeActorHandle(handle);
  return normalized ? `@${normalized}` : null;
}

/**
 * Whether to show the secondary @handle under the primary display name.
 * Hidden when the primary label is identical to the handle (no extra info).
 */
export function shouldShowActorHandleLabel(displayName: string, handle: string): boolean {
  const normalizedHandle = normalizeActorHandle(handle);
  if (!normalizedHandle) return false;
  return displayName.trim().toLowerCase() !== normalizedHandle.toLowerCase();
}

/** Resolve the full presentation tuple used by identity rows and pickers. */
export function resolveActorIdentityPresentation(
  actor: ActorIdentityFields | null | undefined,
  fallback: string,
): ActorIdentityPresentation {
  const displayName = resolveActorDisplayName(actor, fallback);
  const handle = resolveActorHandle(actor);
  const handleLabel = formatActorHandleLabel(handle);
  return {
    displayName,
    handle,
    handleLabel,
    showHandleLabel: handleLabel !== null && shouldShowActorHandleLabel(displayName, handle),
  };
}
