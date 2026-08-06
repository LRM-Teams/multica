"use client";

import { useCallback, useEffect, useMemo, useReducer, useRef, type KeyboardEvent } from "react";
import {
  Background,
  BackgroundVariant,
  MiniMap,
  Panel,
  ReactFlow,
  ReactFlowProvider,
  useEdgesState,
  useNodesState,
  useReactFlow,
  type Node,
  type NodeTypes,
} from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import type {
  ResearchFleetMember,
  ResearchGraphEdge,
  ResearchGraphNode,
  ResearchNodeCommandAction,
  ResearchRunSnapshot,
  ResearchSource,
} from "@multica/core/types";
import type { ResearchPresenceMap } from "@multica/core/research";
import { useIsMobile } from "@multica/ui/hooks/use-mobile";
import {
  AUX_DRAWER_WIDTH_PX,
  CONTROLS_BOTTOM_PX,
  DETAIL_CARD_BOTTOM_PX,
  MINIMAP_HEIGHT_PX,
  MINIMAP_WIDTH_PX,
  OVERLAY_GAP_PX,
  OVERLAY_INSET_PX,
} from "../lib/canvas-overlay-grid";
import type { ChatDrawerMode } from "../lib/chat-drawer-mode";
import {
  resolveCanvasKeyEvent,
  type CanvasKeyboardAction,
  type CanvasOverlayLayer,
} from "../lib/canvas-keyboard-nav";
import type { ResearchFlowNodeData } from "../lib/layout-graph";
import { layoutResearchCanvas } from "../lib/research-canvas-layout";
import { LOGIC_END_NODE_ID, isLogicEndNode } from "../lib/logic-lanes";
import {
  NODE_ENTER_CLASS,
  nodeEnterDelayStyle,
  nodeEnterMotionCss,
  nodeEnterStaggerDelayMs,
} from "../lib/node-enter-motion";
import {
  buildNodeSnapshotMap,
  classifyCanvasDelta,
  reorgBadgeCss,
  reorgTransitionCss,
  REORG_TOTAL_BUDGET_MS,
  P2_DURATION_MS,
  type CanvasNodeSnapshot,
} from "../lib/canvas-reorg-motion";
import { ResearchCanvasDock } from "./research-canvas-dock";
import { ResearchCanvasPluginShell } from "../canvas-plugins/plugin-shell";
import type { ResearchCanvasNodeContext } from "../canvas-plugins/types";
import { ResearchChatFab } from "./research-chat-fab";
import { ResearchFleetAvatarStack } from "./research-fleet-avatar-stack";
import { ResearchGraphNode as ResearchGraphNodeView } from "./research-graph-node";
import { ResearchGitGutterNodeView } from "./research-git-gutter-node";
import { ResearchGitList } from "./research-git-list";
import type { ResearchAuxPanelId } from "./research-module-rail";
import { SYSTEM_NODE_TYPES } from "../lib/node-action-ring";
import { ResearchNodeDetail } from "./research-node-detail";
import { ResearchRunGateBlockers } from "./research-run-gate-blockers";
import type { RunV2GateBlocker } from "../lib/run-v2-canvas-view-model";
import { useT } from "../../i18n/use-t";
import { useResearchCamera } from "../canvas/camera/use-research-camera";

const NODE_ENTER_MOTION_CSS = nodeEnterMotionCss();
const REORG_TRANSITION_CSS = reorgTransitionCss();
const REORG_BADGE_CSS = reorgBadgeCss();

const nodeTypes: NodeTypes = {
  research: ResearchGraphNodeView,
  gitGutter: ResearchGitGutterNodeView,
};

const EMPTY_FLEET_MEMBERS: ResearchFleetMember[] = [];
const EMPTY_RUN_BLOCKERS: RunV2GateBlocker[] = [];

function prefersReducedMotion(): boolean {
  if (typeof window === "undefined" || !window.matchMedia) return false;
  return window.matchMedia("(prefers-reduced-motion: reduce)").matches;
}

/** Derive the read-only display snapshot handed to canvas plugin slots. */
function toCanvasPluginNodeContexts(
  nodes: readonly ResearchGraphNode[],
  selectedId: string | null | undefined,
): ResearchCanvasNodeContext[] {
  return nodes.map((n) => ({
    id: n.id,
    kind: n.node_type,
    title: n.title,
    status: n.status,
    selected: n.id === selectedId,
  }));
}

type FlowNode = Node<ResearchFlowNodeData>;

type CanvasUiState = {
  detailPinned: boolean;
  pinnedNodeId: string | null;
  menuNodeId: string | null;
  zoomPct: number;
  liveText: string;
};

type CanvasUiAction =
  | { type: "pin"; nodeId: string }
  | { type: "clearDetail" }
  | { type: "setMenu"; nodeId: string | null }
  | { type: "setZoom"; pct: number }
  | { type: "setLive"; text: string };

const initialCanvasUi: CanvasUiState = {
  detailPinned: false,
  pinnedNodeId: null,
  menuNodeId: null,
  zoomPct: 100,
  liveText: "",
};

function canvasUiReducer(state: CanvasUiState, action: CanvasUiAction): CanvasUiState {
  switch (action.type) {
    case "pin":
      return { ...state, pinnedNodeId: action.nodeId, detailPinned: true, menuNodeId: null };
    case "clearDetail":
      return { ...state, detailPinned: false, pinnedNodeId: null };
    case "setMenu":
      return state.menuNodeId === action.nodeId ? state : { ...state, menuNodeId: action.nodeId };
    case "setZoom":
      return state.zoomPct === action.pct ? state : { ...state, zoomPct: action.pct };
    case "setLive":
      return { ...state, liveText: action.text };
    default:
      return state;
  }
}

function ResearchCanvasInner({
  nodes,
  edges,
  sources,
  members = EMPTY_FLEET_MEMBERS,
  run,
  runBlockers = EMPTY_RUN_BLOCKERS,
  runDegraded = false,
  sessionStatus,
  presence,
  selectedId,
  onSelect,
  onRetry,
  onNodeCommand,
  onOpenDelivery,
  onOpenChat,
  chatOpen = false,
  chatMode = "empty",
  detailPlacement = "overlay",
  onOpenDetail,
  auxPanel = null,
  onAuxPanelSelect,
}: {
  nodes: ResearchGraphNode[];
  edges: ResearchGraphEdge[];
  sources?: ResearchSource[];
  members?: ResearchFleetMember[];
  run?: ResearchRunSnapshot;
  runBlockers?: RunV2GateBlocker[];
  runDegraded?: boolean;
  sessionStatus?: string | null;
  presence?: ResearchPresenceMap;
  selectedId?: string | null;
  onSelect?: (node: ResearchGraphNode | null) => void;
  onRetry?: (node: ResearchGraphNode) => void;
  onNodeCommand?: (node: ResearchGraphNode, action: ResearchNodeCommandAction) => Promise<void>;
  onOpenDelivery?: () => void;
  onOpenChat?: () => void;
  chatOpen?: boolean;
  chatMode?: ChatDrawerMode;
  detailPlacement?: "overlay" | "drawer";
  onOpenDetail?: (node: ResearchGraphNode) => void;
  /** LRM-1151 — active 轨/源/详 module (drawer content owned by session page). */
  auxPanel?: ResearchAuxPanelId | null;
  onAuxPanelSelect?: (id: ResearchAuxPanelId) => void;
}) {
  const { t } = useT("research");
  // react-doctor-disable-next-line react-doctor/no-event-handler -- flags the layout effect below that classifies the node delta and starts the LRM-1335 reorg motion; it reacts to `nodes`/`edges` arriving from the session query/WS subscription, not a local user event this component can hook a handler into.
  const canvasLayout = useMemo(() => layoutResearchCanvas(nodes, edges), [nodes, edges]);
  const laid = canvasLayout.layout;
  const topology = canvasLayout.topology;
  const [rfNodes, setRfNodes, onNodesChange] = useNodesState(laid.nodes);
  const [rfEdges, setRfEdges, onEdgesChange] = useEdgesState(laid.edges);
  const { fitView, zoomIn, zoomOut, getZoom, getNode, getViewport, setViewport } =
    useReactFlow();
  const isMobile = useIsMobile();
  const [ui, dispatch] = useReducer(canvasUiReducer, initialCanvasUi);
  const { detailPinned, pinnedNodeId, menuNodeId, zoomPct, liveText } = ui;
  // Keyboard focus is handler-only (announce + setCenter); keep off the render path.
  const focusIdRef = useRef<string | null>(null);
  const enterClassById = useRef<Map<string, string> | null>(null);
  if (enterClassById.current === null) {
    enterClassById.current = new Map();
  }
  const laidIdsKey = useMemo(() => laid.nodes.map((n) => n.id).join("|"), [laid.nodes]);

  // LRM-1335: reorg animation state
  const prevSnapshotRef = useRef<Map<string, CanvasNodeSnapshot> | null>(null);
  if (prevSnapshotRef.current === null) {
    prevSnapshotRef.current = new Map();
  }
  const reorgTimerRef = useRef<number | null>(null);
  const reorgActiveRef = useRef(false);
  const fitViewAbortedRef = useRef(false);
  const canvasRootRef = useRef<HTMLDivElement | null>(null);

  const announce = useCallback((text: string) => {
    dispatch({ type: "setLive", text: "" });
    // Retrigger polite live region for identical strings.
    requestAnimationFrame(() => dispatch({ type: "setLive", text }));
  }, []);

  // FE-05: safe-centre camera (interruption-safe focus, drag hand-off, reduced
  // motion). Replaced the ad-hoc `setCenter` focus path below.
  const camera = useResearchCamera({
    getViewport: () => getViewport(),
    setViewport: (vp) => {
      void setViewport({ x: vp.x, y: vp.y, zoom: vp.zoom });
    },
    getContainerSize: () => {
      const root = canvasRootRef.current;
      return root
        ? { width: Math.max(1, root.clientWidth), height: Math.max(1, root.clientHeight) }
        : { width: 1, height: 1 };
    },
    reducedMotion: prefersReducedMotion(),
    announce,
  });

  // LRM-1335: reorg orchestration — classify delta and set data-reorg attr
  const startReorg = useCallback(
    (movedCount: number, newPathCount: number) => {
      const root = canvasRootRef.current;
      if (!root) return;
      reorgActiveRef.current = true;
      fitViewAbortedRef.current = false;
      root.setAttribute("data-reorg", "running");
      // P0: immediate a11y broadcast
      announce(t(($) => $.a11y.reorg_start));

      // P5: cleanup at total budget
      if (reorgTimerRef.current !== null) {
        window.clearTimeout(reorgTimerRef.current);
      }
      reorgTimerRef.current = window.setTimeout(() => {
        if (!reorgActiveRef.current) return;
        reorgActiveRef.current = false;
        root.setAttribute("data-reorg", "");
        // P5 broadcast
        announce(t(($) => $.a11y.reorg_done, { count: movedCount, paths: newPathCount }));
        reorgTimerRef.current = null;
      }, REORG_TOTAL_BUDGET_MS);
    },
    [announce, t],
  );

  /** AC #6: onMoveStart cancels reorg fitView, instant settle. */
  const abortReorgOnMove = useCallback(() => {
    if (!reorgActiveRef.current) return;
    fitViewAbortedRef.current = true;
    // Instant settle: clear reorg state immediately
    reorgActiveRef.current = false;
    const root = canvasRootRef.current;
    if (root) root.setAttribute("data-reorg", "");
    if (reorgTimerRef.current !== null) {
      window.clearTimeout(reorgTimerRef.current);
      reorgTimerRef.current = null;
    }
  }, []);

  useEffect(() => {
    const reduceMotion = prefersReducedMotion();
    const classMap = enterClassById.current!;
    const delayById = new Map<string, number>();
    let newEnterIndex = 0;

    // LRM-1335: classify delta before updating nodes
    const nextSnapshot = buildNodeSnapshotMap(laid.nodes);
    const delta = classifyCanvasDelta(prevSnapshotRef.current ?? new Map(), nextSnapshot);
    prevSnapshotRef.current = nextSnapshot;

    const isReorg = delta.kind === "reorg" && !reduceMotion;
    if (isReorg) {
      startReorg(delta.movedIds.length + delta.removedIds.length, delta.addedIds.length);
    }

    for (const n of laid.nodes) {
      const research = n.data.research;
      const isChrome = n.type === "gitGutter" || !research;
      if (!isChrome && !classMap.has(n.id) && !reduceMotion) {
        classMap.set(n.id, NODE_ENTER_CLASS);
        delayById.set(n.id, nodeEnterStaggerDelayMs(newEnterIndex));
        newEnterIndex += 1;
      }
    }

    setRfNodes(
      laid.nodes.map((n) => {
        const research = n.data.research;
        const isChrome = n.type === "gitGutter" || !research;
        const actorId = research?.actor_agent_id;
        const presenceLabel = actorId ? presence?.[actorId]?.activity : undefined;
        const delayMs = delayById.get(n.id) ?? 0;
        return {
          ...n,
          position: n.position,
          selected: !isChrome && n.id === selectedId,
          draggable: false,
          className: isChrome ? undefined : classMap.get(n.id),
          style: {
            ...n.style,
            ...(delayMs ? nodeEnterDelayStyle(delayMs) : undefined),
          },
          data: {
            ...n.data,
            presenceLabel: presenceLabel || undefined,
            sourceBadgeCount:
              research?.node_type === "finding" &&
              research.payload &&
              typeof research.payload === "object" &&
              "source_id" in (research.payload as object)
                ? 1
                : 0,
            onRetry: onRetry ?? undefined,
            onNodeCommand: onNodeCommand ?? undefined,
            onViewDetail: (node) => {
              if (isLogicEndNode(node)) {
                onOpenDelivery?.();
                return;
              }
              onSelect?.(node);
              if (detailPlacement === "drawer") onOpenDetail?.(node);
              else dispatch({ type: "pin", nodeId: node.id });
            },
            menuOpen: research?.id === menuNodeId,
            onMenuOpenChange: (open) => {
              if (!research) return;
              dispatch({ type: "setMenu", nodeId: open ? research.id : null });
            },
          },
        } satisfies FlowNode;
      }),
    );
    setRfEdges(laid.edges);
  }, [
    laid,
    laidIdsKey,
    selectedId,
    setRfNodes,
    setRfEdges,
    presence,
    onRetry,
    onNodeCommand,
    menuNodeId,
    detailPlacement,
    onOpenDetail,
    onOpenDelivery,
    onSelect,
    startReorg,
  ]);

  useEffect(() => {
    if (!laidIdsKey) return;
    // LRM-1335: if reorg just aborted by user pan, skip fitView
    if (fitViewAbortedRef.current) {
      fitViewAbortedRef.current = false;
      return;
    }
    const reduceMotion = prefersReducedMotion();
    const duration = reduceMotion ? 0 : (reorgActiveRef.current ? P2_DURATION_MS : 320);
    const id = window.setTimeout(() => {
      void fitView({
        padding: 0.18,
        duration,
      });
      dispatch({ type: "setZoom", pct: Math.round(getZoom() * 100) });
    }, 80);
    return () => window.clearTimeout(id);
  }, [laidIdsKey, fitView, getZoom]);

  const selectedNode =
    selectedId && selectedId !== LOGIC_END_NODE_ID
      ? nodes.find((n) => n.id === selectedId) ?? null
      : null;
  const pinnedNode = pinnedNodeId ? nodes.find((n) => n.id === pinnedNodeId) : null;
  const detailNode = selectedNode ?? pinnedNode;
  const sourceList = sources ?? [];
  const showOverlayDetail =
    detailPlacement === "overlay" &&
    (detailPinned ||
      (!!detailNode && SYSTEM_NODE_TYPES.has(detailNode.node_type)));

  const pinDetail = useCallback(
    (node: ResearchGraphNode) => {
      onSelect?.(node);
      announce(
        t(($) => $.a11y.opened_detail, { title: node.title }),
      );
      if (detailPlacement === "drawer") {
        onOpenDetail?.(node);
        dispatch({ type: "clearDetail" });
        return;
      }
      dispatch({ type: "pin", nodeId: node.id });
    },
    [detailPlacement, onOpenDetail, onSelect, announce, t],
  );

  const clearDetail = useCallback(() => {
    dispatch({ type: "clearDetail" });
    onSelect?.(null);
    announce(t(($) => $.a11y.closed_detail));
  }, [onSelect, announce, t]);

  const focusNode = useCallback(
    (id: string) => {
      focusIdRef.current = id;
      const topo = topology.get(id);
      const research = nodes.find((n) => n.id === id);
      const label = research
        ? t(($) => $.a11y.focus_node, {
            title: research.title,
            branch: topo?.branchId ?? "main",
          })
        : undefined;
      const rfNode = getNode(id);
      if (rfNode) {
        const w = (rfNode.measured?.width ?? rfNode.width ?? 240) as number;
        const h = (rfNode.measured?.height ?? rfNode.height ?? 76) as number;
        camera.focus(
          {
            x: rfNode.position.x,
            y: rfNode.position.y,
            width: w,
            height: h,
          },
          label,
        );
      } else if (label) {
        // Node not yet measured/renderable — still announce the move target.
        announce(label);
      }
    },
    [topology, nodes, t, getNode, camera, announce],
  );

  const locateNode = useCallback(
    (id: string) => {
      const node = nodes.find((candidate) => candidate.id === id);
      if (!node) return;
      focusNode(id);
      onSelect?.(node);
    },
    [focusNode, nodes, onSelect],
  );

  /** LRM-1105: apply pure keyboard actions (semantics A) — no neighborByLane B. */
  const applyKeyboardAction = useCallback(
    (action: CanvasKeyboardAction, currentId: string | null) => {
      switch (action.type) {
        case "moveFocus": {
          focusNode(action.nodeId);
          const n = nodes.find((x) => x.id === action.nodeId);
          if (n) onSelect?.(n);
          return;
        }
        case "openDetail": {
          if (!currentId) return;
          const n = nodes.find((x) => x.id === currentId);
          if (!n) return;
          if (isLogicEndNode(n)) onOpenDelivery?.();
          else pinDetail(n);
          return;
        }
        case "openRing": {
          if (!currentId) return;
          dispatch({
            type: "setMenu",
            nodeId: menuNodeId === currentId ? null : currentId,
          });
          return;
        }
        case "closeOverlay": {
          if (action.layer === "ring") {
            dispatch({ type: "setMenu", nodeId: null });
          } else {
            clearDetail();
          }
          return;
        }
        case "zoomIn":
          void zoomIn({ duration: prefersReducedMotion() ? 0 : 160 });
          return;
        case "zoomOut":
          void zoomOut({ duration: prefersReducedMotion() ? 0 : 160 });
          return;
        case "fitView":
          void fitView({
            padding: 0.18,
            duration: prefersReducedMotion() ? 0 : 240,
          });
          return;
        default:
          return;
      }
    },
    [
      focusNode,
      nodes,
      onSelect,
      onOpenDelivery,
      pinDetail,
      menuNodeId,
      clearDetail,
      zoomIn,
      zoomOut,
      fitView,
    ],
  );

  const handleDesktopKeyDown = useCallback(
    (e: KeyboardEvent<HTMLDivElement>) => {
      const current = focusIdRef.current ?? selectedId ?? null;
      const overlay: CanvasOverlayLayer = menuNodeId
        ? "ring"
        : showOverlayDetail || detailPinned
          ? "detail"
          : null;
      const action = resolveCanvasKeyEvent(
        { key: e.key, shiftKey: e.shiftKey },
        {
          focusId: current,
          nodes,
          edges,
          activeBranchId: current
            ? (topology.get(current)?.branchId ?? null)
            : null,
          overlay,
        },
      );
      if (action.type === "noop") return;
      e.preventDefault();
      applyKeyboardAction(action, current);
    },
    [
      selectedId,
      menuNodeId,
      showOverlayDetail,
      detailPinned,
      nodes,
      edges,
      topology,
      applyKeyboardAction,
    ],
  );

  const chatFab =
    !chatOpen && onOpenChat && !(isMobile && showOverlayDetail) ? (
      <ResearchChatFab mode={chatMode} onOpen={onOpenChat} isMobile={isMobile} />
    ) : null;

  const detailToggleOpen =
    detailPlacement === "drawer" ? auxPanel === "detail" : showOverlayDetail;

  const handleToggleDetail = useCallback(() => {
    if (detailPlacement === "drawer") {
      onAuxPanelSelect?.("detail");
      return;
    }
    if (showOverlayDetail) {
      clearDetail();
    } else if (selectedNode) {
      pinDetail(selectedNode);
    }
  }, [
    detailPlacement,
    onAuxPanelSelect,
    showOverlayDetail,
    clearDetail,
    selectedNode,
    pinDetail,
  ]);

  if (isMobile) {
    return (
      <div
        className="relative flex h-full w-full flex-col bg-canvas-bg text-foreground"
        data-testid="research-canvas-overlay-grid"
        data-overlay="narrow"
      >
        <style>{NODE_ENTER_MOTION_CSS}</style>
        <div className="sr-only" aria-live="polite">
          {liveText}
        </div>
        <div className="relative min-h-0 flex-1">
          <ResearchGitList
            nodes={nodes}
            edges={edges}
            selectedId={selectedId}
            onSelect={onSelect}
            onOpenDelivery={onOpenDelivery}
            onRetry={onRetry}
            onNodeCommand={onNodeCommand}
            onOpenDetail={(node) => {
              if (detailPlacement === "drawer") onOpenDetail?.(node);
              else dispatch({ type: "pin", nodeId: node.id });
            }}
            liveMessage={announce}
          />
          <ResearchFleetAvatarStack
            members={members}
            sessionStatus={sessionStatus}
            className="absolute top-3 right-3 z-20"
          />
          {chatFab}
          {showOverlayDetail && detailNode ? (
            <ResearchNodeDetail
              node={detailNode}
              sources={sourceList}
              run={run}
              members={members}
              open={showOverlayDetail}
              placement="sheet"
              onClose={clearDetail}
            />
          ) : null}
        </div>
        {/* LRM-1151: full-width Dock under narrow Git list; zoom hidden. */}
        {onAuxPanelSelect ? (
          <ResearchCanvasDock
            layout="mobile"
            zoomPct={100}
            onZoomIn={() => {}}
            onZoomOut={() => {}}
            onFit={() => {}}
            showZoom={false}
            showDetailToggle={false}
            detailOpen={detailToggleOpen}
            onToggleDetail={handleToggleDetail}
            activeModule={auxPanel}
            onSelectModule={onAuxPanelSelect}
          />
        ) : null}
      </div>
    );
  }

  return (
    <div
      ref={canvasRootRef}
      role="application"
      tabIndex={-1}
      aria-label={t(($) => $.logic.label)}
      className="relative h-full w-full bg-canvas-bg text-foreground outline-none"
      data-testid="research-canvas-overlay-grid"
      data-overlay="desktop"
      data-reorg=""
      onKeyDown={handleDesktopKeyDown}
    >
      <div className="sr-only" aria-live="polite" data-testid="research-canvas-live">
        {liveText}
      </div>
      <style>{`
        ${NODE_ENTER_MOTION_CSS}
        ${REORG_TRANSITION_CSS}
        ${REORG_BADGE_CSS}
        .react-flow__node.selected .research-graph-node-shell {
          border-color: var(--brand);
          box-shadow: none;
          outline: 2px solid color-mix(in oklch, var(--brand) 35%, transparent);
          outline-offset: 2px;
        }
        .react-flow__node:focus-visible .research-graph-node-shell,
        .react-flow__node:focus .research-graph-node-shell {
          outline: 2px solid var(--brand);
          outline-offset: 2px;
        }
        .react-flow__edge { display: none; }
        .react-flow__edge.aggregate-tree-edge { display: block; }
        .react-flow__edge.aggregate-tree-edge .react-flow__edge-path {
          stroke: var(--brand);
          stroke-width: 2;
          stroke-opacity: 0.62;
        }
      `}</style>
      <ResearchCanvasPluginShell
        nodes={toCanvasPluginNodeContexts(nodes, selectedId)}
        selectedNodeId={selectedId ?? null}
        reducedMotion={prefersReducedMotion()}
      >
      <ReactFlow
        nodes={rfNodes}
        edges={rfEdges}
        onNodesChange={onNodesChange}
        onEdgesChange={onEdgesChange}
        onMoveStart={() => {
          abortReorgOnMove();
          // FE-05: a real user pan/drag always wins over any auto-focus.
          camera.userInteracted();
        }}
        onMoveEnd={() => {
          dispatch({ type: "setZoom", pct: Math.round(getZoom() * 100) });
        }}
        nodeTypes={nodeTypes}
        fitView
        minZoom={0.25}
        maxZoom={1.75}
        nodesDraggable={false}
        proOptions={{ hideAttribution: true }}
        onNodeClick={(_evt, node) => {
          if (node.type === "gitGutter") return;
          const research = node.data?.research as ResearchGraphNode | undefined;
          if (!research) return;
          focusIdRef.current = research.id;
          // FE-05 AC1: a mouse click moves the target node into the safe centre.
          focusNode(research.id);
          if (isLogicEndNode(research)) {
            onOpenDelivery?.();
            return;
          }
          // LRM-1116: click opens detail (drawer or overlay), not action ring.
          pinDetail(research);
        }}
        onPaneClick={() => {
          dispatch({ type: "setMenu", nodeId: null });
          clearDetail();
        }}
        className="!bg-transparent"
      >
        <Background
          variant={BackgroundVariant.Dots}
          gap={24}
          size={1}
          color="var(--canvas-dot)"
        />
        <Panel
          position="top-left"
          className="!m-3 flex items-center gap-2 rounded-lg border bg-card/90 px-2.5 py-1.5 text-xs text-muted-foreground backdrop-blur-md"
        >
          <span className="font-medium text-foreground">{t(($) => $.logic.label)}</span>
          <span aria-hidden>·</span>
          <span>
            {t(($) =>
              canvasLayout.mode === "aggregate" ? $.logic.aggregate_hint : $.logic.git_hint,
            )}
          </span>
        </Panel>
        <MiniMap
          pannable
          zoomable
          style={{
            width: MINIMAP_WIDTH_PX,
            height: MINIMAP_HEIGHT_PX,
          }}
          className="!absolute !top-auto !bottom-4 !left-auto !right-4 !m-0 !overflow-hidden !rounded-lg !border !border-border !bg-card/90"
          maskColor="color-mix(in oklch, var(--canvas-bg) 70%, transparent)"
          nodeColor={(n) => (n.type === "gitGutter" ? "transparent" : "var(--brand)")}
        />
      </ReactFlow>
      </ResearchCanvasPluginShell>
      <ResearchRunGateBlockers blockers={runBlockers} degraded={runDegraded} onLocate={locateNode} title={t(($) => $.run_v2.delivery_blocked)} degradedTitle={t(($) => $.run_v2.syncing_title)} degradedBody={t(($) => $.run_v2.syncing_body)} />
      {/* LRM-1151: Canvas Dock bottom-center; yield left when Aux Drawer open. */}
      <div
        className="pointer-events-none absolute z-20 flex justify-center"
        style={{
          left: 0,
          right: auxPanel
            ? AUX_DRAWER_WIDTH_PX + OVERLAY_GAP_PX
            : 0,
          bottom: CONTROLS_BOTTOM_PX,
        }}
        data-testid="research-canvas-controls-slot"
        data-yield={auxPanel ? "drawer" : "none"}
      >
        <ResearchCanvasDock
          zoomPct={zoomPct}
          className="!mb-0"
          onZoomIn={() => {
            void zoomIn({ duration: 160 });
            dispatch({ type: "setZoom", pct: Math.round(getZoom() * 100) });
          }}
          onZoomOut={() => {
            void zoomOut({ duration: 160 });
            dispatch({ type: "setZoom", pct: Math.round(getZoom() * 100) });
          }}
          onFit={() => {
            void fitView({ padding: 0.18, duration: 240 });
            dispatch({ type: "setZoom", pct: Math.round(getZoom() * 100) });
          }}
          detailOpen={detailToggleOpen}
          onToggleDetail={handleToggleDetail}
          activeModule={auxPanel}
          onSelectModule={onAuxPanelSelect}
        />
      </div>
      {showOverlayDetail && detailNode ? (
        <div
          className="pointer-events-auto absolute z-20"
          style={{
            left: OVERLAY_INSET_PX,
            bottom: DETAIL_CARD_BOTTOM_PX,
          }}
          data-testid="research-detail-overlay-slot"
        >
          <ResearchNodeDetail
            node={detailNode}
            sources={sourceList}
            run={run}
            members={members}
            open={showOverlayDetail}
            placement="overlay-card"
            onClose={clearDetail}
          />
        </div>
      ) : null}
      {chatFab}
    </div>
  );
}

export function ResearchCanvas(props: {
  nodes: ResearchGraphNode[];
  edges: ResearchGraphEdge[];
  sources?: ResearchSource[];
  members?: ResearchFleetMember[];
  run?: ResearchRunSnapshot;
  runBlockers?: RunV2GateBlocker[];
  runDegraded?: boolean;
  sessionStatus?: string | null;
  presence?: ResearchPresenceMap;
  selectedId?: string | null;
  onSelect?: (node: ResearchGraphNode | null) => void;
  onRetry?: (node: ResearchGraphNode) => void;
  onNodeCommand?: (node: ResearchGraphNode, action: ResearchNodeCommandAction) => Promise<void>;
  onOpenDelivery?: () => void;
  onOpenChat?: () => void;
  chatOpen?: boolean;
  chatMode?: ChatDrawerMode;
  detailPlacement?: "overlay" | "drawer";
  onOpenDetail?: (node: ResearchGraphNode) => void;
  auxPanel?: ResearchAuxPanelId | null;
  onAuxPanelSelect?: (id: ResearchAuxPanelId) => void;
}) {
  return (
    <ReactFlowProvider>
      <div className="h-full w-full min-h-0">
        <ResearchCanvasInner {...props} />
      </div>
    </ReactFlowProvider>
  );
}
