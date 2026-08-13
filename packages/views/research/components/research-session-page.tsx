"use client";

import { useCallback, useEffect, useMemo, useReducer, useRef, useState } from "react";
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { AlertCircle, Square } from "lucide-react";
import { toast } from "sonner";
import { api } from "@multica/core/api";
import { useAuthStore } from "@multica/core/auth";
import type {
  AgentPanelIdentitySnapshot,
  OpenAgentPanelFn,
} from "@multica/core/agents";
import { useWorkspaceId } from "@multica/core/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
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
  ResearchProductRoundCard,
} from "@multica/core/types";
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
  INITIAL_RESEARCH_SESSION_UI_STATE,
  researchSessionUiReducer,
} from "../lib/research-session-ui-state";
import { ResearchConstellationWorkspace } from "./research-constellation-workspace";
import { ResearchD5Chrome } from "./research-d5-chrome";
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
import { ResearchProductRoundCardView } from "./research-product-round-card";
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
  const isMobile = useIsMobile();
  const currentUserId = useAuthStore((s) => s.user?.id ?? null);
  const chatOpen = useResearchUiStore((s) => s.chatDrawerOpen);
  const d5Lens = useResearchUiStore((s) => s.d5Lens);
  const setD5Lens = useResearchUiStore((s) => s.setD5Lens);
  // LRM-832 — dismiss is per-session (localStorage + in-memory for this visit).
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
  const { data: presence = {} } = useQuery(researchPresenceOptions(wsId, sessionId));
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
  const selectedNodeId = useResearchCanvasStore((s) => s.selectedNodeId);
  const selectCanvasNode = useResearchCanvasStore((s) => s.selectNode);
  const clearCanvasSelection = useResearchCanvasStore((s) => s.clearSelection);
  const clearCanvasFilter = useResearchCanvasStore((s) => s.clearFilter);
  const typedGraph = useMemo(
    () =>
      typedGraphPages?.pages.length
        ? mergeTypedGraphPages(typedGraphPages.pages, {
            pinNodeIds: selectedNodeId ? [selectedNodeId] : [],
          })
        : undefined,
    [typedGraphPages, selectedNodeId],
  );
  const detailGraphNodes = useMemo(
    () => mergeResearchCanvasNodes(data?.nodes ?? [], typedGraph),
    [data?.nodes, typedGraph],
  );
  const detailGraphEdges = useMemo(() => {
    const byId = new Map((data?.edges ?? []).map((edge) => [edge.id, edge]));
    for (const edge of typedGraph?.edges ?? []) {
      const key = edge.id || `${edge.from_node_id}:${edge.edge_type}:${edge.to_node_id}`;
      if (!byId.has(key)) byId.set(key, edge);
    }
    return Array.from(byId.values());
  }, [data?.edges, typedGraph?.edges]);
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
  useEffect(() => {
    clearCanvasSelection();
    clearCanvasFilter();
  }, [sessionId, clearCanvasFilter, clearCanvasSelection]);
  const handleSelectCanvasNode = useCallback(
    (node: ResearchGraphNode | null) => {
      selectCanvasNode(node?.id ?? null);
      if (node) dispatch({ type: "setFamily", family: dimensionFamilyOf(node) });
    },
    [selectCanvasNode],
  );
  const handleFocusDetailNode = useCallback(
    (nodeId: string) => {
      const node = resolveResearchCanvasNode(nodeId, {
        snapshotNodes: data?.nodes,
        typedGraph,
      });
      if (node) handleSelectCanvasNode(enrichResearchNodeForDetail(node, typedGraph));
    },
    [data?.nodes, handleSelectCanvasNode, typedGraph],
  );
  useEffect(() => {
    if (!data) return;
    const linkedNodeId = nav.searchParams.get("node");
    if (!linkedNodeId) return;
    const resolved = resolveResearchCanvasNode(linkedNodeId, {
      snapshotNodes: data.nodes,
      typedGraph,
    });
    if (!resolved) return;
    selectCanvasNode(linkedNodeId);
  }, [data, nav.searchParams, selectCanvasNode, typedGraph]);
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
  // Stick-to-bottom while content grows (live stream / new cards); releases if
  // the user scrolls up to read history — no jump-scroll (LRM-820).
  useAutoScroll(chatScrollRef, chatOpen);

  const send = useMutation({
    mutationFn: (body: string) => api.postResearchMessage(sessionId, { body }),
    onSuccess: () => {
      // Focus before clearBody so empty-state native disabled does not dump focus to BODY.
      composerRef.current?.focus();
      dispatch({ type: "clearBody" });
      void qc.invalidateQueries({ queryKey: researchKeys.snapshot(wsId, sessionId) });
      void qc.invalidateQueries({ queryKey: researchKeys.productRounds(wsId, sessionId) });
    },
    onError: (err) => mutationErrorToast(t(($) => $.session_page.send_failed), err),
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
      ? `（reason=${sessionInterrupt.reason}）`
      : "";
    const body = `请重试刚才失败的唤醒${reason}：${sessionInterrupt.headline}。配置根因仍走运维/LRM-858；本条只请求再试一次。`;
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

  // LRM-833 — 5xx with no cache: dedicated error page + retry.
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

  // Keep successful snapshot on refetch failure so Delivery can show its error
  // surface (LRM-993) instead of blanking the whole session shell.
  if (!data) {
    return (
      <ResearchConnectivityShell>
        <div
          role="alert"
          data-testid="research-session-load-error"
          className="flex h-full flex-col items-center justify-center gap-3 px-6 py-12 text-center"
        >
          <AlertCircle className="size-6 text-destructive" aria-hidden />
          <p className="text-sm text-destructive">
            {error instanceof Error && error.message
              ? error.message
              : t(($) => $.session_page.load_failed)}
          </p>
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() => {
              void refetch();
            }}
          >
            {t(($) => $.session_page.retry)}
          </Button>
        </div>
      </ResearchConnectivityShell>
    );
  }

  const { session, messages, report, sources } = data;
  const fleetMembers = dedupeResearchFleetMembers(data.fleet.members);
  const fleet = { ...data.fleet, members: fleetMembers };
  const linkedNodeId = nav.searchParams.get("node");
  const selectedNode = (() => {
    const base = resolveResearchCanvasNode(selectedNodeId ?? linkedNodeId, {
      snapshotNodes: data.nodes,
      typedGraph,
    });
    return base ? enrichResearchNodeForDetail(base, typedGraph) : null;
  })();
  const executionRows = buildExecutionOverlayRows({
    members: fleet.members,
    presence,
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
  const startedStages = RESEARCH_STAGE_ORDER.filter(
    (stage) =>
      resolveStageStepState(stage, session.current_stage, session.status) !== "upcoming",
  );
  const latestRound: ResearchProductRoundCard | undefined = productRounds?.rounds?.[
    (productRounds.rounds.length ?? 0) - 1
  ];

  const postUser = (body: string) => send.mutate(body);

  // Plain derivation after data-gated early returns — do not use hooks here
  // (rules-of-hooks). goal history is cheap and only needed on the success path.
  const goalVersion = data.run?.run?.goal_version ?? data.run?.contract?.goal_version ?? null;
  const goalHistory = buildGoalVersionHistory({
    currentGoal: session.goal,
    currentVersion: goalVersion,
    messages,
  });
  const goalImpact = typedGraph?.nodes ? summarizeGoalImpact(typedGraph.nodes) : null;
  const projectionMismatch =
    canvasMode === "ready" &&
    !typedGraphLoading &&
    !typedGraphError &&
    (!typedGraph || typedGraph.nodes.length === 0);

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

  const chatFeed = buildFleetChatFeed(messages);
  const runningCards = presenceRunningCards(presence, fleet.members);
  const waitingCard = nextStageWaitingCard(session.current_stage, session.status);
  const showStop = canStop || runningCards.length > 0;
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
    const reason = card.reason ? `（reason=${card.reason}）` : "";
    postFleetAction(
      `请重试刚才失败的唤醒${reason}：${card.summaryHeadline}。配置根因仍走运维/LRM-858；本条只请求再试一次。`,
    );
  };

  const onStepReassign = (card: FleetStepCardModel) => {
    postFleetAction(
      `请将唤醒失败的任务改派给其他活跃成员：${card.title} · ${card.summaryHeadline}`,
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
        typedGraphNodes={typedGraph?.nodes ?? []}
        session={session}
        contract={data.run?.contract}
        canConfirm={canConfirm}
        canHandoff={canHandoff}
        createProject={ui.createProject}
        createChannel={ui.createChannel}
        onCreateProjectChange={(value) => dispatch({ type: "setCreateProject", value })}
        onCreateChannelChange={(value) => dispatch({ type: "setCreateChannel", value })}
        onConfirm={() => confirm.mutate()}
        onReject={(reason) =>
          rejectConfirm.mutate(formatStageGateRejectReply(reason))
        }
        onHandoff={() => handoff.mutate()}
        confirmPending={confirm.isPending}
        rejectPending={rejectConfirm.isPending}
        handoffPending={handoff.isPending}
        onOpenDelivery={() => dispatch({ type: "setDeliveryOpen", value: true })}
        members={fleet.members}
        sources={sources}
        pendingSubstantiveGoal={
          latestRound?.goal_patch_proposal?.trim()
            ? latestRound.goal_patch_proposal
            : null
        }
        onConfirmSubstantiveGoal={(proposal) =>
          steer.mutate({
            goal: proposal,
            reason: "user_confirmed_substantive_goal_proposal",
          })
        }
      />

      {sessionInterrupt ? (
        <ResearchSessionInterruptBanner
          interrupt={sessionInterrupt}
          phase={interruptPhase}
          onRetry={retrySessionInterrupt}
        />
      ) : null}

      {/* LRM-1112: S1–S4 timeline lives inside the single header surface (L2). */}
      <div className="relative flex min-h-0 flex-1">
        <ResearchConstellationWorkspace
          className="min-h-0 flex-1"
          typedGraph={typedGraph}
          typedLoading={typedGraphLoading}
          typedError={typedGraphError}
          projectionMismatch={projectionMismatch}
          onRetryTypedGraph={() => {
            void refetchTypedGraph();
          }}
          retryTypedGraphPending={typedGraphFetching && !typedGraphFetchingNextPage}
          snapshotNodeCount={data.nodes.length}
          typedGraphSessionId={sessionId}
          typedGraphVersion={typedGraph?.graph_version ?? null}
          typedGraphHasNextPage={typedGraphHasNextPage === true}
          typedGraphLoadMorePending={typedGraphFetchingNextPage}
          onLoadMoreTypedGraph={
            typedGraphHasNextPage ? () => void fetchNextTypedGraphPage() : undefined
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
          formingStage={session.current_stage}
          formingMembers={fleet.members}
          formingTasks={data.run?.tasks ?? []}
          formingMessages={messages}
          registerReportController={(controller) => {
            reportControllerRef.current = controller;
          }}
          detailPanel={
            selectedNode ? (
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
                onContinueDeepening={() =>
                  postUser(
                    t(($) => $.d5.detail.continue_message, { title: selectedNode.title }),
                  )
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
            <div
              className="flex items-center gap-2 border-b px-3 py-2.5"
              data-testid="research-chat-header"
              data-chat-mode={chatMode}
            >
              <div className="text-sm font-semibold text-foreground">
                {t(($) => $.panel.chat)}
              </div>
              <ResearchChatModeChip mode={chatMode} />
            </div>
            <div className="border-b px-3 py-2 text-[11px] text-muted-foreground">
              {t(($) => $.panel.fleet)}:{" "}
              {fleet.members
                .filter((m) => m.status !== "archived")
                .map((m) => m.display_name || m.name || m.role)
                .join(" · ")}
            </div>
            {latestRound ? (
              <div className="border-b px-3 py-2">
                <ResearchProductRoundCardView
                  card={latestRound}
                  currentGoal={session.goal}
                  compact
                  pending={send.isPending}
                  onAgree={() =>
                    postUser(
                      `同意罗纳尔多产品轮 Round ${latestRound.round_number} 裁定：${latestRound.decision}`,
                    )
                  }
                  onRejectContinue={() => {
                    void api.stopResearchSession(sessionId).then(() => {
                      void qc.invalidateQueries({
                        queryKey: researchKeys.snapshot(wsId, sessionId),
                      });
                    });
                    postUser(
                      `驳回 continue：请停止调研（Round ${latestRound.round_number}）。`,
                    );
                  }}
                  onRejectStop={() =>
                    postUser(
                      `驳回 stop：请在预算内再开一轮加深（Round ${latestRound.round_number}，剩余 ${latestRound.budget_remaining}）。`,
                    )
                  }
                  onConfirmGoalPatch={(text) =>
                    postUser(`确认将调研最终目标更新为：${text}`)
                  }
                  onEditGoalPatch={(text) =>
                    postUser(`请按以下文本更新调研最终目标：${text}`)
                  }
                  onRejectGoalPatch={() =>
                    postUser(
                      `拒绝本轮目标回灌提案（Round ${latestRound.round_number}），保持当前目标不变。`,
                    )
                  }
                />
              </div>
            ) : null}
            {Object.keys(presence).length > 0 ? (
              <div className="flex flex-wrap gap-1.5 border-b px-3 py-2">
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
                          <ResearchCanvasChangeCard message={item.message} />
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
                              postUser(
                                `同意罗纳尔多产品轮 Round ${card.round_number} 裁定：${card.decision}`,
                              )
                            }
                            onRoundRejectContinue={(card) => {
                              void api.stopResearchSession(sessionId).then(() => {
                                void qc.invalidateQueries({
                                  queryKey: researchKeys.snapshot(wsId, sessionId),
                                });
                              });
                              postUser(
                                `驳回 continue：请停止调研（Round ${card.round_number}）。`,
                              );
                            }}
                            onRoundRejectStop={(card) =>
                              postUser(
                                `驳回 stop：请在预算内再开一轮加深（Round ${card.round_number}，剩余 ${card.budget_remaining}）。`,
                              )
                            }
                            onConfirmGoalPatch={(_card, text) =>
                              postUser(`确认将调研最终目标更新为：${text}`)
                            }
                            onEditGoalPatch={(_card, text) =>
                              postUser(`请按以下文本更新调研最终目标：${text}`)
                            }
                            onRejectGoalPatch={(card) =>
                              postUser(
                                `拒绝本轮目标回灌提案（Round ${card.round_number}），保持当前目标不变。`,
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
