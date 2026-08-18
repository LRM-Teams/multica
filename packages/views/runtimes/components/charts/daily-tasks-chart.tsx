import { ChartTooltipContent } from "@multica/ui/components/ui/chart";
import { useT } from "../../../i18n";
import { tasksChartConfig } from "./usage-chart-configs";
import {
  ChartTotalFooter,
  UsageBarChart,
  type UsageBarSeries,
} from "./usage-bar-chart";

export interface DailyTasksData {
  date: string;
  label: string;
  completed: number;
  failed: number;
}

const tasksSeries = [
  { dataKey: "completed", stackId: "tasks" },
  { dataKey: "failed", stackId: "tasks", radius: [3, 3, 0, 0] },
] satisfies UsageBarSeries<DailyTasksData>[];

export function DailyTasksChart({ data }: { data: DailyTasksData[] }) {
  const { t } = useT("runtimes");

  return (
    <UsageBarChart
      config={tasksChartConfig}
      data={data}
      series={tasksSeries}
      yAxisWidth={40}
      allowDecimals={false}
      tooltip={
        <ChartTooltipContent
          formatter={(value, name) => `${value} ${name}`}
          footer={(payload) => (
            <ChartTotalFooter
              label={t(($) => $.charts.tooltip_total)}
              payload={payload}
              formatTotal={(total) => total.toLocaleString()}
            />
          )}
        />
      }
    />
  );
}
