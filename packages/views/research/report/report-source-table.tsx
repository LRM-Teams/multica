"use client";

import type { ResearchSource } from "@multica/core/types";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import { weightTier } from "./report-weight";

function WeightBadge({ weight }: { weight: number }) {
  const tier = weightTier(weight);
  return (
    <span
      className={cn(
        "inline-flex min-w-[2.75rem] justify-center rounded-full px-2 py-0.5 font-mono text-[11px] font-semibold tabular-nums",
        tier === "hi" && "bg-success/15 text-success",
        tier === "mid" && "bg-warning/15 text-warning",
        tier === "lo" && "bg-muted text-muted-foreground",
      )}
    >
      {weight.toFixed(2)}
    </span>
  );
}

function TypeChip({ value }: { value: string }) {
  return (
    <span className="inline-flex rounded-md border bg-muted/40 px-1.5 py-0.5 font-mono text-[10px] tracking-wide text-muted-foreground lowercase">
      {value || "other"}
    </span>
  );
}

export function ReportSourceTable({ sources }: { sources: ResearchSource[] }) {
  const { t } = useT("research");
  const sorted = sources.toSorted(
    (a, b) => (b.credibility_weight ?? 0) - (a.credibility_weight ?? 0),
  );

  if (sorted.length === 0) {
    return <p className="text-sm text-muted-foreground">—</p>;
  }

  return (
    <div className="overflow-x-auto rounded-[10px] border">
      <table className="w-full min-w-[32rem] border-collapse text-left text-sm">
        <thead className="bg-muted/40 text-[11px] font-medium uppercase tracking-wide text-muted-foreground">
          <tr>
            <th scope="col" className="px-3 py-2">
              {t(($) => $.reader.col_weight)}
            </th>
            <th scope="col" className="px-3 py-2">
              {t(($) => $.reader.col_type)}
            </th>
            <th scope="col" className="px-3 py-2">
              {t(($) => $.reader.col_source)}
            </th>
            <th scope="col" className="px-3 py-2">
              {t(($) => $.reader.col_purpose)}
            </th>
          </tr>
        </thead>
        <tbody>
          {sorted.map((s, i) => {
            const label = s.title || s.url || s.source_class;
            return (
              <tr
                key={s.id}
                className={cn(
                  "border-t",
                  i % 2 === 1 && "bg-muted/20",
                )}
              >
                <td className="px-3 py-2.5 align-middle">
                  <WeightBadge weight={s.credibility_weight ?? 0} />
                </td>
                <td className="px-3 py-2.5 align-middle">
                  <TypeChip value={s.source_class} />
                </td>
                <td className="px-3 py-2.5 align-middle">
                  {s.url ? (
                    <a
                      href={s.url}
                      target="_blank"
                      rel="noreferrer noopener"
                      className="font-medium text-brand underline-offset-2 hover:underline"
                    >
                      {label}
                    </a>
                  ) : (
                    <span className="font-medium">{label}</span>
                  )}
                </td>
                <td className="px-3 py-2.5 align-middle text-muted-foreground">
                  {s.summary || s.excerpt || "—"}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
