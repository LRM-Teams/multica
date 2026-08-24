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
import { Minus, Plus, Scan, X } from "lucide-react";
import type { WorkGraphDetail, WorkGraphNode } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n";
import {
  GOAL_MINI_INITIAL_LAYER_BUDGET,
  goalNodeVisualState,
  layoutGoalMiniGraph,
  visibleGoalMiniGraphSlice,
  type GoalNodeVisualState,
} from "./goal-mini-graph-layout";

const VIEWPORT_HEIGHT = 220;
const MIN_SCALE = 0.55;
const MAX_SCALE = 1.75;
const SCALE_STEP = 0.12;

const stateClasses: Record<GoalNodeVisualState, { card: string; dot: string; label: string }> = {
  pending: {
    card: "border-border/80 bg-muted/70",
    dot: "bg-muted-foreground",
    label: "text-muted-foreground",
  },
  working: {
    card: "border-primary/60 bg-primary/10",
    dot: "bg-primary",
    label: "text-primary",
  },
  reviewing: {
    card: "border-brand/60 bg-brand-soft",
    dot: "bg-brand",
    label: "text-brand",
  },
  done: {
    card: "border-success/50 bg-success/10",
    dot: "bg-success",
    label: "text-success-strong",
  },
  blocked: {
    card: "border-warning/60 bg-warning/10",
    dot: "bg-warning",
    label: "text-warning",
  },
  error: {
    card: "border-destructive/50 bg-destructive/10",
    dot: "bg-destructive",
    label: "text-destructive-strong",
  },
  stale: {
    card: "border-warning/40 bg-warning/5",
    dot: "bg-warning/80",
    label: "text-warning",
  },
};

function clamp(value: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, value));
}

function nodeTitle(node: WorkGraphNode): string {
  return node.objective.trim() || node.role || node.issue_id;
}

export function GoalMiniGraph({ graph }: { graph: WorkGraphDetail }) {
  const { t } = useT("channels");
  const viewportRef = useRef<HTMLDivElement>(null);
  const layout = useMemo(
    () => layoutGoalMiniGraph(graph.nodes, graph.edges),
    [graph.nodes, graph.edges],
  );
  const nodeById = useMemo(
    () => new Map(graph.nodes.map((node) => [node.id, node])),
    [graph.nodes],
  );
  const stateLabels: Record<GoalNodeVisualState, string> = {
    pending: t(($) => $.goal.graph_status_pending),
    working: t(($) => $.goal.graph_status_working),
    reviewing: t(($) => $.goal.graph_status_reviewing),
    done: t(($) => $.goal.graph_status_done),
    blocked: t(($) => $.goal.graph_status_blocked),
    error: t(($) => $.goal.graph_status_error),
    stale: t(($) => $.goal.graph_status_stale),
  };

  const [layerBudget, setLayerBudget] = useState(GOAL_MINI_INITIAL_LAYER_BUDGET);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [scale, setScale] = useState(1);
  const [pan, setPan] = useState({ x: 0, y: 0 });
  const dragRef = useRef<{
    pointerId: number;
    originX: number;
    originY: number;
    panX: number;
    panY: number;
  } | null>(null);

  const slice = useMemo(
    () => visibleGoalMiniGraphSlice(layout, layerBudget),
    [layout, layerBudget],
  );
  const selected = selectedId ? (nodeById.get(selectedId) ?? null) : null;

  const fitToViewport = useCallback(() => {
    const viewport = viewportRef.current;
    if (!viewport || layout.width <= 0 || layout.height <= 0) {
      setScale(1);
      setPan({ x: 0, y: 0 });
      return;
    }
    const availableWidth = Math.max(120, viewport.clientWidth - 8);
    const availableHeight = Math.max(120, viewport.clientHeight - 8);
    const nextScale = clamp(
      Math.min(availableWidth / layout.width, availableHeight / layout.height, 1),
      MIN_SCALE,
      1,
    );
    setScale(nextScale);
    setPan({
      x: (availableWidth - layout.width * nextScale) / 2,
      y: (availableHeight - layout.height * nextScale) / 2,
    });
  }, [layout.height, layout.width]);

  useEffect(() => {
    setLayerBudget(GOAL_MINI_INITIAL_LAYER_BUDGET);
    setSelectedId(null);
    // Fit after viewport mounts / graph identity changes — not on every status tick.
    const frame = requestAnimationFrame(() => fitToViewport());
    return () => cancelAnimationFrame(frame);
  }, [fitToViewport, graph.id, graph.current_version]);

  const zoomBy = useCallback((delta: number, anchor?: { x: number; y: number }) => {
    setScale((prev) => {
      const next = clamp(prev + delta, MIN_SCALE, MAX_SCALE);
      if (next === prev) return prev;
      if (anchor) {
        const ratio = next / prev;
        setPan((current) => ({
          x: anchor.x - (anchor.x - current.x) * ratio,
          y: anchor.y - (anchor.y - current.y) * ratio,
        }));
      }
      return next;
    });
  }, []);

  const onWheel = useCallback(
    (event: ReactWheelEvent<HTMLDivElement>) => {
      if (!event.ctrlKey && !event.metaKey) return;
      event.preventDefault();
      const rect = event.currentTarget.getBoundingClientRect();
      zoomBy(event.deltaY > 0 ? -SCALE_STEP : SCALE_STEP, {
        x: event.clientX - rect.left,
        y: event.clientY - rect.top,
      });
    },
    [zoomBy],
  );

  const onPointerDown = useCallback(
    (event: ReactPointerEvent<HTMLDivElement>) => {
      if (event.button !== 0) return;
      dragRef.current = {
        pointerId: event.pointerId,
        originX: event.clientX,
        originY: event.clientY,
        panX: pan.x,
        panY: pan.y,
      };
      event.currentTarget.setPointerCapture(event.pointerId);
    },
    [pan.x, pan.y],
  );

  const onPointerMove = useCallback((event: ReactPointerEvent<HTMLDivElement>) => {
    const drag = dragRef.current;
    if (!drag || drag.pointerId !== event.pointerId) return;
    setPan({
      x: drag.panX + (event.clientX - drag.originX),
      y: drag.panY + (event.clientY - drag.originY),
    });
  }, []);

  const onPointerUp = useCallback((event: ReactPointerEvent<HTMLDivElement>) => {
    const drag = dragRef.current;
    if (!drag || drag.pointerId !== event.pointerId) return;
    dragRef.current = null;
    if (event.currentTarget.hasPointerCapture(event.pointerId)) {
      event.currentTarget.releasePointerCapture(event.pointerId);
    }
  }, []);

  return (
    <div
      className="space-y-2 rounded-lg border border-border/60 bg-background/70 p-2"
      data-testid="goal-mini-graph"
    >
      <div className="relative">
        <div
          ref={viewportRef}
          className="relative touch-none overflow-hidden rounded-md border border-border/40 bg-muted/20"
          style={{ height: VIEWPORT_HEIGHT }}
          data-testid="goal-mini-graph-viewport"
          onWheel={onWheel}
          onPointerDown={onPointerDown}
          onPointerMove={onPointerMove}
          onPointerUp={onPointerUp}
          onPointerCancel={onPointerUp}
          role="presentation"
        >
          <div
            className="absolute left-0 top-0 origin-top-left will-change-transform"
            style={{
              width: layout.width,
              height: layout.height,
              transform: `translate(${pan.x}px, ${pan.y}px) scale(${scale})`,
            }}
            data-testid="goal-mini-graph-canvas"
          >
            <svg
              className="pointer-events-none absolute inset-0"
              width={layout.width}
              height={layout.height}
              viewBox={`0 0 ${layout.width} ${layout.height}`}
              aria-hidden="true"
            >
              <defs>
                <marker
                  id={`goal-mini-arrow-${graph.id}`}
                  markerWidth="6"
                  markerHeight="6"
                  refX="5"
                  refY="3"
                  orient="auto"
                  markerUnits="userSpaceOnUse"
                >
                  <path d="M0,0 L6,3 L0,6 Z" className="fill-border" />
                </marker>
              </defs>
              <g className="fill-none stroke-border/80" strokeWidth="1.25" strokeLinecap="round">
                {slice.edges.map((edge) => (
                  <path
                    key={edge.id}
                    d={edge.path}
                    markerEnd={`url(#goal-mini-arrow-${graph.id})`}
                    strokeDasharray={edge.required ? undefined : "4 3"}
                  />
                ))}
              </g>
            </svg>

            {slice.nodes.map((position) => {
              const node = nodeById.get(position.id)!;
              const state = goalNodeVisualState(node);
              const colors = stateClasses[state];
              const title = nodeTitle(node);
              const isSelected = selectedId === node.id;
              return (
                <button
                  key={node.id}
                  type="button"
                  data-node-id={node.id}
                  data-state={state}
                  data-testid={`goal-mini-graph-node-${node.id}`}
                  aria-pressed={isSelected}
                  aria-label={`${title} · ${node.role} · ${stateLabels[state]}`}
                  className={cn(
                    "absolute flex items-start gap-1.5 overflow-hidden rounded-md border px-2 py-1.5 text-left shadow-sm outline-none transition-[box-shadow,border-color,background-color] duration-150",
                    "hover:shadow-md focus-visible:ring-2 focus-visible:ring-ring/60",
                    colors.card,
                    node.role === "verifier" && "border-dashed",
                    isSelected && "shadow-md ring-2 ring-ring/70",
                  )}
                  style={{
                    left: position.x - position.width / 2,
                    top: position.y - position.height / 2,
                    width: position.width,
                    height: position.height,
                  }}
                  onPointerDown={(event) => {
                    // Keep node clicks from starting a pan drag.
                    event.stopPropagation();
                  }}
                  onClick={(event) => {
                    event.stopPropagation();
                    setSelectedId((current) => (current === node.id ? null : node.id));
                  }}
                >
                  <span
                    className={cn("mt-1 size-1.5 shrink-0 rounded-full", colors.dot)}
                    aria-hidden="true"
                  />
                  <span className={cn("min-w-0 flex-1", colors.label)}>
                    <span className="line-clamp-2 text-[11px] font-medium leading-tight">
                      {title}
                    </span>
                  </span>
                </button>
              );
            })}
          </div>
        </div>

        <div
          className="pointer-events-auto absolute right-2 top-2 flex items-center gap-0.5 rounded-md border border-border/70 bg-background/95 p-0.5 shadow-sm"
          data-testid="goal-mini-graph-zoom-controls"
        >
          <Button
            type="button"
            size="icon-xs"
            variant="ghost"
            aria-label={t(($) => $.goal.graph_zoom_out)}
            onClick={() => zoomBy(-SCALE_STEP)}
          >
            <Minus aria-hidden="true" />
          </Button>
          <span
            className="min-w-9 text-center text-[10px] tabular-nums text-muted-foreground"
            aria-live="polite"
          >
            {Math.round(scale * 100)}%
          </span>
          <Button
            type="button"
            size="icon-xs"
            variant="ghost"
            aria-label={t(($) => $.goal.graph_zoom_in)}
            onClick={() => zoomBy(SCALE_STEP)}
          >
            <Plus aria-hidden="true" />
          </Button>
          <Button
            type="button"
            size="icon-xs"
            variant="ghost"
            aria-label={t(($) => $.goal.graph_fit)}
            onClick={() => fitToViewport()}
          >
            <Scan aria-hidden="true" />
          </Button>
        </div>
      </div>

      <div className="flex flex-wrap items-center justify-between gap-2 text-[11px] text-muted-foreground">
        <p>
          {t(($) => $.goal.graph_showing_nodes, {
            visible: slice.nodes.length,
            total: graph.nodes.length,
          })}
        </p>
        {slice.hasMore ? (
          <Button
            type="button"
            size="xs"
            variant="ghost"
            className="h-6 px-2 text-[11px]"
            data-testid="goal-mini-graph-show-more"
            onClick={() =>
              setLayerBudget((current) => current + GOAL_MINI_INITIAL_LAYER_BUDGET)
            }
          >
            {t(($) => $.goal.graph_show_more)}
          </Button>
        ) : null}
      </div>

      {selected ? (
        <div
          className="rounded-md border border-border/70 bg-background px-2.5 py-2 text-xs"
          data-testid="goal-mini-graph-detail"
        >
          <div className="mb-1.5 flex items-start justify-between gap-2">
            <p className="font-medium leading-snug text-foreground">{nodeTitle(selected)}</p>
            <Button
              type="button"
              size="icon-xs"
              variant="ghost"
              className="shrink-0"
              aria-label={t(($) => $.goal.graph_detail_close)}
              onClick={() => setSelectedId(null)}
            >
              <X aria-hidden="true" />
            </Button>
          </div>
          <dl className="grid gap-1 text-muted-foreground">
            <div className="flex gap-2">
              <dt className="shrink-0 text-foreground/70">{t(($) => $.goal.graph_detail_status)}</dt>
              <dd>{stateLabels[goalNodeVisualState(selected)]}</dd>
            </div>
            <div className="flex gap-2">
              <dt className="shrink-0 text-foreground/70">{t(($) => $.goal.graph_detail_role)}</dt>
              <dd className="break-all">{selected.role}</dd>
            </div>
            <div className="flex gap-2">
              <dt className="shrink-0 text-foreground/70">{t(($) => $.goal.graph_detail_issue)}</dt>
              <dd className="break-all">{selected.issue_id}</dd>
            </div>
            {selected.completion_contract.length > 0 ? (
              <div className="flex gap-2">
                <dt className="shrink-0 text-foreground/70">
                  {t(($) => $.goal.graph_detail_contract)}
                </dt>
                <dd>
                  <ul className="list-disc space-y-0.5 pl-4">
                    {selected.completion_contract.map((item) => (
                      <li key={item} className="break-words">
                        {item}
                      </li>
                    ))}
                  </ul>
                </dd>
              </div>
            ) : null}
          </dl>
        </div>
      ) : null}

      <p
        className="sr-only"
        role="img"
        aria-label={t(($) => $.goal.graph_accessible_label, { count: graph.nodes.length })}
      >
        {t(($) => $.goal.graph_pan_hint)}
      </p>
      <ol className="sr-only">
        {graph.nodes.map((node) => {
          const state = goalNodeVisualState(node);
          return (
            <li key={node.id}>
              {node.objective || node.issue_id}: {node.role}, {stateLabels[state]}
            </li>
          );
        })}
      </ol>
    </div>
  );
}
