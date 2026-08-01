"use client";

import { useState } from "react";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import type { SourceStrategyChip, SourceStrategyModel } from "../lib/m2-visibility";

export function SourceStrategyStrip({
  model,
  className,
}: {
  model: SourceStrategyModel;
  className?: string;
}) {
  const { t } = useT("research");
  const [activeId, setActiveId] = useState<string | null>(model.chips[0]?.id ?? null);
  const active: SourceStrategyChip | undefined =
    model.chips.find((c) => c.id === activeId) ?? model.chips[0];

  if (model.empty) {
    return (
      <div
        data-testid="source-strategy-strip"
        className={cn("border-b bg-muted/15 px-3 py-2 text-xs text-muted-foreground", className)}
      >
        {t(($) => $.m2.strategy_empty)}
      </div>
    );
  }

  return (
    <div
      data-testid="source-strategy-strip"
      className={cn("space-y-1.5 border-b bg-muted/15 px-3 py-2", className)}
    >
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-[11px] font-medium tracking-wide text-muted-foreground uppercase">
          {t(($) => $.m2.strategy_label)}
        </span>
        {model.chips.map((chip) => (
          <button
            key={chip.id}
            type="button"
            onClick={() => setActiveId(chip.id)}
            className={cn(
              "rounded-full px-2.5 py-0.5 text-[11px] font-medium transition-colors",
              chip.layer === "general"
                ? "bg-[color:var(--source-general)]/15 text-[color:var(--source-general)]"
                : "bg-[color:var(--source-domain)]/15 text-[color:var(--source-domain)]",
              active?.id === chip.id && "ring-1 ring-current",
            )}
          >
            {chip.layer === "general"
              ? t(($) => $.m2.layer_general)
              : t(($) => $.m2.layer_domain)}
            · {chip.label}
          </button>
        ))}
      </div>
      <p className="text-[12px] leading-snug text-foreground/90">
        <span className="text-muted-foreground">{t(($) => $.m2.why_label)} </span>
        {active?.why || model.whyLine || "—"}
      </p>
      {active?.samples?.length ? (
        <ul className="flex flex-wrap gap-x-3 gap-y-1 text-[11px]">
          {active.samples.map((s) => (
            <li key={s.id} className="min-w-0 truncate">
              {s.url ? (
                <a
                  href={s.url}
                  target="_blank"
                  rel="noreferrer noopener"
                  className="font-medium text-brand underline-offset-2 hover:underline"
                >
                  {s.title}
                </a>
              ) : (
                <span className="text-muted-foreground">{s.title}</span>
              )}
            </li>
          ))}
        </ul>
      ) : null}
    </div>
  );
}
