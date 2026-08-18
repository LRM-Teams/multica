"use client";

import type {
  ResearchV6DirectorEntityRef,
  ResearchV6DirectorNodeDetail,
  ResearchV6DirectorProjectionEdge,
  ResearchV6DirectorProjectionNode,
} from "@multica/core/types/research-v6-director";
import { Button } from "@multica/ui/components/ui/button";
import {
  ArrowDownLeft,
  ArrowUpRight,
  GitBranch,
  History,
  Link2,
  LocateFixed,
  MessageSquareText,
  RefreshCw,
} from "lucide-react";
import { useT } from "../../i18n/use-t";
import { Time } from "../../i18n/time";

function relatedNodeId(
  edge: ResearchV6DirectorProjectionEdge,
  selectedNodeId: string,
): string {
  return edge.from_node_id === selectedNodeId
    ? edge.to_node_id
    : edge.from_node_id;
}

export function ResearchV6NodeDetail({
  node,
  detail,
  loading,
  error,
  selectedForChat,
  projectionNodeById,
  onRetry,
  onReference,
  onFocusNode,
}: {
  node: ResearchV6DirectorProjectionNode;
  detail?: ResearchV6DirectorNodeDetail;
  loading: boolean;
  error: boolean;
  selectedForChat: boolean;
  projectionNodeById: ReadonlyMap<string, ResearchV6DirectorProjectionNode>;
  onRetry: () => void;
  onReference: () => void;
  onFocusNode: (nodeId: string) => void;
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
  const recordGroups: Array<[string, ResearchV6DirectorEntityRef[]]> = detail
    ? ([
        [t(($) => $.v6_detail.agents), detail.agent_refs],
        [t(($) => $.v6_detail.work_items), detail.work_item_refs],
        [t(($) => $.v6_detail.attempts), detail.attempt_refs],
        [t(($) => $.v6_detail.evidence), detail.evidence_refs],
        [t(($) => $.v6_detail.discussions), detail.discussion_refs],
        [t(($) => $.v6_detail.reports), detail.report_refs],
      ] satisfies Array<[string, ResearchV6DirectorEntityRef[]]>).filter(
        (entry) => entry[1].length > 0,
      )
    : [];
  const relations = detail
    ? [
        ...detail.incoming.map((edge) => ({ edge, direction: "incoming" as const })),
        ...detail.outgoing.map((edge) => ({ edge, direction: "outgoing" as const })),
      ]
    : [];
  const relationLabel = (kind: string) => {
    const labels = t(($) => $.v6_detail.relation_kind, { returnObjects: true });
    return labels[kind as keyof typeof labels] ?? kind;
  };

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

      <dl className="grid grid-cols-2 gap-px overflow-hidden rounded-xl bg-border/70 sm:grid-cols-4">
        {[
          [t(($) => $.v6_detail.absorbed), node.absorbed ? t(($) => $.v6_detail.yes) : t(($) => $.v6_detail.no)],
          [t(($) => $.v6_detail.terminal), node.terminal ? t(($) => $.v6_detail.yes) : t(($) => $.v6_detail.no)],
          [t(($) => $.v6_detail.expandable), node.expandable ? t(($) => $.v6_detail.yes) : t(($) => $.v6_detail.no)],
          [t(($) => $.v6_detail.hidden_children), String(node.hidden_child_count)],
        ].map(([label, value]) => (
          <div key={label} className="min-w-0 bg-card px-3 py-2.5">
            <dt className="text-[10px] font-medium text-muted-foreground">{label}</dt>
            <dd className="mt-1 truncate text-xs font-semibold">{value}</dd>
          </div>
        ))}
      </dl>

      <dl className="grid grid-cols-2 gap-x-4 gap-y-3 border-y border-border/70 py-3 text-xs">
        <div className="min-w-0">
          <dt className="text-[10px] font-medium text-muted-foreground">
            {t(($) => $.v6_detail.source)}
          </dt>
          <dd className="mt-0.5 truncate font-medium" title={node.canonical_ref.id}>
            {node.canonical_ref.kind} · {node.canonical_ref.id}
          </dd>
        </div>
        <div className="min-w-0">
          <dt className="text-[10px] font-medium text-muted-foreground">
            {t(($) => $.v6_detail.version)}
          </dt>
          <dd className="mt-0.5 truncate font-medium tabular-nums">
            {node.canonical_ref.revision
              ? `r${node.canonical_ref.revision}`
              : node.canonical_ref.version_id ?? t(($) => $.v6_detail.current)}
          </dd>
        </div>
        <div className="min-w-0">
          <dt className="text-[10px] font-medium text-muted-foreground">
            {t(($) => $.v6_detail.updated_at)}
          </dt>
          <dd className="mt-0.5 truncate font-medium tabular-nums">
            <Time kind="full" value={node.updated_at} />
          </dd>
        </div>
        <div className="min-w-0">
          <dt className="text-[10px] font-medium text-muted-foreground">
            {t(($) => $.v6_detail.content_hash)}
          </dt>
          <dd
            className="mt-0.5 truncate font-mono text-[10px]"
            title={node.canonical_ref.content_hash}
          >
            {node.canonical_ref.content_hash ?? t(($) => $.v6_detail.unavailable)}
          </dd>
        </div>
      </dl>

      {state.termination ? (
        <div className="space-y-1 rounded-xl bg-muted/45 px-3 py-2.5">
          <p className="text-xs font-semibold">{state.termination.reason_code}</p>
          <p className="break-words text-xs leading-relaxed text-muted-foreground">
            {state.termination.reason_detail}
          </p>
        </div>
      ) : null}

      <section className="space-y-2" aria-label={t(($) => $.v6_detail.projection_state)}>
        <h3 className="text-xs font-semibold">{t(($) => $.v6_detail.projection_state)}</h3>
        <dl className="grid grid-cols-2 gap-px overflow-hidden rounded-xl bg-border/70 sm:grid-cols-4">
        {[
          [t(($) => $.v6_detail.absorbed), node.absorbed ? t(($) => $.v6_detail.yes) : t(($) => $.v6_detail.no)],
          [t(($) => $.v6_detail.terminal), node.terminal ? t(($) => $.v6_detail.yes) : t(($) => $.v6_detail.no)],
          [t(($) => $.v6_detail.expandable), node.expandable ? t(($) => $.v6_detail.yes) : t(($) => $.v6_detail.no)],
          [t(($) => $.v6_detail.hidden_children), String(node.hidden_child_count)],
        ].map(([label, value]) => (
          <div key={label} className="min-w-0 bg-card px-3 py-2.5">
            <dt className="text-[10px] font-medium text-muted-foreground">{label}</dt>
            <dd className="mt-1 truncate text-xs font-semibold">{value}</dd>
          </div>
        ))}
        </dl>
      </section>

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
          <div className="space-y-1.5">
            {recordGroups.map(([label, records]) => (
              <details key={label} className="rounded-lg bg-muted/35 px-2.5 py-2">
                <summary className="cursor-pointer text-[11px] font-medium focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
                  {label} · {records.length}
                </summary>
                <ul className="mt-2 max-h-40 space-y-1 overflow-y-auto overscroll-contain">
                  {records.map((reference) => (
                    <li
                      key={`${reference.kind}:${reference.id}:${reference.revision ?? reference.version_id ?? "current"}`}
                      className="flex min-w-0 items-center justify-between gap-2 text-[10px] text-muted-foreground"
                    >
                      <span className="truncate" title={`${reference.kind}:${reference.id}`}>
                        {reference.kind} · {reference.id}
                      </span>
                      <span className="shrink-0 tabular-nums">
                        {reference.revision
                          ? `r${reference.revision}`
                          : reference.version_id ?? t(($) => $.v6_detail.current)}
                      </span>
                    </li>
                  ))}
                </ul>
              </details>
            ))}
          </div>
        </div>
      ) : null}

      {relations.length > 0 ? (
        <div className="space-y-2">
          <h3 className="flex items-center gap-2 text-xs font-semibold">
            <LocateFixed className="size-3.5 text-primary" aria-hidden="true" />
            {t(($) => $.v6_detail.relationships)}
          </h3>
          <ul className="space-y-1.5">
            {relations.map(({ edge, direction }) => {
              const relatedId = relatedNodeId(edge, node.id);
              const relatedNode = projectionNodeById.get(relatedId);
              const DirectionIcon =
                direction === "incoming" ? ArrowDownLeft : ArrowUpRight;
              return (
                <li key={`${direction}:${edge.id}`}>
                  <button
                    type="button"
                    className="flex min-h-10 w-full items-center gap-2 rounded-lg bg-muted/45 px-2.5 py-2 text-left transition-colors hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                    disabled={!relatedNode}
                    onClick={() => onFocusNode(relatedId)}
                  >
                    <DirectionIcon
                      className="size-3.5 shrink-0 text-primary"
                      aria-hidden="true"
                    />
                    <span className="min-w-0 flex-1">
                      <span className="block truncate text-xs font-medium">
                        {relatedNode?.title ??
                          relatedNode?.catalog_summary ??
                          t(($) => $.v6_detail.related_node_unavailable)}
                      </span>
                      <span className="block truncate text-[10px] text-muted-foreground">
                        {relationLabel(edge.kind)}
                      </span>
                    </span>
                    {relatedNode ? (
                      <span className="shrink-0 text-[10px] font-semibold text-primary">
                        {relatedNode.tier}
                      </span>
                    ) : null}
                  </button>
                </li>
              );
            })}
          </ul>
        </div>
      ) : null}

      {detail && detail.history_refs.length > 0 ? (
        <div className="space-y-2">
          <h3 className="flex items-center gap-2 text-xs font-semibold">
            <History className="size-3.5 text-primary" aria-hidden="true" />
            {t(($) => $.v6_detail.history)}
          </h3>
          <ul className="flex flex-wrap gap-1.5">
            {detail.history_refs.map((reference) => (
              <li
                key={`${reference.kind}:${reference.id}:${reference.revision ?? reference.version_id ?? "current"}`}
                className="max-w-full rounded-lg bg-muted/55 px-2 py-1 text-[11px] text-muted-foreground"
                title={`${reference.kind}:${reference.id}`}
              >
                <span>{reference.kind}</span>
                {reference.revision ? (
                  <span>
                    {t(($) => $.v6_detail.revision, {
                      revision: reference.revision,
                    })}
                  </span>
                ) : reference.version_id ? (
                  <span> · {reference.version_id}</span>
                ) : null}
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
