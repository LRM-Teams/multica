"use client";

import type {
  ResearchV6DirectorNodeDetail,
  ResearchV6DirectorProjectionNode,
} from "@multica/core/types/research-v6-director";
import { Button } from "@multica/ui/components/ui/button";
import { GitBranch, Link2, MessageSquareText, RefreshCw } from "lucide-react";
import { useT } from "../../i18n/use-t";

export function ResearchV6NodeDetail({
  node,
  detail,
  loading,
  error,
  selectedForChat,
  onRetry,
  onReference,
}: {
  node: ResearchV6DirectorProjectionNode;
  detail?: ResearchV6DirectorNodeDetail;
  loading: boolean;
  error: boolean;
  selectedForChat: boolean;
  onRetry: () => void;
  onReference: () => void;
}) {
  const { t } = useT("research");
  const state = detail?.node.state ?? node.state;
  const titleId = `research-v6-node-detail-title-${node.id}`;
  const canReference = Boolean(
    node.canonical_ref.revision && node.canonical_ref.content_hash,
  );
  const refs: Array<[string, number]> = detail
    ? ([
        [t(($) => $.v6_detail.agents), detail.agent_refs.length],
        [t(($) => $.v6_detail.work_items), detail.work_item_refs.length],
        [t(($) => $.v6_detail.attempts), detail.attempt_refs.length],
        [t(($) => $.v6_detail.evidence), detail.evidence_refs.length],
        [t(($) => $.v6_detail.discussions), detail.discussion_refs.length],
        [t(($) => $.v6_detail.reports), detail.report_refs.length],
      ] satisfies Array<[string, number]>).filter((entry) => entry[1] > 0)
    : [];

  return (
    <section
      className="min-w-0 space-y-5 p-4 text-foreground"
      aria-labelledby={titleId}
    >
      <header className="min-w-0 space-y-2">
        <div className="flex items-center gap-2 text-[11px] font-semibold text-muted-foreground">
          <span className="rounded-md bg-primary/10 px-1.5 py-0.5 text-primary">
            {node.tier}
          </span>
          <span>{node.kind}</span>
          <span aria-hidden="true">·</span>
          <span className="truncate">{node.canonical_ref.kind}</span>
        </div>
        <h2
          id={titleId}
          className="text-balance text-base font-semibold leading-snug"
        >
          {node.title ?? node.catalog_summary}
        </h2>
        {node.title ? (
          <p className="break-words text-sm leading-relaxed text-muted-foreground">
            {node.catalog_summary}
          </p>
        ) : null}
      </header>

      <dl className="grid grid-cols-1 gap-px overflow-hidden rounded-xl bg-border/70 sm:grid-cols-3">
        {[
          [t(($) => $.v6_detail.execution), state.execution],
          [t(($) => $.v6_detail.conclusion), state.conclusion],
          [t(($) => $.v6_detail.integration), state.integration],
        ].map(([label, value]) => (
          <div key={label} className="min-w-0 bg-card px-3 py-2.5">
            <dt className="text-[10px] font-medium text-muted-foreground">{label}</dt>
            <dd className="mt-1 truncate text-xs font-semibold">{value}</dd>
          </div>
        ))}
      </dl>

      {state.termination ? (
        <div className="space-y-1 rounded-xl bg-muted/45 px-3 py-2.5">
          <p className="text-xs font-semibold">{state.termination.reason_code}</p>
          <p className="break-words text-xs leading-relaxed text-muted-foreground">
            {state.termination.reason_detail}
          </p>
        </div>
      ) : null}

      {loading ? (
        <p className="text-xs text-muted-foreground" role="status">
          {t(($) => $.v6_detail.loading)}
        </p>
      ) : null}
      {error ? (
        <div className="flex items-center justify-between gap-3 rounded-xl bg-destructive/10 px-3 py-2.5">
          <p className="text-xs text-foreground">
            {t(($) => $.v6_detail.load_failed)}
          </p>
          <Button type="button" size="sm" variant="ghost" onClick={onRetry}>
            <RefreshCw className="size-3.5" aria-hidden="true" />
            {t(($) => $.v6_detail.retry)}
          </Button>
        </div>
      ) : null}

      {refs.length > 0 ? (
        <div className="space-y-2">
          <h3 className="flex items-center gap-2 text-xs font-semibold">
            <Link2 className="size-3.5 text-primary" aria-hidden="true" />
            {t(($) => $.v6_detail.linked_records)}
          </h3>
          <ul className="flex flex-wrap gap-1.5">
            {refs.map(([label, count]) => (
              <li
                key={label}
                className="rounded-lg bg-muted/55 px-2 py-1 text-[11px] text-muted-foreground"
              >
                {label} · {count}
              </li>
            ))}
          </ul>
        </div>
      ) : null}

      {node.branch_ids.length > 0 ? (
        <div className="space-y-2">
          <h3 className="flex items-center gap-2 text-xs font-semibold">
            <GitBranch className="size-3.5 text-primary" aria-hidden="true" />
            {t(($) => $.v6_detail.branches)}
          </h3>
          <p className="break-all text-[11px] leading-relaxed text-muted-foreground">
            {node.branch_ids.join(" · ")}
          </p>
        </div>
      ) : null}

      <div className="space-y-1.5 border-t border-border/70 pt-4">
        <Button
          type="button"
          className="w-full gap-2"
          variant={selectedForChat ? "secondary" : "default"}
          disabled={!canReference || selectedForChat}
          onClick={onReference}
        >
          <MessageSquareText className="size-4" aria-hidden="true" />
          {selectedForChat
            ? t(($) => $.v6_detail.referenced)
            : t(($) => $.v6_detail.reference_in_chat)}
        </Button>
        {!canReference ? (
          <p className="text-[10px] leading-relaxed text-muted-foreground">
            {t(($) => $.v6_detail.reference_unavailable)}
          </p>
        ) : null}
      </div>
    </section>
  );
}
