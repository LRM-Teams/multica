import { preprocessMentionShortcodes } from "../markdown/mention-shortcodes";
import type { MessagePart } from "../types";

/**
 * True when markdown (or legacy shortcode) content addresses the viewer:
 * a direct `mention://member/{viewerUserId}` link, or a broadcast
 * `mention://all/all`. Used for Slack-like self-mention row washes and
 * WeChat-style group chat system notifications.
 *
 * Deliberately id-based (not display-name substring) so renames and partial
 * name collisions cannot false-positive. Member ids are matched with a
 * trailing boundary so `user-1` does not hit `user-12`.
 */
export function contentMentionsViewer(
  content: string | null | undefined,
  viewerUserId: string | null | undefined,
): boolean {
  if (!content) return false;

  const text = content.includes("[@ ")
    ? preprocessMentionShortcodes(content)
    : content;

  // Broadcast reaches every workspace member, including the viewer.
  if (text.includes("mention://all/all")) return true;

  if (!viewerUserId) return false;

  return includesMemberMention(text, viewerUserId);
}

/**
 * Same as {@link contentMentionsViewer}, also scanning structured text parts
 * (channel messages may carry mention markdown only inside `parts`).
 */
export function messageMentionsViewer(
  content: string | null | undefined,
  viewerUserId: string | null | undefined,
  parts?: MessagePart[] | null,
): boolean {
  if (contentMentionsViewer(content, viewerUserId)) return true;
  if (!parts?.length) return false;
  for (const part of parts) {
    if (part.type === "text" && contentMentionsViewer(part.text, viewerUserId)) {
      return true;
    }
    if (
      part.type === "reference" &&
      part.ref_type === "mention" &&
      part.ref_subtype === "member" &&
      viewerUserId &&
      part.ref_id === viewerUserId
    ) {
      return true;
    }
  }
  return false;
}

/** Match `mention://member/{id}` only when `{id}` is not a prefix of a longer id. */
function includesMemberMention(text: string, userId: string): boolean {
  const needle = `mention://member/${userId}`;
  let from = 0;
  while (from < text.length) {
    const idx = text.indexOf(needle, from);
    if (idx < 0) return false;
    const after = text.charAt(idx + needle.length);
    // Markdown links close with `)`; also accept end-of-string / non-id chars.
    if (!after || after === ")" || !isMentionIdChar(after)) {
      return true;
    }
    from = idx + needle.length;
  }
  return false;
}

function isMentionIdChar(ch: string): boolean {
  // UUIDs + the loose test ids used in fixtures (hex, hyphen, underscore, alnum).
  return /[A-Za-z0-9_-]/.test(ch);
}
