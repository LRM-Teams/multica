"use client";

import * as React from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { api } from "@multica/core/api";
import { agentTemplateDetailOptions } from "@multica/core/agents/queries";
import { useAuthStore } from "@multica/core/auth";
import { useChatStore } from "@multica/core/chat";
import {
  chatKeys,
  chatSessionsOptions,
  pendingChatTasksOptions,
} from "@multica/core/chat/queries";
import { useWorkspaceId } from "@multica/core/hooks";
import { createLogger } from "@multica/core/logger";
import {
  NOTES_ASSISTANT_AGENT_NAME,
  NOTES_ASSISTANT_AGENT_TEMPLATE_SLUG,
  notesAssistantSetupDismissKey,
  resolveNotesAssistantAgent,
} from "@multica/core/notes/notes-assistant-agent";
import { resolvePeriodBriefSynthesizerId } from "@multica/core/notes/period-brief-agent";
import {
  PERIOD_BRIEF_INTENT_NO,
  PERIOD_BRIEF_INTENT_YES,
  PERIOD_BRIEF_SATELLITE_ASK,
  isPeriodBriefAwaitingIntent,
  isPeriodBriefClarifyingPlan,
  periodBriefRunLocksComposer,
} from "@multica/core/notes/period-brief-compose";
import { type PeriodBriefCollectorSlot } from "@multica/core/notes/period-brief-collectors";
import { noteKeys, noteListOptions, notePeriodBriefActiveOptions, notePeriodBriefPlanOptions } from "@multica/core/notes/queries";
import { abbreviateNoteSelection, attachNoteSelectionQuote, type NoteSelectionExcerpt } from "@multica/core/notes/selection-quote";
import {
  attachNotePageRefs,
  type NotePageRef,
} from "@multica/core/notes/page-ref";
import { useWorkspacePaths } from "@multica/core/paths";
import { runtimeListOptions } from "@multica/core/runtimes";
import { agentListOptions, memberListOptions, workspaceKeys } from "@multica/core/workspace/queries";
import type { Agent, CreateAgentRequest, EnsureNotesAssistantAgentResponse, NotePeriodBriefPlan } from "@multica/core/types";
import { useIsMobile } from "@multica/ui/hooks/use-mobile";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { CreateAgentDialog } from "../agents/components/create-agent-dialog";
import { useT } from "../i18n";
import { useViewingTimezone } from "../common/use-viewing-timezone";
import { usePrefersReducedMotion } from "../common/use-prefers-reduced-motion";
import { excludeChannelShellSessions } from "../chat/lib/exclude-channel-shell-sessions";
import { ChatWindow } from "../chat/components/chat-window";
import { noteAssistantSidebarClosesOnLeave } from "../chat/components/chat-window-layout";
import { NoteAssistantFabCluster, type NoteAssistantFabAction } from "./note-assistant-fab-cluster";
import { NoteHighlightsCompose } from "./note-highlights-compose";
import {
  NotePeriodBriefCompose,
} from "./note-period-brief-compose";
import { NotePeriodBriefIntentConfirm } from "./note-period-brief-intent-confirm";
import { NoteSelectionQuotePreview } from "./note-selection-quote-preview";
import { NotePageRefPreview } from "./note-page-ref-preview";
import { NotesAssistantSetupCard } from "./notes-assistant-setup-card";

const logger = createLogger("chat.note-bubble");
const EMPTY_SELECTION_EXCERPTS: NoteSelectionExcerpt[] = [];
const EMPTY_PAGE_REFS: NotePageRef[] = [];

function readSetupHintDismissed(workspaceId: string | undefined): boolean {
  if (!workspaceId || typeof window === "undefined") return false;
  return window.localStorage.getItem(notesAssistantSetupDismissKey(workspaceId)) === "1";
}

function clearSetupHintDismissed(workspaceId: string | undefined) {
  if (!workspaceId || typeof window === "undefined") return;
  window.localStorage.removeItem(notesAssistantSetupDismissKey(workspaceId));
}

/**
 * Notes-page assistant bubble: standalone chat_session bound to the current
 * note page (+ subtree via agent notes get / tree). Uses the workspace
 * 笔记助手 agent (not a free agent picker). Create only on explicit button
 * click — soft open only probes needs_setup.
 */
export function NoteAssistantBubble({
  pageId,
  pageTitle,
}: {
  pageId: string;
  pageTitle?: string;
}) {
  const { t } = useT("layout");
  const wsId = useWorkspaceId();
  const workspacePaths = useWorkspacePaths();
  const queryClient = useQueryClient();
  const currentUser = useAuthStore((s) => s.user);
  const isMobile = useIsMobile();
  const layout = isMobile ? "fullscreen" : "sidebar";
  const openPageId = useChatStore((s) => s.noteBubbleOpenPageId);
  const toggleNoteBubble = useChatStore((s) => s.toggleNoteBubble);
  const setNoteBubbleOpenPageId = useChatStore((s) => s.setNoteBubbleOpenPageId);
  const setNoteSelectionQuote = useChatStore((s) => s.setNoteSelectionQuote);
  const removeNoteSelectionExcerpt = useChatStore((s) => s.removeNoteSelectionExcerpt);
  const addNotePageRef = useChatStore((s) => s.addNotePageRef);
  const removeNotePageRef = useChatStore((s) => s.removeNotePageRef);
  const setNotePageRefs = useChatStore((s) => s.setNotePageRefs);
  const setNoteBubbleActiveSession = useChatStore((s) => s.setNoteBubbleActiveSession);
  const bubbleSessionId = useChatStore((s) => s.noteBubbleActiveSessionByPage[pageId] ?? "");
  const quotePageId = useChatStore((s) => s.noteSelectionQuote?.pageId ?? null);
  const quoteExcerpts = useChatStore((s) => s.noteSelectionQuote?.excerpts);
  const quoteAskedAt = useChatStore((s) => s.noteSelectionQuote?.askedAt ?? 0);
  const excerptsForPage = quotePageId === pageId ? (quoteExcerpts ?? EMPTY_SELECTION_EXCERPTS) : EMPTY_SELECTION_EXCERPTS;
  const pageRefBubbleId = useChatStore((s) => s.notePageRefs?.bubblePageId ?? null);
  const pageRefs = useChatStore((s) => s.notePageRefs?.refs);
  const refsForPage = pageRefBubbleId === pageId ? (pageRefs ?? EMPTY_PAGE_REFS) : EMPTY_PAGE_REFS;
  const { data: notesList } = useQuery(noteListOptions(wsId));
  const { data: activePeriodBrief } = useQuery(notePeriodBriefActiveOptions(wsId, pageId));
  const { data: periodBriefPlanResponse } = useQuery({
    ...notePeriodBriefPlanOptions(wsId, bubbleSessionId),
    enabled: Boolean(wsId && bubbleSessionId),
  });
  const timezone = useViewingTimezone();
  const currentPlan = periodBriefPlanResponse?.plan ?? null;
  const { data: sessions = [] } = useQuery(chatSessionsOptions(wsId));
  const { data: pending } = useQuery(pendingChatTasksOptions(wsId));
  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  const { data: runtimes = [], isLoading: runtimesLoading } = useQuery(runtimeListOptions(wsId));
  const { data: members = [] } = useQuery(memberListOptions(wsId));
  const { data: notesTemplate } = useQuery(
    agentTemplateDetailOptions(NOTES_ASSISTANT_AGENT_TEMPLATE_SLUG),
  );
  const prefersReducedMotion = usePrefersReducedMotion();

  const isOpen = openPageId === pageId;

  React.useEffect(() => {
    return () => {
      const open = useChatStore.getState().noteBubbleOpenPageId;
      if (noteAssistantSidebarClosesOnLeave(open, pageId)) {
        setNoteBubbleOpenPageId(null);
      }
    };
  }, [pageId, setNoteBubbleOpenPageId]);
  const pageSessions = excludeChannelShellSessions(
    sessions.filter((s) => s.context_note_page_id === pageId),
  );
  const unreadSessionCount = pageSessions.filter((s) => s.has_unread).length;
  const chatTaskRunning = (pending?.tasks ?? []).some((task) =>
    pageSessions.some((s) => s.id === task.chat_session_id),
  );

  const listedAssistant = resolveNotesAssistantAgent(agents);
  const [ensureResult, setEnsureResult] = React.useState<EnsureNotesAssistantAgentResponse | null>(
    null,
  );
  const [sessionDismissed, setSessionDismissed] = React.useState(false);
  const [createDialogOpen, setCreateDialogOpen] = React.useState(false);
  const [collectorConfigSlot, setCollectorConfigSlot] =
    React.useState<PeriodBriefCollectorSlot | null>(null);
  const [composerFocusToken, setComposerFocusToken] = React.useState(0);
  const [pendingSend, setPendingSend] = React.useState<{ nonce: number; text: string } | null>(null);
  const seedNonceRef = React.useRef(0);
  const periodBriefCancelRequestedRef = React.useRef(false);
  const [periodBriefSubmitting, setPeriodBriefSubmitting] = React.useState(false);
  const [highlightsOpen, setHighlightsOpen] = React.useState(false);
  const hadPlanRef = React.useRef(false);
  const composerLocked =
    periodBriefRunLocksComposer(activePeriodBrief?.run?.status) || periodBriefSubmitting;
  const showIntentConfirm =
    isPeriodBriefAwaitingIntent(currentPlan) && !composerLocked && !highlightsOpen;
  const showPlanCard =
    isPeriodBriefClarifyingPlan(currentPlan) && !composerLocked && !highlightsOpen;
  const isRunning = chatTaskRunning || composerLocked;

  React.useEffect(() => {
    if (!currentPlan) {
      hadPlanRef.current = false;
      return;
    }
    if (hadPlanRef.current) return;
    hadPlanRef.current = true;
    if (!isOpen) toggleNoteBubble(pageId);
  }, [currentPlan, isOpen, pageId, toggleNoteBubble]);

  const seedIntentReply = React.useCallback((text: string) => {
    seedNonceRef.current += 1;
    setPendingSend({
      nonce: seedNonceRef.current,
      text,
    });
  }, []);

  React.useEffect(() => {
    if (excerptsForPage.length === 0 || !quoteAskedAt) return;
    setComposerFocusToken((n) => n + 1);
  }, [excerptsForPage.length, quoteAskedAt]);

  React.useEffect(() => {
    if (refsForPage.length === 0) return;
    setComposerFocusToken((n) => n + 1);
  }, [refsForPage.length]);

  const wrapOutgoing = React.useCallback((content: string) => {
    const quote = useChatStore.getState().noteSelectionQuote;
    const withQuote =
      quote && quote.pageId === pageId && quote.excerpts.length > 0
        ? attachNoteSelectionQuote(content, quote.excerpts.map((excerpt) => excerpt.text))
        : content;
    const refs = useChatStore.getState().notePageRefs;
    if (!refs || refs.bubblePageId !== pageId || refs.refs.length === 0) {
      return withQuote;
    }
    return attachNotePageRefs(withQuote, refs.refs);
  }, [pageId]);

  const clearComposerAttachments = React.useCallback(() => {
    const quote = useChatStore.getState().noteSelectionQuote;
    if (quote?.pageId === pageId) setNoteSelectionQuote(null);
    const refs = useChatStore.getState().notePageRefs;
    if (refs?.bubblePageId === pageId) setNotePageRefs(null);
  }, [pageId, setNoteSelectionQuote, setNotePageRefs]);

  const removeSelectionExcerpt = React.useCallback((excerptId: string) => {
    const quote = useChatStore.getState().noteSelectionQuote;
    if (quote?.pageId === pageId) removeNoteSelectionExcerpt(excerptId);
  }, [pageId, removeNoteSelectionExcerpt]);

  const handleNotePageDrop = React.useCallback(
    (ref: NotePageRef) => {
      const listed = notesList?.pages?.find((page) => page.id === ref.pageId);
      const title = listed?.title?.trim() || ref.title.trim() || "Untitled";
      addNotePageRef(pageId, { pageId: ref.pageId, title });
    },
    [addNotePageRef, notesList?.pages, pageId],
  );

  const handleRemovePageRef = React.useCallback(
    (refPageId: string) => {
      const refs = useChatStore.getState().notePageRefs;
      if (refs?.bubblePageId === pageId) removeNotePageRef(refPageId);
    },
    [pageId, removeNotePageRef],
  );

  // Prefer the live agent list. After create/restore, ensureResult.agent bridges
  // until the list invalidation lands. needs_setup clears that bridge.
  const assistant: Agent | null = listedAssistant
    ?? (ensureResult?.needs_setup ? null : (ensureResult?.agent ?? null));
  const needsSetup = !assistant;
  const onboardingAvailable = Boolean(ensureResult?.onboarding_available);
  // When 笔记助手 is missing, always show the create card (ignore prior dismiss).
  const hintDismissed =
    Boolean(assistant) && (sessionDismissed || readSetupHintDismissed(wsId));
  const settingsHref = assistant
    ? workspacePaths.members({ kind: "agent", id: assistant.id })
    : workspacePaths.members();

  const applyEnsureResult = React.useCallback(
    (result: EnsureNotesAssistantAgentResponse, opts?: { toastReady?: boolean }) => {
      setEnsureResult(result);
      if (result.needs_setup) {
        clearSetupHintDismissed(wsId);
        setSessionDismissed(false);
      }
      if (result.agent) {
        queryClient.setQueryData<Agent[]>(workspaceKeys.agents(wsId), (current = []) => {
          if (current.some((a) => a.id === result.agent!.id)) {
            return current.map((a) => (a.id === result.agent!.id ? result.agent! : a));
          }
          return [...current, result.agent!];
        });
        if (opts?.toastReady) {
          toast.success(t(($) => $.notes_page.assistant_setup_ready_toast));
          setComposerFocusToken((n) => n + 1);
        }
      }
    },
    [queryClient, t, wsId],
  );

  const { mutate: ensureAssistant, isPending: ensuring } = useMutation({
    mutationFn: (input?: {
      clone_onboarding?: boolean;
      runtime_id?: string;
      model?: string;
    }) => api.ensureNotesAssistantAgent(input),
    onSuccess: (result, variables) => {
      const createdByClick = Boolean(
        variables?.clone_onboarding || (variables?.runtime_id && variables?.model),
      );
      applyEnsureResult(result, { toastReady: createdByClick && Boolean(result.agent) });
      void queryClient.invalidateQueries({ queryKey: workspaceKeys.agents(wsId) });
    },
    onError: (err) => {
      logger.warn("noteBubble.ensure.failed", { error: String(err) });
      // Keep chatting if the agent list already has 笔记助手 (e.g. non-admin soft probe 403).
      if (listedAssistant) return;
      // Soft probe failures still surface the create card; create clicks toast.
      clearSetupHintDismissed(wsId);
      setSessionDismissed(false);
      setEnsureResult({
        created: false,
        needs_setup: true,
        onboarding_available: false,
        setup_hint: true,
      });
    },
  });

  // Soft-ensure: resolve existing 笔记助手 or report needs_setup — never create.
  React.useEffect(() => {
    if (!isOpen || !wsId) return;
    // react-doctor-disable-next-line react-doctor/no-adjust-state-on-prop-change -- open→ensure is intentional product timing, not prop→state mirroring
    ensureAssistant({});
  }, [isOpen, wsId, ensureAssistant]);

  const handleDismissHint = () => {
    if (wsId && typeof window !== "undefined") {
      window.localStorage.setItem(notesAssistantSetupDismissKey(wsId), "1");
    }
    setSessionDismissed(true);
  };

  const handleManualCreate = async (data: CreateAgentRequest): Promise<Agent> => {
    const model = data.model?.trim();
    if (!data.runtime_id || !model) {
      throw new Error(t(($) => $.notes_page.assistant_setup_ensure_failed));
    }
    const result = await api.ensureNotesAssistantAgent({
      runtime_id: data.runtime_id,
      model,
    });
    applyEnsureResult(result, { toastReady: Boolean(result.agent) });
    void queryClient.invalidateQueries({ queryKey: workspaceKeys.agents(wsId) });
    if (!result.agent) {
      throw new Error(t(($) => $.notes_page.assistant_setup_ensure_failed));
    }
    setCreateDialogOpen(false);
    return result.agent;
  };

  const handleCollectorCreate = async (data: CreateAgentRequest): Promise<Agent> => {
    const model = data.model?.trim();
    if (!data.runtime_id || !model) {
      throw new Error(t(($) => $.notes_page.assistant_setup_ensure_failed));
    }
    const result = await api.ensurePeriodBriefCollectors({
      runtime_id: data.runtime_id,
      model,
    });
    const created = result.agents[0];
    if (!created) {
      throw new Error(t(($) => $.notes_page.assistant_setup_ensure_failed));
    }
    queryClient.setQueryData<Agent[]>(workspaceKeys.agents(wsId), (current = []) => {
      const byId = new Map(current.map((agent) => [agent.id, agent]));
      for (const agent of result.agents) {
        byId.set(agent.id, agent);
      }
      return [...byId.values()];
    });
    void queryClient.invalidateQueries({ queryKey: workspaceKeys.agents(wsId) });
    setCollectorConfigSlot(null);
    return created;
  };

  React.useEffect(() => {
    if (openPageId !== pageId) {
      setPendingSend(null);
      setPeriodBriefSubmitting(false);
      setHighlightsOpen(false);
    }
  }, [openPageId, pageId]);

  const handleSeedSendConsumed = React.useCallback(() => {
    setPendingSend(null);
  }, []);

  const handlePeriodBriefPlanChange = React.useCallback((next: NotePeriodBriefPlan) => {
    if (!bubbleSessionId) return;
    void api.putNotePeriodBriefPlan({
      chat_session_id: bubbleSessionId,
      context_note_page_id: pageId,
      window: next.window,
      date: next.date,
      start_date: next.start_date,
      end_date: next.end_date,
      collector_agent_ids: next.collector_agent_ids,
      focus: next.focus,
    }).then(
      (saved) => {
        queryClient.setQueryData(noteKeys.periodBriefPlan(wsId, bubbleSessionId), saved);
      },
      (err: unknown) => {
        showErrorToast(
          err instanceof Error && err.message
            ? err.message
            : t(($) => $.notes_page.period_brief_failed),
        );
        void queryClient.invalidateQueries({ queryKey: noteKeys.periodBriefPlan(wsId, bubbleSessionId) });
      },
    );
  }, [bubbleSessionId, pageId, queryClient, t, wsId]);

  const handleStartPeriodBrief = React.useCallback(() => {
    const agentId = resolvePeriodBriefSynthesizerId(agents);
    if (!agentId) {
      showErrorToast(t(($) => $.notes_page.period_brief_agent_required));
      return;
    }
    const sessionId = useChatStore.getState().noteBubbleActiveSessionByPage[pageId] ?? "";
    if (!sessionId) return;
    periodBriefCancelRequestedRef.current = false;
    setPendingSend(null);
    setPeriodBriefSubmitting(true);
    void api.createNotePeriodBrief({
      agent_id: agentId,
      timezone,
      context_note_page_id: pageId,
      chat_session_id: sessionId,
      from_chat: true,
    }).then(
      (result) => {
        if (!result.job?.id) {
          throw new Error(t(($) => $.notes_page.period_brief_failed));
        }
        if (result.chat_session_id) {
          setNoteBubbleActiveSession(pageId, result.chat_session_id);
          void queryClient.invalidateQueries({ queryKey: chatKeys.messages(result.chat_session_id) });
          void queryClient.invalidateQueries({ queryKey: chatKeys.messagesPage(result.chat_session_id) });
        }
        queryClient.setQueryData(noteKeys.periodBriefPlan(wsId, sessionId), { plan: null });
        void queryClient.invalidateQueries({ queryKey: chatKeys.sessions(wsId) });
        void queryClient.invalidateQueries({ queryKey: noteListOptions(wsId).queryKey });
        void queryClient.invalidateQueries({
          queryKey: notePeriodBriefActiveOptions(wsId, pageId).queryKey,
        });
        if (periodBriefCancelRequestedRef.current) {
          void api.deleteNotePeriodBriefPlan(result.chat_session_id || sessionId).then(() => {
            void queryClient.invalidateQueries({
              queryKey: notePeriodBriefActiveOptions(wsId, pageId).queryKey,
            });
          });
        }
      },
      (error: unknown) => {
        showErrorToast(
          error instanceof Error && error.message
            ? error.message
            : t(($) => $.notes_page.period_brief_failed),
        );
      },
    ).finally(() => {
      setPeriodBriefSubmitting(false);
    });
  }, [agents, pageId, queryClient, setNoteBubbleActiveSession, t, timezone, wsId]);

  const handleCancelPeriodBrief = React.useCallback(() => {
    if (!bubbleSessionId) return;
    periodBriefCancelRequestedRef.current = true;
    setPeriodBriefSubmitting(false);
    queryClient.setQueryData(noteKeys.periodBriefPlan(wsId, bubbleSessionId), { plan: null });
    queryClient.setQueryData(noteKeys.periodBriefActive(wsId, pageId), { run: null });
    void api.deleteNotePeriodBriefPlan(bubbleSessionId).then(
      () => {
        void queryClient.invalidateQueries({ queryKey: noteKeys.periodBriefPlan(wsId, bubbleSessionId) });
        void queryClient.invalidateQueries({ queryKey: noteKeys.periodBriefActive(wsId, pageId) });
        void queryClient.invalidateQueries({ queryKey: chatKeys.messages(bubbleSessionId) });
        void queryClient.invalidateQueries({ queryKey: chatKeys.messagesPage(bubbleSessionId) });
      },
      (err: unknown) => {
        void queryClient.invalidateQueries({ queryKey: noteKeys.periodBriefPlan(wsId, bubbleSessionId) });
        void queryClient.invalidateQueries({ queryKey: noteKeys.periodBriefActive(wsId, pageId) });
        showErrorToast(
          err instanceof Error && err.message
            ? err.message
            : t(($) => $.notes_page.period_brief_stop_failed),
        );
      },
    );
  }, [bubbleSessionId, pageId, queryClient, t, wsId]);

  const handleStopPeriodBrief = React.useCallback(() => {
    const run = activePeriodBrief?.run;
    if (!run?.id || !periodBriefRunLocksComposer(run.status)) return;
    queryClient.setQueryData(noteKeys.periodBriefActive(wsId, pageId), { run: null });
    void api.cancelNotePeriodBrief(run.id).then(
      () => {
        void queryClient.invalidateQueries({ queryKey: noteKeys.periodBriefActive(wsId, pageId) });
      },
      (err: unknown) => {
        void queryClient.invalidateQueries({ queryKey: noteKeys.periodBriefActive(wsId, pageId) });
        showErrorToast(
          err instanceof Error && err.message
            ? err.message
            : t(($) => $.notes_page.period_brief_stop_failed),
        );
      },
    );
  }, [activePeriodBrief?.run, pageId, queryClient, t, wsId]);

  const handleHighlightsSend = React.useCallback((text: string) => {
    seedNonceRef.current += 1;
    setPendingSend({
      nonce: seedNonceRef.current,
      text,
    });
    setHighlightsOpen(false);
  }, []);

  const interceptComposerSend = React.useCallback(() => {
    return highlightsOpen;
  }, [highlightsOpen]);

  const handleFabAction = (action: NoteAssistantFabAction) => {
    logger.info("noteBubble.fab.action", { pageId, action, isOpen });
    if (action === "period_brief") {
      if (!isOpen) toggleNoteBubble(pageId);
      setHighlightsOpen(false);
      if (composerLocked) return;
      if (showPlanCard || showIntentConfirm) return;
      seedNonceRef.current += 1;
      setPendingSend({
        nonce: seedNonceRef.current,
        text: PERIOD_BRIEF_SATELLITE_ASK,
      });
      return;
    }
    if (action === "highlights") {
      if (!isOpen) toggleNoteBubble(pageId);
      if (!composerLocked) {
        setHighlightsOpen(true);
      }
      return;
    }
    toggleNoteBubble(pageId);
  };

  const titleHint = pageTitle?.trim() || t(($) => $.notes_page.assistant_bubble_untitled);
  const tooltip = isRunning
    ? t(($) => $.notes_page.assistant_bubble_running)
    : unreadSessionCount > 0
      ? t(($) => $.notes_page.assistant_bubble_replied, { title: titleHint })
      : t(($) => $.notes_page.assistant_bubble_default, { title: titleHint });

  const showSetupHint =
    isOpen && (needsSetup || (!hintDismissed && Boolean(assistant)));

  const setupSlot =
    showSetupHint ? (
      <NotesAssistantSetupCard
        needsSetup={needsSetup}
        onboardingAvailable={onboardingAvailable}
        ensuring={ensuring}
        settingsHref={settingsHref}
        onCloneOnboarding={() => ensureAssistant({ clone_onboarding: true })}
        onOpenManualCreate={() => setCreateDialogOpen(true)}
        onDismiss={assistant && !needsSetup ? handleDismissHint : undefined}
      />
    ) : null;

  return (
    <>
      <ChatWindow
        contextNotePageId={pageId}
        preferredAgentId={assistant?.id ?? null}
        lockPreferredAgent
        layout={layout}
        composerFocusToken={composerFocusToken}
        seedSend={highlightsOpen ? null : pendingSend}
        onSeedSendConsumed={handleSeedSendConsumed}
        transformOutgoing={wrapOutgoing}
        onSendAccepted={clearComposerAttachments}
        onNotePageDrop={handleNotePageDrop}
        composerPrefix={
          excerptsForPage.length > 0 || refsForPage.length > 0 ? (
            <>
              {refsForPage.length > 0 ? (
                <NotePageRefPreview refs={refsForPage} onRemove={handleRemovePageRef} />
              ) : null}
              {excerptsForPage.length > 0 ? (
                <NoteSelectionQuotePreview
                  excerpts={excerptsForPage.map((excerpt) => ({
                    id: excerpt.id,
                    summary: abbreviateNoteSelection(excerpt.text),
                  }))}
                  onRemove={removeSelectionExcerpt}
                />
              ) : null}
            </>
          ) : null
        }
        transcriptAccessory={
          showIntentConfirm ? (
            <NotePeriodBriefIntentConfirm
              onConfirm={() => seedIntentReply(PERIOD_BRIEF_INTENT_YES)}
              onDecline={() => seedIntentReply(PERIOD_BRIEF_INTENT_NO)}
            />
          ) : showPlanCard && currentPlan ? (
            <NotePeriodBriefCompose
              active
              plan={currentPlan}
              submitting={periodBriefSubmitting}
              onPlanChange={handlePeriodBriefPlanChange}
              onStart={handleStartPeriodBrief}
              onCancel={handleCancelPeriodBrief}
              onConfigureCollector={setCollectorConfigSlot}
            />
          ) : null
        }
        composerAccessory={
          setupSlot || (highlightsOpen && !composerLocked) ? (
            <>
              {setupSlot}
              {highlightsOpen && !composerLocked ? (
                <NoteHighlightsCompose
                  initialText={t(($) => $.notes_page.assistant_highlights_prompt)}
                  onSend={handleHighlightsSend}
                  onCancel={() => setHighlightsOpen(false)}
                />
              ) : null}
            </>
          ) : null
        }
        composerPlaceholder={
          showPlanCard
            ? t(($) => $.notes_page.period_brief_focus_placeholder)
            : excerptsForPage.length > 0
              ? t(($) => $.notes_page.assistant_selection_quote_placeholder)
              : undefined
        }
        onSendIntercept={interceptComposerSend}
        composerLocked={composerLocked}
        onLockedComposerStop={handleStopPeriodBrief}
      />
      {createDialogOpen ? (
        <CreateAgentDialog
          runtimes={runtimes}
          runtimesLoading={runtimesLoading}
          members={members}
          currentUserId={currentUser?.id ?? null}
          prefill={{
            name: NOTES_ASSISTANT_AGENT_NAME,
            description: notesTemplate?.description ?? "",
            instructions: notesTemplate?.instructions ?? "",
            lockIdentity: true,
          }}
          onClose={() => setCreateDialogOpen(false)}
          onCreate={handleManualCreate}
        />
      ) : null}
      {collectorConfigSlot ? (
        <CreateAgentDialog
          runtimes={runtimes}
          runtimesLoading={runtimesLoading}
          members={members}
          currentUserId={currentUser?.id ?? null}
          defaultMachineId={collectorConfigSlot.machineId}
          lockComputer
          labels={{
            title: t(($) => $.notes_page.period_brief_collector_setup_title, {
              label: collectorConfigSlot.label,
            }),
            description: t(($) => $.notes_page.period_brief_collector_setup_body),
            submit: t(($) => $.notes_page.period_brief_collector_setup_save),
            submitting: t(($) => $.notes_page.period_brief_collector_setup_saving),
          }}
          prefill={{
            name: collectorConfigSlot.expectedName,
            runtime_id: collectorConfigSlot.collector?.runtime_id ?? undefined,
            model:
              collectorConfigSlot.collector?.model
              ?? agents.find((agent) => agent.id === collectorConfigSlot.collector?.id)?.model
              ?? undefined,
            lockIdentity: true,
          }}
          onClose={() => setCollectorConfigSlot(null)}
          onCreate={handleCollectorCreate}
        />
      ) : null}
      {!isOpen && (
        <NoteAssistantFabCluster
          tooltip={tooltip}
          isRunning={isRunning}
          unreadCount={unreadSessionCount}
          reducedMotion={prefersReducedMotion}
          onAction={handleFabAction}
        />
      )}
    </>
  );
}
