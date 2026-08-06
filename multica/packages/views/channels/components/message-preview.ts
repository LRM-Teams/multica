import { preprocessMentionShortcodes } from "@multica/ui/markdown";
import { resolveActorDisplayName } from "@multica/core/identity";
import type { Agent, MemberWithUser, MessagePart } from "@multica/core/types";
import { projectInlineReferences } from "../../common/inline-references";
import {
  formatMessagePartsPreview,
  unwrapStructuredPreviewContent,
} from "./message-parts-preview";

export type MentionType = "member" | "agent";
type ChannelAuthorType = "user" | "agent" | "lark" | "system";

export type MentionPreviewResolver = (
  type: MentionType,
  id: string,
  fallbackLabel: string,
) => string;

/**
 * Build a {@link MentionPreviewResolver} from an actor-name lookup — the small
 * adapter every reading surface needs so a projected mention shows the live
 * display name (`@小雅`) rather than the internal handle the author typed
 * (`@actor_14`). Same rule the body's `ActorMention` follows.
 */
export function mentionResolverFrom(
  getActorName: ActorNameResolver,
): MentionPreviewResolver {
  return (type, id, fallbackLabel) => getActorName(type, id, fallbackLabel) || fallbackLabel;
}

export type ActorNameResolver = (
  type: "member" | "agent",
  id: string,
  fallbackLabel: string,
) => string;

export interface ChannelMessagePreviewOptions {
  formatVoice?: (durationSeconds: number | null) => string;
}

export interface ChannelAuthorIdentity {
  type: ChannelAuthorType;
  author_id?: string | null;
  author_name: string;
}

const mentionLinkPattern = /\[@([^\]]+)\]\(mention:\/\/(member|agent)\/([^)]+)\)/g;
const markdownLinkPattern = /\[([^\]]+)\]\([^)]+\)/g;

function matchesAuthorSnapshot(actor: { name: string; display_name?: string | null }, snapshot: string) {
  const normalizedSnapshot = snapshot.trim().toLowerCase().replace(/^@+/, "");
  if (!normalizedSnapshot) return false;
  return [actor.name, actor.display_name ?? ""]
    .map((value) => value.trim().toLowerCase().replace(/^@+/, ""))
    .some((value) => value === normalizedSnapshot);
}

export function resolveChannelAuthorDisplayName(
  author: ChannelAuthorIdentity,
  options: {
    currentUserId?: string | null;
    ownName?: string;
    getActorName?: ActorNameResolver;
    members?: MemberWithUser[];
    agents?: Agent[];
  } = {},
): string {
  const fallback =
    author.type === "user" &&
    author.author_id != null &&
    author.author_id === options.currentUserId
      ? options.ownName ?? author.author_name
      : author.author_name;

  if (author.type === "agent") {
    if (author.author_id && options.getActorName) {
      return options.getActorName("agent", author.author_id, fallback) || fallback;
    }
    const agent = options.agents?.find((a) => matchesAuthorSnapshot(a, fallback));
    return resolveActorDisplayName(agent, fallback);
  }

  if (author.type === "user") {
    if (author.author_id && options.getActorName) {
      return options.getActorName("member", author.author_id, fallback) || fallback;
    }
    const member = options.members?.find((m) => matchesAuthorSnapshot(m, fallback));
    return resolveActorDisplayName(member, fallback);
  }

  return fallback;
}

/**
 * Project the body's structured references into readable PLAIN TEXT (#530).
 *
 * The preview is a reading surface — you are skimming a list, not operating on
 * anything — so a reference becomes text, never an interactive token. But it must
 * go through the SAME span projection as the body, or the two disagree: Frank saw
 * `@actor_14` in the channel-list preview while the body said `@小雅`.
 *
 * Why the old path leaked: `formatMessagePartsPreview` only understands `text` and
 * `sticker` parts and drops everything else. Under #463 a normal mention message
 * carries `parts: [reference]` with a SPAN into `content` and no text part at all —
 * so it produced nothing, and the caller fell back to raw `content`, internal
 * handle and all. That helper predates #463; the meaning of `parts` changed under it.
 *
 * A mention resolves to the actor's live display name — the same rule the body's
 * `ActorMention` follows, which is exactly why the body reads `@小雅`. Everything
 * else (issue refs) renders its span substring verbatim: the projector decorates,
 * it never rewrites the author's words (#467/#600).
 */
export function projectReferencesToText(
  content: string,
  parts: MessagePart[] | null | undefined,
  resolveMention: MentionPreviewResolver,
): string | null {
  const segments = projectInlineReferences(content, parts);
  // No anchored reference → let the caller's existing text path handle it.
  if (!segments.some((seg) => seg.kind === "reference")) return null;
  return segments
    .map((seg) => {
      if (seg.kind === "text") return seg.text;
      const { ref } = seg;
      if (ref.ref_type === "mention" && (ref.ref_subtype === "member" || ref.ref_subtype === "agent")) {
        const resolved = resolveMention(ref.ref_subtype, ref.ref_id, ref.label ?? seg.text);
        return `@${resolved.replace(/^@+/, "")}`;
      }
      // channel-ref (task #912) is ALWAYS authored via the composer's
      // `[Label](mention://channel/<id>)` markdown link — unlike issue-ref,
      // there is no bare-text form — so its span covers the WHOLE markdown
      // link syntax, not just a bare identifier. Falling through to `seg.text`
      // (the #467/#600 "verbatim span substring" rule below) would leak the
      // raw `[Label](mention://channel/<uuid>)` source into the preview.
      if (ref.ref_type === "channel-ref") {
        const label = ref.label?.trim();
        return `#${label || ref.ref_id}`;
      }
      return seg.text;
    })
    .join("");
}

export function formatChannelMessagePreview(
  authorName: string,
  content: string,
  resolveMention: MentionPreviewResolver,
  parts?: MessagePart[] | null,
  options: ChannelMessagePreviewOptions = {},
) {
  const voicePart = parts?.find(
    (part): part is Extract<MessagePart, { type: "voice" }> => part.type === "voice",
  );
  if (voicePart) {
    const durationSeconds = voicePart.duration_ms
      ? Math.max(1, Math.round(voicePart.duration_ms / 1000))
      : null;
    const voiceSummary = options.formatVoice?.(durationSeconds)
      ?? (durationSeconds ? `Voice message · ${durationSeconds}s` : "Voice message");
    return `${authorName}: ${voiceSummary}`.replace(/\s+/g, " ");
  }
  const source =
    projectReferencesToText(content, parts, resolveMention) ??
    formatMessagePartsPreview(parts) ??
    unwrapStructuredPreviewContent(content) ??
    content;
  const readableContent = preprocessMentionShortcodes(source)
    .replace(mentionLinkPattern, (_match: string, label: string, type: MentionType, id: string) => {
      const resolved = resolveMention(type, id, label);
      return `@${resolved.replace(/^@+/, "")}`;
    })
    .replace(markdownLinkPattern, "$1")
    .replace(/\s+/g, " ")
    .trim();

  return `${authorName}: ${readableContent}`.replace(/\s+/g, " ");
}
