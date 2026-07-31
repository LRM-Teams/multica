"use client";

import { useReducer, useRef } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { MessageSquare } from "lucide-react";
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
import { useT } from "../../i18n/use-t";
import {
  RESEARCH_STAGE_ORDER,
  resolveStageStepState,
  stageAnchorId,
} from "../lib/research-stages";
import { ResearchCanvas } from "./research-canvas";
import { ResearchChatCard } from "./research-chat-card";
import { ResearchDeliveryDrawer } from "./research-delivery-drawer";
import { ResearchProductRoundCardView } from "./research-product-round-card";
import { ResearchSessionChrome } from "./research-session-chrome";
import {
  ResearchStageChatMarker,
  ResearchStageTimeline,
} from "./research-stage-timeline";

type UiState = {
  selected: ResearchGraphNode | null;
  body: string;
  createProject: boolean;
  createChannel: boolean;
  deliveryOpen: boolean;
};

type UiAction =
  | { type: "select"; node: ResearchGraphNode | null }
  | { type: "setBody"; body: string }
  | { type: "setCreateProject"; value: boolean }
  | { type: "setCreateChannel"; value: boolean }
  | { type: "setDeliveryOpen"; value: boolean }
  | { type: "clearBody" };

const initialUi: UiState = {
  selected: null,
  body: "",
  createProject: true,
  createChannel: true,
  deliveryOpen: false,
};

function uiReducer(state: UiState, action: UiAction): UiState {
  switch (action.type) {
    case "select":
      return { ...state, selected: action.node };
    case "setBody":
      return { ...state, body: action.body };
    case "setCreateProject":
      return { ...state, createProject: action.value };
    case "setCreateChannel":
      return { ...state, createChannel: action.value };
    case "setDeliveryOpen":
      return { ...state, deliveryOpen: action.value };
    case "clearBody":
      return { ...state, body: "" };
    default:
      return state;
  }
}

export function ResearchSessionPage({ sessionId }: { sessionId: string }) {
  const { t } = useT("research");
  const wsId = useWorkspaceId();
  const qc = useQueryClient();
  const chatOpen = useResearchUiStore((s) => s.chatDrawerOpen);
  const setChatOpen = useResearchUiStore((s) => s.setChatDrawerOpen);
  const { data, isLoading } = useQuery(researchSessionSnapshotOptions(wsId, sessionId));
  const { data: presence = {} } = useQuery(researchPresenceOptions(wsId, sessionId));
  const { data: productRounds } = useQuery(researchProductRoundsOptions(wsId, sessionId));
  const [ui, dispatch] = useReducer(uiReducer, initialUi);
  const chatScrollRef = useRef<HTMLDivElement>(null);

  const send = useMutation({
    mutationFn: (body: string) => api.postResearchMessage(sessionId, { body }),
    onSuccess: () => {
      dispatch({ type: "clearBody" });
      void qc.invalidateQueries({ queryKey: researchKeys.snapshot(wsId, sessionId) });
      void qc.invalidateQueries({ queryKey: researchKeys.productRounds(wsId, sessionId) });
    },
  });

  const confirm = useMutation({
    mutationFn: () => api.confirmResearchSession(sessionId),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: researchKeys.snapshot(wsId, sessionId) });
    },
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
  });

  if (isLoading || !data) {
    return (
      <div className="flex h-full flex-col gap-0">
        <Skeleton className="h-16 w-full" />
        <Skeleton className="min-h-0 flex-1" />
      </div>
    );
  }

  const { session, messages, report, sources } = data;
  const fleetMembers = dedupeResearchFleetMembers(data.fleet.members);
  const fleet = { ...data.fleet, members: fleetMembers };
  const canConfirm = session.status === "awaiting_user_confirm" || session.status === "running";
  const canHandoff = session.status === "completed" || session.status === "awaiting_user_confirm";
  const startedStages = RESEARCH_STAGE_ORDER.filter(
    (stage) =>
      resolveStageStepState(stage, session.current_stage, session.status) !== "upcoming",
  );
  const latestRound: ResearchProductRoundCard | undefined = productRounds?.rounds?.[
    (productRounds.rounds.length ?? 0) - 1
  ];

  const postUser = (body: string) => send.mutate(body);

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
      />

      <ResearchStageTimeline
        currentStage={session.current_stage}
        sessionStatus={session.status}
        onSelectStage={scrollToStage}
      />

      <div className="flex min-h-0 flex-1">
        <section className="relative min-h-0 min-w-0 flex-1">
          <ResearchCanvas
            nodes={data.nodes}
            edges={data.edges}
            sources={sources}
            members={fleet.members}
            presence={presence}
            selectedId={ui.selected?.id}
            onSelect={(node) => dispatch({ type: "select", node })}
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
          <ResearchDeliveryDrawer
            open={ui.deliveryOpen}
            onClose={() => dispatch({ type: "setDeliveryOpen", value: false })}
            report={report}
            sources={sources}
          />
          {!chatOpen ? (
            <Button
              type="button"
              size="icon"
              className="absolute bottom-5 right-5 z-10 h-12 w-12 rounded-full shadow-lg"
              onClick={() => setChatOpen(true)}
              aria-label={t(($) => $.panel.chat)}
            >
              <MessageSquare className="h-5 w-5" />
            </Button>
          ) : null}
        </section>

        {chatOpen ? (
          <aside className="flex w-[340px] shrink-0 flex-col border-l bg-background">
            <div className="flex items-center justify-between border-b px-3 py-2">
              <div className="text-xs font-medium text-muted-foreground">{t(($) => $.panel.chat)}</div>
              <Button type="button" size="sm" variant="ghost" onClick={() => setChatOpen(false)}>
                {t(($) => $.panel.hide_chat)}
              </Button>
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
              <div className="space-y-1 border-b px-3 py-2 text-[11px]">
                <div className="font-medium text-muted-foreground">{t(($) => $.panel.presence)}</div>
                {fleet.members
                  .filter((m) => presence[m.agent_id]?.activity)
                  .map((m) => (
                    <div key={m.agent_id} className="truncate text-muted-foreground">
                      <span className="text-foreground">
                        {m.display_name || m.name || m.role}
                      </span>
                      {" · "}
                      {presence[m.agent_id]?.activity}
                    </div>
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
              {messages.length === 0 ? (
                <p className="px-1 text-xs text-muted-foreground">{t(($) => $.chat.empty)}</p>
              ) : (
                messages.map((m) => (
                  <ResearchChatCard
                    key={m.id}
                    message={m}
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
                      postUser(`驳回 continue：请停止调研（Round ${card.round_number}）。`);
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
                ))
              )}
            </div>
            <div className="space-y-2 border-t p-3">
              <Textarea
                rows={3}
                value={ui.body}
                onChange={(e) => dispatch({ type: "setBody", body: e.target.value })}
                placeholder={t(($) => $.panel.chat_placeholder)}
              />
              <Button
                size="sm"
                disabled={!ui.body.trim() || send.isPending}
                onClick={() => send.mutate(ui.body.trim())}
              >
                {t(($) => $.panel.send)}
              </Button>
            </div>
          </aside>
        ) : null}
      </div>
    </div>
  );
}
