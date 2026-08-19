"use client";

import { cn } from "@multica/ui/lib/utils";
import { Tooltip, TooltipTrigger, TooltipContent } from "@multica/ui/components/ui/tooltip";
import { useT } from "../../i18n/use-t";
import {
  localizeTemplateField,
  type ResearchTemplate,
} from "../lib/research-templates";

/**
 * Per-template accent — light half matches the LRM-1140 A2 frozen chip/tag
 * palette. Dark half follows the LRM-269 locked rule for accent pills on a dark
 * canvas: never a near-white fill block, instead a low-alpha tint of the same
 * hue plus a light hue text, so the tag reads as a state token rather than a
 * bright patch. Keep both halves per tone; a light-only tone is a regression.
 */
const TAG_TONES: Record<string, string> = {
  industry:
    "border-blue-400 bg-blue-50 text-blue-700 dark:border-blue-400/45 dark:bg-blue-400/[0.14] dark:text-blue-200",
  competitor:
    "border-indigo-300 bg-indigo-50 text-indigo-700 dark:border-indigo-300/45 dark:bg-indigo-400/[0.14] dark:text-indigo-200",
  tech_selection:
    "border-teal-400 bg-teal-50 text-teal-800 dark:border-teal-400/45 dark:bg-teal-400/[0.14] dark:text-teal-200",
};

type ResearchTemplateInjectTagProps = {
  template: ResearchTemplate;
  className?: string;
};

/**
 * LRM-1138 / LRM-1140 A2: colorful short-name tag inside the composer intent
 * area proving a template prompt is injected (not a full-prompt dump).
 */
export function ResearchTemplateInjectTag({
  template,
  className,
}: ResearchTemplateInjectTagProps) {
  const { t, i18n } = useT("research");
  const title = localizeTemplateField(template.title, i18n?.language);
  const tone = TAG_TONES[template.id] ?? TAG_TONES.industry;

  return (
    <Tooltip>
      <TooltipTrigger
        render={
          <span
            data-testid="research-template-inject-tag"
            data-template-id={template.id}
            aria-label={t(($) => $.home.template_chip, { title })}
            className={cn(
              "inline-flex h-[25px] shrink-0 items-center gap-1.5 rounded-md border px-2 text-xs font-semibold whitespace-nowrap",
              tone,
              className,
            )}
          />
        }
      >
        <span
          className="size-1.5 shrink-0 rounded-full bg-current"
          aria-hidden
        />
        {title}
      </TooltipTrigger>
      <TooltipContent side="top">{t(($) => $.home.template_chip, { title })}</TooltipContent>
    </Tooltip>
  );
}
