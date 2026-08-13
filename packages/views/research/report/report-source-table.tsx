"use client";

import type { ResearchSource } from "@multica/core/types";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import {
  isResearchSourceFailed,
  resolveSourceFailureReasonCode,
  type SourceFailureReasonCode,
} from "./report-source-degrade";
import { weightTier } from "./report-weight";

function WeightBadge({ weight }: { weight: number }) {
  const tier = weightTier(weight);
  return (
    <span
      className={cn(
        "inline-flex min-w-[2.75rem] justify-center rounded-full px-2 py-0.5 font-mono text-[11px] font-semibold tabular-nums",
        tier === "hi" && "bg-success/15 text-success-strong",
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

  const reasonLabel = (code: SourceFailureReasonCode): string => {
    switch (code) {
      case "timeout":
        return t(($) => $.reader.source_fail_timeout);
      case "http":
        return t(($) => $.reader.source_fail_http);
      case "invalid_url":
        return t(($) => $.reader.source_fail_invalid_url);
      case "missing":
        return t(($) => $.reader.source_fail_missing);
      case "fetch_failed":
        return t(($) => $.reader.source_fail_fetch);
      default:
        return t(($) => $.reader.source_fail_unknown);
    }
  };

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
            const failed = isResearchSourceFailed(s);
            const reasonCode = resolveSourceFailureReasonCode(s);
            const label = s.title || s.url || s.source_class;
            // Localized reason only — never append raw payload codes (ETIMEDOUT, etc.).
            const reasonText = failed && reasonCode ? reasonLabel(reasonCode) : null;
            return (
              <tr
                key={s.id}
                data-testid="research-source-row"
                data-source-failed={failed ? "true" : "false"}
                className={cn(
                  "border-t",
                  i % 2 === 1 && "bg-muted/20",
                  failed && "bg-destructive/5",
                )}
              >
                <td className="px-3 py-2.5 align-middle">
                  {failed ? (
                    <span className="inline-flex rounded-full bg-destructive/15 px-2 py-0.5 text-[11px] font-medium text-destructive">
                      {t(($) => $.reader.source_failed_badge)}
                    </span>
                  ) : typeof s.credibility_weight === "number" ? (
                    <WeightBadge weight={s.credibility_weight} />
                  ) : (
                    <span className="text-muted-foreground">—</span>
                  )}
                </td>
                <td className="px-3 py-2.5 align-middle">
                  <TypeChip value={s.source_class} />
                </td>
                <td className="px-3 py-2.5 align-middle">
                  {s.url && !failed ? (
                    <a
                      href={s.url}
                      target="_blank"
                      rel="noreferrer noopener"
                      className="font-medium text-brand underline-offset-2 hover:underline"
                    >
                      {label}
                    </a>
                  ) : (
                    <span className={cn("font-medium", failed && "text-muted-foreground")}>
                      {label || t(($) => $.reader.citation_untitled)}
                    </span>
                  )}
                  {failed && reasonText ? (
                    <p
                      data-testid="research-source-fail-reason"
                      className="mt-0.5 text-[11px] text-destructive"
                    >
                      {reasonText}
                    </p>
                  ) : null}
                </td>
                <td className="px-3 py-2.5 align-middle text-muted-foreground">
                  {failed
                    ? t(($) => $.reader.source_failed_purpose)
                    : s.summary || s.excerpt || "—"}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
