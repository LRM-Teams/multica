import { isIssueUuid } from "./issue-chip";

/**
 * Prefer the author's link text / live identifier over a bare UUID.
 *
 * LRM-493 / LRM-238: never silently paint a truncated UUID (`fe57cec6-…`) when
 * we have (or can resolve) an LRM-id. Author label wins when it is already a
 * human identifier; otherwise the live `issue.identifier` wins once resolved.
 */
export function resolveIssueMentionDisplayText(
  issueId: string,
  fallbackLabel: string | undefined,
  identifier: string | undefined,
): string | null {
  const label = fallbackLabel?.trim();
  if (label && !isIssueUuid(label)) return label;
  if (identifier) return identifier;
  if (!isIssueUuid(issueId)) return issueId;
  // Explicit UUID mention with no author label and no resolve yet — refuse to
  // paint the UUID rather than truncate it as a fake identifier.
  return null;
}
