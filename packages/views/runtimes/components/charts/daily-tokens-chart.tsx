import { ChartTooltipContent } from "@multica/ui/components/ui/chart";
import { formatTokens, type DailyTokenData } from "../../utils";
import { useT } from "../../../i18n";
import { tokenStackConfig } from "./usage-chart-configs";
import {
  ChartTotalFooter,
  UsageBarChart,
  type UsageBarSeries,
} from "./usage-bar-chart";

const tokenSeries = [
  { dataKey: "input", stackId: "tokens" },
  { dataKey: "output", stackId: "tokens" },
  { dataKey: "cacheRead", stackId: "tokens" },
  { dataKey: "cacheWrite", stackId: "tokens", radius: [3, 3, 0, 0] },
] satisfies UsageBarSeries<DailyTokenData>[];

export function DailyTokensChart({ data }: { data: DailyTokenData[] }) {
  const { t } = useT("runtimes");

  return (
    <UsageBarChart
      config={tokenStackConfig}
      data={data}
      series={tokenSeries}
      yAxisWidth={50}
      yTickFormatter={formatTokens}
      tooltip={
        <ChartTooltipContent
          formatter={(value, name) =>
            typeof value === "number"
              ? `${formatTokens(value)} ${name}`
              : `${value} ${name}`
          }
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
