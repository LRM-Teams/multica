"use client";

import type { ResearchSession } from "@multica/core/types";
import { useT } from "../../i18n/use-t";
import { resolveSessionCreateParams } from "../lib/research-create-params";

/**
 * LRM-838 — read-only create params in session tools sheet.
 */
export function ResearchSessionParamsSummary({
  session,
}: {
  session: Pick<ResearchSession, "goal" | "depth_tier">;
}) {
  const { t, i18n } = useT("research");
  const params = resolveSessionCreateParams({
    goal: session.goal,
    depth_tier: session.depth_tier,
    uiLanguage: i18n?.language,
  });
  const w = params.source_weights;
  const depthLabel =
    params.depth_tier === "shallow"
      ? t(($) => $.create_params.depth_tiers.shallow.label)
      : params.depth_tier === "deep"
        ? t(($) => $.create_params.depth_tiers.deep.label)
        : t(($) => $.create_params.depth_tiers.standard.label);
  const languageLabel =
    params.language === "zh"
      ? t(($) => $.create_params.language_options.zh)
      : t(($) => $.create_params.language_options.en);

  return (
    <div
      data-testid="research-session-params-summary"
      className="space-y-4 text-sm"
    >
      <p className="text-[11px] leading-relaxed text-muted-foreground">
        {t(($) => $.create_params.session_hint)}
      </p>
      <dl className="space-y-3">
        <div className="rounded-xl border border-border/70 bg-card/70 px-3 py-2.5">
          <dt className="text-[11px] font-medium text-muted-foreground">
            {t(($) => $.create_params.depth_label)}
          </dt>
          <dd className="mt-1 font-medium text-foreground">{depthLabel}</dd>
        </div>
        <div className="rounded-xl border border-border/70 bg-card/70 px-3 py-2.5">
          <dt className="text-[11px] font-medium text-muted-foreground">
            {t(($) => $.create_params.language_label)}
          </dt>
          <dd className="mt-1 font-medium text-foreground">{languageLabel}</dd>
        </div>
        <div className="rounded-xl border border-border/70 bg-card/70 px-3 py-2.5">
          <dt className="text-[11px] font-medium text-muted-foreground">
            {t(($) => $.create_params.weights_label)}
          </dt>
          <dd className="mt-2 space-y-1.5 font-mono text-xs tabular-nums text-foreground">
            <div className="flex justify-between gap-2">
              <span>{t(($) => $.create_params.weight_rows.primary.label)}</span>
              <span>{w.primary.toFixed(2)}</span>
            </div>
            <div className="flex justify-between gap-2">
              <span>{t(($) => $.create_params.weight_rows.secondary.label)}</span>
              <span>{w.secondary.toFixed(2)}</span>
            </div>
            <div className="flex justify-between gap-2">
              <span>{t(($) => $.create_params.weight_rows.community.label)}</span>
              <span>{w.community.toFixed(2)}</span>
            </div>
          </dd>
        </div>
      </dl>
    </div>
  );
}
