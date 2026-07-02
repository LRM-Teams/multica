import { preprocessMentionShortcodes } from "@multica/ui/markdown";
import { resolveActorDisplayName } from "@multica/core/identity";
import type { Agent, MemberWithUser } from "@multica/core/types";

type MentionType = "member" | "agent";
type ChannelAuthorType = "user" | "agent" | "lark" | "system";

export type MentionPreviewResolver = (
  type: MentionType,
  id: string,
  fallbackLabel: string,
) => string;

export type ActorNameResolver = (
  type: "member" | "agent",
  id: string,
  fallbackLabel: string,
) => string;

export interface ChannelAuthorIdentity {
  author_type: ChannelAuthorType;
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
    author.author_type === "user" &&
    author.author_id != null &&
    author.author_id === options.currentUserId
      ? options.ownName ?? author.author_name
      : author.author_name;

  if (author.author_type === "agent") {
    if (author.author_id && options.getActorName) {
      return options.getActorName("agent", author.author_id, fallback) || fallback;
    }
    const agent = options.agents?.find((a) => matchesAuthorSnapshot(a, fallback));
    return resolveActorDisplayName(agent, fallback);
  }

  if (author.author_type === "user") {
    if (author.author_id && options.getActorName) {
      return options.getActorName("member", author.author_id, fallback) || fallback;
    }
    const member = options.members?.find((m) => matchesAuthorSnapshot(m, fallback));
    return resolveActorDisplayName(member, fallback);
  }

  return fallback;
}

export function formatChannelMessagePreview(
  authorName: string,
  content: string,
  resolveMention: MentionPreviewResolver,
) {
  const readableContent = preprocessMentionShortcodes(content)
    .replace(mentionLinkPattern, (_match: string, label: string, type: MentionType, id: string) => {
      const resolved = resolveMention(type, id, label);
      return `@${resolved.replace(/^@+/, "")}`;
    })
    .replace(markdownLinkPattern, "$1")
    .replace(/\s+/g, " ")
    .trim();

  return `${authorName}: ${readableContent}`.replace(/\s+/g, " ");
}
