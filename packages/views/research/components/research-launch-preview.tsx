"use client";

import type { ResearchFleet } from "@multica/core/types/research";
import { cn } from "@multica/ui/lib/utils";
import { Check, CircleDot, Network, ShieldCheck } from "lucide-react";
import { useT } from "../../i18n/use-t";
import type { ResearchCreateParamsDraft } from "../lib/research-create-params";

export function ResearchLaunchPreview({
  params,
  fleet,
  className,
}: {
  params: ResearchCreateParamsDraft;
  fleet?: ResearchFleet;
  className?: string;
}) {
  const { t } = useT("research");
  const members = fleet?.members.filter((member) => member.status !== "archived") ?? [];
  const lead = members.find((member) => member.is_lead) ?? members[0];
  const depthLabel = t(($) =>
    params.depth_tier === "deep"
      ? $.home_preview.depth_deep
      : params.depth_tier === "shallow"
        ? $.home_preview.depth_shallow
        : $.home_preview.depth_standard,
  );

  return (
    <aside
      className={cn(
        "relative overflow-hidden rounded-xl border border-brand/25 bg-card/92 p-4",
        className,
      )}
      aria-label={t(($) => $.home_preview.title)}
      data-testid="research-launch-preview"
    >
      <div className="absolute right-[-28px] top-[-38px] size-36 rounded-full bg-brand/10 blur-3xl" aria-hidden />
      <div className="relative">
        <div className="flex items-center justify-between gap-3">
          <div>
            <h2 className="text-sm font-medium text-foreground">{t(($) => $.home_preview.title)}</h2>
            <p className="mt-1 text-xs text-muted-foreground">{t(($) => $.home_preview.subtitle)}</p>
          </div>
          <span className="flex size-8 items-center justify-center rounded-lg bg-brand/12 text-brand" aria-hidden>
            <Network className="size-4" />
          </span>
        </div>

        <dl className="mt-4 grid grid-cols-2 gap-x-4 gap-y-3">
          <div>
            <dt className="text-xs text-muted-foreground">{t(($) => $.home_preview.lead)}</dt>
            <dd className="mt-1 truncate text-sm font-medium text-foreground">
              {lead?.display_name || lead?.name || t(($) => $.home_preview.auto_assign)}
            </dd>
          </div>
          <div>
            <dt className="text-xs text-muted-foreground">{t(($) => $.home_preview.fleet)}</dt>
            <dd className="mt-1 text-sm font-medium tabular-nums text-foreground">
              {t(($) => $.home_preview.agent_count, { count: Math.max(members.length, 1) })}
            </dd>
          </div>
          <div>
            <dt className="text-xs text-muted-foreground">{t(($) => $.home_preview.depth)}</dt>
            <dd className="mt-1 text-sm font-medium text-foreground">{depthLabel}</dd>
          </div>
          <div>
            <dt className="text-xs text-muted-foreground">{t(($) => $.home_preview.source_policy)}</dt>
            <dd className="mt-1 text-sm font-medium tabular-nums text-foreground">
              {Math.round(params.source_weights.primary * 100)}% {t(($) => $.home_preview.primary_sources)}
            </dd>
          </div>
        </dl>

        <div className="mt-4 border-t border-border/70 pt-3">
          <div className="flex items-center gap-1" aria-label={t(($) => $.home_preview.stages)}>
            {["S1", "S2", "S3", "S4"].map((stage, index) => (
              <div key={stage} className="flex min-w-0 flex-1 items-center gap-1">
                <span className={cn(
                  "flex size-6 shrink-0 items-center justify-center rounded-full border text-xs font-medium",
                  index === 0 ? "border-brand/55 bg-brand/12 text-brand" : "border-border text-muted-foreground",
                )}>
                  {index === 0 ? <CircleDot className="size-3" aria-hidden /> : stage.slice(1)}
                </span>
                {index < 3 ? <span className="h-px min-w-2 flex-1 bg-border" aria-hidden /> : null}
              </div>
            ))}
          </div>
          <div className="mt-2 flex items-center gap-1.5 text-xs text-muted-foreground">
            <ShieldCheck className="size-3.5 text-success" aria-hidden />
            <span>{t(($) => $.home_preview.evidence_note)}</span>
            <Check className="ml-auto size-3.5 text-success" aria-hidden />
          </div>
        </div>
      </div>
    </aside>
  );
}
