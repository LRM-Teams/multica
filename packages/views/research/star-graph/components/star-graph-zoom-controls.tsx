"use client";

import { Minus, Plus, Scan } from "lucide-react";
import { cn } from "@multica/ui/lib/utils";

export function StarGraphZoomControls({
  zoomPct,
  onZoomIn,
  onZoomOut,
  onFit,
  className,
}: {
  zoomPct: number;
  onZoomIn: () => void;
  onZoomOut: () => void;
  onFit: () => void;
  className?: string;
}) {
  return (
    <div
      data-testid="star-graph-zoom-controls"
      className={cn("sg-zoom-controls pointer-events-auto", className)}
    >
      <button type="button" aria-label="缩小" onClick={onZoomOut}>
        <Minus className="size-3.5" aria-hidden="true" />
      </button>
      <span aria-live="polite">{zoomPct}%</span>
      <button type="button" aria-label="放大" onClick={onZoomIn}>
        <Plus className="size-3.5" aria-hidden="true" />
      </button>
      <button type="button" aria-label="适应内容" onClick={onFit}>
        <Scan className="size-3.5" aria-hidden="true" />
      </button>
    </div>
  );
}
