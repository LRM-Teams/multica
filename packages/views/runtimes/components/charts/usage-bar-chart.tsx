import type { ReactElement, ReactNode } from "react";
// react-doctor-disable-next-line react-doctor/prefer-dynamic-import -- this refactor only moves the existing eager Recharts imports from eight chart wrappers into their shared renderer; route loading is unchanged.
import {
  Bar,
  BarChart,
  CartesianGrid,
  Cell,
  XAxis,
  YAxis,
  type BarProps,
  type DefaultTooltipContentProps,
  type TooltipValueType,
} from "recharts";
import {
  ChartContainer,
  ChartTooltip,
  type ChartConfig,
} from "@multica/ui/components/ui/chart";

type TooltipPayload = NonNullable<
  DefaultTooltipContentProps<TooltipValueType, number | string>["payload"]
>;

export interface UsageBarSeries<T> {
  dataKey: Extract<keyof T, string>;
  stackId?: string;
  radius?: BarProps["radius"];
}

export function UsageBarChart<T extends { label: string }>({
  config,
  data,
  series,
  tooltip,
  yAxisWidth,
  yTickFormatter,
  allowDecimals,
  getCellKey,
  getCellOpacity,
}: {
  config: ChartConfig;
  data: T[];
  series: UsageBarSeries<T>[];
  tooltip: ReactElement;
  yAxisWidth: number;
  yTickFormatter?: (value: number) => string;
  allowDecimals?: boolean;
  getCellKey?: (datum: T, index: number) => string;
  getCellOpacity?: (datum: T) => number;
}) {
  return (
    <ChartContainer config={config} className="aspect-[3/1] w-full">
      <BarChart data={data} margin={{ left: 0, right: 0, top: 4, bottom: 0 }}>
        <CartesianGrid vertical={false} />
        <XAxis
          dataKey="label"
          tickLine={false}
          axisLine={false}
          tickMargin={8}
          interval="preserveStartEnd"
        />
        <YAxis
          tickLine={false}
          axisLine={false}
          tickMargin={8}
          tickFormatter={yTickFormatter}
          allowDecimals={allowDecimals}
          width={yAxisWidth}
        />
        <ChartTooltip content={tooltip} />
        {series.map((item) => (
          <Bar
            key={item.dataKey}
            dataKey={item.dataKey}
            stackId={item.stackId}
            fill={`var(--color-${item.dataKey})`}
            radius={item.radius}
          >
            {getCellOpacity
              ? data.map((datum, index) => (
                  <Cell
                    key={`${item.dataKey}-${getCellKey?.(datum, index) ?? index}`}
                    fillOpacity={getCellOpacity(datum)}
                  />
                ))
              : null}
          </Bar>
        ))}
      </BarChart>
    </ChartContainer>
  );
}

export function ChartTotalFooter({
  label,
  payload,
  formatTotal,
}: {
  label: ReactNode;
  payload: TooltipPayload;
  formatTotal: (total: number) => ReactNode;
}) {
  const total = payload.reduce(
    (sum, item) => sum + (typeof item.value === "number" ? item.value : 0),
    0,
  );

  return (
    <div className="flex items-center justify-between gap-2 font-medium">
      <span>{label}</span>
      <span className="font-mono tabular-nums">{formatTotal(total)}</span>
    </div>
  );
}
