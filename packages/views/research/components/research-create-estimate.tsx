"use client";

import { cn } from "@multica/ui/lib/utils";
import { Tooltip, TooltipTrigger, TooltipContent } from "@multica/ui/components/ui/tooltip";
import { useT } from "../../i18n/use-t";
import {
  resolveCreateEstimate,
  type ResolveCreateEstimateOptions,
} from "../lib/research-create-estimate";
import type { ResearchCreateParamsDraft } from "../lib/research-create-params";

/**
 * Compact composer line — visible without opening the params sheet.
 * Updates synchronously with draft params (LRM-839 linkage).
 */
export function ResearchCreateEstimateSummary({
  params,
  resolveOptions,
  className,
}: {
  params: ResearchCreateParamsDraft;
  resolveOptions?: ResolveCreateEstimateOptions;
  className?: string;
}) {
  const { t } = useT("research");
  const result = resolveCreateEstimate(params, resolveOptions);
  const duration =
    result.status === "ready"
      ? t(($) => $.create_estimate.duration_range, {
          min: result.estimate.duration_min,
          max: result.estimate.duration_max,
        })
      : t(($) => $.create_estimate.unknown);
  const cost =
    result.status === "ready"
      ? result.estimate.cost_tier === "low"
        ? t(($) => $.create_estimate.cost_tiers.low)
        : result.estimate.cost_tier === "high"
          ? t(($) => $.create_estimate.cost_tiers.high)
          : t(($) => $.create_estimate.cost_tiers.medium)
      : t(($) => $.create_estimate.unknown);

  return (
    <Tooltip>
      <TooltipTrigger
        render={
          <p
            data-testid="research-create-estimate-summary"
            data-estimate-status={result.status}
            className={cn("text-xs text-muted-foreground", className)}
          />
        }
      >
        <span className="font-medium text-foreground/80">
          {t(($) => $.create_estimate.badge)}
        </span>
        <span aria-hidden className="mx-1.5 text-border">
          ·
        </span>
        <span data-testid="research-create-estimate-summary-duration">{duration}</span>
        <span aria-hidden className="mx-1.5 text-border">
          ·
        </span>
        <span data-testid="research-create-estimate-summary-cost">{cost}</span>
      </TooltipTrigger>
      <TooltipContent side="top">{t(($) => $.create_estimate.disclaimer)}</TooltipContent>
    </Tooltip>
  );
}

/**
 * Params-panel estimate strip — duration + cost tier, marked as estimate.
 * Unknown copy when lookup fails; never disables Done.
 */
export function ResearchCreateEstimatePanel({
  params,
  resolveOptions,
  className,
}: {
  params: ResearchCreateParamsDraft;
  resolveOptions?: ResolveCreateEstimateOptions;
  className?: string;
}) {
  const { t } = useT("research");
  const result = resolveCreateEstimate(params, resolveOptions);
  const duration =
    result.status === "ready"
      ? t(($) => $.create_estimate.duration_range, {
          min: result.estimate.duration_min,
          max: result.estimate.duration_max,
        })
      : t(($) => $.create_estimate.unknown);
  const cost =
    result.status === "ready"
      ? result.estimate.cost_tier === "low"
        ? t(($) => $.create_estimate.cost_tiers.low)
        : result.estimate.cost_tier === "high"
          ? t(($) => $.create_estimate.cost_tiers.high)
          : t(($) => $.create_estimate.cost_tiers.medium)
      : t(($) => $.create_estimate.unknown);

  return (
    <section
      data-testid="research-create-estimate"
      data-estimate-status={result.status}
      aria-live="polite"
      className={cn(
        "rounded-xl border border-border/70 bg-muted/30 px-3 py-2.5",
        className,
      )}
    >
      <div className="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1">
        <h3 className="text-sm font-semibold text-foreground">
          {t(($) => $.create_estimate.title)}
        </h3>
        <span className="text-[11px] font-medium text-muted-foreground">
          {t(($) => $.create_estimate.badge)}
        </span>
      </div>
      <dl className="mt-2 grid grid-cols-2 gap-3">
        <div>
          <dt className="text-[11px] text-muted-foreground">
            {t(($) => $.create_estimate.duration_label)}
          </dt>
          <dd
            data-testid="research-create-estimate-duration"
            className="mt-0.5 text-sm font-medium tabular-nums text-foreground"
          >
            {duration}
          </dd>
        </div>
        <div>
          <dt className="text-[11px] text-muted-foreground">
            {t(($) => $.create_estimate.cost_label)}
          </dt>
          <dd
            data-testid="research-create-estimate-cost"
            className="mt-0.5 text-sm font-medium text-foreground"
          >
            {cost}
          </dd>
        </div>
      </dl>
      <p className="mt-2 text-[11px] leading-relaxed text-muted-foreground">
        {t(($) => $.create_estimate.disclaimer)}
      </p>
    </section>
  );
}
