import type { ChannelMessage } from "@multica/core/types";
import {
  actorAvatarCacheKey,
  resolveActorAvatarUrl,
  __resetActorAvatarOkCacheForTests,
} from "../../common/actor-avatar-url";

/**
 * Message → identity helpers for chat bubbles (LRM-224 Option B).
 *
 * Bubbles render the shared identity-first `ActorAvatar`; these helpers only
 * map message fields onto that contract and keep the historical test reset
 * entry point. Supersedes the LRM-221 payload→sticky→directory helper —
 * directory/sticky live in `resolveActorAvatarUrl`, keyed by actor id.
 */

/** Map a channel message author onto the actor-directory key space. */
export function messageAuthorActor(
  message: ChannelMessage,
): { actorType: "agent" | "member"; actorId: string } | null {
  if (!message.author_id) return null;
  if (message.type === "agent") {
    return { actorType: "agent", actorId: message.author_id };
  }
  if (message.type === "user") {
    return { actorType: "member", actorId: message.author_id };
  }
  return null;
}

/** @deprecated Prefer `messageAuthorActor` + `actorAvatarCacheKey`. */
export function authorAvatarCacheKey(message: ChannelMessage): string | null {
  const author = messageAuthorActor(message);
  if (!author) return null;
  return actorAvatarCacheKey(author.actorType, author.actorId);
}

/**
 * Resolve a bubble avatar URL via the shared identity-first pipeline.
 * Prefer rendering `<ActorAvatar actorType actorId avatarUrlHint />` instead
 * of calling this from new UI — kept for tests and transitional call sites.
 */
export function resolveCachedAuthorAvatarUrl(
  message: ChannelMessage,
  directoryUrl?: string | null,
): string | undefined {
  const author = messageAuthorActor(message);
  if (!author) return undefined;
  return resolveActorAvatarUrl({
    actorType: author.actorType,
    actorId: author.actorId,
    directoryUrl,
    hintUrl: message.author_avatar_url,
  });
}

/** Test-only helper — clears the shared identity sticky cache. */
export function __resetAuthorAvatarOkCacheForTests() {
  __resetActorAvatarOkCacheForTests();
}
