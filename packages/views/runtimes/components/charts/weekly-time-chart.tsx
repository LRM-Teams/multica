import { ChartTooltipContent } from "@multica/ui/components/ui/chart";
import { useT } from "../../../i18n";
import { timeChartConfig } from "./usage-chart-configs";
import {
  UsageBarChart,
  type UsageBarSeries,
} from "./usage-bar-chart";

export interface WeeklyTimeData {
  weekStart: string;
  weekEnd: string;
  label: string;
  rangeLabel: string;
  partial: boolean;
  daysCovered: number;
  totalSeconds: number;
}

const timeSeries = [
  { dataKey: "totalSeconds", radius: [3, 3, 0, 0] },
] satisfies UsageBarSeries<WeeklyTimeData>[];

export function WeeklyTimeChart({
  data,
  formatY,
  formatTooltip,
}: {
  data: WeeklyTimeData[];
  formatY: (seconds: number) => string;
  formatTooltip: (seconds: number) => string;
}) {
  const { t } = useT("usage");

  return (
    <UsageBarChart
      config={timeChartConfig}
      data={data}
      series={timeSeries}
      yAxisWidth={56}
      yTickFormatter={formatY}
      getCellKey={(datum) => datum.weekStart}
      getCellOpacity={(datum) => (datum.partial ? 0.5 : 1)}
      tooltip={
        <ChartTooltipContent
          labelKey="rangeLabel"
          labelFormatter={(_label, payload) => {
            const row = payload[0]?.payload as WeeklyTimeData | undefined;
            if (!row) return "";
            return row.partial
              ? t(($) => $.weekly.partial_label, {
                  range: row.rangeLabel,
                  covered: row.daysCovered,
                })
              : row.rangeLabel;
          }}
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
