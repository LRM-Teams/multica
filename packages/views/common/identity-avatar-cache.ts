import { resolvePublicFileUrl } from "@multica/core/workspace/avatar-url";

/**
 * LRM-224 / LRM-223 option B: identity-first sticky face cache.
 * Keyed by `actorType:actorId`. Message payload URLs only *seed* the cache;
 * null / undefined / missing must never clear an existing entry.
 */

export type IdentityActorType = "member" | "agent" | "squad" | "user";

const identityAvatarOkCache = new Map<string, string>();

export function identityActorKey(
  actorType: string,
  actorId: string,
): string | null {
  if (!actorId) return null;
  // Chat messages use type "user"; directory / ActorAvatar use "member".
  const normalized = actorType === "user" ? "member" : actorType;
  if (
    normalized !== "member" &&
    normalized !== "agent" &&
    normalized !== "squad"
  ) {
    return null;
  }
  return `${normalized}:${actorId}`;
}

/** Remember a good URL for this actor. No-ops on empty values. */
export function rememberIdentityAvatarUrl(
  actorType: string,
  actorId: string,
  url: string | null | undefined,
): void {
  const key = identityActorKey(actorType, actorId);
  const resolved = resolvePublicFileUrl(url) ?? undefined;
  if (!key || !resolved) return;
  identityAvatarOkCache.set(key, resolved);
}

/**
 * Resolve face URL: explicit hint (if present) → sticky cache → directory.
 * Falsy hint never clears sticky / directory.
 */
export function resolveIdentityAvatarUrl(options: {
  actorType: string;
  actorId: string;
  /** Message / row payload — only accelerates; omit/null does not wipe. */
  avatarUrlHint?: string | null;
  /** From workspace directory (profile card source of truth). */
  directoryUrl?: string | null;
}): string | undefined {
  const { actorType, actorId, avatarUrlHint, directoryUrl } = options;
  const key = identityActorKey(actorType, actorId);

  const fromHint = resolvePublicFileUrl(avatarUrlHint) ?? undefined;
  if (fromHint) {
    if (key) identityAvatarOkCache.set(key, fromHint);
    return fromHint;
  }

  const sticky = key ? identityAvatarOkCache.get(key) : undefined;
  if (sticky) return sticky;

  const fromDirectory = resolvePublicFileUrl(directoryUrl) ?? undefined;
  if (fromDirectory) {
    if (key) identityAvatarOkCache.set(key, fromDirectory);
    return fromDirectory;
  }
  return undefined;
}

/** Test-only. */
export function __resetIdentityAvatarOkCacheForTests() {
  identityAvatarOkCache.clear();
}
