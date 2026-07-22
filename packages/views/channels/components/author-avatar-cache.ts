/**
 * @deprecated LRM-224 — prefer views `ActorAvatar` + `identity-avatar-cache`.
 * Kept as a thin adapter for residual call sites / tests during migration.
 */
import type { ChannelMessage } from "@multica/core/types";
import {
  resolveIdentityAvatarUrl,
  __resetIdentityAvatarOkCacheForTests,
} from "../../common/identity-avatar-cache";

export function resolveCachedAuthorAvatarUrl(
  message: ChannelMessage,
  directoryUrl?: string | null,
): string | undefined {
  if (!message.author_id) return undefined;
  if (message.type !== "agent" && message.type !== "user") return undefined;
  return resolveIdentityAvatarUrl({
    actorType: message.type === "user" ? "member" : "agent",
    actorId: message.author_id,
    avatarUrlHint: message.author_avatar_url,
    directoryUrl,
  });
}

/** Test-only helper — clears the shared identity sticky cache. */
export function __resetAuthorAvatarOkCacheForTests() {
  __resetIdentityAvatarOkCacheForTests();
}
