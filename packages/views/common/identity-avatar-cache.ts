import {
  isLegacyUploadsAvatarUrl as isLegacyUploadsAvatarUrlFn,
  resolvePublicFileUrl,
} from "@multica/core/workspace/avatar-url";

/**
 * LRM-224 / LRM-223 option B: identity-first sticky face cache.
 * Keyed by `actorType:actorId`. Message payload URLs only *seed* the cache;
 * null / undefined / missing must never clear an existing entry.
 * LRM-855: a legacy `/uploads/` hint must not overwrite a sticky OSS/CDN URL.
 */

/** Fallback when unit tests mock only `resolvePublicFileUrl`. */
function isLegacyUploadsAvatarUrl(url: string | null | undefined): boolean {
  if (typeof isLegacyUploadsAvatarUrlFn === "function") {
    return isLegacyUploadsAvatarUrlFn(url);
  }
  if (!url) return false;
  if (url.startsWith("/uploads/")) return true;
  try {
    return new URL(url).pathname.startsWith("/uploads/");
  } catch {
    return false;
  }
}

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

function shouldReplaceSticky(next: string, sticky: string | undefined): boolean {
  if (!sticky) return true;
  // Keep a non-legacy sticky face when a stale `/uploads/` row tries to reseed.
  if (isLegacyUploadsAvatarUrl(next) && !isLegacyUploadsAvatarUrl(sticky)) {
    return false;
  }
  return true;
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
  const sticky = identityAvatarOkCache.get(key);
  if (!shouldReplaceSticky(resolved, sticky)) return;
  identityAvatarOkCache.set(key, resolved);
}

/**
 * Resolve face URL: live directory → explicit hint → sticky cache.
 *
 * Message payload URLs are transport hints used while the live directory is
 * unavailable; they must never override a current profile avatar after an edit.
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
  const sticky = key ? identityAvatarOkCache.get(key) : undefined;

  const fromDirectory = resolvePublicFileUrl(directoryUrl) ?? undefined;
  if (fromDirectory) {
    if (key) identityAvatarOkCache.set(key, fromDirectory);
    return fromDirectory;
  }

  const fromHint = resolvePublicFileUrl(avatarUrlHint) ?? undefined;
  if (fromHint) {
    if (key && shouldReplaceSticky(fromHint, sticky)) {
      identityAvatarOkCache.set(key, fromHint);
      return fromHint;
    }
    // Stale `/uploads/` hint while sticky already holds OSS — keep sticky.
    if (sticky && !shouldReplaceSticky(fromHint, sticky)) return sticky;
    return fromHint;
  }

  if (sticky) return sticky;
  return undefined;
}

/** Test-only. */
export function __resetIdentityAvatarOkCacheForTests() {
  identityAvatarOkCache.clear();
}
