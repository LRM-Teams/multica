"use client";

import { Building2, Scale, Cpu } from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import { Tooltip, TooltipTrigger, TooltipContent } from "@multica/ui/components/ui/tooltip";
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

type ResearchTemplateChipRowProps = {
  selectedId: string | null;
  onToggle: (template: ResearchTemplate) => void;
  className?: string;
};

/**
 * LRM-1092 / LRM-1072: template pills inside the home composer (replaces external cards).
 *
 * Pixel theme 2026-08-22 (supersedes the LRM-1189 blue triple): chips are
 * `.px-chip` bevel plates; the selected state is the gold-pressed bevel keyed
 * off `aria-checked` in research-home-visual.css. No raw hex in TSX.
 *
 * LRM-1218: bottom divider uses solid/subtle border vocabulary (dashed
 * borders stay reserved for drop/placeholder surfaces).
 */
export function ResearchTemplateChipRow({
  selectedId,
  onToggle,
  className,
}: ResearchTemplateChipRowProps) {
  const { t, i18n } = useT("research");
  const language = i18n?.language;

  return (
    <div
      role="radiogroup"
      aria-label={t(($) => $.home.templates_label)}
      data-testid="research-template-chip-row"
      className={cn(
        "flex gap-1.5 overflow-x-auto border-b border-border/60 px-3 py-1.5 md:flex-wrap md:overflow-visible md:px-3.5",
        className,
      )}
    >
      {RESEARCH_TEMPLATES.map((template) => {
        const Icon = ICONS[template.id] ?? Building2;
        const title = localizeTemplateField(template.title, language);
        const blurb = localizeTemplateField(template.blurb, language);
        const selected = selectedId === template.id;
        return (
          <Tooltip key={template.id}>
            <TooltipTrigger
              render={
                <button
                  type="button"
                  role="radio"
                  aria-checked={selected}
                  onClick={() => onToggle(template)}
                  data-testid={`research-template-chip-${template.id}`}
                  className="px-chip inline-flex h-7 shrink-0 items-center gap-1.5 px-2.5 text-xs whitespace-nowrap"
                >
                  <Icon className="size-3 opacity-70" aria-hidden />
                  {title}
                </button>
              }
            />
            <TooltipContent side="top">{blurb}</TooltipContent>
          </Tooltip>
        );
      })}
    </div>
  );
}
