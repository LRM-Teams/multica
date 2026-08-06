"use client";

import { useQuery } from "@tanstack/react-query";
import { Cell, Pie, PieChart } from "recharts";
import { channelStatsOptions } from "@multica/core/channels";
import { useActorName } from "@multica/core/workspace/hooks";
import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from "@multica/ui/components/ui/chart";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { useT } from "../../i18n/use-t";
import { resolveChannelAuthorDisplayName } from "./message-preview";

const PALETTE = [
  "var(--chart-1)",
  "var(--chart-2)",
  "var(--chart-3)",
  "var(--chart-4)",
  "var(--chart-5)",
];

function Stat({ label, value }: { label: string; value: number }) {
  return (
    <div className="rounded-lg border bg-muted/30 px-2 py-2 text-center">
      <div className="text-lg font-semibold tabular-nums">{value}</div>
      <div className="text-[11px] text-muted-foreground">{label}</div>
    </div>
  );
}

/**
 * Channel activity summary: message / file / member totals plus a donut of
 * messages-per-author (each participant in its own slice color).
 */
export function ChannelStatsPanel({ channelId }: { channelId: string }) {
  const { t } = useT("channels");
  const { getActorName } = useActorName();
  const { data, isPending, isError } = useQuery(channelStatsOptions(channelId));

  if (isPending) {
    return <Skeleton className="h-44" />;
  }
  if (isError || !data) {
    return (
      <p className="py-6 text-center text-xs text-muted-foreground">
        {isError ? t(($) => $.stats.error) : t(($) => $.stats.empty)}
      </p>
    );
  }

  const pieData = data.by_author.map((a, i) => ({
    name: resolveChannelAuthorDisplayName(
      {
        type: a.author_type,
        author_id: a.author_id,
        author_name: a.author_name,
      },
      { getActorName },
    ),
    value: a.count,
    fill: PALETTE[i % PALETTE.length],
  }));

  return (
    <div className="space-y-3">
      <div className="grid grid-cols-3 gap-2">
        <Stat label={t(($) => $.stats.messages)} value={data.total_messages} />
        <Stat label={t(($) => $.stats.files)} value={data.file_count} />
        <Stat label={t(($) => $.stats.members)} value={data.member_count} />
      </div>

      {pieData.length > 0 && (
        <>
          <p className="text-xs font-medium text-muted-foreground">{t(($) => $.stats.by_author)}</p>
          <ChartContainer config={{} as ChartConfig} className="mx-auto aspect-square max-h-40">
            <PieChart>
              <ChartTooltip content={<ChartTooltipContent nameKey="name" />} />
              <Pie data={pieData} dataKey="value" nameKey="name" innerRadius={36} strokeWidth={2}>
                {pieData.map((d) => (
                  <Cell key={d.name} fill={d.fill} />
                ))}
              </Pie>
            </PieChart>
          </ChartContainer>
          <ul className="space-y-1">
            {pieData.map((d) => (
              <li key={d.name} className="flex items-center justify-between gap-2 text-xs">
                <span className="flex min-w-0 items-center gap-1.5">
                  <span className="size-2 shrink-0 rounded-full" style={{ backgroundColor: d.fill }} />
                  <span className="truncate">{d.name}</span>
                </span>
                <span className="shrink-0 tabular-nums text-muted-foreground">{d.value}</span>
              </li>
            ))}
          </ul>
        </>
      )}
    </div>
  );
}
