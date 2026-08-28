"use client";

import * as React from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { api } from "@multica/core/api";
import { agentTemplateDetailOptions } from "@multica/core/agents/queries";
import { useAuthStore } from "@multica/core/auth";
import { useChatStore } from "@multica/core/chat";
import { chatKeys, chatSessionsOptions, pendingChatTasksOptions } from "@multica/core/chat/queries";
import { useWorkspaceId } from "@multica/core/hooks";
import { createLogger } from "@multica/core/logger";
import {
  NOTES_ASSISTANT_AGENT_NAME,
  NOTES_ASSISTANT_AGENT_TEMPLATE_SLUG,
  notesAssistantSetupDismissKey,
  resolveNotesAssistantAgent,
} from "@multica/core/notes/notes-assistant-agent";
import {
  periodBriefRunLocksComposer,
  looksLikePeriodBriefRequest,
  resolvePeriodBriefComposeRequest,
} from "@multica/core/notes/period-brief-compose";
import { type PeriodBriefCollectorSlot } from "@multica/core/notes/period-brief-collectors";
import { isValidPeriodBriefCustomRange } from "@multica/core/notes/period-brief-window";
import { noteListOptions, notePeriodBriefActiveOptions } from "@multica/core/notes/queries";
import { abbreviateNoteSelection, attachNoteSelectionQuote, type NoteSelectionExcerpt } from "@multica/core/notes/selection-quote";
import { useWorkspacePaths } from "@multica/core/paths";
import { runtimeListOptions } from "@multica/core/runtimes";
import { agentListOptions, memberListOptions, workspaceKeys } from "@multica/core/workspace/queries";
import type { Agent, CreateAgentRequest, EnsureNotesAssistantAgentResponse } from "@multica/core/types";
import { useIsMobile } from "@multica/ui/hooks/use-mobile";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { CreateAgentDialog } from "../agents/components/create-agent-dialog";
import { useT } from "../i18n";
import { usePrefersReducedMotion } from "../common/use-prefers-reduced-motion";
import { excludeChannelShellSessions } from "../chat/lib/exclude-channel-shell-sessions";
import { ChatWindow } from "../chat/components/chat-window";
import { noteAssistantSidebarClosesOnLeave } from "../chat/components/chat-window-layout";
import { NoteAssistantFabCluster, type NoteAssistantFabAction } from "./note-assistant-fab-cluster";
import { NoteHighlightsCompose } from "./note-highlights-compose";
import {
  NotePeriodBriefCompose,
  type NotePeriodBriefResolved,
} from "./note-period-brief-compose";
import { NotePeriodBriefIntentConfirm } from "./note-period-brief-intent-confirm";
import { NoteSelectionQuotePreview } from "./note-selection-quote-preview";
import { NotesAssistantSetupCard } from "./notes-assistant-setup-card";

const logger = createLogger("chat.note-bubble");
const EMPTY_SELECTION_EXCERPTS: NoteSelectionExcerpt[] = [];

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
  const setNoteBubbleActiveSession = useChatStore((s) => s.setNoteBubbleActiveSession);
  const quotePageId = useChatStore((s) => s.noteSelectionQuote?.pageId ?? null);
  const quoteExcerpts = useChatStore((s) => s.noteSelectionQuote?.excerpts);
  const quoteAskedAt = useChatStore((s) => s.noteSelectionQuote?.askedAt ?? 0);
  const excerptsForPage = quotePageId === pageId ? (quoteExcerpts ?? EMPTY_SELECTION_EXCERPTS) : EMPTY_SELECTION_EXCERPTS;
  const { data: activePeriodBrief } = useQuery(notePeriodBriefActiveOptions(wsId, pageId));
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
  const [composerResetToken, setComposerResetToken] = React.useState(0);
  const [pendingSend, setPendingSend] = React.useState<{ nonce: number; text: string } | null>(null);
  const seedNonceRef = React.useRef(0);
  const [periodBriefOpen, setPeriodBriefOpen] = React.useState(false);
  const [periodBriefSubmitting, setPeriodBriefSubmitting] = React.useState(false);
  const [periodBriefConfirmText, setPeriodBriefConfirmText] = React.useState<string | null>(null);
  const [highlightsOpen, setHighlightsOpen] = React.useState(false);
  const periodBriefResolvedRef = React.useRef<NotePeriodBriefResolved | null>(null);
  const periodBriefBypassRef = React.useRef<string | null>(null);
  const composerLocked =
    periodBriefRunLocksComposer(activePeriodBrief?.run?.status) || periodBriefSubmitting;
  const isRunning = chatTaskRunning || composerLocked;

  React.useEffect(() => {
    if (excerptsForPage.length === 0 || !quoteAskedAt) return;
    setComposerFocusToken((n) => n + 1);
  }, [excerptsForPage.length, quoteAskedAt]);

  const wrapOutgoing = React.useCallback((content: string) => {
    const quote = useChatStore.getState().noteSelectionQuote;
    if (!quote || quote.pageId !== pageId || quote.excerpts.length === 0) return content;
    return attachNoteSelectionQuote(content, quote.excerpts.map((excerpt) => excerpt.text));
  }, [pageId]);

  const clearSelectionQuote = React.useCallback(() => {
    const quote = useChatStore.getState().noteSelectionQuote;
    if (quote?.pageId === pageId) setNoteSelectionQuote(null);
  }, [pageId, setNoteSelectionQuote]);

  const removeSelectionExcerpt = React.useCallback((excerptId: string) => {
    const quote = useChatStore.getState().noteSelectionQuote;
    if (quote?.pageId === pageId) removeNoteSelectionExcerpt(excerptId);
  }, [pageId, removeNoteSelectionExcerpt]);

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
      setPeriodBriefOpen(false);
      setPeriodBriefSubmitting(false);
      setPeriodBriefConfirmText(null);
      setHighlightsOpen(false);
    }
  }, [openPageId, pageId]);

  const handleSeedSendConsumed = React.useCallback(() => {
    setPendingSend(null);
  }, []);

  const handlePeriodBriefResolved = React.useCallback((resolved: NotePeriodBriefResolved) => {
    periodBriefResolvedRef.current = resolved;
  }, []);

  const submitPeriodBrief = React.useCallback(async (text: string): Promise<boolean> => {
    const resolved = periodBriefResolvedRef.current;
    if (!resolved) return false;
    const request = resolvePeriodBriefComposeRequest(
      resolved.selection,
      resolved.collectors,
      text,
    );
    if (!resolved.agentId) {
      showErrorToast(t(($) => $.notes_page.period_brief_agent_required));
      return false;
    }
    if (request.collector_ids.length === 0) {
      showErrorToast(t(($) => $.notes_page.period_brief_collectors_required));
      return false;
    }
    if (
      request.window === "custom" &&
      !isValidPeriodBriefCustomRange(request.start_date ?? "", request.end_date ?? "")
    ) {
      showErrorToast(t(($) => $.notes_page.period_brief_custom_range_invalid));
      return false;
    }
    setPeriodBriefSubmitting(true);
    try {
      const result = await api.createNotePeriodBrief({
        window: request.window,
        date: request.date,
        start_date: request.start_date,
        end_date: request.end_date,
        timezone: resolved.timezone,
        agent_id: resolved.agentId,
        collector_agent_ids: request.collector_ids,
        context_note_page_id: pageId,
        ...(request.focus ? { focus: request.focus } : {}),
      });
      if (!result.job?.id) {
        throw new Error(t(($) => $.notes_page.period_brief_failed));
      }
      if (result.chat_session_id) {
        setNoteBubbleActiveSession(pageId, result.chat_session_id);
        void queryClient.invalidateQueries({ queryKey: chatKeys.messages(result.chat_session_id) });
        void queryClient.invalidateQueries({ queryKey: chatKeys.messagesPage(result.chat_session_id) });
      }
      setPeriodBriefOpen(false);
      void queryClient.invalidateQueries({ queryKey: chatKeys.sessions(wsId) });
      void queryClient.invalidateQueries({ queryKey: noteListOptions(wsId).queryKey });
      void queryClient.invalidateQueries({
        queryKey: notePeriodBriefActiveOptions(wsId, pageId).queryKey,
      });
      return true;
    } catch (error: unknown) {
      showErrorToast(
        error instanceof Error && error.message
          ? error.message
          : t(($) => $.notes_page.period_brief_failed),
      );
      return false;
    } finally {
      setPeriodBriefSubmitting(false);
    }
  }, [pageId, queryClient, setNoteBubbleActiveSession, t, wsId]);

  const interceptPeriodBriefCompose = React.useCallback((text: string) => {
    if (periodBriefBypassRef.current !== null) {
      const bypass = periodBriefBypassRef.current;
      periodBriefBypassRef.current = null;
      if (text === bypass) {
        setPeriodBriefConfirmText(null);
        return false;
      }
    }
    if (highlightsOpen) {
      if (looksLikePeriodBriefRequest(text)) {
        setHighlightsOpen(false);
        setPeriodBriefOpen(false);
        setPeriodBriefConfirmText(text);
        return true;
      }
      return true;
    }
    if (periodBriefOpen || composerLocked) {
      setPeriodBriefConfirmText(null);
      return false;
    }
    if (!looksLikePeriodBriefRequest(text)) {
      setPeriodBriefConfirmText(null);
      return false;
    }
    setPeriodBriefConfirmText(text);
    return true;
  }, [composerLocked, highlightsOpen, periodBriefOpen]);

  const acceptPeriodBriefIntent = React.useCallback(() => {
    setPeriodBriefConfirmText(null);
    setHighlightsOpen(false);
    setPeriodBriefOpen(true);
  }, []);

  const declinePeriodBriefIntent = React.useCallback(() => {
    const text = periodBriefConfirmText;
    setPeriodBriefConfirmText(null);
    if (!text) return;
    periodBriefBypassRef.current = text;
    seedNonceRef.current += 1;
    setComposerResetToken((n) => n + 1);
    setPendingSend({
      nonce: seedNonceRef.current,
      text,
    });
  }, [periodBriefConfirmText]);

  const handleHighlightsSend = React.useCallback((text: string) => {
    seedNonceRef.current += 1;
    setPendingSend({
      nonce: seedNonceRef.current,
      text,
    });
    setHighlightsOpen(false);
  }, []);

  const handleFabAction = (action: NoteAssistantFabAction) => {
    logger.info("noteBubble.fab.action", { pageId, action, isOpen });
    if (action === "period_brief") {
      if (!isOpen) toggleNoteBubble(pageId);
      if (!composerLocked) {
        setHighlightsOpen(false);
        setPeriodBriefConfirmText(null);
        setPeriodBriefOpen(true);
      }
      return;
    }
    if (action === "highlights") {
      if (!isOpen) toggleNoteBubble(pageId);
      if (!composerLocked) {
        setPeriodBriefOpen(false);
        setPeriodBriefConfirmText(null);
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
        composerResetToken={composerResetToken}
        seedSend={periodBriefOpen || highlightsOpen ? null : pendingSend}
        onSeedSendConsumed={handleSeedSendConsumed}
        transformOutgoing={wrapOutgoing}
        onSendAccepted={clearSelectionQuote}
        transcriptAccessory={
          periodBriefConfirmText ? (
            <NotePeriodBriefIntentConfirm
              userText={periodBriefConfirmText}
              onYes={acceptPeriodBriefIntent}
              onNo={declinePeriodBriefIntent}
            />
          ) : null
        }
        composerPrefix={
          excerptsForPage.length > 0 ? (
            <NoteSelectionQuotePreview
              excerpts={excerptsForPage.map((excerpt) => ({
                id: excerpt.id,
                summary: abbreviateNoteSelection(excerpt.text),
              }))}
              onRemove={removeSelectionExcerpt}
            />
          ) : null
        }
        composerAccessory={
          setupSlot
          || (periodBriefOpen && !composerLocked)
          || (highlightsOpen && !composerLocked)
            ? (
              <>
                {setupSlot}
                {periodBriefOpen && !composerLocked ? (
                  <NotePeriodBriefCompose
                    active={periodBriefOpen}
                    submitting={periodBriefSubmitting}
                    onResolvedChange={handlePeriodBriefResolved}
                    onCancel={() => setPeriodBriefOpen(false)}
                    onConfigureCollector={setCollectorConfigSlot}
                  />
                ) : null}
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
          periodBriefOpen && !composerLocked
            ? t(($) => $.notes_page.period_brief_focus_placeholder)
            : excerptsForPage.length > 0
              ? t(($) => $.notes_page.assistant_selection_quote_placeholder)
              : undefined
        }
        allowEmptySend={periodBriefOpen && !composerLocked}
        onSendIntercept={interceptPeriodBriefCompose}
        onSendOverride={periodBriefOpen && !composerLocked ? submitPeriodBrief : undefined}
        composerLocked={composerLocked}
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
          prefill={{
            name: collectorConfigSlot.expectedName,
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
