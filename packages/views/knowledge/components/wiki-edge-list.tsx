"use client";

import type { KnowledgeEdge } from "@multica/core/types";
import { useWorkspacePaths } from "@multica/core/paths";
import { AppLink } from "../../navigation";
import { useT } from "../../i18n";
import { cn } from "@multica/ui/lib/utils";
import { edgeCounterpart, edgeTargetHref } from "../lib/edge-href";
import { isWikiEdgeType, type WikiEdgeType } from "../lib/page-kind";

const EDGE_TONE: Record<WikiEdgeType, string> = {
  derived_from: "bg-sky-500/10 text-sky-700 dark:text-sky-300",
  about: "bg-violet-500/10 text-violet-700 dark:text-violet-300",
  shared_to: "bg-emerald-500/10 text-emerald-700 dark:text-emerald-300",
  supersedes: "bg-amber-500/10 text-amber-800 dark:text-amber-300",
  owned_by: "bg-rose-500/10 text-rose-700 dark:text-rose-300",
};

function shortId(id: string): string {
  if (id.length <= 12) return id;
  return `${id.slice(0, 8)}…`;
}

export function WikiEdgeList({
  pageId,
  edges,
  loading,
}: {
  pageId: string;
  edges: KnowledgeEdge[];
  loading?: boolean;
}) {
  const { t } = useT("knowledge");
  const paths = useWorkspacePaths();

  if (loading) {
    return (
      <div className="space-y-2" data-testid="wiki-edge-list-loading">
        <div className="h-10 animate-pulse rounded-md bg-muted/60" />
        <div className="h-10 animate-pulse rounded-md bg-muted/60" />
        <div className="h-10 animate-pulse rounded-md bg-muted/40" />
      </div>
    );
  }

  if (edges.length === 0) {
    return (
      <div
        className="rounded-lg border border-dashed px-3 py-4 text-sm text-muted-foreground"
        data-testid="wiki-edge-list-empty"
      >
        <p className="font-medium text-foreground">{t(($) => $.page.contacts_empty_title)}</p>
        <p className="mt-1">{t(($) => $.page.contacts_empty_body)}</p>
      </div>
    );
  }

  const ordered = edges.toSorted((a, b) => {
    const ai = isWikiEdgeType(a.edge_type) ? a.edge_type : "zzz";
    const bi = isWikiEdgeType(b.edge_type) ? b.edge_type : "zzz";
    return ai.localeCompare(bi) || a.created_at.localeCompare(b.created_at);
  });

  return (
    <ul className="space-y-2" data-testid="wiki-edge-list">
      {ordered.map((edge) => {
        const other = edgeCounterpart(pageId, edge);
        const href = edgeTargetHref(paths, other.kind, other.id);
        const edgeKey = isWikiEdgeType(edge.edge_type) ? edge.edge_type : null;
        const typeLabel = edgeKey
          ? t(($) => $.page.edge[edgeKey])
          : edge.edge_type;
        const kindLabel =
          other.kind === "issue"
            ? t(($) => $.page.node.issue)
            : other.kind === "channel"
              ? t(($) => $.page.node.channel)
              : other.kind === "project"
                ? t(($) => $.page.node.project)
                : other.kind === "agent"
                  ? t(($) => $.page.node.agent)
                  : other.kind === "member"
                    ? t(($) => $.page.node.member)
                    : other.kind === "team_knowledge"
                      ? t(($) => $.page.node.team_knowledge)
                      : t(($) => $.page.node.unknown);
        const actionLabel =
          edge.edge_type === "derived_from"
            ? t(($) => $.page.edge.back_source)
            : t(($) => $.page.edge.open);

        const body = (
          <>
            <span
              className={cn(
                "shrink-0 rounded px-1.5 py-0.5 font-mono text-[10px] font-medium uppercase tracking-wide",
                edgeKey ? EDGE_TONE[edgeKey] : "bg-muted text-muted-foreground",
              )}
            >
              {typeLabel}
            </span>
            <div className="min-w-0 flex-1">
              <div className="truncate text-sm font-medium">
                {kindLabel} · {shortId(other.id)}
              </div>
              <div className="truncate text-[11px] text-muted-foreground">{other.kind}</div>
            </div>
            <span className="shrink-0 text-xs text-muted-foreground">{actionLabel}</span>
          </>
        );

        return (
          <li key={edge.id}>
            {href ? (
              <AppLink
                href={href}
                className="flex items-center gap-2 rounded-md border bg-card/40 px-2.5 py-2 transition-colors hover:bg-accent/50"
              >
                {body}
              </AppLink>
            ) : (
              <div className="flex items-center gap-2 rounded-md border bg-card/40 px-2.5 py-2 opacity-80">
                {body}
              </div>
            )}
          </li>
        );
      })}
    </ul>
  );
}
