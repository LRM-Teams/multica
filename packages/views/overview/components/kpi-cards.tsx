"use client";

import { useQuery } from "@tanstack/react-query";
import { TrendingUp, TrendingDown } from "lucide-react";
import { agentListOptions } from "@multica/core/workspace/queries";
import { issueStatusCountsOptions } from "@multica/core/issues/queries";
import { cn } from "@multica/ui/lib/utils";
import { Card, CardContent } from "@multica/ui/components/ui/card";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { useT } from "../../i18n";
import { PlaceholderBadge } from "./placeholder-badge";
import { MOCK_TRENDS, MOCK_SPEND, MOCK_BUDGET, MOCK_LONGEST_WAIT, type KpiTrend } from "../mock";

function KpiCard({
  label,
  value,
  detail,
  trend,
  mock,
  loading,
  vsYesterday,
}: {
  label: string;
  value: string;
  detail?: string;
  trend?: KpiTrend;
  mock?: boolean;
  loading?: boolean;
  vsYesterday: string;
}) {
  return (
    <Card size="sm">
      <CardContent className="flex flex-col gap-1">
        <div className="flex items-center justify-between gap-2">
          <span className="truncate text-xs text-muted-foreground">{label}</span>
          {mock ? <PlaceholderBadge /> : null}
        </div>
        {loading ? (
          <Skeleton className="h-8 w-16" />
        ) : (
          <span className="text-2xl font-semibold tabular-nums">{value}</span>
        )}
        {detail ? (
          <span className="truncate text-[11px] text-muted-foreground">{detail}</span>
        ) : null}
        {trend ? (
          <span
            className={cn(
              "inline-flex items-center gap-0.5 text-[11px]",
              trend.dir === "down" ? "text-warning" : "text-success",
            )}
          >
            {trend.dir === "down" ? (
              <TrendingDown className="size-3" />
            ) : (
              <TrendingUp className="size-3" />
            )}
            {trend.delta} {vsYesterday}
          </span>
        ) : null}
      </CardContent>
    </Card>
  );
}

/**
 * Five headline KPIs. Real: active-agent breakdown (agent list + status) and
 * task/approval counts (issue status totals). Mock: the spend card (no dollar
 * rollup) and the "vs yesterday" trend tags (no day-over-day series).
 */
export function KpiCards({ wsId }: { wsId: string }) {
  const { t } = useT("overview");
  const vsYesterday = t(($) => $.kpi.vs_yesterday);

  const { data: agents = [], isPending: agentsPending } = useQuery({
    ...agentListOptions(wsId),
    enabled: !!wsId,
  });
  const { data: counts, isPending: countsPending } = useQuery({
    ...issueStatusCountsOptions(wsId),
    enabled: !!wsId,
  });

  const live = agents.filter((a) => !a.archived_at);
  const total = live.length;
  const idle = live.filter((a) => a.status === "idle").length;
  const errored = live.filter((a) => a.status === "error").length;
  const working = live.filter((a) => a.status === "working").length;

  const done = counts?.done ?? 0;
  const blocked = counts?.blocked ?? 0;
  const inReview = counts?.in_review ?? 0;
  const totalIssues =
    (counts?.backlog ?? 0) +
    (counts?.todo ?? 0) +
    (counts?.in_progress ?? 0) +
    (counts?.in_review ?? 0) +
    done +
    blocked;
  const successRate = done + blocked > 0 ? Math.round((done / (done + blocked)) * 100) : 0;

  const cards = [
    {
      key: "active_agents",
      label: t(($) => $.kpi.active_agents),
      value: `${working} / ${total}`,
      detail: t(($) => $.kpi.active_agents_detail, { idle, error: errored }),
      loading: agentsPending,
    },
    {
      key: "tasks_done",
      label: t(($) => $.kpi.tasks_done),
      value: `${done} / ${totalIssues}`,
      detail: t(($) => $.kpi.tasks_blocked_detail, { count: blocked }),
      loading: countsPending,
    },
    {
      key: "success_rate",
      label: t(($) => $.kpi.success_rate),
      value: `${successRate}%`,
      loading: countsPending,
    },
    {
      key: "spend",
      label: t(($) => $.kpi.spend),
      value: MOCK_SPEND,
      detail: t(($) => $.kpi.budget_detail, { budget: MOCK_BUDGET }),
      mock: true,
    },
    {
      key: "pending_approval",
      label: t(($) => $.kpi.pending_approval),
      value: `${inReview}`,
      detail: t(($) => $.kpi.longest_wait_detail, { wait: MOCK_LONGEST_WAIT }),
      loading: countsPending,
    },
  ] as const;

  return (
    <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-5">
      {cards.map((c) => (
        <KpiCard
          key={c.key}
          label={c.label}
          value={c.value}
          detail={"detail" in c ? c.detail : undefined}
          trend={MOCK_TRENDS[c.key]}
          mock={"mock" in c ? c.mock : undefined}
          loading={"loading" in c ? c.loading : undefined}
          vsYesterday={vsYesterday}
        />
      ))}
    </div>
  );
}
