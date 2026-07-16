"use client";

import { isIssueUuid, useResolvedIssue } from "./issue-chip";
import { IssueRefLink } from "./issue-ref-link";

interface IssueMentionCardProps {
  issueId: string;
  /** Fallback text when issue is not in store (e.g. "MUL-7") */
  fallbackLabel?: string;
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
export function IssueMentionCard({ issueId, fallbackLabel }: IssueMentionCardProps) {
  const isUuid = isIssueUuid(issueId);
  const issue = useResolvedIssue(issueId);

  if (!isUuid && !issue) {
    // Auto-linked identifier that doesn't resolve — keep it as plain text.
    return <span className="not-prose">{fallbackLabel ?? issueId}</span>;
  }

  // `source="fallback"` is invisible to the reader, deliberately: it exists only so a
  // test can assert that every occurrence of an identifier in one message was
  // anchored. Unifying the look closed the channel that made a missed anchor obvious
  // (#521 — Frank spotted the parser bug only because the miss rendered as a chip),
  // so the signal moved to where assertions can see it and users cannot. It gets
  // deleted along with this whole path once #521 lands (#463/#510 tail).
  return <IssueRefLink issueId={issueId} text={fallbackLabel ?? issueId} source="fallback" />;
}
