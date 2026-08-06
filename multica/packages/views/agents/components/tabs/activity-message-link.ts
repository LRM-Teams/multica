import type { ActivityEvent } from "./activity-event";

/**
 * Deep-link URL to the original chat message an Activity Output event refers to
 * (#v0 "查看原消息" — Activity is an observation surface, so the row jumps to the
 * already-rendered message in chat rather than re-rendering the full body).
 *
 * The channel container lives on `target_ref` and is NEVER inferred from
 * `source_refs` (Barry, #503 contract: container = target_ref, source facts =
 * source_refs); the message id is the `message` source ref. v0 supports channel
 * jumps only — a `thread` still needs its root/thread route resolved and a `dm`
 * has no message-level route yet (`target_ref.id` is the chat_session_id), so
 * both return null and the caller renders the affordance disabled instead of
 * jumping to the wrong place.
 *
 * Returns null when there is no resolvable (channel, message) pair.
 */
export function activityMessagePermalink(
  event: ActivityEvent,
  channelDetail: (id: string) => string,
): string | null {
  const target = event.target_ref;
  if (target.kind !== "channel" || !target.id) return null;
  const messageId = event.source_refs?.find((ref) => ref.kind === "message" && ref.id)?.id;
  if (!messageId) return null;
  return `${channelDetail(target.id)}?message=${encodeURIComponent(messageId)}`;
}
