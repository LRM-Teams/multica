import { isIssueUuid } from "./issue-chip";

/**
 * Resolve the interim / unresolved ink for an issue mention.
 *
 * LRM-508: once `title` is known it wins (title-first; LRM-xxx is not primary
 * ink). Until then prefer a non-UUID author label or live identifier.
 *
 * LRM-493 / LRM-238: never silently paint a truncated UUID (`fe57cec6-…`).
 */
export function resolveIssueMentionDisplayText(
  issueId: string,
  fallbackLabel: string | undefined,
  identifier: string | undefined,
  title?: string | undefined,
): string | null {
  const trimmedTitle = title?.trim();
  if (trimmedTitle) return trimmedTitle;
  const label = fallbackLabel?.trim();
  if (label && !isIssueUuid(label)) return label;
  if (identifier) return identifier;
  if (!isIssueUuid(issueId)) return issueId;
  // Explicit UUID mention with no author label and no resolve yet — refuse to
  // paint the UUID rather than truncate it as a fake identifier.
  return null;
}
