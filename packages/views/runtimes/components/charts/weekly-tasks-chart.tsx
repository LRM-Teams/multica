import { ChartTooltipContent } from "@multica/ui/components/ui/chart";
import { useT } from "../../../i18n";
import { tasksChartConfig } from "./usage-chart-configs";
import {
  ChartTotalFooter,
  UsageBarChart,
  type UsageBarSeries,
} from "./usage-bar-chart";

export interface WeeklyTasksData {
  weekStart: string;
  weekEnd: string;
  label: string;
  rangeLabel: string;
  partial: boolean;
  daysCovered: number;
  completed: number;
  failed: number;
}

const tasksSeries = [
  { dataKey: "completed", stackId: "tasks" },
  { dataKey: "failed", stackId: "tasks", radius: [3, 3, 0, 0] },
] satisfies UsageBarSeries<WeeklyTasksData>[];

export function WeeklyTasksChart({ data }: { data: WeeklyTasksData[] }) {
  const { t } = useT("usage");
  const { t: tRuntimes } = useT("runtimes");

  return (
    <UsageBarChart
      config={tasksChartConfig}
      data={data}
      series={tasksSeries}
      yAxisWidth={40}
      allowDecimals={false}
      getCellKey={(datum) => datum.weekStart}
      getCellOpacity={(datum) => (datum.partial ? 0.5 : 1)}
      tooltip={
        <ChartTooltipContent
          labelKey="rangeLabel"
          labelFormatter={(_label, payload) => {
            const row = payload[0]?.payload as WeeklyTasksData | undefined;
            if (!row) return "";
            return row.partial
              ? t(($) => $.weekly.partial_label, {
                  range: row.rangeLabel,
                  covered: row.daysCovered,
                })
              : row.rangeLabel;
          }}
          formatter={(value, name) => `${value} ${name}`}
          footer={(payload) => (
            <ChartTotalFooter
              label={tRuntimes(($) => $.charts.tooltip_total)}
              payload={payload}
              formatTotal={(total) => total.toLocaleString()}
            />
          )}
        />
      }
    />
  );
}
