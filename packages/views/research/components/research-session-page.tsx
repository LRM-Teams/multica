"use client";

import { useReducer } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { MessageSquare } from "lucide-react";
import { api } from "@multica/core/api";
import { useWorkspaceId } from "@multica/core/hooks";
import {
  researchKeys,
  researchSessionSnapshotOptions,
  type ResearchPresenceMap,
} from "@multica/core/research";
import type { ResearchGraphNode } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { useT } from "../../i18n/use-t";
import { ResearchCanvas } from "./research-canvas";
import { ResearchSessionChrome } from "./research-session-chrome";

type UiState = {
  selected: ResearchGraphNode | null;
  body: string;
  createProject: boolean;
  createChannel: boolean;
  chatOpen: boolean;
};

type UiAction =
  | { type: "select"; node: ResearchGraphNode | null }
  | { type: "setBody"; body: string }
  | { type: "setCreateProject"; value: boolean }
  | { type: "setCreateChannel"; value: boolean }
  | { type: "setChatOpen"; value: boolean }
  | { type: "clearBody" };

const initialUi: UiState = {
  selected: null,
  body: "",
  createProject: true,
  createChannel: true,
  chatOpen: true,
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
    case "setChatOpen":
      return { ...state, chatOpen: action.value };
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
  const { data, isLoading } = useQuery(researchSessionSnapshotOptions(wsId, sessionId));
  const { data: presence = {} } = useQuery<ResearchPresenceMap>({
    queryKey: researchKeys.presence(wsId, sessionId),
    queryFn: () => ({}),
    enabled: !!wsId && !!sessionId,
    staleTime: Infinity,
  });
  const [ui, dispatch] = useReducer(uiReducer, initialUi);

  const send = useMutation({
    mutationFn: () => api.postResearchMessage(sessionId, { body: ui.body.trim() }),
    onSuccess: () => {
      dispatch({ type: "clearBody" });
      void qc.invalidateQueries({ queryKey: researchKeys.snapshot(wsId, sessionId) });
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

  const { session, fleet, messages } = data;
  const canConfirm = session.status === "awaiting_user_confirm" || session.status === "running";
  const canHandoff = session.status === "completed" || session.status === "awaiting_user_confirm";

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
        selectedSummary={
          ui.selected ? `${ui.selected.title} — ${ui.selected.summary}` : null
        }
      />

      <div className="flex min-h-0 flex-1">
        <section className="relative min-h-0 min-w-0 flex-1">
          <ResearchCanvas
            nodes={data.nodes}
            edges={data.edges}
            selectedId={ui.selected?.id}
            onSelect={(node) => dispatch({ type: "select", node })}
          />
          {!ui.chatOpen ? (
            <Button
              type="button"
              size="icon"
              className="absolute bottom-5 right-5 z-10 h-12 w-12 rounded-full shadow-lg"
              onClick={() => dispatch({ type: "setChatOpen", value: true })}
              aria-label={t(($) => $.panel.chat)}
            >
              <MessageSquare className="h-5 w-5" />
            </Button>
          ) : null}
        </section>

        {ui.chatOpen ? (
          <aside className="flex w-[340px] shrink-0 flex-col border-l bg-background">
            <div className="flex items-center justify-between border-b px-3 py-2">
              <div className="text-xs font-medium text-muted-foreground">{t(($) => $.panel.chat)}</div>
              <Button
                type="button"
                size="sm"
                variant="ghost"
                onClick={() => dispatch({ type: "setChatOpen", value: false })}
              >
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
            <div className="flex-1 space-y-2 overflow-y-auto p-3">
              {messages.map((m) => (
                <div
                  key={m.id}
                  className={
                    m.sender_type === "user"
                      ? "ml-4 rounded-xl border border-primary/20 bg-primary/10 px-3 py-2 text-sm shadow-sm"
                      : "mr-4 rounded-xl border bg-card px-3 py-2 text-sm shadow-sm"
                  }
                >
                  <div className="mb-1 text-[10px] uppercase text-muted-foreground">{m.sender_type}</div>
                  {m.body}
                </div>
              ))}
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
                onClick={() => send.mutate()}
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
