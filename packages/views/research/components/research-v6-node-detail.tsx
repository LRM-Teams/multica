"use client";

import type {
  ResearchV6DirectorEntityRef,
  ResearchV6DirectorNodeDetail,
  ResearchV6DirectorProjectionEdge,
  ResearchV6DirectorProjectionNode,
  ResearchV6DirectorWorkActivity,
} from "@multica/core/types/research-v6-director";
import type { RunnerActivityTimelineRow } from "@multica/core/types/events";
import { Button } from "@multica/ui/components/ui/button";
import { Tooltip, TooltipTrigger, TooltipContent } from "@multica/ui/components/ui/tooltip";
import {
  ArrowDownLeft,
  ArrowUpRight,
  GitBranch,
  History,
  LoaderCircle,
  Link2,
  LocateFixed,
  MessageSquareText,
  RefreshCw,
  TerminalSquare,
  Wrench,
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

/** Live executor caption for the selected node, derived from run presence. */
export type ResearchV6NodeLiveActivity = {
  /** Executor display name. */
  name: string;
  /** Latest live caption (progress note or protocol milestone). */
  activity: string;
  phase: "queued" | "running" | "stale";
  /** Unix ms of the latest live signal; null when undated. */
  updatedAt: number | null;
};

export function ResearchV6NodeDetail({
  node,
  detail,
  workActivity,
  workTimeline,
  workActivityLoading = false,
  workActivityError = false,
  loading,
  error,
  selectedForChat,
  projectionNodeById,
  liveActivity,
  onRetry,
  onRetryWorkActivity,
  onReference,
  onFocusNode,
}: {
  node: ResearchV6DirectorProjectionNode;
  detail?: ResearchV6DirectorNodeDetail;
  workActivity?: ResearchV6DirectorWorkActivity;
  workTimeline?: readonly RunnerActivityTimelineRow[];
  workActivityLoading?: boolean;
  workActivityError?: boolean;
  loading: boolean;
  error: boolean;
  selectedForChat: boolean;
  projectionNodeById: ReadonlyMap<string, ResearchV6DirectorProjectionNode>;
  liveActivity?: ResearchV6NodeLiveActivity | null;
  onRetry: () => void;
  onRetryWorkActivity?: () => void;
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
  const stateValueLabel = (value: string) => {
    const labels = t(($) => $.v6_detail.state_value, { returnObjects: true });
    return labels[value as keyof typeof labels] ?? value;
  };
  const activityTitleLabels = t(($) => $.v6_detail.activity_title, {
    returnObjects: true,
  });
  const activityTitleLabel = (title: string) => {
    const key = title
      .trim()
      .toLowerCase()
      .replace(/[.…]+$/u, "")
      .replace(/[^a-z0-9]+/g, "_");
    return (
      activityTitleLabels[key as keyof typeof activityTitleLabels] ??
      t(($) => $.v6_detail.processing)
    );
  };
  const workSteps = workTimeline ?? [];
  const workProgressPercent =
    workActivity && workActivity.progress_total > 0
      ? Math.min(
          100,
          Math.max(
            0,
            Math.round(
              (workActivity.progress_step / workActivity.progress_total) * 100,
            ),
          ),
        )
      : null;

  return (
    <section
      className="min-w-0 space-y-5 p-4 text-foreground"
      aria-labelledby={titleId}
    >
      <header className="min-w-0 space-y-2">
        <div className="flex items-center gap-2 text-xs font-medium text-muted-foreground">
          <span className="rounded-md bg-primary/10 px-1.5 py-0.5 text-primary">
            {node.tier}
          </span>
          <span>{node.kind}</span>
          <span aria-hidden="true">·</span>
          <span className="truncate">{node.canonical_ref.kind}</span>
        </div>
        <h2
          id={titleId}
          className="text-balance text-base font-medium leading-snug"
        >
          {node.title ?? node.catalog_summary}
        </h2>
        {node.title ? (
          <p className="break-words text-sm leading-relaxed text-muted-foreground">
            {node.catalog_summary}
          </p>
        ) : null}
      </header>

      {liveActivity ? (
        <div
          className="space-y-1 rounded-xl border border-primary/25 bg-primary/5 px-3 py-2.5"
          role="status"
          aria-live="polite"
        >
          <div className="flex items-center gap-2 text-[11px] font-semibold">
            <span
              className={
                liveActivity.phase === "stale"
                  ? "size-2 shrink-0 rounded-full bg-amber-500"
                  : "size-2 shrink-0 animate-pulse rounded-full bg-primary"
              }
              aria-hidden="true"
            />
            <span className="truncate text-primary">
              {t(($) => $.v6_detail.live_activity)} · {liveActivity.name}
            </span>
            {liveActivity.updatedAt != null ? (
              <span className="ml-auto shrink-0 font-normal text-muted-foreground">
                <Time
                  kind="relative"
                  value={new Date(liveActivity.updatedAt).toISOString()}
                />
              </span>
            ) : null}
          </div>
          <p className="break-words text-sm leading-relaxed text-foreground">
            {liveActivity.activity}
          </p>
          {liveActivity.phase === "stale" ? (
            <p className="text-[11px] leading-relaxed text-muted-foreground">
              {t(($) => $.v6_detail.live_activity_stale)}
            </p>
          ) : null}
        </div>
      ) : null}

      {node.kind === "work_s" ? (
        <section
          className="space-y-3 rounded-xl bg-primary/[0.06] p-3"
          aria-label={t(($) => $.v6_detail.work_activity)}
        >
          <div className="flex items-center gap-3">
            <span className="flex size-9 shrink-0 items-center justify-center rounded-full bg-primary/15 text-xs font-medium text-primary">
              {(workActivity?.agent_name || node.title || "A").slice(0, 1).toUpperCase()}
            </span>
            <div className="min-w-0 flex-1">
              <div className="flex items-center gap-2">
                <p className="truncate text-sm font-medium">
                  {workActivity?.agent_name || node.title || t(($) => $.v6_detail.agent_unknown)}
                </p>
                {state.execution === "running" ? (
                  <LoaderCircle className="size-3.5 shrink-0 animate-spin text-primary motion-reduce:animate-none" aria-hidden="true" />
                ) : null}
              </div>
              <p className="text-xs text-muted-foreground">
                {t(($) => $.v6_detail.work_status, { status: stateValueLabel(state.execution) })}
              </p>
            </div>
          </div>
          <div>
            <h3 className="text-xs font-medium text-foreground">
              {t(($) => $.v6_detail.current_task)}
            </h3>
            <p className="mt-1 break-words text-xs leading-relaxed text-muted-foreground">
              {workActivityLoading
                ? t(($) => $.v6_detail.work_activity_loading)
                : workActivityError
                  ? t(($) => $.v6_detail.work_activity_failed)
                  : workActivity?.mission ||
                    node.catalog_summary ||
                    t(($) => $.v6_detail.task_waiting)}
            </p>
          </div>
          {workActivity?.progress ? (
            <div className="rounded-lg bg-background/65 px-2.5 py-2">
              <div className="flex items-center justify-between gap-2 text-xs font-medium text-muted-foreground">
                <p>{t(($) => $.v6_detail.latest_progress)}</p>
                {workProgressPercent !== null ? <span>{workProgressPercent}%</span> : null}
              </div>
              {workProgressPercent !== null ? (
                <div
                  className="mt-2 h-1.5 overflow-hidden rounded-full bg-muted"
                  role="progressbar"
                  aria-label={t(($) => $.v6_detail.latest_progress)}
                  aria-valuemin={0}
                  aria-valuemax={100}
                  aria-valuenow={workProgressPercent}
                >
                  <span
                    className="block h-full rounded-full bg-primary transition-[width] motion-reduce:transition-none"
                    style={{ width: `${workProgressPercent}%` }}
                  />
                </div>
              ) : null}
              <p className="mt-1 text-xs leading-relaxed">{workActivity.progress}</p>
            </div>
          ) : null}
          <div className="space-y-2" aria-live="polite" aria-atomic="false">
            <h3 className="text-xs font-medium text-foreground">
              {t(($) => $.v6_detail.work_process)}
            </h3>
            {workActivityLoading ? (
              <p className="text-xs leading-relaxed text-muted-foreground" role="status">
                {t(($) => $.v6_detail.work_activity_loading)}
              </p>
            ) : workActivityError ? (
              <div className="flex items-center justify-between gap-3 rounded-lg bg-destructive/10 px-2.5 py-2" role="alert">
                <p className="text-xs leading-relaxed text-foreground">
                  {t(($) => $.v6_detail.work_activity_failed)}
                </p>
                {onRetryWorkActivity ? (
                  <Button type="button" size="sm" variant="ghost" onClick={onRetryWorkActivity}>
                    <RefreshCw className="size-3.5" aria-hidden="true" />
                    {t(($) => $.v6_detail.retry_activity)}
                  </Button>
                ) : null}
              </div>
            ) : workSteps.length > 0 ? (
              <ol className="space-y-1.5">
                {workSteps.map((activity) => {
                  const Icon = activity.body_kind === "command" ? Wrench : TerminalSquare;
                  const detailText = activity.subtext || activity.body;
                  return (
                    <li key={activity.id} className="flex gap-2 rounded-lg bg-background/55 px-2.5 py-2">
                      <Icon className="mt-0.5 size-3.5 shrink-0 text-primary" aria-hidden="true" />
                      <div className="min-w-0">
                        <p className="text-xs font-medium leading-relaxed">
                          {activityTitleLabel(activity.title)}
                        </p>
                        {detailText ? (
                          <p className="line-clamp-3 break-words text-xs leading-relaxed text-muted-foreground">
                            {detailText}
                          </p>
                        ) : null}
                      </div>
                    </li>
                  );
                })}
              </ol>
            ) : (
              <p className="text-xs leading-relaxed text-muted-foreground" role="status">
                {state.execution === "running"
                  ? t(($) => $.v6_detail.process_waiting)
                  : t(($) => $.v6_detail.process_empty)}
              </p>
            )}
          </div>
        </section>
      ) : null}

      <section className="space-y-2" aria-label={t(($) => $.v6_detail.projection_state)}>
      <h3 className="text-xs font-medium">{t(($) => $.v6_detail.projection_state)}</h3>
      <dl className="grid grid-cols-1 gap-px overflow-hidden rounded-xl bg-border/70 sm:grid-cols-3">
        {[
          [t(($) => $.v6_detail.execution), stateValueLabel(state.execution)],
          [t(($) => $.v6_detail.conclusion), stateValueLabel(state.conclusion)],
          [t(($) => $.v6_detail.integration), stateValueLabel(state.integration)],
        ].map(([label, value]) => (
          <div key={label} className="min-w-0 bg-card px-3 py-2.5">
            <dt className="text-xs font-medium text-muted-foreground">{label}</dt>
            <dd className="mt-1 truncate text-xs font-medium">{value}</dd>
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
            <dt className="text-xs font-medium text-muted-foreground">{label}</dt>
            <dd className="mt-1 truncate text-xs font-medium">{value}</dd>
          </div>
        ))}
      </dl>
      </section>

      <dl className="grid grid-cols-2 gap-x-4 gap-y-3 border-y border-border/70 py-3 text-xs">
        <div className="min-w-0">
          <dt className="text-xs font-medium text-muted-foreground">
            {t(($) => $.v6_detail.source)}
          </dt>
          <Tooltip>
            <TooltipTrigger render={<dd className="mt-0.5 truncate font-medium" />}>
              {node.canonical_ref.kind} · {node.canonical_ref.id}
            </TooltipTrigger>
            <TooltipContent side="top">{node.canonical_ref.id}</TooltipContent>
          </Tooltip>
        </div>
        <div className="min-w-0">
          <dt className="text-xs font-medium text-muted-foreground">
            {t(($) => $.v6_detail.version)}
          </dt>
          <dd className="mt-0.5 truncate font-medium tabular-nums">
            {node.canonical_ref.revision
              ? `r${node.canonical_ref.revision}`
              : node.canonical_ref.version_id ?? t(($) => $.v6_detail.current)}
          </dd>
        </div>
        <div className="min-w-0">
          <dt className="text-xs font-medium text-muted-foreground">
            {t(($) => $.v6_detail.updated_at)}
          </dt>
          <dd className="mt-0.5 truncate font-medium tabular-nums">
            <Time kind="full" value={node.updated_at} />
          </dd>
        </div>
        <div className="min-w-0">
          <dt className="text-xs font-medium text-muted-foreground">
            {t(($) => $.v6_detail.content_hash)}
          </dt>
          <Tooltip>
            <TooltipTrigger
              render={<dd className="mt-0.5 truncate font-mono text-xs" />}
            >
              {node.canonical_ref.content_hash ?? t(($) => $.v6_detail.unavailable)}
            </TooltipTrigger>
            <TooltipContent side="top">{node.canonical_ref.content_hash}</TooltipContent>
          </Tooltip>
        </div>
      </dl>

      {state.termination ? (
        <div className="space-y-1 rounded-xl bg-muted/45 px-3 py-2.5">
          <p className="text-xs font-medium">{state.termination.reason_code}</p>
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
          <h3 className="flex items-center gap-2 text-xs font-medium">
            <Link2 className="size-3.5 text-primary" aria-hidden="true" />
            {t(($) => $.v6_detail.linked_records)}
          </h3>
          <ul className="flex flex-wrap gap-1.5">
            {refs.map(([label, count]) => (
              <li
                key={label}
                className="rounded-lg bg-muted/55 px-2 py-1 text-xs text-muted-foreground"
              >
                {label} · {count}
              </li>
            ))}
          </ul>
          <div className="space-y-1.5">
            {recordGroups.map(([label, records]) => (
              <details key={label} className="rounded-lg bg-muted/35 px-2.5 py-2">
                <summary className="cursor-pointer text-xs font-medium focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
                  {label} · {records.length}
                </summary>
                <ul className="mt-2 max-h-40 space-y-1 overflow-y-auto overscroll-contain">
                  {records.map((reference) => (
                    <li
                      key={`${reference.kind}:${reference.id}:${reference.revision ?? reference.version_id ?? "current"}`}
                      className="flex min-w-0 items-center justify-between gap-2 text-xs text-muted-foreground"
                    >
                      <Tooltip>
                        <TooltipTrigger render={<span className="truncate" />}>
                          {reference.kind} · {reference.id}
                        </TooltipTrigger>
                        <TooltipContent side="top">{`${reference.kind}:${reference.id}`}</TooltipContent>
                      </Tooltip>
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
          <h3 className="flex items-center gap-2 text-xs font-medium">
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
                      <span className="block truncate text-xs text-muted-foreground">
                        {relationLabel(edge.kind)}
                      </span>
                    </span>
                    {relatedNode ? (
                      <span className="shrink-0 text-xs font-medium text-primary">
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
          <h3 className="flex items-center gap-2 text-xs font-medium">
            <History className="size-3.5 text-primary" aria-hidden="true" />
            {t(($) => $.v6_detail.history)}
          </h3>
          <ul className="flex flex-wrap gap-1.5">
            {detail.history_refs.map((reference) => (
              <Tooltip
                key={`${reference.kind}:${reference.id}:${reference.revision ?? reference.version_id ?? "current"}`}
              >
                <TooltipTrigger
                  render={
                    <li className="max-w-full rounded-lg bg-muted/55 px-2 py-1 text-xs text-muted-foreground" />
                  }
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
                </TooltipTrigger>
                <TooltipContent side="top">{`${reference.kind}:${reference.id}`}</TooltipContent>
              </Tooltip>
            ))}
          </ul>
        </div>
      ) : null}

      {node.branch_ids.length > 0 ? (
        <div className="space-y-2">
          <h3 className="flex items-center gap-2 text-xs font-medium">
            <GitBranch className="size-3.5 text-primary" aria-hidden="true" />
            {t(($) => $.v6_detail.branches)}
          </h3>
          <p className="break-all text-xs leading-relaxed text-muted-foreground">
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
          <p className="text-xs leading-relaxed text-muted-foreground">
            {t(($) => $.v6_detail.reference_unavailable)}
          </p>
        ) : null}
      </div>
    </section>
  );
}
