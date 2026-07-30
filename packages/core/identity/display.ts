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

/**
 * LRM-749/LRM-710 — same-scope actors (e.g. workspace agents) whose resolved
 * display name collides with at least one other actor get a weak `@handle`
 * label back; non-colliding actors get no entry at all (zero-noise rule: a
 * unique name must not grow a handle next to it).
 *
 * Comparison is trim + case-insensitive so「贝克汉姆」/「贝克汉姆 」and
 * "Wendy"/"wendy" count as the same display name. Actors with no usable
 * handle are skipped (a label that adds nothing would not disambiguate).
 */
export function computeDuplicatedHandleLabels(
  actors: ReadonlyArray<{ id: string } & ActorIdentityFields>,
): Map<string, string> {
  const countByDisplay = new Map<string, number>();
  const resolved = actors.map((actor) => {
    const key = resolveActorDisplayName(actor, actor.name).trim().toLowerCase();
    countByDisplay.set(key, (countByDisplay.get(key) ?? 0) + 1);
    return { actor, key };
  });
  const labels = new Map<string, string>();
  for (const { actor, key } of resolved) {
    if ((countByDisplay.get(key) ?? 0) < 2) continue;
    const label = formatActorHandleLabel(actor.name);
    if (label) labels.set(actor.id, label);
  }
  return labels;
}
