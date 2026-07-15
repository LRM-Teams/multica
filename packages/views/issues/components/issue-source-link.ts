import type { IssueSourceMessageRef } from "@multica/core/types";

/**
 * Deep-link URL to the chat message an issue was created from (#470 "From
 * discussion" back-jump). The server resolves the source to a real,
 * member-visible channel row and canonicalizes a reply anchor to its thread
 * root, so one channel deep-link (`?message=`) reaches the right place for
 * every channel kind (group or dm) — no per-kind gating needed. Mirrors the
 * #509 activity permalink pattern.
 *
 * Returns null when there is no source ref so the caller can hide the row.
 */
export function issueSourceMessageHref(
  source: IssueSourceMessageRef | undefined,
  channelDetail: (id: string) => string,
): string | null {
  if (!source?.channel_id || !source.message_id) return null;
  return `${channelDetail(source.channel_id)}?message=${encodeURIComponent(source.message_id)}`;
}
