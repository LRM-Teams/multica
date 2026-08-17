"use client";

/* react-doctor-disable react-doctor/prefer-useReducer, react-doctor/jsx-no-jsx-as-prop -- intentional independent UI state and stable shell slots. */
import { useCallback, useEffect, useMemo, useReducer, useRef, useState } from "react";
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { AlertCircle, Square } from "lucide-react";
import { toast } from "sonner";
import { api, ApiError } from "@multica/core/api";
import { createResearchV6DirectorProjectionTransport } from "@multica/core/api/research-v6-director";
import {
  researchV6DirectorNodeDetailOptions,
  researchV6DirectorReportOptions,
  researchV6DirectorReportsOptions,
} from "@multica/core/research-v6/director-queries";
import {
  researchV6DirectorSelectedRefFromNode,
  researchV6DirectorSelectionIdentity,
  useResearchV6DirectorSelectionStore,
} from "@multica/core/research-v6";
import { useAuthStore } from "@multica/core/auth";
import type {
  AgentPanelIdentitySnapshot,
  OpenAgentPanelFn,
} from "@multica/core/agents";
import { useWorkspaceId } from "@multica/core/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import { agentListOptions } from "@multica/core/workspace/queries";
import { useWS } from "@multica/core/realtime";
import type { WSEventType } from "@multica/core/types/events";
import {
  dedupeResearchFleetMembers,
  isResearchD5Lens,
  mergeTypedGraphPages,
  researchGraphTypedInfiniteOptions,
  researchKeys,
  researchPresenceOptions,
  researchProductRoundsOptions,
  researchSessionSnapshotOptions,
  useResearchCanvasStore,
  useResearchUiStore,
} from "@multica/core/research";
import type {
  ResearchClarificationQuestion,
  ResearchGraphNode,
  ResearchNodeCommandAction,
  ResearchProductRoundCard,
} from "@multica/core/types";
import { createSafeId } from "@multica/core/utils";
import { memberListOptions } from "@multica/core/workspace/queries";
import { Button } from "@multica/ui/components/ui/button";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { useAutoScroll } from "@multica/ui/hooks/use-auto-scroll";
import { useIsMobile } from "@multica/ui/hooks/use-mobile";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { cn } from "@multica/ui/lib/utils";
import { AgentPanelProvider } from "../../common/agent-panel-context";
import { ResolvedAgentSidePanel } from "../../common/resolved-agent-side-panel";
import { useT } from "../../i18n/use-t";
import {
  CHANNEL_DETAIL_SIDE_WIDTH_STORAGE_KEY,
  useProfilePanelWidth,
} from "../../layout/use-profile-panel-width";
import { useNavigation } from "../../navigation/context";
import {
  formatClarificationFormReply,
  formatClarificationOptionReply,
  formatClarificationSkipReply,
} from "../lib/clarification-question";
import {
  buildFleetChatFeed,
  nextStageWaitingCard,
  presenceRunningCards,
  type FleetStepGeneratedLabels,
  type FleetStepCardModel,
} from "../lib/fleet-step-cards";
import { resolveCanvasBodyMode } from "../lib/canvas-body-mode";
import { buildExecutionOverlayRows } from "../execution-overlay/index";
import { resolveChatDrawerMode } from "../lib/chat-drawer-mode";
import {
  dismissCompletionGuide,
  isCompletionGuideDismissed,
  resolveCompletionGuideKind,
} from "../lib/completion-guide";
import { deliveryContentCount } from "../lib/delivery-mode";
import {
  buildHumanBoundary,
  dimensionFamilyOf,
} from "../lib/m2-visibility";
import { isResearchSessionStoppable } from "../lib/research-stream";
import { guardPrematureGateProjection } from "../lib/research-projection-contract";
import type { ResearchD5Lens } from "../lib/research-d5-lens";
import { buildGoalVersionHistory, summarizeGoalImpact } from "../lib/research-d5-goal-history";
import {
  enrichResearchNodeForDetail,
  mergeResearchCanvasNodes,
  resolveResearchCanvasNode,
} from "../lib/resolve-research-canvas-node";
import {
  RESEARCH_STAGE_ORDER,
  resolveStageStepState,
  stageAnchorTargetId,
  buildStageMessageAnchors,
} from "../lib/research-stages";
import {
  isPostRetryWakeFailure,
  resolveSessionInterrupt,
} from "../lib/session-interrupt";
import { isServerError } from "../lib/network-status";
import { formatStageGateRejectReply } from "../lib/stage-gate-confirm";
import { useBrowserOnline } from "../lib/use-browser-online";
import {
  canvasSnapshotToTypedGraph,
  useResearchV6DirectorCanvas,
  useResearchSessionCanvas,
} from "../v6-session-adapter";
import {
  INITIAL_RESEARCH_SESSION_UI_STATE,
  researchSessionUiReducer,
} from "../lib/research-session-ui-state";
import { ResearchConstellationWorkspace } from "./research-constellation-workspace";
import { ResearchD5Chrome } from "./research-d5-chrome";
import { ResearchDirectorChatHeader } from "./research-director-chat-header";
import { ResearchDirectorAssignmentPicker } from "./research-director-assignment-picker";
import { ResearchCanvasChangeCard, isCanvasChangeProcessMessage } from "./research-canvas-change-card";
import { ResearchChatCard } from "./research-chat-card";
import {
  ResearchChatModeBody,
  ResearchChatModeChip,
} from "./research-chat-mode-body";
import { ResearchCompletionCard } from "./research-completion-card";
import { ResearchConnectivityShell } from "./research-connectivity-shell";
import { ResearchDeliveryDrawer } from "./research-delivery-drawer";
import { ResearchFleetStepCard } from "./research-fleet-step-card";
import { ResearchLiveStream } from "./research-live-stream";
import { ResearchNodeDetail } from "./research-node-detail";
import { ResearchSelectedRefChip } from "./research-selected-ref-chip";
import { ResearchV6NodeDetail } from "./research-v6-node-detail";
import { ResearchV6ReportModal } from "./research-v6-report-modal";
import { ResearchProductRoundCardView } from "./research-product-round-card";
import { ResearchProjectionContractNotice } from "./research-projection-contract-notice";
import { ResearchServerErrorPage } from "./research-server-error-page";
import { ResearchSessionBoundary } from "./research-session-boundary";
import {
  ResearchSessionInterruptBanner,
  type InterruptBannerPhase,
} from "./research-session-interrupt-banner";
import { ResearchSessionPageSkeleton } from "./research-session-page-skeleton";
import { ResearchShellAtmosphere } from "./research-shell-atmosphere";
import {
  ResearchStageChatMarker,
} from "./research-stage-timeline";

function mutationErrorToast(fallback: string, err: unknown) {
  showErrorToast(err instanceof Error && err.message ? err.message : fallback);
}

type ResearchNodeCommandErrorKey =
  | "permission_denied"
  | "session_terminal"
  | "node_stale"
  | "state_version_conflict"
  | "action_not_allowed"
  | "idempotency_conflict"
  | "invalid_request"
  | "run_not_running"
  | "not_retryable"
  | "no_eligible_member";

function researchNodeCommandErrorKey(error: unknown): ResearchNodeCommandErrorKey | null {
  if (!(error instanceof ApiError) || (error.status !== 403 && error.status !== 409)) {
    return null;
  }
  if (!error.body || typeof error.body !== "object") return null;
  const messageKey = (error.body as Record<string, unknown>).message_key;
  if (typeof messageKey !== "string") return null;
  const key = messageKey.replace(/^research\.node_command\./, "");
  switch (key) {
    case "permission_denied":
    case "session_terminal":
    case "node_stale":
    case "state_version_conflict":
    case "action_not_allowed":
    case "idempotency_conflict":
    case "invalid_request":
    case "run_not_running":
    case "not_retryable":
    case "no_eligible_member":
      return key;
    default:
      return null;
  }
}

export function ResearchSessionPage({ sessionId }: { sessionId: string }) {
  return (
    <ResearchSessionBoundary sessionId={sessionId}>
      <ResearchSessionPageContent sessionId={sessionId} />
    </ResearchSessionBoundary>
  );
}

function ResearchSessionPageContent({ sessionId }: { sessionId: string }) {
  const { t } = useT("research");
  const { t: tAgents } = useT("agents");
  const wsId = useWorkspaceId();
  const paths = useWorkspacePaths();
  const nav = useNavigation();
  const qc = useQueryClient();
  const { subscribe, onReconnect, onConnectionStatus } = useWS();
  const isMobile = useIsMobile();
  const currentUserId = useAuthStore((s) => s.user?.id ?? null);
  const chatOpen = useResearchUiStore((s) => s.chatDrawerOpen);
  const d5Lens = useResearchUiStore((s) => s.d5Lens);
  const setD5Lens = useResearchUiStore((s) => s.setD5Lens);
  const setD5RailOpen = useResearchUiStore((s) => s.setD5RailOpen);
  const setD5RailMode = useResearchUiStore((s) => s.setD5RailMode);
  // LRM-832 — dismiss is per-session (localStorage + in-memory for this visit).
  // react-doctor-disable-next-line react-doctor/prefer-useReducer -- these independent UI concerns are intentionally owned by their respective stores/hooks.
  const [dismissedSessionId, setDismissedSessionId] = useState<string | null>(null);
  const completionDismissed =
    dismissedSessionId === sessionId || isCompletionGuideDismissed(sessionId);
  const dismissCompletion = useCallback(() => {
    dismissCompletionGuide(sessionId);
    setDismissedSessionId(sessionId);
  }, [sessionId]);
  const online = useBrowserOnline();
  const { data, isLoading, isFetching, isError, error, refetch } = useQuery(
    researchSessionSnapshotOptions(wsId, sessionId),
  );
  const { data: presenceData } = useQuery(
    researchPresenceOptions(wsId, sessionId),
  );
  const presence = presenceData ?? {};
  const { data: productRounds } = useQuery(researchProductRoundsOptions(wsId, sessionId));
  const {
    data: typedGraphPages,
    isLoading: typedGraphLoading,
    isError: typedGraphError,
    refetch: refetchTypedGraph,
    fetchNextPage: fetchNextTypedGraphPage,
    hasNextPage: typedGraphHasNextPage,
    isFetchingNextPage: typedGraphFetchingNextPage,
    isFetching: typedGraphFetching,
  } = useInfiniteQuery(researchGraphTypedInfiniteOptions(wsId, sessionId));
  const selectedNodeId = useResearchCanvasStore(
    (s) => s.selectedNodeBySession[sessionId] ?? null,
  );
  const selectSessionCanvasNode = useResearchCanvasStore(
    (s) => s.selectSessionNode,
  );
  const appliedNodeLinkRef = useRef<string | null>(null);
  const typedGraph = useMemo(
    () =>
      typedGraphPages?.pages.length
        ? mergeTypedGraphPages(typedGraphPages.pages, {
            pinNodeIds: selectedNodeId ? [selectedNodeId] : [],
          })
        : undefined,
    [typedGraphPages, selectedNodeId],
  );
  const directorV6Enabled =
    data?.run?.run.orchestrator_version === "research-run-v6";
  const { data: workspaceAgents = [] } = useQuery({
    ...agentListOptions(wsId),
    enabled: directorV6Enabled,
  });
  const persistedDirectorAgentId = data?.session.active_assignments?.find(
    (assignment) =>
      assignment.role === "director" || assignment.role === "research_director",
  )?.agent_id ?? null;
  const directorTransport = useMemo(
    () => createResearchV6DirectorProjectionTransport(api),
    [],
  );
  const directorRealtimeBus = useMemo(
    () => ({
      subscribeEvent: (event: string, handler: (payload: unknown) => void) =>
        subscribe(event as WSEventType, handler),
      onBusReconnect: (handler: () => void) => onReconnect(handler),
      onBusConnectionStatus: onConnectionStatus,
    }),
    [onConnectionStatus, onReconnect, subscribe],
  );
  const directorCanvas = useResearchV6DirectorCanvas({
    workspaceId: wsId,
    runId: sessionId,
    transport: directorTransport,
    enabled: directorV6Enabled,
    expansionFailureLabel: t(($) => $.panel.expansion_failed),
    realtimeBus: directorRealtimeBus,
  });
  const selectedDirectorProjectionNode = selectedNodeId
    ? directorCanvas.canvas?.projectionNodeById.get(selectedNodeId) ?? null
    : null;
  const {
    data: directorNodeDetailData,
    isLoading: directorNodeDetailLoading,
    isError: directorNodeDetailError,
    refetch: refetchDirectorNodeDetail,
  } = useQuery({
    ...researchV6DirectorNodeDetailOptions(
      directorTransport,
      wsId,
      sessionId,
      directorCanvas.snapshotId ?? "00000000-0000-0000-0000-000000000000",
      selectedDirectorProjectionNode?.id ?? "unselected",
      "full",
    ),
    enabled:
      directorV6Enabled &&
      Boolean(directorCanvas.snapshotId && selectedDirectorProjectionNode),
  });
  const directorSelectionIdentity = researchV6DirectorSelectionIdentity(
    wsId,
    sessionId,
  );
  const selectedDirectorReference = useResearchV6DirectorSelectionStore(
    (state) => state.byProjection[directorSelectionIdentity] ?? null,
  );
  const selectDirectorReference = useResearchV6DirectorSelectionStore(
    (state) => state.select,
  );
  const clearDirectorReference = useResearchV6DirectorSelectionStore(
    (state) => state.clear,
  );
  // The current durable run contract is session-keyed. Probe the V6 projection
  // with that stable key for legacy runs only; Director V6 uses the strict
  // workspace/run/snapshot projection contract above and never falls through
  // this compatibility probe.
  const projectionGateway = useResearchSessionCanvas({
    wsId,
    sessionId,
    runId: data && !directorV6Enabled ? sessionId : undefined,
    transports: {
      loadV6Snapshot: (runId, signal) =>
        api.getResearchV6ProjectionSnapshot(runId, { signal }),
      loadV5Session: async (id) => {
        const snapshot = await api.getResearchSessionSnapshot(id);
        return { sessionId: id, nodes: snapshot.nodes, edges: snapshot.edges };
      },
    },
  });
  const rawDisplayTypedGraph = useMemo(
    () => {
      if (directorV6Enabled) return directorCanvas.canvas?.graph;
      if (projectionGateway.status === "error") return undefined;
      return projectionGateway.source === "v6" && projectionGateway.canvas
        ? canvasSnapshotToTypedGraph(sessionId, projectionGateway.snapshot)
        : typedGraph;
    },
    [
      projectionGateway.canvas,
      projectionGateway.snapshot,
      projectionGateway.source,
      projectionGateway.status,
      directorCanvas.canvas,
      directorV6Enabled,
      sessionId,
      typedGraph,
    ],
  );
  const guardedProjection = guardPrematureGateProjection({
    graph: rawDisplayTypedGraph,
    runId: sessionId,
    stage: data?.session.current_stage ?? "",
    sessionStatus: data?.session.status ?? "",
  });
  const displayTypedGraph = guardedProjection.graph;
  const projectionSource = directorV6Enabled ? "v6" : projectionGateway.source;
  const canvasUsesV5 = !directorV6Enabled && projectionGateway.status === "v5";
  const canvasLoading =
    (directorV6Enabled && directorCanvas.isLoading) ||
    (!directorV6Enabled && projectionGateway.status === "probing") ||
    (canvasUsesV5 && typedGraphLoading);
  const canvasError =
    (directorV6Enabled && directorCanvas.error !== null) ||
    (!directorV6Enabled && projectionGateway.status === "error") ||
    (canvasUsesV5 && typedGraphError);
  const canvasRetryPending =
    (directorV6Enabled && directorCanvas.isFetching) ||
    (!directorV6Enabled && projectionGateway.isFetching) ||
    (canvasUsesV5 && typedGraphFetching && !typedGraphFetchingNextPage);
  const detailGraphNodes = useMemo(
    () => mergeResearchCanvasNodes(data?.nodes ?? [], displayTypedGraph),
    [data?.nodes, displayTypedGraph],
  );
  const detailGraphEdges = useMemo(() => {
    const byId = new Map((data?.edges ?? []).map((edge) => [edge.id, edge]));
    for (const edge of displayTypedGraph?.edges ?? []) {
      const key = edge.id || `${edge.from_node_id}:${edge.edge_type}:${edge.to_node_id}`;
      if (!byId.has(key)) byId.set(key, edge);
    }
    return Array.from(byId.values());
  }, [data?.edges, displayTypedGraph?.edges]);
  useEffect(() => {
    const fromUrl = nav.searchParams.get("lens");
    if (isResearchD5Lens(fromUrl) && fromUrl !== d5Lens) {
      setD5Lens(fromUrl);
    }
  }, [d5Lens, nav.searchParams, sessionId, setD5Lens]);
  const handleD5LensChange = useCallback(
    (lens: ResearchD5Lens) => {
      setD5Lens(lens);
      const params = new URLSearchParams(nav.searchParams.toString());
      if (lens === "relations") params.delete("lens");
      else params.set("lens", lens);
      const qs = params.toString();
      nav.replace(qs ? `${nav.pathname}?${qs}` : nav.pathname);
    },
    [nav, setD5Lens],
  );
  const [ui, dispatch] = useReducer(
    researchSessionUiReducer,
    INITIAL_RESEARCH_SESSION_UI_STATE,
  );
  const [selectedDirectorReportId, setSelectedDirectorReportId] = useState<
    string | null
  >(null);
  const [assignedDirectorAgentId, setAssignedDirectorAgentId] = useState<string | null>(null);
  const assignedDirectorAgent = workspaceAgents.find(
    (agent) => agent.id === assignedDirectorAgentId,
  );
  // react-doctor-disable-next-line react-doctor/query-mutation-missing-invalidation -- onSuccess updates the persisted Director assignment and the session snapshot is refreshed by the assignment flow.
  const directorAssignment = useMutation({
    mutationFn: ({ agentId, reason }: { agentId: string; reason: string }) =>
      api.replaceResearchV6Director(wsId, sessionId, {
        directorAgentId: agentId,
        expectedStateVersion: data?.run?.run.state_version ?? 0,
        reason,
        clientRequestId: crypto.randomUUID(),
      }),
    onSuccess: (assignment) => {
      if (assignment) setAssignedDirectorAgentId(assignment.directorAgentId);
    },
  });
  const {
    data: directorReportsData,
    isLoading: directorReportsLoading,
    refetch: refetchDirectorReports,
  } = useQuery({
    ...researchV6DirectorReportsOptions(directorTransport, wsId, sessionId),
    enabled: directorV6Enabled,
  });
  const directorReportId =
    (selectedDirectorReportId &&
    directorReportsData?.some((item) => item.id === selectedDirectorReportId)
      ? selectedDirectorReportId
      : null) ??
    directorReportsData?.find((item) => item.status === "published")?.id ??
    directorReportsData?.[0]?.id ??
    null;
  const {
    data: directorReportDetailData,
    isFetching: directorReportDetailFetching,
    refetch: refetchDirectorReportDetail,
  } = useQuery({
    ...researchV6DirectorReportOptions(
      directorTransport,
      wsId,
      sessionId,
      directorReportId ?? "00000000-0000-0000-0000-000000000000",
    ),
    enabled: directorV6Enabled && ui.deliveryOpen && Boolean(directorReportId),
  });
  const handleSelectCanvasNode = useCallback(
    (node: ResearchGraphNode | null) => {
      selectSessionCanvasNode(sessionId, node?.id ?? null);
      if (node) dispatch({ type: "setFamily", family: dimensionFamilyOf(node) });
    },
    [selectSessionCanvasNode, sessionId],
  );
  const handleFocusDetailNode = useCallback(
    (nodeId: string) => {
      const node = resolveResearchCanvasNode(nodeId, {
        snapshotNodes: data?.nodes,
        typedGraph: displayTypedGraph,
      });
      if (node) {
        handleSelectCanvasNode(
          enrichResearchNodeForDetail(node, displayTypedGraph),
        );
      }
    },
    [data?.nodes, displayTypedGraph, handleSelectCanvasNode],
  );
  const handleFocusCanvasChangeNode = useCallback(
    (nodeId: string) => {
      handleD5LensChange("relations");
      handleFocusDetailNode(nodeId);
    },
    [handleD5LensChange, handleFocusDetailNode],
  );
  useEffect(() => {
    if (!data) return;
    const linkedNodeId = nav.searchParams.get("node");
    if (!linkedNodeId) {
      appliedNodeLinkRef.current = null;
      return;
    }
    const linkKey = `${sessionId}:${linkedNodeId}`;
    if (appliedNodeLinkRef.current === linkKey) return;
    const resolved = resolveResearchCanvasNode(linkedNodeId, {
      snapshotNodes: data.nodes,
      typedGraph: displayTypedGraph,
    });
    if (!resolved) return;
    appliedNodeLinkRef.current = linkKey;
    selectSessionCanvasNode(sessionId, linkedNodeId);
    setD5RailMode("detail");
    setD5RailOpen(true);
  }, [
    data,
    displayTypedGraph,
    nav.searchParams,
    selectSessionCanvasNode,
    setD5RailMode,
    setD5RailOpen,
    sessionId,
  ]);
  // LRM-776 — dock Agent side panel like channels/DM (local AgentPanelProvider).
  const [agentDock, setAgentDock] = useState<{
    agentId: string;
    snapshot: AgentPanelIdentitySnapshot | null;
  } | null>(null);
  const handleOpenAgentPanel = useCallback<OpenAgentPanelFn>((agentId, snapshot) => {
    setAgentDock({ agentId, snapshot: snapshot ?? null });
  }, []);
  const { data: workspaceMembers = [] } = useQuery({
    ...memberListOptions(wsId),
    enabled: !!agentDock,
  });
  const {
    width: agentSideWidth,
    onResizePointerDown: onAgentSideResizePointerDown,
  } = useProfilePanelWidth(CHANNEL_DETAIL_SIDE_WIDTH_STORAGE_KEY);
  const chatScrollRef = useRef<HTMLDivElement>(null);
  // LRM-1250 / LRM-1248 AC4 — focus restore target after successful send.
  const composerRef = useRef<HTMLTextAreaElement>(null);
  const reportControllerRef = useRef<{ open: () => void } | null>(null);
  const nodeCommandRequestRef = useRef<{
    sessionId: string;
    nodeId: string;
    action: ResearchNodeCommandAction;
    requestId: string;
  } | null>(null);
  // Stick-to-bottom while content grows (live stream / new cards); releases if
  // the user scrolls up to read history — no jump-scroll (LRM-820).
  useAutoScroll(chatScrollRef, chatOpen);

  const send = useMutation({
    mutationFn: (body: string) =>
      api.postResearchMessage(sessionId, {
        body,
        selected_research_refs:
          directorV6Enabled && selectedDirectorReference
            ? [selectedDirectorReference]
            : undefined,
      }),
    onSuccess: () => {
      // Focus before clearBody so empty-state native disabled does not dump focus to BODY.
      composerRef.current?.focus();
      dispatch({ type: "clearBody" });
      if (directorV6Enabled) clearDirectorReference(wsId, sessionId);
      void qc.invalidateQueries({ queryKey: researchKeys.snapshot(wsId, sessionId) });
      void qc.invalidateQueries({ queryKey: researchKeys.productRounds(wsId, sessionId) });
    },
    onError: (err) => mutationErrorToast(t(($) => $.session_page.send_failed), err),
  });

  // react-doctor-disable-next-line react-doctor/query-mutation-missing-invalidation -- node commands publish realtime projection events; invalidating every query here would race the authoritative event stream.
  const nodeCommand = useMutation({
    mutationFn: ({
      node,
      action,
    }: {
      node: ResearchGraphNode;
      action: ResearchNodeCommandAction;
    }) => {
      const current = nodeCommandRequestRef.current;
      const requestId =
        current?.sessionId === sessionId &&
        current.nodeId === node.id &&
        current.action === action
          ? current.requestId
          : createSafeId();
      nodeCommandRequestRef.current = {
        sessionId,
        nodeId: node.id,
        action,
        requestId,
      };
      return api.postResearchNodeCommand(sessionId, node.id, {
        action,
        client_request_id: requestId,
      });
    },
    onSuccess: (_response, variables) => {
      if (
        nodeCommandRequestRef.current?.nodeId === variables.node.id &&
        nodeCommandRequestRef.current.action === variables.action
      ) {
        nodeCommandRequestRef.current = null;
      }
    },
    onError: (error) => {
      const key = researchNodeCommandErrorKey(error);
      switch (key) {
        case "permission_denied":
          showErrorToast(t(($) => $.d5.detail.permission_denied));
          break;
        case "session_terminal":
          showErrorToast(t(($) => $.d5.detail.session_terminal));
          break;
        case "node_stale":
          showErrorToast(t(($) => $.d5.detail.node_stale));
          break;
        case "state_version_conflict":
          showErrorToast(t(($) => $.d5.detail.state_version_conflict));
          break;
        case "action_not_allowed":
          showErrorToast(t(($) => $.d5.detail.action_not_allowed));
          break;
        case "idempotency_conflict":
          showErrorToast(t(($) => $.d5.detail.idempotency_conflict));
          break;
        case "invalid_request":
          showErrorToast(t(($) => $.d5.detail.invalid_request));
          break;
        case "run_not_running":
          showErrorToast(t(($) => $.d5.detail.run_not_running));
          break;
        case "not_retryable":
          showErrorToast(t(($) => $.d5.detail.not_retryable));
          break;
        case "no_eligible_member":
          showErrorToast(t(($) => $.d5.detail.no_eligible_member));
          break;
        default:
          showErrorToast(t(($) => $.d5.detail.command_failed));
      }
    },
    onSettled: () => {
      void refetch();
      void refetchTypedGraph();
      projectionGateway.refetch();
    },
  });

  const stop = useMutation({
    mutationFn: () => api.stopResearchSession(sessionId),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: researchKeys.snapshot(wsId, sessionId) });
      void qc.invalidateQueries({ queryKey: researchKeys.sessions(wsId) });
      void qc.invalidateQueries({ queryKey: researchKeys.presence(wsId, sessionId) });
      toast.success(t(($) => $.actions.stop_done));
    },
    onError: (err) =>
      showErrorToast(err instanceof Error ? err.message : String(err)),
  });

  const steer = useMutation({
    mutationFn: ({ goal, reason }: { goal: string; reason: string }) =>
      api.steerResearchRun(sessionId, { goal, reason }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: researchKeys.snapshot(wsId, sessionId) });
      void qc.invalidateQueries({ queryKey: researchKeys.sessions(wsId) });
    },
    onError: (err) => mutationErrorToast(t(($) => $.session_page.send_failed), err),
  });

  const confirm = useMutation({
    mutationFn: () => api.confirmResearchSession(sessionId),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: researchKeys.snapshot(wsId, sessionId) });
      void qc.invalidateQueries({ queryKey: researchKeys.sessions(wsId) });
      toast.success(t(($) => $.session_page.confirm_done));
    },
    onError: (err) => mutationErrorToast(t(($) => $.session_page.confirm_failed), err),
  });

  // LRM-840 — reject stage-gate confirm: tip → agent + status resumes via BE.
  const rejectConfirm = useMutation({
    mutationFn: (body: string) => api.postResearchMessage(sessionId, { body }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: researchKeys.snapshot(wsId, sessionId) });
      void qc.invalidateQueries({ queryKey: researchKeys.sessions(wsId) });
      toast.success(t(($) => $.session_page.reject_done));
    },
    onError: (err) => mutationErrorToast(t(($) => $.session_page.reject_failed), err),
  });

  const handoff = useMutation({
    mutationFn: () =>
      api.researchSessionHandoff(sessionId, {
        create_project: ui.createProject,
        create_channel: ui.createChannel,
      }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: researchKeys.snapshot(wsId, sessionId) });
    },
    onError: (err) => mutationErrorToast(t(($) => $.session_page.handoff_failed), err),
  });

  // LRM-890 M2 visibility models — derived from graph nodes / report.
  const humanBoundary = useMemo(
    () => buildHumanBoundary(data?.nodes ?? [], data?.report),
    [data?.nodes, data?.report],
  );

  // LRM-824 — anchor targets (hooks must stay above the early returns below).
  const stageFirstMessageId = useMemo(
    () => buildStageMessageAnchors(data?.messages ?? []),
    [data?.messages],
  );

  // LRM-823 — session interrupt banner (wake_failed / disconnect). Hooks stay
  // above early returns; banner auto-hides when the tip process event recovers.
  const sessionInterrupt = useMemo(
    () => resolveSessionInterrupt(data?.messages ?? []),
    [data?.messages],
  );
  const interruptId = sessionInterrupt?.messageId ?? null;
  const [interruptPhase, setInterruptPhase] = useState<InterruptBannerPhase>("idle");
  const interruptSyncIdRef = useRef<string | null>(interruptId);
  const interruptRetryPriorIdRef = useRef<string | null>(null);
  // Adjust phase when the tip interrupt identity changes (no useEffect — react-doctor).
  if (interruptId !== interruptSyncIdRef.current) {
    interruptSyncIdRef.current = interruptId;
    if (!interruptId) {
      interruptRetryPriorIdRef.current = null;
      setInterruptPhase("idle");
    } else if (
      isPostRetryWakeFailure(data?.messages ?? [], interruptRetryPriorIdRef.current)
    ) {
      setInterruptPhase("retry_failed");
    }
  }

  const retrySessionInterrupt = useCallback(() => {
    if (!sessionInterrupt) return;
    const reason = sessionInterrupt.reason
      ? ` (reason=${sessionInterrupt.reason})`
      : "";
    const body = t(($) => $.director_messages.wake_retry, {
      reason,
      headline: sessionInterrupt.headline,
    });
    interruptRetryPriorIdRef.current = sessionInterrupt.messageId;
    setInterruptPhase("pending");
    void api
      .postResearchMessage(sessionId, { body })
      .then(() =>
        qc.invalidateQueries({ queryKey: researchKeys.snapshot(wsId, sessionId) }),
      )
      .then(() => {
        // Request accepted — keep banner until tip process recovers; secondary
        // feedback only when a newer wake_failed lands (sync above).
        setInterruptPhase((phase) => (phase === "retry_failed" ? phase : "idle"));
      })
      .catch((err) => {
        setInterruptPhase("retry_failed");
        mutationErrorToast(t(($) => $.session_page.send_failed), err);
      });
  }, [sessionInterrupt, sessionId, qc, wsId, t]);

  // LRM-833 — 5xx with no cache: dedicated error page + retry. This must stay
  // ahead of the fetching skeleton so a background retry does not unmount the
  // focused retry control and erase the visible error context.
  if (!data && isError && isServerError(error)) {
    return (
      <ResearchConnectivityShell>
        <ResearchServerErrorPage
          onRetry={() => {
            void refetch();
          }}
          message={error instanceof Error ? error.message : null}
          retrying={isFetching}
        />
      </ResearchConnectivityShell>
    );
  }

  // Keep the generic load failure mounted while its refetch is pending for the
  // same focus/restoration contract as the dedicated 5xx surface. Offline wins
  // below so the connectivity shell can retain its offline-first skeleton.
  if (!data && isError && online) {
    return (
      <ResearchConnectivityShell>
        <div
          role="alert"
          data-testid="research-session-load-error"
          className="flex h-full flex-col items-center justify-center gap-3 px-6 py-12 text-center"
        >
          <AlertCircle className="size-6 text-destructive" aria-hidden />
          <div className="max-w-md space-y-1.5">
            <h2 className="text-sm font-medium text-destructive">
              {t(($) => $.session_page.load_failed)}
            </h2>
            <p className="text-sm text-muted-foreground">
              {t(($) => $.session_page.load_failed_hint)}
            </p>
            {error instanceof Error && error.message ? (
              <details
                data-testid="research-session-load-error-diagnostics"
                className="pt-1 text-left text-xs text-muted-foreground"
              >
                <summary className="cursor-pointer text-center">
                  {t(($) => $.session_page.technical_details)}
                </summary>
                <code
                  lang="en"
                  dir="ltr"
                  className="mt-2 block max-h-24 overflow-auto rounded-md bg-muted/60 p-2 whitespace-pre-wrap break-words"
                >
                  {error.message}
                </code>
              </details>
            ) : null}
          </div>
          <Button
            type="button"
            variant="outline"
            size="sm"
            aria-disabled={isFetching || undefined}
            className={isFetching ? "cursor-not-allowed opacity-50" : undefined}
            onClick={() => {
              if (isFetching) return;
              void refetch();
            }}
          >
            {t(($) =>
              isFetching ? $.connectivity.retrying : $.session_page.retry,
            )}
          </Button>
        </div>
      </ResearchConnectivityShell>
    );
  }

  // LRM-799: never keep a permanent skeleton on failure — only while loading.
  // LRM-781 / LRM-979: skeleton mirrors chrome + canvas shell so first paint does not flash blank.
  // LRM-833: offline with no cache keeps skeleton under the connectivity banner (no white screen).
  if (isLoading || (isFetching && !data) || (!data && !online)) {
    return (
      <ResearchConnectivityShell>
        <ResearchSessionPageSkeleton />
      </ResearchConnectivityShell>
    );
  }

  // Keep successful snapshot on refetch failure so Delivery can show its error
  // surface (LRM-993) instead of blanking the whole session shell. The remaining
  // no-data case is offline and is handled by the skeleton branch above.
  if (!data) {
    return null;
  }

  const { session, messages, report, sources } = data;
  const fleetMembers = dedupeResearchFleetMembers(data.fleet.members);
  const fleet = { ...data.fleet, members: fleetMembers };
  const directorMember =
    fleet.members.find(
      (member) => member.agent_id === fleet.lead_agent_id,
    ) ??
    fleet.members.find((member) => member.is_lead) ??
    null;
  const linkedNodeId = nav.searchParams.get("node");
  const selectedNode = (() => {
    const base = resolveResearchCanvasNode(selectedNodeId ?? linkedNodeId, {
      snapshotNodes: data.nodes,
      typedGraph: displayTypedGraph,
    });
    return base ? enrichResearchNodeForDetail(base, displayTypedGraph) : null;
  })();
  const executionRows = buildExecutionOverlayRows({
    members: fleet.members,
    presence,
    presenceAvailable: presenceData != null,
    nodes: data.nodes,
    run: data.run,
  });
  // LRM-1329 — drawer overview owns error/permission; cards stay fact-only.
  const canvasMode = resolveCanvasBodyMode({
    nodes: data.nodes,
    edges: data.edges,
    sessionStatus: session.status,
  });
  const canConfirm = session.status === "awaiting_user_confirm" || session.status === "running";
  const canHandoff = session.status === "completed" || session.status === "awaiting_user_confirm";
  const canStop = isResearchSessionStoppable(session.status);
  const isPaused = session.status === "paused";
  const completionKind = resolveCompletionGuideKind(session.status);
  const showCompletionGuide = Boolean(completionKind) && !completionDismissed;
  // Director V6 has no fixed S1-S4 lifecycle. Keep the historical markers
  // available only for V1-V5 sessions instead of projecting legacy stages
  // onto the server-owned Director graph.
  const startedStages = directorV6Enabled
    ? []
    : RESEARCH_STAGE_ORDER.filter(
        (stage) =>
          resolveStageStepState(stage, session.current_stage, session.status) !==
          "upcoming",
      );
  const latestRound: ResearchProductRoundCard | undefined = productRounds?.rounds?.[
    (productRounds.rounds.length ?? 0) - 1
  ];

  const postUser = (body: string) => send.mutate(body);
  const postUserCommitted = (body: string) =>
    send.mutateAsync(body).then(() => undefined);
  const stopAndPostUser = async (body: string) => {
    const stopRequest = api.stopResearchSession(sessionId).catch((error: unknown) => {
      showErrorToast(error instanceof Error ? error.message : String(error));
      throw error;
    });
    await Promise.all([stopRequest, postUserCommitted(body)]);
    void qc.invalidateQueries({
      queryKey: researchKeys.snapshot(wsId, sessionId),
    });
  };

  // Plain derivation after data-gated early returns — do not use hooks here
  // (rules-of-hooks). goal history is cheap and only needed on the success path.
  const goalVersion = data.run?.run?.goal_version ?? data.run?.contract?.goal_version ?? null;
  const goalHistory = buildGoalVersionHistory({
    currentGoal: session.goal,
    currentVersion: goalVersion,
    messages,
  });
  const goalImpact = displayTypedGraph?.nodes
    ? summarizeGoalImpact(displayTypedGraph.nodes)
    : null;
  const projectionMismatch =
    canvasMode === "ready" &&
    !canvasLoading &&
    !canvasError &&
    (!displayTypedGraph || displayTypedGraph.nodes.length === 0);

  const onClarificationOption = (
    question: ResearchClarificationQuestion,
    optionId: string,
  ) => {
    const option = question.options.find((o) => o.id === optionId);
    if (!option) return;
    postUser(formatClarificationOptionReply(question, option));
  };

  const onClarificationForm = (
    question: ResearchClarificationQuestion,
    values: Record<string, string>,
  ) => {
    postUser(formatClarificationFormReply(question, values));
  };

  const onClarificationSkip = (question: ResearchClarificationQuestion) => {
    // Skip posts a user tip so the fleet can continue — never blocks the session.
    postUser(formatClarificationSkipReply(question));
  };

  const fleetStepLabels: FleetStepGeneratedLabels = {
    opTitles: {
      session_kickoff: t(($) => $.step_card.generated.ops.session_kickoff),
      wake_failed: t(($) => $.step_card.generated.ops.wake_failed),
      graph_append: t(($) => $.step_card.generated.ops.graph_append),
      source_upsert: t(($) => $.step_card.generated.ops.source_upsert),
      report_patch: t(($) => $.step_card.generated.ops.report_patch),
      stage_eval: t(($) => $.step_card.generated.ops.stage_eval),
      session_stopped: t(($) => $.step_card.generated.ops.session_stopped),
      session_resumed: t(($) => $.step_card.generated.ops.session_resumed),
      roster_hire: t(($) => $.step_card.generated.ops.roster_hire),
      roster_optimize: t(($) => $.step_card.generated.ops.roster_optimize),
      roster_archive: t(($) => $.step_card.generated.ops.roster_archive),
      product_round_judgment: t(
        ($) => $.step_card.generated.ops.product_round_judgment,
      ),
      clarification_question: t(
        ($) => $.step_card.generated.ops.clarification_question,
      ),
    },
    process: t(($) => $.step_card.generated.process),
    memberReady: (count) =>
      t(($) => $.step_card.generated.member_ready, { count }),
    domain: (value) => t(($) => $.step_card.generated.domain, { value }),
    dimensions: (count) =>
      t(($) => $.step_card.generated.dimensions, { count }),
    stage: (value) => t(($) => $.step_card.generated.stage, { value }),
    mergedFailures: (count) =>
      t(($) => $.step_card.generated.merged_failures, { count }),
    delivery: t(($) => $.step_card.generated.delivery),
    waiting: t(($) => $.step_card.generated.waiting),
  };
  const chatFeed = buildFleetChatFeed(messages, fleetStepLabels);
  const runningCards = directorV6Enabled
    ? []
    : presenceRunningCards(presence, fleet.members);
  const waitingCard = directorV6Enabled
    ? null
    : nextStageWaitingCard(
        session.current_stage,
        session.status,
        fleetStepLabels,
      );
  const showStop =
    !directorV6Enabled && (canStop || runningCards.length > 0);
  // LRM-992 — drawer/FAB four-state mode (align with fleet strip language).
  // Live stream / stop chrome counts as in-progress activity (not empty stub).
  const chatHasFeed =
    chatFeed.length > 0 ||
    runningCards.length > 0 ||
    !!waitingCard ||
    showStop;
  const chatMode = resolveChatDrawerMode(chatHasFeed ? 1 : 0, session.status, {
    loading: send.isPending && !chatHasFeed,
    error: send.isError
      ? send.error instanceof Error
        ? send.error.message
        : t(($) => $.session_page.send_failed)
      : null,
  });
  const chatErrorMessage =
    send.error instanceof Error ? send.error.message : null;

  const postFleetAction = (body: string) => {
    void api
      .postResearchMessage(sessionId, { body })
      .then(() =>
        qc.invalidateQueries({ queryKey: researchKeys.snapshot(wsId, sessionId) }),
      );
  };

  const onStepRetry = (card: FleetStepCardModel) => {
    const reason = card.reason ? ` (reason=${card.reason})` : "";
    postFleetAction(
      t(($) => $.director_messages.wake_retry, {
        reason,
        headline: card.summaryHeadline,
      }),
    );
  };

  const onStepReassign = (card: FleetStepCardModel) => {
    postFleetAction(
      t(($) => $.director_messages.wake_reassign, {
        title: card.title,
        summary: card.summaryHeadline,
      }),
    );
  };

  const agentPanelNode = agentDock ? (
    <ResolvedAgentSidePanel
      agentId={agentDock.agentId}
      identitySnapshot={agentDock.snapshot}
      currentUserId={currentUserId}
      members={workspaceMembers}
      onClose={() => setAgentDock(null)}
      variant={isMobile ? "page" : "panel"}
      doneLabel={
        isMobile ? tAgents(($) => $.side_panel.back_to_messages) : undefined
      }
    />
  ) : null;

  const sessionBody = (
    <div
      className="research-d5-theme relative flex h-full min-h-0 flex-col"
      data-testid="research-session-page"
    >
      {/* LRM-971: homepage-family shell atmosphere behind chrome + canvas. */}
      <ResearchShellAtmosphere heightClassName="h-[320px]" />
      <ResearchD5Chrome
        activeLens={d5Lens}
        onLensChange={handleD5LensChange}
        goalVersion={goalVersion}
        goalHistory={goalHistory}
        goalImpact={goalImpact}
        typedGraphNodes={displayTypedGraph?.nodes ?? []}
        projectionSource={projectionSource}
        session={session}
        contract={data.run?.contract}
        canConfirm={!directorV6Enabled && canConfirm}
        canHandoff={canHandoff}
        createProject={ui.createProject}
        createChannel={ui.createChannel}
        onCreateProjectChange={(value) => dispatch({ type: "setCreateProject", value })}
        onCreateChannelChange={(value) => dispatch({ type: "setCreateChannel", value })}
        onConfirm={() => confirm.mutate()}
        onReject={(reason) =>
          rejectConfirm
            .mutateAsync(formatStageGateRejectReply(reason))
            .then(() => undefined)
        }
        onHandoff={() => handoff.mutateAsync().then(() => undefined)}
        confirmPending={confirm.isPending}
        rejectPending={rejectConfirm.isPending}
        handoffPending={handoff.isPending}
        onOpenDelivery={() => {
          dispatch({ type: "setDeliveryOpen", value: true });
          if (directorV6Enabled) void refetchDirectorReports();
        }}
        members={directorV6Enabled ? [] : fleet.members}
        sources={sources}
        pendingSubstantiveGoal={
          !directorV6Enabled && latestRound?.goal_patch_proposal?.trim()
            ? latestRound.goal_patch_proposal
            : null
        }
        onConfirmSubstantiveGoal={(proposal) =>
          steer
            .mutateAsync({
              goal: proposal,
              reason: "user_confirmed_substantive_goal_proposal",
            })
            .then(() => undefined)
        }
        confirmSubstantivePending={steer.isPending}
      />

      {sessionInterrupt ? (
        <ResearchSessionInterruptBanner
          interrupt={sessionInterrupt}
          phase={interruptPhase}
          onRetry={retrySessionInterrupt}
        />
      ) : null}

      {guardedProjection.diagnostic ? (
        <ResearchProjectionContractNotice diagnostic={guardedProjection.diagnostic} />
      ) : null}

      {/* LRM-1112: S1–S4 timeline lives inside the single header surface (L2). */}
      <div className="relative flex min-h-0 flex-1">
        <ResearchConstellationWorkspace
          className="min-h-0 flex-1"
          typedGraph={displayTypedGraph}
          typedLoading={canvasLoading}
          typedError={canvasError}
          projectionErrorReason={
            directorV6Enabled
              ? directorCanvas.error?.message
              : projectionGateway.error?.reason
          }
          projectionMismatch={projectionMismatch}
          onRetryTypedGraph={() => {
            void refetchTypedGraph();
            if (directorV6Enabled) directorCanvas.refetch();
            else projectionGateway.refetch();
          }}
          retryTypedGraphPending={canvasRetryPending}
          snapshotNodeCount={data.nodes.length}
          typedGraphSessionId={sessionId}
          typedGraphVersion={displayTypedGraph?.graph_version ?? null}
          projectionSource={projectionSource}
          expansionControl={
            directorV6Enabled ? directorCanvas.expansionControl : undefined
          }
          densityBins={
            directorV6Enabled ? directorCanvas.canvas?.densityBins : undefined
          }
          typedGraphHasNextPage={
            directorV6Enabled
              ? directorCanvas.hasNextSnapshotPage
              : canvasUsesV5 && typedGraphHasNextPage === true
          }
          typedGraphLoadMorePending={
            directorV6Enabled
              ? directorCanvas.isFetching
              : canvasUsesV5 && typedGraphFetchingNextPage
          }
          onLoadMoreTypedGraph={
            directorV6Enabled && directorCanvas.hasNextSnapshotPage
              ? directorCanvas.loadNextSnapshotPage
              : canvasUsesV5 && typedGraphHasNextPage
              ? () => void fetchNextTypedGraphPage()
              : undefined
          }
          snapshotNodes={data.nodes}
          selectedNode={selectedNode}
          onSelectNode={handleSelectCanvasNode}
          executionRows={executionRows}
          onOpenAgentPanel={handleOpenAgentPanel}
          canvasMode={canvasMode}
          activeLens={d5Lens}
          onActiveLensChange={handleD5LensChange}
          sessionStatus={session.status}
          sources={sources}
          run={data.run}
          members={fleet.members}
          formingMode={
            canvasMode === "forming" || canvasMode === "stalled" ? canvasMode : undefined
          }
          formingStage={directorV6Enabled ? undefined : session.current_stage}
          formingMembers={directorV6Enabled ? [] : fleet.members}
          formingTasks={directorV6Enabled ? [] : (data.run?.tasks ?? [])}
          formingMessages={messages}
          registerReportController={(controller) => {
            reportControllerRef.current = controller;
          }}
          detailPanel={
            directorV6Enabled ? (
              selectedDirectorProjectionNode ? (
                <ResearchV6NodeDetail
                  node={selectedDirectorProjectionNode}
                  detail={directorNodeDetailData}
                  loading={directorNodeDetailLoading}
                  error={directorNodeDetailError}
                  selectedForChat={
                    selectedDirectorReference?.stable_id ===
                    `${selectedDirectorProjectionNode.canonical_ref.kind}:${selectedDirectorProjectionNode.canonical_ref.id}`
                  }
                  projectionNodeById={
                    directorCanvas.canvas?.projectionNodeById ?? new Map()
                  }
                  onRetry={() => void refetchDirectorNodeDetail()}
                  onFocusNode={(nodeId) => {
                    if (!directorCanvas.canvas?.projectionNodeById.has(nodeId)) return;
                    handleD5LensChange("relations");
                    selectSessionCanvasNode(sessionId, nodeId);
                  }}
                  onReference={() => {
                    const reference = researchV6DirectorSelectedRefFromNode(
                      selectedDirectorProjectionNode,
                    );
                    if (reference) {
                      selectDirectorReference(wsId, sessionId, reference);
                    }
                  }}
                />
              ) : (
                <p className="p-4 text-sm text-muted-foreground">
                  {t(($) => $.panel.aux_detail_empty)}
                </p>
              )
            ) : selectedNode ? (
              <ResearchNodeDetail
                node={selectedNode}
                sources={sources}
                run={data.run}
                members={fleet.members}
                graphNodes={detailGraphNodes}
                graphEdges={detailGraphEdges}
                onFocusNode={handleFocusDetailNode}
                open
                placement="inline"
                onClose={() => handleSelectCanvasNode(null)}
                onOpenReport={() => reportControllerRef.current?.open()}
                onNodeCommand={
                  directorV6Enabled
                    ? undefined
                    : (action) =>
                        nodeCommand.mutate({ node: selectedNode, action })
                }
                pendingNodeCommand={
                  nodeCommand.isPending ? nodeCommand.variables?.action : null
                }
              />
            ) : (
              <p className="p-4 text-sm text-muted-foreground">
                {t(($) => $.panel.aux_detail_empty)}
              </p>
            )
          }
          chatPanel={
            <>
            <ResearchDirectorChatHeader
              director={directorMember}
              fallbackName={
                directorV6Enabled
                  ? assignedDirectorAgent?.display_name || assignedDirectorAgent?.name
                  : undefined
              }
              activity={
                directorMember
                  ? presence[directorMember.agent_id]?.activity
                  : null
              }
              modeChip={<ResearchChatModeChip mode={chatMode} />}
              mode={chatMode}
            />
            {directorV6Enabled ? (
              /* react-doctor-disable-next-line react-doctor/jsx-no-jsx-as-prop -- picker is a deliberate slot in the shared chat shell and is gated by the V6 route. */
              <ResearchDirectorAssignmentPicker
                agents={workspaceAgents}
                currentAgentId={assignedDirectorAgentId}
                pending={directorAssignment.isPending}
                error={directorAssignment.error instanceof Error ? directorAssignment.error.message : null}
                onAssign={(agentId, reason) => directorAssignment.mutate({ agentId, reason })}
              />
            ) : null}
            {!directorV6Enabled ? (
              <div className="border-b px-3 py-2 text-[11px] text-muted-foreground">
                {t(($) => $.panel.fleet)}:{" "}
                {/* react-doctor-disable-next-line react-doctor/js-combine-iterations -- the two projections intentionally render different accessible status groups. */}
                {fleet.members
                  .filter((m) => m.status !== "archived")
                  .map((m) => m.display_name || m.name || m.role)
                  .join(" · ")}
              </div>
            ) : null}
            {!directorV6Enabled && latestRound ? (
              <div className="border-b px-3 py-2">
                <ResearchProductRoundCardView
                  card={latestRound}
                  currentGoal={session.goal}
                  compact
                  pending={send.isPending}
                  onAgree={() =>
                    postUserCommitted(
                      t(($) => $.director_messages.round_agree, {
                        round: latestRound.round_number,
                        decision: latestRound.decision,
                      }),
                    )
                  }
                  onRejectContinue={async () => {
                    await stopAndPostUser(
                      t(($) => $.director_messages.round_reject_continue, {
                        round: latestRound.round_number,
                      }),
                    );
                  }}
                  onRejectStop={() =>
                    postUserCommitted(
                      t(($) => $.director_messages.round_reject_stop, {
                        round: latestRound.round_number,
                        remaining: latestRound.budget_remaining,
                      }),
                    )
                  }
                  onConfirmGoalPatch={(text) =>
                    postUserCommitted(
                      t(($) => $.director_messages.goal_confirm, { goal: text }),
                    )
                  }
                  onEditGoalPatch={(text) =>
                    postUserCommitted(
                      t(($) => $.director_messages.goal_edit, { goal: text }),
                    )
                  }
                  onRejectGoalPatch={() =>
                    postUserCommitted(
                      t(($) => $.director_messages.goal_reject, {
                        round: latestRound.round_number,
                      }),
                    )
                  }
                />
              </div>
            ) : null}
            {!directorV6Enabled && Object.keys(presence).length > 0 ? (
              <div className="flex flex-wrap gap-1.5 border-b px-3 py-2">
                {/* react-doctor-disable-next-line react-doctor/js-combine-iterations -- the two projections intentionally render different accessible status groups. */}
                {fleet.members
                  .filter((m) => presence[m.agent_id]?.activity)
                  .map((m) => (
                    <span
                      key={m.agent_id}
                      className="rounded-full border border-transparent bg-primary/10 px-2 py-0.5 text-[10.5px] font-medium text-primary"
                    >
                      {m.display_name || m.name || m.role}
                      {" · "}
                      {presence[m.agent_id]?.activity}
                    </span>
                  ))}
                {fleet.members
                  .filter(
                    (m) =>
                      m.status === "active" &&
                      !presence[m.agent_id]?.activity,
                  )
                  .slice(0, 4)
                  .map((m) => (
                    <span
                      key={`idle-${m.agent_id}`}
                      className="rounded-full border border-border bg-muted/40 px-2 py-0.5 text-[10.5px] text-muted-foreground"
                    >
                      {m.display_name || m.name || m.role}
                      {" · "}
                      {t(($) => $.step_card.standby)}
                    </span>
                  ))}
              </div>
            ) : null}
            <div
              ref={chatScrollRef}
              className="flex-1 space-y-2.5 overflow-y-auto p-3"
              data-testid="research-chat-feed"
              data-chat-mode={chatMode}
            >
              {/* LRM-824 — anchor once per stage: marker if that stage has no
                  tagged message, else the first tagged message bubble. */}
              {startedStages.map((stage) =>
                stageFirstMessageId.get(stage) ? null : (
                  <ResearchStageChatMarker
                    key={stage}
                    stage={stage}
                    label={t(($) => $.stage[stage])}
                  />
                ),
              )}
              {chatMode === "empty" || chatMode === "loading" ? (
                <ResearchChatModeBody mode={chatMode} />
              ) : null}
              {chatMode === "error" ? (
                <ResearchChatModeBody
                  mode="error"
                  errorMessage={chatErrorMessage}
                  onRetry={() => {
                    if (ui.body.trim()) send.mutate(ui.body.trim());
                    else void send.reset();
                  }}
                />
              ) : null}
              {chatMode === "running" || (chatMode === "error" && chatHasFeed) ? (
                <>
                  {chatFeed.map((item) =>
                    item.kind === "chat" ? (
                      <div
                        key={item.message.id}
                        id={stageAnchorTargetId(item.message.id)}
                        className="scroll-mt-3 space-y-2"
                      >
                        {isCanvasChangeProcessMessage(item.message) ? (
                          <ResearchCanvasChangeCard
                            message={item.message}
                            onFocusNode={handleFocusCanvasChangeNode}
                          />
                        ) : (
                          <ResearchChatCard
                            message={item.message}
                            members={fleet.members}
                            messages={messages}
                            currentGoal={session.goal}
                            roundPending={send.isPending}
                            clarificationPending={send.isPending}
                            onClarificationOption={onClarificationOption}
                            onClarificationForm={onClarificationForm}
                            onClarificationSkip={onClarificationSkip}
                            onRoundAgree={(card) =>
                              postUserCommitted(
                                t(($) => $.director_messages.round_agree, {
                                  round: card.round_number,
                                  decision: card.decision,
                                }),
                              )
                            }
                            onRoundRejectContinue={async (card) => {
                              await stopAndPostUser(
                                t(($) => $.director_messages.round_reject_continue, {
                                  round: card.round_number,
                                }),
                              );
                            }}
                            onRoundRejectStop={(card) =>
                              postUserCommitted(
                                t(($) => $.director_messages.round_reject_stop, {
                                  round: card.round_number,
                                  remaining: card.budget_remaining,
                                }),
                              )
                            }
                            onConfirmGoalPatch={(_card, text) =>
                              postUserCommitted(
                                t(($) => $.director_messages.goal_confirm, {
                                  goal: text,
                                }),
                              )
                            }
                            onEditGoalPatch={(_card, text) =>
                              postUserCommitted(
                                t(($) => $.director_messages.goal_edit, {
                                  goal: text,
                                }),
                              )
                            }
                            onRejectGoalPatch={(card) =>
                              postUserCommitted(
                                t(($) => $.director_messages.goal_reject, {
                                  round: card.round_number,
                                }),
                              )
                            }
                          />
                        )}
                      </div>
                    ) : (
                      <ResearchFleetStepCard
                        key={item.id}
                        card={item}
                        onRetry={onStepRetry}
                        onReassign={onStepReassign}
                      />
                    ),
                  )}
                  {runningCards.map((card) => (
                    <ResearchFleetStepCard key={card.id} card={card} />
                  ))}
                  {waitingCard ? (
                    <ResearchFleetStepCard key={waitingCard.id} card={waitingCard} />
                  ) : null}
                  {showStop ? <ResearchLiveStream sessionId={sessionId} /> : null}
                </>
              ) : null}
            </div>
            {isPaused ? (
              <output
                data-testid="research-paused-banner"
                className="block border-t border-warning/25 bg-warning/10 px-3 py-2 text-[12px] leading-snug text-foreground"
              >
                <span className="font-medium">{t(($) => $.panel.paused_title)}</span>
                {" · "}
                <span className="text-muted-foreground">{t(($) => $.panel.paused_hint)}</span>
              </output>
            ) : null}
            </>
          }
          composer={
            <div className="border-t bg-card p-3">
              <div className="rounded-xl border border-border/80 bg-muted/25 p-2 shadow-sm focus-within:border-primary/35 focus-within:ring-2 focus-within:ring-primary/15">
                {/* react-doctor-disable-next-line react-doctor/jsx-no-jsx-as-prop -- composer is a stable shell slot whose content must follow the selected Director reference. */}
                {directorV6Enabled && selectedDirectorReference ? (
                  <ul
                    className="mb-2"
                    aria-label={selectedDirectorReference.display_summary}
                  >
                    <ResearchSelectedRefChip
                      reference={selectedDirectorReference}
                      disabled={send.isPending}
                      onRemove={() => clearDirectorReference(wsId, sessionId)}
                    />
                  </ul>
                ) : null}
                <Textarea
                  ref={composerRef}
                  data-testid="research-chat-composer"
                  rows={2}
                  value={ui.body}
                  onChange={(e) => dispatch({ type: "setBody", body: e.target.value })}
                  onKeyDown={(e) => {
                    // LRM-800 / cancelled LRM-782: Enter sends; Shift+Enter newline.
                    if (e.key !== "Enter" || e.shiftKey || e.nativeEvent.isComposing) return;
                    e.preventDefault();
                    if (!ui.body.trim() || send.isPending) return;
                    send.mutate(ui.body.trim());
                  }}
                  placeholder={
                    isPaused
                      ? t(($) => $.panel.chat_placeholder_paused)
                      : t(($) => $.panel.chat_placeholder)
                  }
                  className="min-h-[56px] resize-none border-0 bg-transparent px-1 py-1 text-[13px] shadow-none focus-visible:ring-0"
                />
                <div className="mt-1.5 flex flex-col gap-1.5 px-0.5 sm:flex-row sm:items-center sm:justify-between sm:gap-2">
                  <span
                    data-testid="research-chat-composer-hint"
                    className="min-w-0 text-[10px] leading-snug text-muted-foreground"
                    title={t(($) => $.step_card.composer_hint)}
                  >
                    {t(($) => $.step_card.composer_hint)}
                  </span>
                  <div className="flex shrink-0 items-center justify-end gap-1.5">
                    {showStop ? (
                      <Button
                        type="button"
                        size="default"
                        variant="outline"
                        className={cn(
                          "h-9 min-w-[88px] gap-1.5 px-3 text-[13px] font-semibold",
                          // LRM-1246 S3 — keep Stop focusable while pending (LRM-1213).
                          stop.isPending && "opacity-50 cursor-not-allowed",
                        )}
                        aria-disabled={stop.isPending || undefined}
                        aria-label={t(($) => $.panel.stop_aria)}
                        title={t(($) => $.panel.stop_tooltip)}
                        data-testid="research-session-composer-stop"
                        onClick={() => {
                          if (stop.isPending) return;
                          stop.mutate();
                        }}
                      >
                        <Square className="h-3.5 w-3.5 fill-current" />
                        {stop.isPending
                          ? t(($) => $.panel.stopping)
                          : t(($) => $.actions.stop)}
                      </Button>
                    ) : null}
                    <Button
                      type="button"
                      size="default"
                      className={cn(
                        "h-9 min-w-[88px] px-4 text-[13px] font-semibold shadow-sm",
                        // LRM-1250 S4 — keep Send focusable while pending (LRM-1248 / LRM-1213).
                        send.isPending && "opacity-50 cursor-not-allowed",
                      )}
                      // Empty: native disabled OK. Pending: aria-disabled only (not native).
                      disabled={(!ui.body.trim() && !send.isPending) || undefined}
                      aria-disabled={send.isPending || undefined}
                      data-testid="research-session-composer-send"
                      onClick={() => {
                        if (!ui.body.trim() || send.isPending) return;
                        send.mutate(ui.body.trim());
                      }}
                    >
                      {send.isPending
                        ? t(($) => $.step_card.sending)
                        : t(($) => $.panel.send)}
                    </Button>
                  </div>
                </div>
              </div>
            </div>
          }
        />

        {!isMobile && agentPanelNode ? (
          <div
            data-testid="research-agent-side-slot"
            className="relative flex shrink-0 flex-col border-l border-border/30 bg-background"
            style={{ width: agentSideWidth }}
          >
            <button
              type="button"
              data-testid="research-agent-side-resize"
              aria-label={tAgents(($) => $.side_panel.resize_aria)}
              className="absolute inset-y-0 left-0 z-10 w-1.5 cursor-col-resize border-0 bg-transparent p-0 hover:bg-foreground/10"
              onPointerDown={onAgentSideResizePointerDown}
            />
            {agentPanelNode}
          </div>
        ) : null}
      </div>

      {/* LRM-832 — terminal next-step guide (dismiss persists; below Delivery z-80). */}
      {showCompletionGuide && completionKind ? (
        <ResearchCompletionCard
          kind={completionKind}
          onViewReport={() => {
            dismissCompletion();
            dispatch({ type: "setDeliveryOpen", value: true });
            if (directorV6Enabled) void refetchDirectorReports();
          }}
          onNewResearch={() => {
            dismissCompletion();
            nav.push(paths.research());
          }}
          onHome={() => {
            dismissCompletion();
            nav.push(paths.root());
          }}
          onDismiss={dismissCompletion}
        />
      ) : null}

      {/* Portal-friendly mount: keep delivery modal outside the canvas
          `relative`/`overflow` section so it cannot collapse into a corner float. */}
      {directorV6Enabled ? (
        <ResearchV6ReportModal
          open={ui.deliveryOpen}
          onOpenChange={(open) =>
            dispatch({ type: "setDeliveryOpen", value: open })
          }
          appOrigin={
            typeof window === "undefined" ? "" : window.location.origin
          }
          report={
            directorReportDetailData
              ? {
                  id: directorReportDetailData.id,
                  title: directorReportDetailData.title,
                  packageHash: directorReportDetailData.package_hash,
                  sandboxUrl: directorReportDetailData.sandbox_url ?? "",
                  reportOrigin: directorReportDetailData.report_origin ?? "",
                  plainTextFallback: directorReportDetailData.plain_text,
                  revision: directorReportDetailData.revision,
                  status: directorReportDetailData.status,
                  inputCount:
                    directorReportsData?.find(
                      (item) => item.id === directorReportDetailData.id,
                    )?.input_count ?? directorReportDetailData.input_refs.length,
                }
              : null
          }
          history={(directorReportsData ?? []).map((item) => ({
            id: item.id,
            revision: item.revision,
            status: item.status,
            title: item.title,
            publishedAt: item.published_at,
          }))}
          onSelectReport={setSelectedDirectorReportId}
          selectedReportId={directorReportId}
          loading={directorReportsLoading || directorReportDetailFetching}
          onRequestFreshCapability={() => {
            void refetchDirectorReportDetail();
          }}
        />
      ) : (
        <ResearchDeliveryDrawer
          open={ui.deliveryOpen}
          onClose={() => dispatch({ type: "setDeliveryOpen", value: false })}
          report={report}
          sources={sources}
          titleFallback={session.title}
          boundary={humanBoundary}
          sessionStatus={session.status}
          loading={
            isFetching && deliveryContentCount(report, sources.length) <= 0
          }
          error={
            isError
              ? error instanceof Error && error.message
                ? error.message
                : t(($) => $.session_page.load_failed)
              : null
          }
          onRetry={() => {
            void refetch();
          }}
        />
      )}
    </div>
  );

  return (
    <ResearchConnectivityShell>
      <AgentPanelProvider onOpenAgent={handleOpenAgentPanel}>
        {isMobile && agentPanelNode ? agentPanelNode : sessionBody}
      </AgentPanelProvider>
    </ResearchConnectivityShell>
  );
}
