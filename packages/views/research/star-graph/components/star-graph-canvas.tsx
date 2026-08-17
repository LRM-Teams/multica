"use client";

import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent as ReactKeyboardEvent,
  type PointerEvent as ReactPointerEvent,
  type WheelEvent as ReactWheelEvent,
} from "react";
import { useResearchCanvasStore } from "@multica/core/research";
import type { ResearchCanvasFilter, TypedGraphNode } from "@multica/core/research";
import { emptyCanvasFilter, indexTypedGraphNodes } from "@multica/core/research";
import type { ResearchGraphNode } from "@multica/core/types";
import { StarGraphMapKey } from "@multica/ui/components/star-graph";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../../i18n/use-t";
import type { D5LensDisplayHints } from "../../lib/research-d5-lens-display";
import { focusD5LensDisplayHints } from "../../lib/research-d5-lens-display";
import {
  buildNodeAccessibleName,
  resolveCanvasKeyEvent,
  type CanvasKeyboardAction,
  type CanvasOverlayLayer,
  type GraphEdgeLike,
} from "../../lib/canvas-keyboard-nav";
import type { MotionDirective } from "../../motion/directives";
import type { StarGraphExpansionControl } from "../lib/star-graph-expansion";
import {
  buildStarGraphExpansionMotion,
  selectStarGraphExpansionRelationIds,
} from "../lib/star-graph-expansion-motion";
import {
  buildStarGraphFusionGhosts,
  type StarGraphFusionTransition,
} from "../lib/star-graph-fusion-transition";
import { selectSemanticLabelNodeIds } from "../lib/star-graph-semantic-labels";
import type {
  StarCanvasViewModel,
  StarEntityView,
} from "../lib/star-canvas-view-model";
import { selectStarGraphCollapseGhosts } from "../lib/star-graph-collapse-ghosts";
import {
  computeClusterHiddenCounts,
  edgeBudgetForViewport,
  filterEntitiesForCanvasDisplay,
  filterRelationsToVisibleEntities,
  selectVisibleEntityIds,
  STAR_GRAPH_SEMANTIC_NODE_BUDGET,
} from "../lib/star-graph-visible-budget";
import { StarGraphClusterLayer } from "./star-graph-cluster-layer";
import { StarGraphCollapseGhostLayer } from "./star-graph-collapse-ghost-layer";
import {
  computeEntityBounds,
  computeEntityBoundsForIds,
  fitCameraToBounds,
  focusCameraOnEntity,
  zoomCamera,
  zoomPercent,
  type StarGraphCamera,
} from "./star-graph-canvas-utils";
import { StarGraphEdges } from "./star-graph-edges";
import { StarGraphFusionGhostLayer } from "./star-graph-fusion-ghost-layer";
import {
  StarGraphEntityLayer,
  type StarGraphEntityLabels,
} from "./star-graph-entity-layer";
import { StarGraphZoomControls } from "./star-graph-zoom-controls";
import "./star-graph-canvas.css";

export interface StarGraphCanvasProps {
  model: StarCanvasViewModel;
  /** Stable research-session id used to restore camera state without cross-session leakage. */
  cameraSessionId?: string;
  selectedNodeId?: string | null;
  onSelectNode?: (nodeId: string) => void;
  onOpenNode?: (nodeId: string) => void;
  /** Server-declared one-layer expansion state; absent keeps legacy behavior. */
  expansionControl?: StarGraphExpansionControl;
  /** Server-declared committed absorption used only for outgoing-node ghosts. */
  fusionTransition?: StarGraphFusionTransition | null;
  fusionLowPerformance?: boolean;
  summaryTitle?: string;
  summaryDetail?: string;
  filterHiddenNote?: string;
  showMapKey?: boolean;
  clusterLabels?: ReadonlyMap<string, string>;
  frontierLabel?: string;
  lensHints?: D5LensDisplayHints;
  motionDirectives?: ReadonlyMap<string, MotionDirective | null>;
  onHelp?: () => void;
  keyboardNav?: {
    nodes: ResearchGraphNode[];
    edges: GraphEdgeLike[];
    overlay?: CanvasOverlayLayer;
    onCloseOverlay?: (layer: "ring" | "detail") => void;
  };
  rightPanelWidth?: number;
  nodeAccessibleNames?: ReadonlyMap<string, string>;
  relatedNodeIds?: ReadonlySet<string>;
  /** V6-only semantic policy: S-tier relations appear only for the selected S node. */
  hideUnselectedSTierRelations?: boolean;
  /** V6-only screen-space label selection for M+ landmarks. */
  semanticLandmarkLabels?: boolean;
  /** V6 micro-node treatment; legacy runs retain labeled S nodes. */
  sTierPresentation?: "label" | "point";
  /** When set and no persisted viewport exists, initial camera fits these entities only. */
  initialFitEntityIdList?: readonly string[];
  entityBudget?: number;
  hiddenCountLabel?: (count: number) => string;
  loadMoreLabel?: string;
  onLoadMore?: () => void;
  loadMorePending?: boolean;
  typedNodes?: readonly TypedGraphNode[];
  canvasFilter?: ResearchCanvasFilter;
  className?: string;
}

const DEFAULT_CAMERA: StarGraphCamera = { x: 0, y: 0, zoom: 1 };
const EMPTY_CANVAS_FILTER: ResearchCanvasFilter = emptyCanvasFilter();

export function StarGraphCanvas({
  model,
  cameraSessionId,
  selectedNodeId = null,
  onSelectNode,
  onOpenNode,
  expansionControl,
  fusionTransition,
  fusionLowPerformance = false,
  summaryTitle,
  summaryDetail,
  filterHiddenNote,
  showMapKey = true,
  clusterLabels,
  frontierLabel,
  lensHints,
  motionDirectives,
  onHelp,
  keyboardNav,
  rightPanelWidth = 0,
  nodeAccessibleNames,
  relatedNodeIds,
  hideUnselectedSTierRelations = false,
  semanticLandmarkLabels = false,
  sTierPresentation = "label",
  initialFitEntityIdList,
  entityBudget = STAR_GRAPH_SEMANTIC_NODE_BUDGET,
  hiddenCountLabel,
  loadMoreLabel,
  onLoadMore,
  loadMorePending = false,
  typedNodes,
  canvasFilter = EMPTY_CANVAS_FILTER,
  className,
}: StarGraphCanvasProps) {
  const { t } = useT("research");
  const rootRef = useRef<HTMLDivElement>(null);
  const previousEntitiesRef = useRef<readonly StarEntityView[]>(model.entities);
  const initialCameraRef = useRef(false);
  const storedViewport = useResearchCanvasStore((s) =>
    cameraSessionId
      ? (s.viewportBySession?.[cameraSessionId] ?? null)
      : s.viewport,
  );
  const setStoredViewport = useResearchCanvasStore((s) => s.setViewport);
  const setSessionViewport = useResearchCanvasStore((s) => s.setSessionViewport);
  const [viewport, setViewport] = useState({ width: 0, height: 0 });
  const [camera, setCameraState] = useState<StarGraphCamera>(
    () => storedViewport ?? DEFAULT_CAMERA,
  );
  const [liveText, setLiveText] = useState("");
  const previousModelRef = useRef<StarCanvasViewModel | null>(null);
  const collapseSnapshotRef = useRef<{
    sequence: string;
    ghosts: ReturnType<typeof selectStarGraphCollapseGhosts>;
  } | null>(null);
  const collapseGhosts = useMemo(
    () => {
      const transition = expansionControl?.transition;
      if (transition?.kind !== "collapse") {
        collapseSnapshotRef.current = null;
        return [];
      }
      const sequence = String(transition.sequence);
      if (collapseSnapshotRef.current?.sequence === sequence) {
        return collapseSnapshotRef.current.ghosts;
      }
      const ghosts = selectStarGraphCollapseGhosts(
        previousModelRef.current,
        model,
        transition,
      );
      collapseSnapshotRef.current = { sequence, ghosts };
      return ghosts;
    },
    [expansionControl?.transition, model],
  );
  useEffect(() => {
    previousModelRef.current = model;
  }, [model]);
  const [cameraTransitioning, setCameraTransitioning] = useState(false);
  const cameraTransitionTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const entityMotionDirectives = useMemo(() => {
    const expansionDirectives = buildStarGraphExpansionMotion(
      model,
      expansionControl?.transition,
      expansionControl?.lowPerformance,
    );
    if (expansionDirectives.size === 0) return motionDirectives;
    return new Map([
      ...(motionDirectives?.entries() ?? []),
      ...expansionDirectives.entries(),
    ]);
  }, [expansionControl?.lowPerformance, expansionControl?.transition, model, motionDirectives]);
  const expansionRelationIds = useMemo(
    () =>
      selectStarGraphExpansionRelationIds(
        model,
        expansionControl?.transition,
      ),
    [expansionControl?.transition, model],
  );
  const fusionGhosts = useMemo(
    () =>
      buildStarGraphFusionGhosts(
        previousEntitiesRef.current,
        model.entities,
        fusionTransition,
      ),
    [fusionTransition, model.entities],
  );
  useEffect(() => {
    previousEntitiesRef.current = model.entities;
  }, [model.entities]);
  const dragRef = useRef<{ startX: number; startY: number; cameraX: number; cameraY: number } | null>(
    null,
  );
  const entityLabels = useMemo<StarGraphEntityLabels>(
    () => ({
      tierHeaders: {
        xxl: t(($) => $.d5.star_graph.tier_headers.xxl),
        xl: t(($) => $.d5.star_graph.tier_headers.xl),
        l: t(($) => $.d5.star_graph.tier_headers.l),
        m: t(($) => $.d5.star_graph.tier_headers.m),
        s: "",
      },
      documentCount: (count) =>
        t(($) => $.d5.star_graph.metrics.documents, { count }),
      confidence: (value) =>
        t(($) => $.d5.star_graph.metrics.confidence, { value }),
      conclusionCount: (count) =>
        t(($) => $.d5.star_graph.metrics.conclusions, { count }),
      documentBadge: (count) =>
        t(($) => $.d5.star_graph.metrics.document_badge, { count }),
    }),
    [t],
  );
  const mapKeyLabels = useMemo(
    () => ({
      ariaLabel: t(($) => $.d5.star_graph.map_key.aria_label),
      mapKey: t(($) => $.d5.star_graph.map_key.title),
      agentTier: t(($) => $.d5.star_graph.map_key.agent_tier),
      help: t(($) => $.d5.star_graph.map_key.help),
      tierDescriptions: {
        xxl: t(($) => $.d5.star_graph.map_key.tiers.xxl),
        xl: t(($) => $.d5.star_graph.map_key.tiers.xl),
        l: t(($) => $.d5.star_graph.map_key.tiers.l),
        m: t(($) => $.d5.star_graph.map_key.tiers.m),
        s: t(($) => $.d5.star_graph.map_key.tiers.s),
      },
      relations: {
        decompose: {
          label: t(($) => $.d5.star_graph.map_key.relations.decompose.label),
          description: t(
            ($) => $.d5.star_graph.map_key.relations.decompose.description,
          ),
        },
        support: {
          label: t(($) => $.d5.star_graph.map_key.relations.support.label),
          description: t(
            ($) => $.d5.star_graph.map_key.relations.support.description,
          ),
        },
        challenge: {
          label: t(($) => $.d5.star_graph.map_key.relations.challenge.label),
          description: t(
            ($) => $.d5.star_graph.map_key.relations.challenge.description,
          ),
        },
        newdir: {
          label: t(($) => $.d5.star_graph.map_key.relations.newdir.label),
          description: t(
            ($) => $.d5.star_graph.map_key.relations.newdir.description,
          ),
        },
        neutral: {
          label: t(($) => $.d5.star_graph.map_key.relations.neutral.label),
          description: t(
            ($) => $.d5.star_graph.map_key.relations.neutral.description,
          ),
        },
      },
    }),
    [t],
  );
  const zoomLabels = useMemo(
    () => ({
      zoomOut: t(($) => $.d5.star_graph.zoom.out),
      zoomIn: t(($) => $.d5.star_graph.zoom.in),
      fit: t(($) => $.d5.star_graph.zoom.fit),
    }),
    [t],
  );

  const setCamera = useCallback(
    (next: StarGraphCamera | ((current: StarGraphCamera) => StarGraphCamera)) => {
      setCameraState((current) => {
        const resolved = typeof next === "function" ? next(current) : next;
        if (cameraSessionId && setSessionViewport) {
          setSessionViewport(cameraSessionId, resolved);
        } else {
          setStoredViewport(resolved);
        }
        return resolved;
      });
    },
    [cameraSessionId, setSessionViewport, setStoredViewport],
  );

  const stopCameraTransition = useCallback(() => {
    if (cameraTransitionTimerRef.current) {
      clearTimeout(cameraTransitionTimerRef.current);
      cameraTransitionTimerRef.current = null;
    }
    setCameraTransitioning(false);
  }, []);

  const beginCameraTransition = useCallback(() => {
    if (cameraTransitionTimerRef.current) {
      clearTimeout(cameraTransitionTimerRef.current);
    }
    setCameraTransitioning(true);
    cameraTransitionTimerRef.current = setTimeout(() => {
      cameraTransitionTimerRef.current = null;
      setCameraTransitioning(false);
    }, 420);
  }, []);

  useEffect(() => stopCameraTransition, [stopCameraTransition]);

  const bounds = useMemo(() => computeEntityBounds(model.entities), [model.entities]);

  const typedNodeIndex = useMemo(
    () => (typedNodes ? indexTypedGraphNodes(typedNodes) : new Map()),
    [typedNodes],
  );

  const displayEntities = useMemo(
    () =>
      filterEntitiesForCanvasDisplay(model.entities, {
        filter: canvasFilter,
        nodeById: typedNodeIndex,
        rootId: model.rootId,
        selectedNodeId,
        relatedNodeIds,
      }),
    [
      canvasFilter,
      model.entities,
      model.rootId,
      relatedNodeIds,
      selectedNodeId,
      typedNodeIndex,
    ],
  );

  const visibleEntityIds = useMemo(
    () =>
      selectVisibleEntityIds(displayEntities, {
        rootId: model.rootId,
        selectedNodeId,
        relatedNodeIds,
        budget: entityBudget,
        zoom: camera.zoom,
      }),
    [
      camera.zoom,
      displayEntities,
      entityBudget,
      model.rootId,
      relatedNodeIds,
      selectedNodeId,
    ],
  );

  const clusterHiddenCounts = useMemo(
    () => computeClusterHiddenCounts(displayEntities, visibleEntityIds),
    [displayEntities, visibleEntityIds],
  );

  const visibleEntities = useMemo(
    () => displayEntities.filter((entity) => visibleEntityIds.has(entity.id)),
    [displayEntities, visibleEntityIds],
  );

  const visibleLabelNodeIds = useMemo(
    () =>
      selectSemanticLabelNodeIds(visibleEntities, {
        zoom: camera.zoom,
        selectedNodeId,
        enabled: semanticLandmarkLabels,
      }),
    [camera.zoom, selectedNodeId, semanticLandmarkLabels, visibleEntities],
  );

  const nodeTierById = useMemo(
    () => new Map(model.entities.map((entity) => [entity.id, entity.tier])),
    [model.entities],
  );

  const visibleRelations = useMemo(
    () =>
      filterRelationsToVisibleEntities(model.relations, visibleEntityIds, {
        budget: edgeBudgetForViewport(viewport.width),
        focusNodeId: selectedNodeId ?? model.rootId,
        relatedNodeIds,
        nodeTierById: hideUnselectedSTierRelations ? nodeTierById : undefined,
      }),
    [
      model.relations,
      model.rootId,
      hideUnselectedSTierRelations,
      nodeTierById,
      relatedNodeIds,
      selectedNodeId,
      viewport.width,
      visibleEntityIds,
    ],
  );
  const focusedLensHints = useMemo(
    () =>
      focusD5LensDisplayHints(
        lensHints,
        model,
        selectedNodeId,
        relatedNodeIds,
      ),
    [lensHints, model, relatedNodeIds, selectedNodeId],
  );

  const hiddenEntityCount = displayEntities.length - visibleEntities.length;

  const initialCameraInputsRef = useRef({
    bounds,
    initialFitEntityIdList,
    entities: model.entities,
    storedViewport,
  });
  initialCameraInputsRef.current = {
    bounds,
    initialFitEntityIdList,
    entities: model.entities,
    storedViewport,
  };

  useEffect(() => {
    initialCameraRef.current = false;
    if (storedViewport) {
      setCameraState(storedViewport);
      initialCameraRef.current = true;
      return;
    }
    if (!bounds || viewport.width <= 0 || viewport.height <= 0) return;
    const neighborhoodBounds =
      initialFitEntityIdList && initialFitEntityIdList.length > 0
        ? computeEntityBoundsForIds(model.entities, new Set(initialFitEntityIdList))
        : null;
    setCamera(fitCameraToBounds(neighborhoodBounds ?? bounds, viewport));
    initialCameraRef.current = true;
  }, [
    bounds,
    cameraSessionId,
    initialFitEntityIdList,
    model.entities,
    setCamera,
    storedViewport,
    viewport,
  ]);

  useEffect(() => {
    const node = rootRef.current;
    if (!node) return;

    const applyInitialCamera = (nextViewport: { width: number; height: number }) => {
      if (initialCameraRef.current || nextViewport.width <= 0 || nextViewport.height <= 0) {
        return;
      }
      const {
        bounds: nextBounds,
        initialFitEntityIdList: fitList,
        entities,
        storedViewport: persisted,
      } = initialCameraInputsRef.current;
      if (!nextBounds) return;
      if (persisted) {
        setCameraState(persisted);
        initialCameraRef.current = true;
        return;
      }
      const neighborhoodBounds =
        fitList && fitList.length > 0
          ? computeEntityBoundsForIds(entities, new Set(fitList))
          : null;
      const fitBounds = neighborhoodBounds ?? nextBounds;
      setCamera(fitCameraToBounds(fitBounds, nextViewport));
      initialCameraRef.current = true;
    };

    const observer = new ResizeObserver((entries) => {
      const entry = entries[0];
      if (!entry) return;
      const nextViewport = {
        width: entry.contentRect.width,
        height: entry.contentRect.height,
      };
      setViewport(nextViewport);
      applyInitialCamera(nextViewport);
    });
    observer.observe(node);
    const rect = node.getBoundingClientRect();
    applyInitialCamera({ width: rect.width, height: rect.height });
    return () => observer.disconnect();
  }, [setCamera]);

  const fitToContent = useCallback(() => {
    if (!bounds || viewport.width <= 0 || viewport.height <= 0) return;
    beginCameraTransition();
    setCamera(fitCameraToBounds(bounds, viewport));
  }, [beginCameraTransition, bounds, setCamera, viewport]);

  const focusNodeButton = useCallback((nodeId: string) => {
    const buttons = rootRef.current?.querySelectorAll<HTMLElement>(
      '[data-testid="star-graph-node"]',
    );
    for (const button of buttons ?? []) {
      if (button.dataset.nodeId === nodeId) {
        button.focus({ preventScroll: true });
        return;
      }
    }
    rootRef.current?.focus({ preventScroll: true });
  }, []);

  const focusSelectedEntity = useCallback(
    (nodeId: string | null) => {
      if (!nodeId || viewport.width <= 0 || viewport.height <= 0) return;
      const entity = model.entities.find((candidate) => candidate.id === nodeId);
      if (!entity) return;
      if (nodeId === model.rootId) {
        fitToContent();
        focusNodeButton(nodeId);
        return;
      }
      beginCameraTransition();
      setCamera((current) =>
        focusCameraOnEntity(
          entity,
          viewport,
          current,
          { rightPanelWidth },
        ),
      );
      focusNodeButton(nodeId);
    },
    [
      beginCameraTransition,
      fitToContent,
      focusNodeButton,
      model.entities,
      model.rootId,
      rightPanelWidth,
      setCamera,
      viewport,
    ],
  );

  useEffect(() => {
    if (!selectedNodeId) return;
    focusSelectedEntity(selectedNodeId);
  }, [focusSelectedEntity, rightPanelWidth, selectedNodeId]);

  const handleZoomIn = useCallback(() => {
    stopCameraTransition();
    setCamera((current) =>
      zoomCamera(current, current.zoom * 1.12, {
        x: viewport.width / 2,
        y: viewport.height / 2,
      }),
    );
  }, [setCamera, stopCameraTransition, viewport.height, viewport.width]);

  const handleZoomOut = useCallback(() => {
    stopCameraTransition();
    setCamera((current) =>
      zoomCamera(current, current.zoom / 1.12, {
        x: viewport.width / 2,
        y: viewport.height / 2,
      }),
    );
  }, [setCamera, stopCameraTransition, viewport.height, viewport.width]);

  const handleWheel = useCallback(
    (event: ReactWheelEvent<HTMLDivElement>) => {
      event.preventDefault();
      stopCameraTransition();
      const rect = rootRef.current?.getBoundingClientRect();
      if (!rect) return;
      const anchor = {
        x: event.clientX - rect.left,
        y: event.clientY - rect.top,
      };
      const delta = event.deltaY > 0 ? 0.92 : 1.08;
      setCamera((current) => zoomCamera(current, current.zoom * delta, anchor));
    },
    [setCamera, stopCameraTransition],
  );

  const handlePointerDown = useCallback((event: ReactPointerEvent<HTMLDivElement>) => {
    if (event.button !== 0) return;
    if ((event.target as HTMLElement).closest('[data-testid="star-graph-node"], button')) {
      return;
    }
    stopCameraTransition();
    event.currentTarget.setPointerCapture(event.pointerId);
    dragRef.current = {
      startX: event.clientX,
      startY: event.clientY,
      cameraX: camera.x,
      cameraY: camera.y,
    };
  }, [camera.x, camera.y, stopCameraTransition]);

  const handlePointerMove = useCallback((event: ReactPointerEvent<HTMLDivElement>) => {
    const drag = dragRef.current;
    if (!drag) return;
    setCamera((current) => ({
      ...current,
      x: drag.cameraX + (event.clientX - drag.startX),
      y: drag.cameraY + (event.clientY - drag.startY),
    }));
  }, [setCamera]);

  const handlePointerUp = useCallback((event: ReactPointerEvent<HTMLDivElement>) => {
    dragRef.current = null;
    if (event.currentTarget.hasPointerCapture(event.pointerId)) {
      event.currentTarget.releasePointerCapture(event.pointerId);
    }
  }, []);

  const applyKeyboardAction = useCallback(
    (action: CanvasKeyboardAction, focusId: string | null) => {
      switch (action.type) {
        case "moveFocus": {
          onSelectNode?.(action.nodeId);
          const node = keyboardNav?.nodes.find((candidate) => candidate.id === action.nodeId);
          if (node) {
            setLiveText(
              nodeAccessibleNames?.get(node.id) ?? buildNodeAccessibleName(node),
            );
          }
          focusNodeButton(action.nodeId);
          return;
        }
        case "openDetail":
          if (!focusId) return;
          if (expansionControl?.expandableNodeIds.has(focusId)) {
            onSelectNode?.(focusId);
            expansionControl.onToggleNode(focusId);
          } else {
            onOpenNode?.(focusId);
          }
          return;
        case "closeOverlay":
          keyboardNav?.onCloseOverlay?.(action.layer);
          rootRef.current?.focus();
          return;
        case "zoomIn":
          handleZoomIn();
          return;
        case "zoomOut":
          handleZoomOut();
          return;
        case "fitView":
          fitToContent();
          return;
        default:
          return;
      }
    },
    [
      fitToContent,
      focusNodeButton,
      handleZoomIn,
      handleZoomOut,
      keyboardNav,
      nodeAccessibleNames,
      onOpenNode,
      onSelectNode,
      expansionControl,
    ],
  );

  const handleKeyDown = useCallback(
    (event: ReactKeyboardEvent<HTMLDivElement>) => {
      if (!keyboardNav) return;
      const focusId = selectedNodeId ?? null;
      if ((event.key === "f" || event.key === "F") && focusId) {
        event.preventDefault();
        focusSelectedEntity(focusId);
        const node = keyboardNav.nodes.find((candidate) => candidate.id === focusId);
        if (node) {
          setLiveText(
            nodeAccessibleNames?.get(node.id) ?? buildNodeAccessibleName(node),
          );
        }
        return;
      }
      const action = resolveCanvasKeyEvent(event, {
        focusId,
        nodes: keyboardNav.nodes,
        edges: keyboardNav.edges,
        overlay: keyboardNav.overlay ?? null,
      });
      if (action.type === "noop") return;
      event.preventDefault();
      applyKeyboardAction(action, focusId);
    },
    [
      applyKeyboardAction,
      focusSelectedEntity,
      keyboardNav,
      nodeAccessibleNames,
      selectedNodeId,
    ],
  );

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
      role="application"
      tabIndex={keyboardNav ? 0 : undefined}
      aria-label={t(($) => $.d5.star_graph.canvas_label)}
      onPointerDown={handlePointerDown}
      onPointerMove={handlePointerMove}
      onPointerUp={handlePointerUp}
      onPointerCancel={handlePointerUp}
      onWheel={handleWheel}
      onKeyDown={handleKeyDown}
    >
      {keyboardNav ? (
        <div className="sr-only" aria-live="polite" data-testid="star-graph-canvas-live">
          {liveText}
        </div>
      ) : null}
      {(summaryTitle || summaryDetail || filterHiddenNote || hiddenEntityCount > 0 || onLoadMore) && (
        <div data-testid="star-graph-summary" className="sg-summary-label">
          {summaryTitle && <b>{summaryTitle}</b>}
          {summaryDetail && <span>{summaryDetail}</span>}
          {filterHiddenNote ? (
            <span data-testid="star-graph-filter-note">{filterHiddenNote}</span>
          ) : null}
          {hiddenEntityCount > 0 ? (
            <span data-testid="star-graph-budget-note">
              · {visibleEntities.length}/{displayEntities.length}
            </span>
          ) : null}
          {onLoadMore && loadMoreLabel ? (
            <button
              type="button"
              data-testid="star-graph-load-more"
              className="sg-summary-load-more"
              disabled={loadMorePending}
              onClick={onLoadMore}
            >
              {loadMoreLabel}
            </button>
          ) : null}
        </div>
      )}

      <div
        className={cn(
          "sg-canvas-world",
          cameraTransitioning && "sg-camera-transitioning",
        )}
        style={{
          width: worldSize.width,
          height: worldSize.height,
          transform: `translate(${camera.x}px, ${camera.y}px) scale(${camera.zoom})`,
        }}
      >
        <StarGraphClusterLayer
          clusters={model.clusters}
          frontiers={model.frontiers}
          frontierLabel={frontierLabel}
          clusterLabels={clusterLabels}
          hiddenCounts={clusterHiddenCounts}
          hiddenCountLabel={hiddenCountLabel}
        />
        <StarGraphEdges
          relations={visibleRelations}
          labelObstacles={visibleEntities}
          width={worldSize.width}
          height={worldSize.height}
          lensHints={focusedLensHints}
          relationLabels={{
            decompose: mapKeyLabels.relations.decompose.label,
            support: mapKeyLabels.relations.support.label,
            challenge: mapKeyLabels.relations.challenge.label,
            newdir: mapKeyLabels.relations.newdir.label,
          }}
          revealingRelationIds={expansionRelationIds}
          revealLowPerformance={expansionControl?.lowPerformance}
        />
        <StarGraphFusionGhostLayer
          ghosts={fusionGhosts}
          lowPerformance={fusionLowPerformance}
        />
        <StarGraphEntityLayer
          entities={visibleEntities}
          selectedNodeId={selectedNodeId}
          nodeAccessibleNames={nodeAccessibleNames}
          lensHints={focusedLensHints}
          motionDirectives={entityMotionDirectives}
          expansionControl={expansionControl}
          visibleLabelNodeIds={visibleLabelNodeIds}
          sTierPresentation={sTierPresentation}
          labels={entityLabels}
          onSelectNode={onSelectNode}
          onOpenNode={onOpenNode}
        />
        <StarGraphCollapseGhostLayer
          ghosts={collapseGhosts}
          labels={entityLabels}
          lowPerformance={expansionControl?.lowPerformance}
          sTierPresentation={sTierPresentation}
        />
      </div>

      {showMapKey ? (
        <StarGraphMapKey
          onHelp={onHelp}
          labels={mapKeyLabels}
          className="absolute bottom-4 left-5 z-10"
        />
      ) : null}

      <StarGraphZoomControls
        zoomPct={zoomPercent(camera)}
        onZoomIn={handleZoomIn}
        onZoomOut={handleZoomOut}
        onFit={fitToContent}
        labels={zoomLabels}
      />
    </div>
  );
}
