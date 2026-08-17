import { ChartTooltipContent } from "@multica/ui/components/ui/chart";
import type { WeeklyCostStackData } from "../../utils";
import { useT } from "../../../i18n";
import { costStackConfig } from "./usage-chart-configs";
import {
  ChartTotalFooter,
  UsageBarChart,
  type UsageBarSeries,
} from "./usage-bar-chart";

const costSeries = [
  { dataKey: "input", stackId: "cost" },
  { dataKey: "output", stackId: "cost" },
  { dataKey: "cacheWrite", stackId: "cost", radius: [3, 3, 0, 0] },
] satisfies UsageBarSeries<WeeklyCostStackData>[];

export function WeeklyCostChart({ data }: { data: WeeklyCostStackData[] }) {
  const { t } = useT("runtimes");

  return (
    <UsageBarChart
      config={costStackConfig}
      data={data}
      series={costSeries}
      yAxisWidth={50}
      yTickFormatter={(value) => `$${value}`}
      getCellKey={(datum) => datum.weekStart}
      getCellOpacity={(datum) => (datum.partial ? 0.5 : 1)}
      tooltip={
        <ChartTooltipContent
          labelKey="rangeLabel"
          labelFormatter={(_label, payload) => {
            const row = payload[0]?.payload as
              | WeeklyCostStackData
              | undefined;
            if (!row) return "";
            return row.partial
              ? t(($) => $.usage.weekly_partial_label, {
                  range: row.rangeLabel,
                  covered: row.daysCovered,
                })
              : row.rangeLabel;
          }}
          formatter={(value, name) =>
            typeof value === "number"
              ? `$${value.toFixed(2)} ${name}`
              : `${value} ${name}`
          }
          footer={(payload) => (
            <ChartTotalFooter
              label={t(($) => $.charts.tooltip_total)}
              payload={payload}
              formatTotal={(total) => `$${total.toFixed(2)}`}
            />
          )}
        />
      }
    />
  );
}
