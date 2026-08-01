"use client";

import { useMemo, useReducer, useRef } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { AlertCircle, Square } from "lucide-react";
import { toast } from "sonner";
import { api } from "@multica/core/api";
import { useWorkspaceId } from "@multica/core/hooks";
import {
  dedupeResearchFleetMembers,
  researchKeys,
  researchPresenceOptions,
  researchProductRoundsOptions,
  researchSessionSnapshotOptions,
  useResearchUiStore,
} from "@multica/core/research";
import type { ResearchGraphNode, ResearchProductRoundCard } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { useAutoScroll } from "@multica/ui/hooks/use-auto-scroll";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { useT } from "../../i18n/use-t";
import {
  buildFleetChatFeed,
  nextStageWaitingCard,
  presenceRunningCards,
  type FleetStepCardModel,
} from "../lib/fleet-step-cards";
import {
  buildExplorationDimensions,
  buildHumanBoundary,
  buildSourceStrategy,
  dimensionFamilyOf,
} from "../lib/m2-visibility";
import { isResearchSessionStoppable } from "../lib/research-stream";
import {
  RESEARCH_STAGE_ORDER,
  resolveStageStepState,
  stageAnchorId,
} from "../lib/research-stages";
import { ExplorationRail } from "./exploration-rail";
import { HumanBoundaryCard } from "./human-boundary-card";
import { ResearchCanvas } from "./research-canvas";
import { ResearchChatCard } from "./research-chat-card";
import { ResearchDeliveryDrawer } from "./research-delivery-drawer";
import { ResearchFleetStepCard } from "./research-fleet-step-card";
import { ResearchLiveStream } from "./research-live-stream";
import { ResearchProductRoundCardView } from "./research-product-round-card";
import { ResearchSessionChrome } from "./research-session-chrome";
import {
  ResearchStageChatMarker,
  ResearchStageTimeline,
} from "./research-stage-timeline";
import { SourceStrategyStrip } from "./source-strategy-strip";
import { VisibilityTabs } from "./visibility-tabs";

type UiState = {
  selected: ResearchGraphNode | null;
  body: string;
  createProject: boolean;
  createChannel: boolean;
  deliveryOpen: boolean;
  selectedFamily: string | null;
};

type UiAction =
  | { type: "select"; node: ResearchGraphNode | null }
  | { type: "setBody"; body: string }
  | { type: "setCreateProject"; value: boolean }
  | { type: "setCreateChannel"; value: boolean }
  | { type: "setDeliveryOpen"; value: boolean }
  | { type: "setFamily"; family: string | null }
  | { type: "clearBody" };

const initialUi: UiState = {
  selected: null,
  body: "",
  createProject: true,
  createChannel: true,
  deliveryOpen: false,
  selectedFamily: null,
};

function uiReducer(state: UiState, action: UiAction): UiState {
  switch (action.type) {
    case "select":
      return {
        ...state,
        selected: action.node,
        selectedFamily: action.node ? dimensionFamilyOf(action.node) : state.selectedFamily,
      };
    case "setBody":
      return { ...state, body: action.body };
    case "setCreateProject":
      return { ...state, createProject: action.value };
    case "setCreateChannel":
      return { ...state, createChannel: action.value };
    case "setDeliveryOpen":
      return { ...state, deliveryOpen: action.value };
    case "setFamily":
      return { ...state, selectedFamily: action.family };
    case "clearBody":
      return { ...state, body: "" };
    default:
      return state;
  }
}

function mutationErrorToast(fallback: string, err: unknown) {
  showErrorToast(err instanceof Error && err.message ? err.message : fallback);
}

export function ResearchSessionPage({ sessionId }: { sessionId: string }) {
  const { t } = useT("research");
  const wsId = useWorkspaceId();
  const qc = useQueryClient();
  const chatOpen = useResearchUiStore((s) => s.chatDrawerOpen);
  const setChatOpen = useResearchUiStore((s) => s.setChatDrawerOpen);
  const { data, isLoading, isError, error, refetch } = useQuery(
    researchSessionSnapshotOptions(wsId, sessionId),
  );
  const { data: presence = {} } = useQuery(researchPresenceOptions(wsId, sessionId));
  const { data: productRounds } = useQuery(researchProductRoundsOptions(wsId, sessionId));
  const [ui, dispatch] = useReducer(uiReducer, initialUi);
  const chatScrollRef = useRef<HTMLDivElement>(null);
  // Stick-to-bottom while content grows (live stream / new cards); releases if
  // the user scrolls up to read history — no jump-scroll (LRM-820).
  useAutoScroll(chatScrollRef, chatOpen);

  const send = useMutation({
    mutationFn: (body: string) => api.postResearchMessage(sessionId, { body }),
    onSuccess: () => {
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

  const confirm = useMutation({
    mutationFn: () => api.confirmResearchSession(sessionId),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: researchKeys.snapshot(wsId, sessionId) });
    },
    onError: (err) => mutationErrorToast(t(($) => $.session_page.confirm_failed), err),
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

  // LRM-890 M2 visibility models — derived from graph nodes / sources / report.
  const explorationDims = useMemo(
    () => buildExplorationDimensions(data?.nodes ?? []),
    [data?.nodes],
  );
  const sourceStrategy = useMemo(
    () => buildSourceStrategy(data?.sources ?? []),
    [data?.sources],
  );
  const humanBoundary = useMemo(
    () => buildHumanBoundary(data?.nodes ?? [], data?.report),
    [data?.nodes, data?.report],
  );

  // LRM-799: never keep a permanent skeleton on failure — only while loading.
  if (isLoading) {
    return (
      <div className="flex h-full flex-col gap-0" aria-busy="true">
        <Skeleton className="h-16 w-full" />
        <Skeleton className="min-h-0 flex-1" />
      </div>
    );
  }

  if (isError || !data) {
    return (
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
    );
  }

  const { session, messages, report, sources } = data;
  const fleetMembers = dedupeResearchFleetMembers(data.fleet.members);
  const fleet = { ...data.fleet, members: fleetMembers };
  const canConfirm = session.status === "awaiting_user_confirm" || session.status === "running";
  const canHandoff = session.status === "completed" || session.status === "awaiting_user_confirm";
  const canStop = isResearchSessionStoppable(session.status);
  const isPaused = session.status === "paused";
  const startedStages = RESEARCH_STAGE_ORDER.filter(
    (stage) =>
      resolveStageStepState(stage, session.current_stage, session.status) !== "upcoming",
  );
  const latestRound: ResearchProductRoundCard | undefined = productRounds?.rounds?.[
    (productRounds.rounds.length ?? 0) - 1
  ];

  const postUser = (body: string) => send.mutate(body);

  const chatFeed = buildFleetChatFeed(messages);
  const runningCards = presenceRunningCards(presence, fleet.members);
  const waitingCard = nextStageWaitingCard(session.current_stage, session.status);
  const showStop = canStop || runningCards.length > 0;

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

  const scrollToStage = (stage: string) => {
    setChatOpen(true);
    // Wait a frame so the chat pane mounts before scrolling.
    requestAnimationFrame(() => {
      const el =
        document.getElementById(stageAnchorId(stage)) ??
        chatScrollRef.current?.querySelector(`[data-research-stage="${stage}"]`);
      el?.scrollIntoView({ behavior: "smooth", block: "start" });
    });
  };

  return (
    <div className="relative flex h-full min-h-0 flex-col">
      <ResearchSessionChrome
        session={session}
        canConfirm={canConfirm}
        canHandoff={canHandoff}
        createProject={ui.createProject}
        createChannel={ui.createChannel}
        onCreateProjectChange={(value) => dispatch({ type: "setCreateProject", value })}
        onCreateChannelChange={(value) => dispatch({ type: "setCreateChannel", value })}
        onConfirm={() => confirm.mutate()}
        onHandoff={() => handoff.mutate()}
        confirmPending={confirm.isPending}
        handoffPending={handoff.isPending}
        onOpenDelivery={() => dispatch({ type: "setDeliveryOpen", value: true })}
        selectedSummary={
          ui.selected ? `${ui.selected.title} — ${ui.selected.summary}` : null
        }
        members={fleet.members}
        sources={sources}
      />

      <ResearchStageTimeline
        currentStage={session.current_stage}
        sessionStatus={session.status}
        onSelectStage={scrollToStage}
      />

      <VisibilityTabs
        dimensions={explorationDims}
        strategy={sourceStrategy}
        boundary={humanBoundary}
        selectedFamily={ui.selectedFamily}
        selectedQuestionId={ui.selected?.id}
        onSelectFamily={(family) => dispatch({ type: "setFamily", family })}
        onSelectQuestion={(id) => {
          const node = data.nodes.find((n) => n.id === id) ?? null;
          dispatch({ type: "select", node });
        }}
      />

      <div className="hidden border-b sm:block">
        <SourceStrategyStrip model={sourceStrategy} />
      </div>

      <div className="flex min-h-0 flex-1">
        <ExplorationRail
          className="hidden sm:flex"
          dimensions={explorationDims}
          selectedFamily={ui.selectedFamily}
          selectedQuestionId={ui.selected?.id}
          onSelectFamily={(family) => dispatch({ type: "setFamily", family })}
          onSelectQuestion={(id) => {
            const node = data.nodes.find((n) => n.id === id) ?? null;
            dispatch({ type: "select", node });
          }}
        />
        <section className="relative min-h-0 min-w-0 flex-1">
          <ResearchCanvas
            nodes={data.nodes}
            edges={data.edges}
            sources={sources}
            members={fleet.members}
            presence={presence}
            selectedId={ui.selected?.id}
            onSelect={(node) => dispatch({ type: "select", node })}
            onOpenDelivery={() => dispatch({ type: "setDeliveryOpen", value: true })}
            onOpenChat={() => setChatOpen(true)}
            chatOpen={chatOpen}
            onRetry={(node) => {
              // LRM-848 entry → LRM-828 retry path. Until a dedicated BE API lands,
              // ask the fleet lead to re-explore from this dead_end via chat.
              const body = t(($) => $.ring.retry_message, {
                title: node.title || node.node_type,
                id: node.id,
              });
              void api
                .postResearchMessage(sessionId, { body })
                .then(() =>
                  qc.invalidateQueries({ queryKey: researchKeys.snapshot(wsId, sessionId) }),
                );
            }}
          />
          {data.nodes.length === 0 ? (
            <div
              data-testid="research-session-canvas-forming"
              className="pointer-events-none absolute inset-0 z-[5] flex items-center justify-center px-6"
            >
              <div className="max-w-sm text-center">
                <p className="text-sm font-medium text-foreground">
                  {t(($) => $.session_page.canvas_forming_title)}
                </p>
                <p className="mt-1.5 text-xs text-muted-foreground">
                  {t(($) => $.session_page.canvas_forming_hint)}
                </p>
              </div>
            </div>
          ) : null}
        </section>

        <aside className="hidden w-[260px] shrink-0 flex-col gap-3 overflow-y-auto border-l bg-background p-3 lg:flex">
          <HumanBoundaryCard model={humanBoundary} />
        </aside>

        {chatOpen ? (
          <aside className="flex w-[min(100%,380px)] shrink-0 flex-col border-l bg-background">
            <div className="flex items-center justify-between border-b px-3 py-2.5">
              <div className="text-sm font-semibold text-foreground">{t(($) => $.panel.chat)}</div>
              <Button type="button" size="sm" variant="ghost" onClick={() => setChatOpen(false)}>
                {t(($) => $.panel.hide_chat)}
              </Button>
            </div>
            <div className="border-b p-3 lg:hidden">
              <HumanBoundaryCard model={humanBoundary} />
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
            <div ref={chatScrollRef} className="flex-1 space-y-2.5 overflow-y-auto p-3">
              {startedStages.map((stage) => (
                <ResearchStageChatMarker
                  key={stage}
                  stage={stage}
                  label={t(($) => $.stage[stage])}
                />
              ))}
              {messages.length === 0 && runningCards.length === 0 && !showStop ? (
                <p className="px-1 text-xs text-muted-foreground">{t(($) => $.chat.empty)}</p>
              ) : (
                <>
                  {chatFeed.map((item) =>
                    item.kind === "chat" ? (
                      <ResearchChatCard
                        key={item.message.id}
                        message={item.message}
                        members={fleet.members}
                        currentGoal={session.goal}
                        roundPending={send.isPending}
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
              )}
            </div>
            {isPaused ? (
              <output
                data-testid="research-paused-banner"
                className="block border-t border-amber-500/25 bg-amber-500/10 px-3 py-2 text-[12px] leading-snug text-foreground"
              >
                <span className="font-medium">{t(($) => $.panel.paused_title)}</span>
                {" · "}
                <span className="text-muted-foreground">{t(($) => $.panel.paused_hint)}</span>
              </output>
            ) : null}
            <div className="border-t bg-card p-3">
              <div className="rounded-xl border border-border/80 bg-muted/25 p-2 shadow-sm focus-within:border-primary/35 focus-within:ring-2 focus-within:ring-primary/15">
                <Textarea
                  rows={2}
                  value={ui.body}
                  onChange={(e) => dispatch({ type: "setBody", body: e.target.value })}
                  onKeyDown={(e) => {
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
                <div className="mt-1.5 flex items-center justify-between gap-2 px-0.5">
                  <span className="text-[10px] text-muted-foreground">
                    {t(($) => $.step_card.composer_hint)}
                  </span>
                  <div className="flex items-center gap-1.5">
                    {showStop ? (
                      <Button
                        type="button"
                        size="default"
                        variant="outline"
                        className="h-9 min-w-[88px] gap-1.5 px-3 text-[13px] font-semibold"
                        disabled={stop.isPending}
                        aria-label={t(($) => $.panel.stop_aria)}
                        title={t(($) => $.panel.stop_tooltip)}
                        onClick={() => stop.mutate()}
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
                      className="h-9 min-w-[88px] px-4 text-[13px] font-semibold shadow-sm"
                      disabled={!ui.body.trim() || send.isPending}
                      onClick={() => send.mutate(ui.body.trim())}
                    >
                      {send.isPending
                        ? t(($) => $.step_card.sending)
                        : t(($) => $.panel.send)}
                    </Button>
                  </div>
                </div>
              </div>
            </div>
          </aside>
        ) : null}
      </div>

      {/* Portal-friendly mount: keep delivery modal outside the canvas
          `relative`/`overflow` section so it cannot collapse into a corner float. */}
      <ResearchDeliveryDrawer
        open={ui.deliveryOpen}
        onClose={() => dispatch({ type: "setDeliveryOpen", value: false })}
        report={report}
        sources={sources}
        titleFallback={session.title}
        boundary={humanBoundary}
      />
    </div>
  );
}
