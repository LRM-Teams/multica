"use client";

import { AppLink } from "../../navigation";
import { useWorkspacePaths } from "@multica/core/paths";
import { IssueChip, isIssueUuid, useResolvedIssue } from "./issue-chip";

interface IssueMentionCardProps {
  issueId: string;
  /** Fallback text when issue is not in store (e.g. "MUL-7") */
  fallbackLabel?: string;
}

/**
 * Navigable chip — wraps IssueChip in an AppLink pointing at the issue's
 * detail page. Hover/cursor affordance is layered onto the chip itself so
 * the visual target matches the clickable target.
 *
 * Two sources feed this:
 *   - Explicit mentions (`mention://issue/<uuid>`) — always navigable, even
 *     before the issue resolves (the author deliberately picked it).
 *   - Auto-linked bare identifiers (`MUL-123` in prose) — only navigable once
 *     they resolve to a real issue; an unresolved identifier renders as plain
 *     text so we never produce a dead link for a false match.
 */
export function IssueMentionCard({ issueId, fallbackLabel }: IssueMentionCardProps) {
  const p = useWorkspacePaths();
  const isUuid = isIssueUuid(issueId);
  const issue = useResolvedIssue(issueId);

  if (!isUuid && !issue) {
    // Auto-linked identifier that doesn't resolve — keep it as plain text.
    return <span className="not-prose">{fallbackLabel ?? issueId}</span>;
  }

  return (
    <AppLink
      href={p.issueDetail(issue?.id ?? issueId)}
      className="issue-mention not-prose inline-flex"
    >
      <IssueChip
        issueId={issueId}
        fallbackLabel={fallbackLabel}
        className="cursor-pointer hover:bg-accent transition-colors"
      />
    </AppLink>
  );
}
