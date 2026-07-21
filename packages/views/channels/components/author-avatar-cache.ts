import { resolvePublicFileUrl } from "@multica/core/workspace/avatar-url";
import type { ChannelMessage } from "@multica/core/types";

/**
 * LRM-202: remember the last good avatar URL per author so a later message
 * that omits `author_avatar_url` does not flash a gray text placeholder while
 * an earlier bubble from the same author already showed the real face.
 *
 * Kept outside the bubble component file so Fast Refresh can preserve
 * component state (react-doctor: only-export-components).
 */
const authorAvatarOkCache = new Map<string, string>();

export function authorAvatarCacheKey(message: ChannelMessage): string | null {
  if (!message.author_id) return null;
  if (message.type !== "agent" && message.type !== "user") return null;
  return `${message.type}:${message.author_id}`;
}

export function resolveCachedAuthorAvatarUrl(message: ChannelMessage): string | undefined {
  const key = authorAvatarCacheKey(message);
  const fromPayload = resolvePublicFileUrl(message.author_avatar_url) ?? undefined;
  if (fromPayload) {
    if (key) authorAvatarOkCache.set(key, fromPayload);
    return fromPayload;
  }
  return key ? authorAvatarOkCache.get(key) : undefined;
}

/** Test-only helper to isolate sticky avatar-cache cases. */
export function __resetAuthorAvatarOkCacheForTests() {
  authorAvatarOkCache.clear();
}
