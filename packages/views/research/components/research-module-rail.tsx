"use client";

import { cn } from "@multica/ui/lib/utils";
import { useIsMobile } from "@multica/ui/hooks/use-mobile";
import { useT } from "../../i18n/use-t";

/** LRM-1061 / LRM-1056 v2 — left-of-canvas module triggers (one drawer at a time). */
export type ResearchAuxPanelId = "trajectory" | "sources" | "detail";

const MODULES: {
  id: ResearchAuxPanelId;
  icoKey: "module_trajectory_ico" | "module_sources_ico" | "module_detail_ico";
  labelKey: "module_trajectory" | "module_sources" | "module_detail";
}[] = [
  {
    id: "trajectory",
    icoKey: "module_trajectory_ico",
    labelKey: "module_trajectory",
  },
  {
    id: "sources",
    icoKey: "module_sources_ico",
    labelKey: "module_sources",
  },
  {
    id: "detail",
    icoKey: "module_detail_ico",
    labelKey: "module_detail",
  },
];

export function ResearchModuleRail({
  active,
  onSelect,
  className,
}: {
  active: ResearchAuxPanelId | null;
  onSelect: (id: ResearchAuxPanelId) => void;
  className?: string;
}) {
  const { t } = useT("research");
  const isMobile = useIsMobile();

  return (
    <div
      data-testid="research-module-rail"
      className={cn(
        "pointer-events-auto absolute top-3 left-3 z-30 flex flex-col gap-1.5",
        className,
      )}
    >
      {MODULES.map((mod) => {
        const on = active === mod.id;
        return (
          <button
            key={mod.id}
            type="button"
            data-testid={`research-module-${mod.id}`}
            data-active={on ? "true" : "false"}
            aria-pressed={on}
            onClick={() => onSelect(mod.id)}
            className={cn(
              "inline-flex h-8 items-center gap-1.5 rounded-lg border px-2 text-[11px] font-medium shadow-sm backdrop-blur-md transition-colors",
              on
                ? "border-brand/40 bg-brand/12 text-brand"
                : "border-border/70 bg-card/90 text-muted-foreground hover:bg-muted/70 hover:text-foreground",
              isMobile && "h-8 w-8 justify-center px-0",
            )}
          >
            <span className="font-semibold tracking-wide">
              {t(($) => $.panel[mod.icoKey])}
            </span>
            {!isMobile ? (
              <span className="max-w-[5.5rem] truncate">
                {t(($) => $.panel[mod.labelKey])}
              </span>
            ) : null}
          </button>
        );
      })}
    </div>
  );
}
