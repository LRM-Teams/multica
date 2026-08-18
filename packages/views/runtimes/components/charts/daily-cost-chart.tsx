import { ChartTooltipContent } from "@multica/ui/components/ui/chart";
import type { DailyCostStackData } from "../../utils";
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
] satisfies UsageBarSeries<DailyCostStackData>[];

export function DailyCostChart({ data }: { data: DailyCostStackData[] }) {
  const { t } = useT("runtimes");

  return (
    <UsageBarChart
      config={costStackConfig}
      data={data}
      series={costSeries}
      yAxisWidth={50}
      yTickFormatter={(value) => `$${value}`}
      tooltip={
        <ChartTooltipContent
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
