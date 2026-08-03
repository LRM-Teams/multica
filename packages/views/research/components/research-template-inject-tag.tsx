"use client";

import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import {
  localizeTemplateField,
  type ResearchTemplate,
} from "../lib/research-templates";

/** Per-template accent — matches LRM-1140 A2 frozen chip/tag palette. */
const TAG_TONES: Record<string, string> = {
  industry:
    "border-blue-400 bg-blue-50 text-blue-700",
  competitor:
    "border-indigo-300 bg-indigo-50 text-indigo-700",
  tech_selection:
    "border-teal-400 bg-teal-50 text-teal-800",
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
    <span
      data-testid="research-template-inject-tag"
      data-template-id={template.id}
      title={t(($) => $.home.template_chip, { title })}
      aria-label={t(($) => $.home.template_chip, { title })}
      className={cn(
        "inline-flex h-[25px] shrink-0 items-center gap-1.5 rounded-md border px-2 text-xs font-semibold whitespace-nowrap",
        tone,
        className,
      )}
    >
      <span
        className="size-1.5 shrink-0 rounded-full bg-current"
        aria-hidden
      />
      {title}
    </span>
  );
}
