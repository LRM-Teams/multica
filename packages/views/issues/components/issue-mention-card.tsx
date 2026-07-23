"use client";

import { isIssueUuid, useResolvedIssue } from "./issue-chip";
import { IssueRefLink } from "./issue-ref-link";

interface IssueMentionCardProps {
  issueId: string;
  /** Fallback text when issue is not in store (e.g. "MUL-7") */
  fallbackLabel?: string;
  /** Source row id when this legacy reference is rendered in a Messages timeline. */
  sourceMessageId?: string;
}

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

/**
 * An issue reference in a message body that the server did NOT anchor — the legacy
 * client-side path: `preprocessIssueRefs` rewrites a bare `MUL-123` in prose into
 * `mention://issue/MUL-123`, and this renders it.
 *
 * It renders {@link IssueRefLink} — the SAME component the span projector uses for
 * anchored references (#520). It used to render `IssueChip`, which is how Frank
 * caught one message showing `LRM-126` three times in two different looks: two
 * anchored (plain link) and one unanchored (a bordered chip). A compat path is
 * allowed to survive; it is not allowed to grow a second face. Sharing the component
 * — not merely matching the CSS — is what makes that structural.
 *
 * The chip lives on in the EDITOR (`issue-reference.tsx` / `mention-view.tsx`), and
 * correctly: there you operate on the reference, so its box is a functional signal
 * that it is one atomic token. Here you are reading it, so it is clickable text.
 *
 * Two sources feed this:
 *   - Explicit mentions (`mention://issue/<uuid>`) — always navigable, even before
 *     the issue resolves (the author deliberately picked it).
 *   - Auto-linked bare identifiers (`MUL-123` in prose) — only navigable once they
 *     resolve to a real issue; an unresolved identifier renders as plain text so we
 *     never produce a dead link for a false match.
 */
export function IssueMentionCard({ issueId, fallbackLabel, sourceMessageId }: IssueMentionCardProps) {
  const isUuid = isIssueUuid(issueId);
  const issue = useResolvedIssue(issueId);
  const displayText = resolveIssueMentionDisplayText(
    issueId,
    fallbackLabel,
    issue?.identifier,
  );

  if (!isUuid && !issue) {
    // Auto-linked identifier that doesn't resolve — keep it as plain text.
    return <span className="not-prose">{fallbackLabel ?? issueId}</span>;
  }

  // UUID mention still loading / deleted with no human label — do not flash UUID
  // ink (LRM-493). Once `useResolvedIssue` lands, we re-render with the identifier.
  if (!displayText) {
    return null;
  }

  // `source="fallback"` is invisible to the reader, deliberately: it exists only so a
  // test can assert that every occurrence of an identifier in one message was
  // anchored. Unifying the look closed the channel that made a missed anchor obvious
  // (#521 — Frank spotted the parser bug only because the miss rendered as a chip),
  // so the signal moved to where assertions can see it and users cannot. It gets
  // deleted along with this whole path once #521 lands (#463/#510 tail).
  return (
    <IssueRefLink
      issueId={issueId}
      text={displayText}
      source="fallback"
      sourceMessageId={sourceMessageId}
    />
  );
}
