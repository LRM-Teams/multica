"use client";

import { Building2, Scale, Cpu } from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import {
  RESEARCH_TEMPLATES,
  localizeTemplateField,
  type ResearchTemplate,
} from "../lib/research-templates";

const ICONS: Record<string, typeof Building2> = {
  industry: Building2,
  competitor: Scale,
  tech_selection: Cpu,
};

type ResearchTemplateCardsProps = {
  onSelect: (template: ResearchTemplate) => void;
  className?: string;
};

export function ResearchTemplateCards({ onSelect, className }: ResearchTemplateCardsProps) {
  const { t, i18n } = useT("research");
  const language = i18n?.language;

  return (
    <section
      aria-label={t(($) => $.home.templates_label)}
      className={cn("w-full max-w-3xl", className)}
    >
      <h2 className="px-1 text-xs font-medium text-muted-foreground">
        {t(($) => $.home.templates_label)}
      </h2>
      {/* Narrow: horizontal scroll; sm+: wrap/grid so every card stays reachable. */}
      <div className="mt-2 flex gap-3 overflow-x-auto pb-1 sm:grid sm:grid-cols-3 sm:overflow-visible sm:pb-0">
        {RESEARCH_TEMPLATES.map((template) => {
          const Icon = ICONS[template.id] ?? Building2;
          const title = localizeTemplateField(template.title, language);
          const blurb = localizeTemplateField(template.blurb, language);
          const params = localizeTemplateField(template.params, language);
          return (
            <button
              key={template.id}
              type="button"
              onClick={() => onSelect(template)}
              className={cn(
                "flex min-w-[220px] shrink-0 flex-col gap-2 rounded-xl border bg-card p-4 text-left shadow-sm transition-colors",
                "hover:border-brand/40 hover:bg-brand/4 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand/30",
                "sm:min-w-0",
              )}
            >
              <div className="flex items-center gap-2">
                <span className="flex size-8 items-center justify-center rounded-lg bg-brand/10 text-brand">
                  <Icon className="size-4" aria-hidden />
                </span>
                <span className="truncate text-sm font-medium">{title}</span>
              </div>
              <p className="line-clamp-2 text-xs text-muted-foreground">{blurb}</p>
              <div className="mt-auto flex flex-wrap gap-1">
                {params.slice(0, 3).map((p) => (
                  <span
                    key={p}
                    className="rounded-md bg-muted px-1.5 py-0.5 text-[10px] text-muted-foreground"
                  >
                    {p}
                  </span>
                ))}
              </div>
            </button>
          );
        })}
      </div>
    </section>
  );
}
