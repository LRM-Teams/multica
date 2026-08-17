import { ChartTooltipContent } from "@multica/ui/components/ui/chart";
import { timeChartConfig } from "./usage-chart-configs";
import {
  UsageBarChart,
  type UsageBarSeries,
} from "./usage-bar-chart";

export interface DailyTimeData {
  date: string;
  label: string;
  totalSeconds: number;
}

const timeSeries = [
  { dataKey: "totalSeconds", radius: [3, 3, 0, 0] },
] satisfies UsageBarSeries<DailyTimeData>[];

export function DailyTimeChart({
  data,
  formatY,
  formatTooltip,
}: {
  data: DailyTimeData[];
  formatY: (seconds: number) => string;
  formatTooltip: (seconds: number) => string;
}) {
  return (
    <UsageBarChart
      config={timeChartConfig}
      data={data}
      series={timeSeries}
      yAxisWidth={56}
      yTickFormatter={formatY}
      tooltip={
        <ChartTooltipContent
          formatter={(value, name) =>
            typeof value === "number"
              ? `${formatTooltip(value)} ${name}`
              : `${value} ${name}`
          }
        />
      }
    />
  );
}
