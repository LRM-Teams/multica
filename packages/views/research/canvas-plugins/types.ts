"use client";

import type { ComponentType, LazyExoticComponent } from "react";
import type { NodeTypes } from "@xyflow/react";

/**
 * FE-10 — Research canvas plugin slot contracts.
 *
 * The six slots are the typed seams where feature display modules
 * (node renderer, Insight, Dispute, motion, execution overlay, trajectory
 * jump) register into the canvas. They are strictly DISPLAY-ONLY:
 *
 *  - Slots receive a read-only view snapshot (`CanvasPluginContext`) and are
 *    forbidden from writing back to the committed Projection or creating
 *    canonical nodes. Feature modules build their own context through the
 *    graph-model adapters — they never author canonical facts here.
 *  - Every slot may be independently registered or absent. Absent slots
 *    render the generic business-free fallback from `fallbacks.tsx`.
 *  - Every registration is a lazy chunk, isolated in its own
 *    Suspense + ErrorBoundary, so one module failing to load never takes
 *    down the whole canvas.
 */

/** The six typed plugin slots. */
export const RESEARCH_CANVAS_PLUGIN_SLOT_IDS = [
  "nodeRenderer",
  "insight",
  "dispute",
  "motion",
  "executionOverlay",
  "trajectoryJump",
] as const;

export type ResearchCanvasPluginSlotId =
  (typeof RESEARCH_CANVAS_PLUGIN_SLOT_IDS)[number];

/** Display-only facts about a single rendered canvas node (read-only). */
export interface ResearchCanvasNodeContext {
  id: string;
  kind: string;
  title: string;
  status: string;
  selected: boolean;
}

/**
 * Display-only view snapshot handed to slots. Built from existing display
 * models (graph-model / v6-common view models); never a canonical Projection,
 * never mutated after construction.
 */
export interface ResearchCanvasPluginContext {
  nodes: readonly ResearchCanvasNodeContext[];
  selectedNodeId: string | null;
  /** Respect `prefers-reduced-motion` so motion plugins degrade safely. */
  reducedMotion: boolean;
}

/** Generic render-surface props shared by the five panel/overlay slots. */
export interface PanelPluginProps {
  context: ResearchCanvasPluginContext;
}

/**
 * The `nodeRenderer` slot contributes ReactFlow node-type renderers. Because
 * ReactFlow resolves renderers by node `type` (a map, not one mounted node), a
 * nodeRenderPlugin is modelled as a lazy *contributor*: it receives the base
 * renderers already owned by the canvas and returns its additions/overrides.
 */
export type ResearchCanvasNodeRendererPayload = (
  base: NodeTypes,
) => Partial<NodeTypes>;

/**
 * Per-slot props/payload map. The registry is typed against this map so a bad
 * registration mismatches at compile time (`类型化注册槽位`).
 */
export interface ResearchCanvasPluginSlotsMap {
  nodeRenderer: { baseNodeTypes: NodeTypes };
  insight: PanelPluginProps;
  dispute: PanelPluginProps;
  motion: PanelPluginProps;
  executionOverlay: PanelPluginProps;
  trajectoryJump: PanelPluginProps;
}

/** What a slot's loaded default export must be. */
export type ResearchCanvasPluginPayload<
  T extends keyof ResearchCanvasPluginSlotsMap,
> = T extends "nodeRenderer"
  ? ResearchCanvasNodeRendererPayload
  : ComponentType<ResearchCanvasPluginSlotsMap[T]>;

/** A lazy-loaded display component for the five panel/overlay slots. */
export type ResearchCanvasPluginComponent<
  T extends keyof ResearchCanvasPluginSlotsMap,
> = LazyExoticComponent<ComponentType<ResearchCanvasPluginSlotsMap[T]>>;

/**
 * One registered plugin. The `load` thunk lazily resolves to the slot's
 * display payload; the registration is inert until a slot host/renderer
 * actually invokes it, so an unregistered or unused module never enters the
 * initial bundle.
 */
export interface ResearchCanvasPluginRegistration<
  T extends keyof ResearchCanvasPluginSlotsMap,
> {
  /** Stable plugin id used for diagnostics / tests. */
  readonly id: string;
  readonly slot: T;
  /** Lazy loader returning the display payload's default export. */
  readonly load: () => Promise<{
    default: ResearchCanvasPluginPayload<T>;
  }>;
}
