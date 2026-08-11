"use client";

import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type PointerEvent as ReactPointerEvent,
  type WheelEvent as ReactWheelEvent,
} from "react";
import { StarGraphMapKey } from "@multica/ui/components/star-graph";
import { cn } from "@multica/ui/lib/utils";
import type { D5LensDisplayHints } from "../../lib/research-d5-lens-display";
import type { MotionDirective } from "../../motion/directives";
import type { StarCanvasViewModel } from "../lib/star-canvas-view-model";
import { StarGraphClusterLayer } from "./star-graph-cluster-layer";
import {
  computeEntityBounds,
  fitCameraToBounds,
  zoomCamera,
  zoomPercent,
  type StarGraphCamera,
} from "./star-graph-canvas-utils";
import { StarGraphEdges } from "./star-graph-edges";
import { StarGraphEntityLayer } from "./star-graph-entity-layer";
import { StarGraphZoomControls } from "./star-graph-zoom-controls";
import "./star-graph-canvas.css";

export interface StarGraphCanvasProps {
  model: StarCanvasViewModel;
  selectedNodeId?: string | null;
  onSelectNode?: (nodeId: string) => void;
  onOpenNode?: (nodeId: string) => void;
  summaryTitle?: string;
  summaryDetail?: string;
  showMapKey?: boolean;
  newFrontierLabel?: string;
  lensHints?: D5LensDisplayHints;
  motionDirectives?: ReadonlyMap<string, MotionDirective | null>;
  onHelp?: () => void;
  className?: string;
}

const DEFAULT_CAMERA: StarGraphCamera = { x: 0, y: 0, zoom: 1 };

export function StarGraphCanvas({
  model,
  selectedNodeId = null,
  onSelectNode,
  onOpenNode,
  summaryTitle,
  summaryDetail,
  showMapKey = true,
  newFrontierLabel,
  lensHints,
  motionDirectives,
  onHelp,
  className,
}: StarGraphCanvasProps) {
  const rootRef = useRef<HTMLDivElement>(null);
  const [viewport, setViewport] = useState({ width: 0, height: 0 });
  const [camera, setCamera] = useState<StarGraphCamera>(DEFAULT_CAMERA);
  const dragRef = useRef<{ startX: number; startY: number; cameraX: number; cameraY: number } | null>(
    null,
  );

  const bounds = useMemo(() => computeEntityBounds(model.entities), [model.entities]);

  useEffect(() => {
    const node = rootRef.current;
    if (!node) return;
    const observer = new ResizeObserver((entries) => {
      const entry = entries[0];
      if (!entry) return;
      setViewport({
        width: entry.contentRect.width,
        height: entry.contentRect.height,
      });
    });
    observer.observe(node);
    return () => observer.disconnect();
  }, []);

  const fitToContent = useCallback(() => {
    if (!bounds || viewport.width <= 0 || viewport.height <= 0) return;
    setCamera(fitCameraToBounds(bounds, viewport));
  }, [bounds, viewport]);

  useEffect(() => {
    fitToContent();
  }, [fitToContent, model.version]);

  const handleZoomIn = useCallback(() => {
    setCamera((current) =>
      zoomCamera(current, current.zoom * 1.12, {
        x: viewport.width / 2,
        y: viewport.height / 2,
      }),
    );
  }, [viewport.height, viewport.width]);

  const handleZoomOut = useCallback(() => {
    setCamera((current) =>
      zoomCamera(current, current.zoom / 1.12, {
        x: viewport.width / 2,
        y: viewport.height / 2,
      }),
    );
  }, [viewport.height, viewport.width]);

  const handleWheel = useCallback(
    (event: ReactWheelEvent<HTMLDivElement>) => {
      event.preventDefault();
      const rect = rootRef.current?.getBoundingClientRect();
      if (!rect) return;
      const anchor = {
        x: event.clientX - rect.left,
        y: event.clientY - rect.top,
      };
      const delta = event.deltaY > 0 ? 0.92 : 1.08;
      setCamera((current) => zoomCamera(current, current.zoom * delta, anchor));
    },
    [],
  );

  const handlePointerDown = useCallback((event: ReactPointerEvent<HTMLDivElement>) => {
    if (event.button !== 0) return;
    if ((event.target as HTMLElement).closest('[data-testid="star-graph-node"], button')) {
      return;
    }
    event.currentTarget.setPointerCapture(event.pointerId);
    dragRef.current = {
      startX: event.clientX,
      startY: event.clientY,
      cameraX: camera.x,
      cameraY: camera.y,
    };
  }, [camera.x, camera.y]);

  const handlePointerMove = useCallback((event: ReactPointerEvent<HTMLDivElement>) => {
    const drag = dragRef.current;
    if (!drag) return;
    setCamera((current) => ({
      ...current,
      x: drag.cameraX + (event.clientX - drag.startX),
      y: drag.cameraY + (event.clientY - drag.startY),
    }));
  }, []);

  const handlePointerUp = useCallback((event: ReactPointerEvent<HTMLDivElement>) => {
    dragRef.current = null;
    if (event.currentTarget.hasPointerCapture(event.pointerId)) {
      event.currentTarget.releasePointerCapture(event.pointerId);
    }
  }, []);

  const worldSize = useMemo(() => {
    if (!bounds) {
      return { width: viewport.width || 1, height: viewport.height || 1 };
    }
    const pad = 120;
    return {
      width: Math.max(bounds.width + pad * 2, viewport.width || 1),
      height: Math.max(bounds.height + pad * 2, viewport.height || 1),
    };
  }, [bounds, viewport.height, viewport.width]);

  return (
    <div
      ref={rootRef}
      data-testid="star-graph-canvas"
      className={cn("sg-canvas-root research-semantic-motion", className)}
      aria-label="Research constellation canvas"
      onPointerDown={handlePointerDown}
      onPointerMove={handlePointerMove}
      onPointerUp={handlePointerUp}
      onPointerCancel={handlePointerUp}
      onWheel={handleWheel}
    >
      {(summaryTitle || summaryDetail) && (
        <div data-testid="star-graph-summary" className="sg-summary-label pointer-events-none">
          {summaryTitle && <b>{summaryTitle}</b>}
          {summaryDetail && <span>{summaryDetail}</span>}
        </div>
      )}

      <div
        className="sg-canvas-world"
        style={{
          width: worldSize.width,
          height: worldSize.height,
          transform: `translate(${camera.x}px, ${camera.y}px) scale(${camera.zoom})`,
        }}
      >
        <StarGraphClusterLayer
          clusters={model.clusters}
          entities={model.entities}
          rootId={model.rootId}
          newFrontierLabel={newFrontierLabel}
        />
        <StarGraphEdges
          relations={model.relations}
          width={worldSize.width}
          height={worldSize.height}
          lensHints={lensHints}
        />
        <StarGraphEntityLayer
          entities={model.entities}
          selectedNodeId={selectedNodeId}
          lensHints={lensHints}
          motionDirectives={motionDirectives}
          onSelectNode={onSelectNode}
          onOpenNode={onOpenNode}
        />
      </div>

      {showMapKey && <StarGraphMapKey onHelp={onHelp} className="absolute bottom-4 left-5 z-10" />}

      <StarGraphZoomControls
        zoomPct={zoomPercent(camera)}
        onZoomIn={handleZoomIn}
        onZoomOut={handleZoomOut}
        onFit={fitToContent}
      />
    </div>
  );
}
