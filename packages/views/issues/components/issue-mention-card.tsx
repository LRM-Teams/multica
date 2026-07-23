"use client";

import { isIssueUuid, useResolvedIssue } from "./issue-chip";
import { IssueRefLink } from "./issue-ref-link";
import { resolveIssueMentionDisplayText } from "./issue-mention-display-text";

interface IssueMentionCardProps {
  issueId: string;
  /** Fallback text when issue is not in store (e.g. "MUL-7") — auto-link miss only. */
  fallbackLabel?: string;
  /** Source row id when this legacy reference is rendered in a Messages timeline. */
  sourceMessageId?: string;
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
 * Display (LRM-508): title primary (clickable); optional muted identifier secondary
 * — same title-first口径 as LRM-423. No title → render nothing (never LRM-xxx/UUID).
 *
 * Two sources feed this:
 *   - Explicit mentions (`mention://issue/<uuid>`) — always navigable once titled.
 *   - Auto-linked bare identifiers (`MUL-123` in prose) — only navigable once they
 *     resolve to a real issue; an unresolved identifier renders as plain text so we
 *     never produce a dead link for a false match.
 */
export function IssueMentionCard({ issueId, fallbackLabel, sourceMessageId }: IssueMentionCardProps) {
  const isUuid = isIssueUuid(issueId);
  const issue = useResolvedIssue(issueId);
  const displayText = resolveIssueMentionDisplayText(issue?.title);

  if (!isUuid && !issue) {
    // Auto-linked identifier that doesn't resolve — keep it as plain text.
    // This is a false-match path, not a titled mention; LRM-xxx here is the
    // author's bare token, not a silent stand-in for a missing title.
    return <span className="not-prose">{fallbackLabel ?? issueId}</span>;
  }

  // Loading / deleted / resolved-without-title — do not flash LRM-xxx or UUID
  // (LRM-508 / LRM-238). Once title lands, we re-render.
  if (!displayText) {
    return null;
  }

  const identifier = issue?.identifier?.trim();
  const showMutedId = Boolean(identifier) && identifier !== displayText;

  // `source="fallback"` is invisible to the reader, deliberately: it exists only so a
  // test can assert that every occurrence of an identifier in one message was
  // anchored. Unifying the look closed the channel that made a missed anchor obvious
  // (#521 — Frank spotted the parser bug only because the miss rendered as a chip),
  // so the signal moved to where assertions can see it and users cannot. It gets
  // deleted along with this whole path once #521 lands (#463/#510 tail).
  return (
    <span className="inline-flex max-w-full min-w-0 flex-wrap items-baseline gap-x-1">
      <IssueRefLink
        issueId={issueId}
        text={displayText}
        source="fallback"
        sourceMessageId={sourceMessageId}
      />
      {showMutedId ? (
        <span className="shrink-0 text-[11px] tabular-nums text-muted-foreground">
          {identifier}
        </span>
      ) : null}
    </span>
  );
}
