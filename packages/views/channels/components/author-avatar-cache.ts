import { resolvePublicFileUrl } from "@multica/core/workspace/avatar-url";
import type { ChannelMessage } from "@multica/core/types";

/**
 * LRM-202 / LRM-218 / LRM-221: remember the last good avatar URL per author so
 * a later message that omits `author_avatar_url` does not flash a text/glyph
 * placeholder while an earlier bubble from the same author (or the actor
 * directory / profile card) already has the real face.
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

/**
 * Resolve the avatar URL for a chat bubble.
 *
 * Priority: message payload → sticky same-author cache → actor directory
 * (same source as the profile card). Directory is a last resort so bubbles
 * stay consistent with the profile card when WS/list payloads omit the URL
 * (LRM-218 regression: “整体头像又变文字”).
 */
export function resolveCachedAuthorAvatarUrl(
  message: ChannelMessage,
  directoryUrl?: string | null,
): string | undefined {
  const key = authorAvatarCacheKey(message);
  const fromPayload = resolvePublicFileUrl(message.author_avatar_url) ?? undefined;
  if (fromPayload) {
    if (key) authorAvatarOkCache.set(key, fromPayload);
    return fromPayload;
  }
  const sticky = key ? authorAvatarOkCache.get(key) : undefined;
  if (sticky) return sticky;

  const fromDirectory = resolvePublicFileUrl(directoryUrl) ?? undefined;
  if (fromDirectory) {
    if (key) authorAvatarOkCache.set(key, fromDirectory);
    return fromDirectory;
  }
  return undefined;
}

/** Test-only helper to isolate sticky avatar-cache cases. */
export function __resetAuthorAvatarOkCacheForTests() {
  authorAvatarOkCache.clear();
}
