import { preprocessMentionShortcodes } from "@multica/ui/markdown";

type MentionType = "member" | "agent";

export type MentionPreviewResolver = (
  type: MentionType,
  id: string,
  fallbackLabel: string,
) => string;

const mentionLinkPattern = /\[@([^\]]+)\]\(mention:\/\/(member|agent)\/([^)]+)\)/g;
const markdownLinkPattern = /\[([^\]]+)\]\([^)]+\)/g;

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
