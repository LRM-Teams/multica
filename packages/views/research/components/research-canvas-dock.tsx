"use client";

import { PanelRight, Scan, ZoomIn, ZoomOut } from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import {
  ResearchModuleRail,
  type ResearchAuxPanelId,
} from "./research-module-rail";

export function ResearchCanvasDock({
  zoomPct,
  onZoomIn,
  onZoomOut,
  onFit,
  detailOpen,
  onToggleDetail,
  activeModule = null,
  onSelectModule,
  showZoom = true,
  showDetailToggle = true,
  layout = "desktop",
  disabled = false,
  className,
}: {
  zoomPct: number;
  onZoomIn: () => void;
  onZoomOut: () => void;
  onFit: () => void;
  detailOpen: boolean;
  onToggleDetail: () => void;
  /** LRM-1151 — which aux module is pressed (轨/源/详). */
  activeModule?: ResearchAuxPanelId | null;
  onSelectModule?: (id: ResearchAuxPanelId) => void;
  /** Narrow Logic Strip: hide zoom group per LRM-1143. */
  showZoom?: boolean;
  showDetailToggle?: boolean;
  /** `desktop` = bottom-center pill; `mobile` = full-width strip. */
  layout?: "desktop" | "mobile";
  disabled?: boolean;
  className?: string;
}) {
  const { t } = useT("research");
  const isMobile = layout === "mobile";

  if (isMobile) {
    return (
      <div
        className={cn(
          "pointer-events-auto z-10 flex w-full items-center border-t border-border bg-card px-2.5 py-1.5",
          className,
        )}
        role="toolbar"
        aria-label={t(($) => $.dock.label)}
        aria-busy={disabled || undefined}
        data-testid="research-canvas-dock"
        data-layout="mobile"
      >
        {onSelectModule ? (
          <ResearchModuleRail
            layout="bar"
            active={activeModule}
            onSelect={onSelectModule}
            disabled={disabled}
          />
        ) : null}
      </div>
    );
  }

  return (
    <div
      className={cn(
        "pointer-events-auto z-10 flex min-h-12 items-center gap-0.5 rounded-full border border-border bg-card/90 px-1.5 py-1 shadow-lg backdrop-blur-md",
        className,
      )}
      role="toolbar"
      aria-label={t(($) => $.dock.label)}
      aria-busy={disabled || undefined}
      data-testid="research-canvas-dock"
      data-layout="desktop"
    >
      {onSelectModule ? (
        <>
          <ResearchModuleRail
            layout="dock"
            active={activeModule}
            onSelect={onSelectModule}
            disabled={disabled}
          />
          <div className="mx-1 h-[22px] w-px bg-border" aria-hidden />
        </>
      ) : null}

      {showZoom ? (
        <>
          <button
            type="button"
            className="flex size-9 items-center justify-center rounded-full text-foreground hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand/40 disabled:opacity-50"
            aria-label={t(($) => $.dock.zoom_out)}
            disabled={disabled}
            onClick={onZoomOut}
          >
            <ZoomOut className="size-4" aria-hidden />
          </button>
          <span className="min-w-9 text-center text-[11px] text-muted-foreground tabular-nums">
            {zoomPct}%
          </span>
          <button
            type="button"
            className="flex size-9 items-center justify-center rounded-full text-foreground hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand/40 disabled:opacity-50"
            aria-label={t(($) => $.dock.zoom_in)}
            disabled={disabled}
            onClick={onZoomIn}
          >
            <ZoomIn className="size-4" aria-hidden />
          </button>
          <button
            type="button"
            className="flex size-9 items-center justify-center rounded-full text-foreground hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand/40 disabled:opacity-50"
            aria-label={t(($) => $.dock.fit)}
            disabled={disabled}
            onClick={onFit}
          >
            <Scan className="size-4" aria-hidden />
          </button>
        </>
      ) : null}

      {showDetailToggle ? (
        <>
          {showZoom || onSelectModule ? (
            <div className="mx-1 h-[22px] w-px bg-border" aria-hidden />
          ) : null}
          <button
            type="button"
            className={cn(
              "flex size-9 items-center justify-center rounded-full bg-brand text-brand-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand/40 disabled:opacity-50",
              detailOpen && "ring-2 ring-brand/40",
            )}
            aria-label={t(($) => $.dock.toggle_detail)}
            aria-pressed={detailOpen}
            disabled={disabled}
            onClick={onToggleDetail}
          >
            <PanelRight className="size-4" aria-hidden />
          </button>
        </>
      ) : null}
    </div>
  );
}
