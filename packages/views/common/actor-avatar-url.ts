import { resolvePublicFileUrl } from "@multica/core/workspace/avatar-url";

/**
 * LRM-224 / Option B — identity-keyed sticky avatar URL cache.
 *
 * All product surfaces resolve faces by `actorType + actorId`. Message (or
 * other payload) URLs are optional acceleration only: when present they refresh
 * this cache; when absent they must never clear a known face.
 *
 * Kept outside the React component file so Fast Refresh can preserve component
 * state (react-doctor: only-export-components).
 */
const actorAvatarOkCache = new Map<string, string>();

export function actorAvatarCacheKey(
  actorType: string,
  actorId: string,
): string {
  return `${actorType}:${actorId}`;
}

export type ResolveActorAvatarUrlInput = {
  actorType: string;
  actorId: string;
  /**
   * Author-directory / profile-card source of truth. Callers that already ran
   * `getActorAvatarUrl` (which public-resolves) should pass that value through
   * unchanged — we do not re-resolve it.
   */
  directoryUrl?: string | null;
  /** Optional payload hint (e.g. message.author_avatar_url). May be relative. */
  hintUrl?: string | null;
};

/**
 * Resolve an avatar URL by identity.
 *
 * Priority (LRM-224 Option B):
 *   1. Actor directory (when known)
 *   2. Sticky same-actor cache
 *   3. Optional payload hint (also seeds/refreshes the sticky cache)
 *   4. `undefined` → caller paints the design placeholder (色圆字母)
 *
 * Missing hint ≠ clear. Directory miss falls through to sticky/hint/placeholder.
 */
export function resolveActorAvatarUrl(
  input: ResolveActorAvatarUrlInput,
): string | undefined {
  const key = actorAvatarCacheKey(input.actorType, input.actorId);

  // Only touch `resolvePublicFileUrl` (and thus api.getBaseUrl) when a hint
  // string is present — empty/null stays a no-op so directory-only surfaces
  // don't require an API mock in unit tests.
  const fromHint =
    input.hintUrl != null && input.hintUrl !== ""
      ? resolvePublicFileUrl(input.hintUrl) ?? undefined
      : undefined;
  if (fromHint) {
    actorAvatarOkCache.set(key, fromHint);
  }

  const fromDirectory =
    typeof input.directoryUrl === "string" && input.directoryUrl.length > 0
      ? input.directoryUrl
      : undefined;
  if (fromDirectory) {
    actorAvatarOkCache.set(key, fromDirectory);
    return fromDirectory;
  }

  const sticky = actorAvatarOkCache.get(key);
  if (sticky) return sticky;
  return fromHint;
}

/** Test-only helper to isolate sticky avatar-cache cases. */
export function __resetActorAvatarOkCacheForTests() {
  actorAvatarOkCache.clear();
}
