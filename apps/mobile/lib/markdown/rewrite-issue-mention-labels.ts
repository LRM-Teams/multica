/**
 * Rewrite `mention://issue/…` link labels to the live issue title (LRM-508).
 *
 * Enriched markdown cannot inject React for links, so mobile paints whatever
 * markdown label is in the source. Replace LRM-xxx / UUID labels with the
 * resolved title before render. Missing title → empty label (explicit empty;
 * never leave LRM-xxx or a UUID as silent stand-in — LRM-238).
 */

const ISSUE_MENTION_RE = /\[([^\]]*)\]\(mention:\/\/issue\/([^)]+)\)/g;

/** Escape characters that would break a markdown link label. */
export function escapeMarkdownLinkLabel(label: string): string {
  return label.replace(/\\/g, "\\\\").replace(/\[/g, "\\[").replace(/\]/g, "\\]");
}

/**
 * @param resolveTitle - return trimmed title, `null` when resolved-without-title,
 *   or `undefined` while still unknown. Both null/undefined clear the label.
 */
export function rewriteIssueMentionLabels(
  content: string,
  resolveTitle: (issueId: string) => string | null | undefined,
): string {
  if (!content.includes("mention://issue/")) return content;
  return content.replace(ISSUE_MENTION_RE, (_match, _label: string, id: string) => {
    const title = resolveTitle(id)?.trim();
    if (title) {
      return `[${escapeMarkdownLinkLabel(title)}](mention://issue/${id})`;
    }
    // Unresolved or empty title — strip LRM-xxx / UUID ink (LRM-508).
    return `[](mention://issue/${id})`;
  });
}

/** Collect unique issue ids referenced by `mention://issue/<id>` links. */
export function collectIssueMentionIds(content: string): string[] {
  if (!content.includes("mention://issue/")) return [];
  const ids: string[] = [];
  const seen = new Set<string>();
  for (const match of content.matchAll(ISSUE_MENTION_RE)) {
    const id = match[2];
    if (!id || seen.has(id)) continue;
    seen.add(id);
    ids.push(id);
  }
  return ids;
}
