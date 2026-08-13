"use client";

import { Minus, Plus, Scan } from "lucide-react";
import { cn } from "@multica/ui/lib/utils";

export function StarGraphZoomControls({
  zoomPct,
  onZoomIn,
  onZoomOut,
  onFit,
  labels,
  className,
}: {
  zoomPct: number;
  onZoomIn: () => void;
  onZoomOut: () => void;
  onFit: () => void;
  labels: { zoomOut: string; zoomIn: string; fit: string };
  className?: string;
}) {
  return (
    <div
      data-testid="star-graph-zoom-controls"
      className={cn("sg-zoom-controls pointer-events-auto", className)}
    >
      <button type="button" aria-label={labels.zoomOut} onClick={onZoomOut}>
        <Minus className="size-3.5" aria-hidden="true" />
      </button>
      <span aria-live="polite">{zoomPct}%</span>
      <button type="button" aria-label={labels.zoomIn} onClick={onZoomIn}>
        <Plus className="size-3.5" aria-hidden="true" />
      </button>
      <button type="button" aria-label={labels.fit} onClick={onFit}>
        <Scan className="size-3.5" aria-hidden="true" />
      </button>
    </div>
  );
}
