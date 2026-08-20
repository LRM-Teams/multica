"use client";

import * as React from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { api } from "@multica/core/api";
import { agentTemplateDetailOptions } from "@multica/core/agents/queries";
import { useAuthStore } from "@multica/core/auth";
import { useChatStore } from "@multica/core/chat";
import { chatSessionsOptions, pendingChatTasksOptions } from "@multica/core/chat/queries";
import { useWorkspaceId } from "@multica/core/hooks";
import { createLogger } from "@multica/core/logger";
import {
  NOTES_ASSISTANT_AGENT_NAME,
  NOTES_ASSISTANT_AGENT_TEMPLATE_SLUG,
  notesAssistantSetupDismissKey,
  resolveNotesAssistantAgent,
} from "@multica/core/notes/notes-assistant-agent";
import { useWorkspacePaths } from "@multica/core/paths";
import { runtimeListOptions } from "@multica/core/runtimes";
import { agentListOptions, memberListOptions, workspaceKeys } from "@multica/core/workspace/queries";
import type { Agent, CreateAgentRequest, EnsureNotesAssistantAgentResponse } from "@multica/core/types";
import { useIsMobile } from "@multica/ui/hooks/use-mobile";
import { CreateAgentDialog } from "../agents/components/create-agent-dialog";
import { useT } from "../i18n";
import { usePrefersReducedMotion } from "../common/use-prefers-reduced-motion";
import { excludeChannelShellSessions } from "../chat/lib/exclude-channel-shell-sessions";
import { ChatWindow } from "../chat/components/chat-window";
import { NoteAssistantFabCluster, type NoteAssistantFabAction } from "./note-assistant-fab-cluster";
import { NotesAssistantSetupCard } from "./notes-assistant-setup-card";

const logger = createLogger("chat.note-bubble");

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
  onOpenPeriodBrief,
  onOpenWorker,
}: {
  pageId: string;
  pageTitle?: string;
  onOpenPeriodBrief: () => void;
  onOpenWorker: () => void;
}) {
  const { t } = useT("layout");
  const wsId = useWorkspaceId();
  const workspacePaths = useWorkspacePaths();
  const queryClient = useQueryClient();
  const currentUser = useAuthStore((s) => s.user);
  const isMobile = useIsMobile();
  const layout = isMobile ? "fullscreen" : "floating";
  const openPageId = useChatStore((s) => s.noteBubbleOpenPageId);
  const toggleNoteBubble = useChatStore((s) => s.toggleNoteBubble);
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
  const pageSessions = excludeChannelShellSessions(
    sessions.filter((s) => s.context_note_page_id === pageId),
  );
  const unreadSessionCount = pageSessions.filter((s) => s.has_unread).length;
  const isRunning = (pending?.tasks ?? []).some((task) =>
    pageSessions.some((s) => s.id === task.chat_session_id),
  );

  const listedAssistant = resolveNotesAssistantAgent(agents);
  const [ensureResult, setEnsureResult] = React.useState<EnsureNotesAssistantAgentResponse | null>(
    null,
  );
  const [sessionDismissed, setSessionDismissed] = React.useState(false);
  const [createDialogOpen, setCreateDialogOpen] = React.useState(false);
  const [composerFocusToken, setComposerFocusToken] = React.useState(0);
  const [pendingSend, setPendingSend] = React.useState<{ nonce: number; text: string } | null>(null);
  const seedNonceRef = React.useRef(0);

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

  React.useEffect(() => {
    if (openPageId !== pageId) setPendingSend(null);
  }, [openPageId, pageId]);

  const handleSeedSendConsumed = React.useCallback(() => {
    setPendingSend(null);
  }, []);

  const handleFabAction = (action: NoteAssistantFabAction) => {
    logger.info("noteBubble.fab.action", { pageId, action, isOpen });
    if (action === "period_brief") {
      onOpenPeriodBrief();
      return;
    }
    if (action === "worker") {
      onOpenWorker();
      return;
    }
    if (action === "highlights") {
      seedNonceRef.current += 1;
      setPendingSend({
        nonce: seedNonceRef.current,
        text: t(($) => $.notes_page.assistant_highlights_prompt),
      });
      if (!isOpen) toggleNoteBubble(pageId);
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
        headerAccessory={setupSlot}
        composerFocusToken={composerFocusToken}
        seedSend={pendingSend}
        onSeedSendConsumed={handleSeedSendConsumed}
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
