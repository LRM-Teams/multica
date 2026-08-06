"use client";

import { PanelRight, Scan, ZoomIn, ZoomOut } from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";

export function ResearchCanvasDock({
  zoomPct,
  onZoomIn,
  onZoomOut,
  onFit,
  detailOpen,
  onToggleDetail,
  className,
}: {
  zoomPct: number;
  onZoomIn: () => void;
  onZoomOut: () => void;
  onFit: () => void;
  detailOpen: boolean;
  onToggleDetail: () => void;
  className?: string;
}) {
  const { t } = useT("research");

  return (
    <div
      className={cn(
        "pointer-events-auto z-10 mb-[18px] flex items-center gap-0.5 rounded-full border bg-card/90 px-2 py-1.5 shadow-lg backdrop-blur-md",
        className,
      )}
      role="toolbar"
      aria-label={t(($) => $.dock.label)}
    >
      <div className="flex h-[38px] items-center gap-1.5 px-3 text-[12.5px] font-semibold text-foreground">
        {/* eslint-disable-next-line i18next/no-literal-string -- decorative glyph, not copy */}
        <span className="text-brand" aria-hidden>
          ★
        </span>
        <span className="max-w-[120px] truncate">{t(($) => $.dock.north_star)}</span>
      </div>
      <div className="mx-1 h-[22px] w-px bg-border" />
      <span className="min-w-9 text-center text-[11px] text-muted-foreground tabular-nums">
        {zoomPct}%
      </span>
      <button
        type="button"
        className="flex size-[38px] items-center justify-center rounded-full text-foreground hover:bg-muted"
        aria-label={t(($) => $.dock.zoom_out)}
        onClick={onZoomOut}
      >
        <ZoomOut className="size-4" />
      </button>
      <button
        type="button"
        className="flex size-[38px] items-center justify-center rounded-full text-foreground hover:bg-muted"
        aria-label={t(($) => $.dock.zoom_in)}
        onClick={onZoomIn}
      >
        <ZoomIn className="size-4" />
      </button>
      <button
        type="button"
        className="flex size-[38px] items-center justify-center rounded-full text-foreground hover:bg-muted"
        aria-label={t(($) => $.dock.fit)}
        onClick={onFit}
      >
        <Scan className="size-4" />
      </button>
      <div className="mx-1 h-[22px] w-px bg-border" />
      <button
        type="button"
        className={cn(
          "flex size-[38px] items-center justify-center rounded-full bg-brand text-brand-foreground shadow-[0_0_14px_color-mix(in_oklch,var(--brand)_45%,transparent)]",
          detailOpen && "ring-2 ring-brand/40",
        )}
        aria-label={t(($) => $.dock.toggle_detail)}
        aria-pressed={detailOpen}
        onClick={onToggleDetail}
      >
        <PanelRight className="size-4" />
      </button>
    </div>
  );
}
