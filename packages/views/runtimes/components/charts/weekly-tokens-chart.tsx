import { ChartTooltipContent } from "@multica/ui/components/ui/chart";
import { formatTokens, type WeeklyTokenData } from "../../utils";
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
] satisfies UsageBarSeries<WeeklyTokenData>[];

export function WeeklyTokensChart({ data }: { data: WeeklyTokenData[] }) {
  const { t } = useT("runtimes");

  return (
    <UsageBarChart
      config={tokenStackConfig}
      data={data}
      series={tokenSeries}
      yAxisWidth={50}
      yTickFormatter={formatTokens}
      getCellKey={(datum) => datum.weekStart}
      getCellOpacity={(datum) => (datum.partial ? 0.5 : 1)}
      tooltip={
        <ChartTooltipContent
          labelKey="rangeLabel"
          labelFormatter={(_label, payload) => {
            const row = payload[0]?.payload as WeeklyTokenData | undefined;
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
