"use client";

import type { ComponentProps } from "react";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import { ResearchSessionChrome } from "./research-session-chrome";
import {
  RESEARCH_D5_LENSES,
  type ResearchD5Lens,
} from "../lib/research-d5-lens";

type ChromeProps = ComponentProps<typeof ResearchSessionChrome>;

export function ResearchD5Chrome({
  activeLens,
  onLensChange,
  className,
  ...chromeProps
}: ChromeProps & {
  activeLens: ResearchD5Lens;
  onLensChange: (lens: ResearchD5Lens) => void;
  className?: string;
}) {
  const { t } = useT("research");

  return (
    <div data-testid="research-d5-chrome" className={cn("d5-chrome-shell", className)}>
      <div className="d5-chrome-top">
        <div className="d5-brand">
          <span className="d5-logo" aria-hidden>
            M
          </span>
          <div className="min-w-0">
            <b>{t(($) => $.d5.brand_title)}</b>
            <span>{t(($) => $.d5.brand_subtitle)}</span>
          </div>
        </div>

        <div className="d5-lens-group" role="tablist" aria-label={t(($) => $.d5.lens_group)}>
          {RESEARCH_D5_LENSES.map((lens) => (
            <button
              key={lens}
              type="button"
              role="tab"
              aria-selected={activeLens === lens}
              data-testid={`research-d5-lens-${lens}`}
              className={cn("d5-lens-btn", activeLens === lens && "d5-lens-btn-active")}
              onClick={() => onLensChange(lens)}
            >
              {t(($) => $.d5.lens[lens])}
            </button>
          ))}
        </div>
      </div>

      <ResearchSessionChrome {...chromeProps} />
    </div>
  );
}
