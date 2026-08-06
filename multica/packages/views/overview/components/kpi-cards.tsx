"use client";

import { useQuery } from "@tanstack/react-query";
import { TrendingUp, TrendingDown } from "lucide-react";
import { agentListOptions } from "@multica/core/workspace/queries";
import { agentTaskStatsOptions } from "@multica/core/agents/queries";
import { issueStatusCountsOptions, issueReviewStatsOptions } from "@multica/core/issues/queries";
import { dashboardUsageDailyOptions } from "@multica/core/dashboard";
import { useCustomPricingStore } from "@multica/core/runtimes/custom-pricing-store";
import { cn } from "@multica/ui/lib/utils";
import { Card, CardContent } from "@multica/ui/components/ui/card";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { useViewingTimezone } from "../../common/use-viewing-timezone";
import { computeDailyTotals } from "../../dashboard/utils";
import { todayIso, addDaysIso } from "../../runtimes/utils";
import { useT } from "../../i18n";
import { MOCK_TRENDS, type KpiTrend } from "../mock";

// Compact human duration for the longest-approval-wait detail: "42min" /
// "3h 5min" / "2d 4h". Matches the terse unit style the design used.
function formatWait(totalSeconds: number): string {
  const mins = Math.floor(totalSeconds / 60);
  if (mins < 60) return `${mins}min`;
  const hours = Math.floor(mins / 60);
  if (hours < 24) {
    const m = mins % 60;
    return m > 0 ? `${hours}h ${m}min` : `${hours}h`;
  }
  const days = Math.floor(hours / 24);
  const h = hours % 24;
  return h > 0 ? `${days}d ${h}h` : `${days}d`;
}

function KpiCard({
  label,
  value,
  detail,
  trend,
  loading,
  vsYesterday,
}: {
  label: string;
  value: string;
  detail?: string;
  trend?: KpiTrend;
  loading?: boolean;
  vsYesterday: string;
}) {
  return (
    <Card size="sm">
      <CardContent className="flex flex-col gap-1">
        <span className="truncate text-xs text-muted-foreground">{label}</span>
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

interface KpiCardData {
  key: string;
  label: string;
  value: string;
  detail?: string;
  loading?: boolean;
  trend?: KpiTrend;
}

/**
 * Five headline KPIs. Real: active-agent breakdown (agent list + status),
 * task/approval counts (issue status totals), and spend — today's cost derived
 * from the usage dashboard's daily series (same estimateCost pricing as the
 * Usage page) with a real vs-yesterday delta. Mock: the remaining "vs yesterday"
 * trend tags (agents / success rate) and the pending-approval longest-wait.
 */
export function KpiCards({ wsId }: { wsId: string }) {
  const { t } = useT("overview");
  const vsYesterday = t(($) => $.kpi.vs_yesterday);

  const { data: agents = [], isPending: agentsPending } = useQuery({
    ...agentListOptions(wsId),
    enabled: !!wsId,
  });
  const { data: counts } = useQuery({
    ...issueStatusCountsOptions(wsId),
    enabled: !!wsId,
  });
  const { data: reviewStats, isPending: reviewPending } = useQuery({
    ...issueReviewStatsOptions(wsId),
    enabled: !!wsId,
  });
  const { data: taskStats, isPending: taskStatsPending } = useQuery({
    ...agentTaskStatsOptions(wsId),
    enabled: !!wsId,
  });

  // Spend = today's cost, derived from the usage dashboard's daily series via
  // the same estimateCost pricing the Usage page uses. Two days are fetched so
  // the "vs yesterday" delta is real too.
  const tz = useViewingTimezone();
  // Subscribe so cost re-derives when the user edits custom model pricing.
  useCustomPricingStore((s) => s.pricings);
  const { data: usage = [], isPending: usagePending } = useQuery({
    ...dashboardUsageDailyOptions(wsId, 2, null, tz),
    enabled: !!wsId,
  });

  const live = agents.filter((a) => !a.archived_at);
  const total = live.length;
  const idle = live.filter((a) => a.status === "idle").length;
  const errored = live.filter((a) => a.status === "error").length;
  const working = live.filter((a) => a.status === "working").length;

  const inReview = counts?.in_review ?? 0;

  // Task success rate mirrors the adjacent "tasks done" card: it's about agent
  // TASKS (issue runs, comment runs, AND channel replies — whatever
  // agentTaskStats counts), not issue resolution. Computing it from issue
  // status counts read 0% in workspaces that only run group-chat tasks (no
  // issues), even with every task completed. Denominator is decided tasks
  // (completed + failed) so it reconciles with that card.
  const tasksCompleted = taskStats?.completed ?? 0;
  const tasksFailed = taskStats?.failed ?? 0;
  const tasksDecided = tasksCompleted + tasksFailed;
  const successRate = tasksDecided > 0 ? Math.round((tasksCompleted / tasksDecided) * 100) : 0;

  const todayDate = todayIso(tz);
  const yesterdayDate = addDaysIso(todayDate, -1);
  const todaySpend = computeDailyTotals(usage.filter((u) => u.date === todayDate)).cost;
  const yesterdaySpend = computeDailyTotals(usage.filter((u) => u.date === yesterdayDate)).cost;
  const spendDelta = todaySpend - yesterdaySpend;
  const spendTrend: KpiTrend | undefined =
    usage.length > 0
      ? {
          delta: `${spendDelta >= 0 ? "+" : "-"}$${Math.abs(spendDelta).toFixed(2)}`,
          dir: spendDelta >= 0 ? "up" : "down",
        }
      : undefined;

  const cards: KpiCardData[] = [
    {
      key: "active_agents",
      label: t(($) => $.kpi.active_agents),
      value: `${working} / ${total}`,
      detail: t(($) => $.kpi.active_agents_detail, { idle, error: errored }),
      loading: agentsPending,
      trend: MOCK_TRENDS.active_agents,
    },
    {
      key: "tasks_done",
      label: t(($) => $.kpi.tasks_done),
      // Denominator is decided tasks (completed + failed) so it reconciles with
      // the failed detail; queued/running/cancelled are excluded.
      value: `${tasksCompleted} / ${tasksDecided}`,
      detail: t(($) => $.kpi.tasks_failed_detail, { count: tasksFailed }),
      loading: taskStatsPending,
    },
    {
      key: "success_rate",
      label: t(($) => $.kpi.success_rate),
      value: `${successRate}%`,
      loading: taskStatsPending,
      trend: MOCK_TRENDS.success_rate,
    },
    {
      key: "spend",
      label: t(($) => $.kpi.spend),
      value: `$ ${todaySpend.toFixed(2)}`,
      loading: usagePending,
      trend: spendTrend,
    },
    {
      key: "pending_approval",
      label: t(($) => $.kpi.pending_approval),
      value: `${reviewStats?.count ?? inReview}`,
      detail:
        (reviewStats?.count ?? 0) > 0
          ? t(($) => $.kpi.longest_wait_detail, {
              wait: formatWait(reviewStats?.longest_wait_seconds ?? 0),
            })
          : undefined,
      loading: reviewPending,
    },
  ];

  return (
    <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-5">
      {cards.map((c) => (
        <KpiCard
          key={c.key}
          label={c.label}
          value={c.value}
          detail={c.detail}
          trend={c.trend}
          loading={c.loading}
          vsYesterday={vsYesterday}
        />
      ))}
    </div>
  );
}
