"use client";

import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";

/** LRM-1061 / LRM-1151 — aux panel ids (one drawer at a time). */
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

/**
 * LRM-1151 — Dock module group (轨 / 源 / 详).
 * Desktop: compact horizontal pills inside the canvas Dock.
 * Narrow: full-width three-equal strip under Logic Strip.
 * Content still opens the single ResearchAuxDrawer / Sheet.
 *
 * LRM-1329 — sources: desktop shows full title (no ellipsis); narrow bar keeps
 * short「源」glyph with aria-label = full title.
 */
export function ResearchModuleRail({
  active,
  onSelect,
  className,
  layout = "dock",
  disabled = false,
}: {
  active: ResearchAuxPanelId | null;
  onSelect: (id: ResearchAuxPanelId) => void;
  className?: string;
  /** `dock` = inline pill group; `bar` = full-width ⅓ strip (narrow). */
  layout?: "dock" | "bar";
  disabled?: boolean;
}) {
  const { t } = useT("research");
  const isBar = layout === "bar";

  return (
    <div
      data-testid="research-module-rail"
      data-layout={layout}
      className={cn(
        "pointer-events-auto flex",
        isBar ? "w-full items-stretch gap-0.5" : "items-center gap-0.5",
        className,
      )}
    >
      {MODULES.map((mod) => {
        const on = active === mod.id;
        const fullLabel = t(($) => $.panel[mod.labelKey]);
        const ico = t(($) => $.panel[mod.icoKey]);
        const isSources = mod.id === "sources";
        // Narrow sources: glyph only + full accessible name (LRM-1329).
        const ariaLabel = isSources && isBar ? fullLabel : undefined;

        return (
          <button
            key={mod.id}
            type="button"
            data-testid={`research-module-${mod.id}`}
            data-active={on ? "true" : "false"}
            aria-pressed={on}
            aria-label={ariaLabel}
            disabled={disabled}
            onClick={() => onSelect(mod.id)}
            className={cn(
              "inline-flex items-center justify-center gap-1 font-semibold transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand/40",
              isBar
                ? "min-h-11 min-w-0 flex-1 flex-col rounded-lg px-1.5 py-1.5 text-[11px]"
                : "h-9 rounded-full px-2.5 text-[11px]",
              // Sources dock: allow full title width (no 4.5rem clip).
              isSources && !isBar && "max-w-none px-3",
              on
                ? "bg-brand/12 text-brand"
                : "text-muted-foreground hover:bg-muted/70 hover:text-foreground",
              disabled && "pointer-events-none opacity-50",
            )}
          >
            <span
              className="font-semibold tracking-wide"
              aria-hidden={isBar || undefined}
            >
              {ico}
            </span>
            {isSources && isBar ? (
              <span className="sr-only">{fullLabel}</span>
            ) : (
              <span
                className={cn(
                  isSources && !isBar
                    ? "min-w-0 break-words whitespace-normal text-left leading-tight"
                    : "truncate",
                  !isSources && isBar && "max-w-full text-[9px] font-medium",
                  !isSources && !isBar && "max-w-[4.5rem]",
                )}
              >
                {fullLabel}
              </span>
            )}
          </button>
        );
      })}
    </div>
  );
}
