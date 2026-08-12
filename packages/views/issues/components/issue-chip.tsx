"use client";

import { useQuery } from "@tanstack/react-query";
import type { Issue } from "@multica/core/types";
import { issueListOptions, issueDetailOptions } from "@multica/core/issues/queries";
import { useWorkspaceId } from "@multica/core/hooks";
import { StatusIcon } from "./status-icon";

/** True when `s` is a canonical UUID (an explicit mention) rather than a
 *  human identifier like "MUL-123" (an auto-linked reference). */
export function isIssueUuid(s: string): boolean {
  return /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(s);
}

/**
 * Resolve an issue by UUID **or** human identifier ("MUL-123"). Looks in the
 * first-page list cache first, then falls back to a detail fetch (the backend
 * loader accepts either form). Returns undefined while loading or when the
 * issue can't be resolved (deleted, other workspace, no permission).
 */
export function useResolvedIssue(key: string): Issue | undefined {
  const wsId = useWorkspaceId();
  const { data: issues = [] } = useQuery(issueListOptions(wsId));
  const listIssue = issues.find((i) => i.id === key || i.identifier === key);
  const { data: detailIssue } = useQuery({
    ...issueDetailOptions(wsId, key),
    enabled: !listIssue && !!key,
  });
  return listIssue ?? detailIssue;
}

/**
 * Compact, presentation-only representation of an issue —
 * `<StatusIcon> <identifier> <title>`, bordered, truncating to max-w-72.
 *
 * This is the single source of truth for the "issue-mention card" look.
 * It is intentionally **not** a link or button: callers wrap it in whatever
 * interactive shell they need (AppLink for markdown mentions, an <a> with
 * cmd-click support inside the editor's NodeView, a plain span next to a
 * dismiss button in chat's context anchor card, …).
 *
 * Size budget: must fit within a 14px line-box when used inline — hence
 * `py-0.5` + text-xs (see MentionView docstring for the math).
 */
export interface IssueChipProps {
  issueId: string;
  /** Shown while the issue is still resolving (identifier is fine). */
  fallbackLabel?: string;
  /**
   * Shown when the issue cannot be resolved (deleted / no access).
   * Must not include a previously cached title — S1-R2 inaccessible degrade.
   */
  unresolvedLabel?: string;
  /** Extra classes — callers layer interaction hints here
   *  (e.g. `hover:bg-accent cursor-pointer` for navigable variants). */
  className?: string;
}

const BASE_CLASS =
  "issue-mention inline-flex items-center gap-1.5 rounded-md border mx-0.5 px-2 py-0.5 text-xs max-w-72";

export function IssueChip({ issueId, fallbackLabel, unresolvedLabel, className }: IssueChipProps) {
  const wsId = useWorkspaceId();
  const { data: issues = [] } = useQuery(issueListOptions(wsId));
  const listIssue = issues.find((i) => i.id === issueId || i.identifier === issueId);
  const detailQuery = useQuery({
    ...issueDetailOptions(wsId, issueId),
    enabled: !listIssue && !!issueId,
  });
  const issue = listIssue ?? detailQuery.data;
  const cls = className ? `${BASE_CLASS} ${className}` : BASE_CLASS;

  if (issue) {
    return (
      <span className={cls}>
        <StatusIcon status={issue.status} className="h-3.5 w-3.5 shrink-0" />
        <span className="font-medium text-muted-foreground shrink-0">
          {issue.identifier}
        </span>
        <span className="text-foreground truncate">{issue.title}</span>
      </span>
    );
  }

  const stillLoading = !listIssue && (detailQuery.isLoading || detailQuery.isFetching);
  if (stillLoading) {
    return (
      <span className={cls}>
        <span className="font-medium text-muted-foreground">
          {fallbackLabel ?? issueId.slice(0, 8)}
        </span>
      </span>
    );
  }

  // Unresolved / inaccessible: never surface a title from attrs or stale cache.
  return (
    <span className={cls} data-issue-unresolved="true">
      <span className="font-medium text-muted-foreground">
        {unresolvedLabel ?? fallbackLabel ?? issueId.slice(0, 8)}
      </span>
    </span>
  );
}
