"use client";

import { CartesianGrid, Line, LineChart, XAxis, YAxis } from "recharts";
import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from "@multica/ui/components/ui/chart";
import type { BucketHistoryPoint } from "@multica/core/provider-limits";

const remainingConfig = {
  remaining: { label: "Remaining", color: "var(--chart-1)" },
} satisfies ChartConfig;

// Points with no derivable remaining value (partial/unavailable/error
// readings) are passed through as `null` so recharts breaks the line
// instead of interpolating a value nobody reported.
export function ProviderLimitHistoryChart({
  points,
  unit,
}: {
  points: BucketHistoryPoint[];
  unit: string;
}) {
  const data = points.map((point) => ({
    label: new Date(point.checkedAt).toLocaleString(),
    remaining: point.remaining,
  }));

  return (
    <ChartContainer config={remainingConfig} className="aspect-[3/1] w-full">
      <LineChart data={data} margin={{ left: 0, right: 8, top: 4, bottom: 0 }}>
        <CartesianGrid vertical={false} />
        <XAxis
          dataKey="label"
          tickLine={false}
          axisLine={false}
          tickMargin={8}
          interval="preserveStartEnd"
        />
        <YAxis tickLine={false} axisLine={false} tickMargin={8} width={50} />
        <ChartTooltip
          content={
            <ChartTooltipContent
              formatter={(value) =>
                typeof value === "number" ? `${value} ${unit}` : String(value)
              }
            />
          }
        />
        <Line
          type="monotone"
          dataKey="remaining"
          stroke="var(--color-remaining)"
          connectNulls={false}
          dot
        />
      </LineChart>
    </ChartContainer>
  );
}
