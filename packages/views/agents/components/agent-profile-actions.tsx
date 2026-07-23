"use client";

import { useState } from "react";
import {
  AlertCircle,
  Bug,
  ClipboardList,
  Loader2,
  MessageSquare,
  RotateCcw,
  Trash2,
} from "lucide-react";
import { useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import type { Agent, AgentRuntime, MemberWithUser } from "@multica/core/types";
import { api } from "@multica/core/api";
import { useModalStore } from "@multica/core/modals";
import { useIssueDraftStore } from "@multica/core/issues/stores/draft-store";
import { workspaceKeys } from "@multica/core/workspace/queries";
import {
  formatActorHandleLabel,
  resolveActorDisplayName,
  resolveActorHandle,
} from "@multica/core/identity";
import { copyText } from "@multica/ui/lib/clipboard";
import { cn } from "@multica/ui/lib/utils";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@multica/ui/components/ui/alert-dialog";
import { useOpenDM } from "../../common/use-open-dm";
import { useT } from "../../i18n/use-t";

/**
 * LRM-448 · Profile v4 locked Actions stack (Computer IA + Multica tokens).
 * Vertical named actions — no header Message+⋯, no More overflow.
 * Tone: default / warn / danger via border+tint (not neo-brutal solids).
 */
export function AgentProfileActions({
  agent,
  runtime,
  members,
  canManage,
}: {
  agent: Agent;
  runtime: AgentRuntime | null;
  members: readonly MemberWithUser[];
  canManage: boolean;
}) {
  const { t } = useT("agents");
  const qc = useQueryClient();
  const { openDM, isPending: openingDM } = useOpenDM();
  const [confirmReset, setConfirmReset] = useState(false);
  const [confirmArchive, setConfirmArchive] = useState(false);
  const [resetting, setResetting] = useState(false);
  const [archiving, setArchiving] = useState(false);

  const isArchived = !!agent.archived_at;
  const displayName = resolveActorDisplayName(agent, agent.id);

  const invalidateAgents = () => {
    qc.invalidateQueries({ queryKey: workspaceKeys.agents(agent.workspace_id) });
  };

  const buildDiagnosticText = () => {
    const handle = formatActorHandleLabel(resolveActorHandle(agent)) || `@${agent.name}`;
    const owner =
      members.find((m) => m.user_id === agent.owner_id)?.display_name ||
      members.find((m) => m.user_id === agent.owner_id)?.name ||
      agent.owner_id ||
      "—";
    const lines = [
      `Agent: ${displayName} (${handle})`,
      `ID: ${agent.id}`,
      `Workspace: ${agent.workspace_id}`,
      `Owner: ${owner}`,
      `Runtime: ${runtime?.name ?? agent.runtime_id ?? "—"}`,
      `Model: ${agent.model || "—"}`,
      `Thinking: ${agent.thinking_level || "—"}`,
      `Visibility: ${agent.visibility}`,
      `Status: ${agent.status}`,
      `Archived: ${agent.archived_at ? "yes" : "no"}`,
    ];
    return lines.join("\n");
  };

  const handleCopyDiagnostic = async () => {
    const ok = await copyText(buildDiagnosticText());
    if (ok) {
      toast.success(t(($) => $.side_panel.actions_copy_success));
    } else {
      toast.error(t(($) => $.side_panel.actions_copy_failed));
    }
  };

  const handleReport = () => {
    const diagnostic = buildDiagnosticText();
    useIssueDraftStore.getState().setDraft({
      title: t(($) => $.side_panel.actions_report_title, { name: displayName }),
      description: `${t(($) => $.side_panel.actions_report_body_intro)}\n\n\`\`\`\n${diagnostic}\n\`\`\``,
    });
    useModalStore.getState().open("create-issue");
  };

  const handleReset = async () => {
    setResetting(true);
    try {
      const { cancelled } = await api.cancelAgentTasks(agent.id);
      invalidateAgents();
      toast.success(
        cancelled === 0
          ? t(($) => $.row_actions.no_tasks_to_cancel_toast)
          : t(($) => $.row_actions.cancelled_tasks_toast, { count: cancelled }),
      );
    } catch (e) {
      toast.error(
        e instanceof Error ? e.message : t(($) => $.row_actions.cancel_failed_toast),
      );
    } finally {
      setResetting(false);
    }
  };

  const handleArchive = async () => {
    setArchiving(true);
    try {
      await api.archiveAgent(agent.id);
      invalidateAgents();
      toast.success(t(($) => $.row_actions.agent_archived_toast));
    } catch (e) {
      toast.error(
        e instanceof Error ? e.message : t(($) => $.row_actions.archive_failed_toast),
      );
    } finally {
      setArchiving(false);
    }
  };

  return (
    <section aria-label={t(($) => $.side_panel.actions_section)} data-testid="agent-profile-actions">
      <h3 className="mb-2 text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">
        {t(($) => $.side_panel.actions_section)}
      </h3>
      <div className="flex flex-col gap-2">
        {!isArchived ? (
          <ActionButton
            variant="primary"
            testId="agent-profile-action-message"
            disabled={openingDM}
            onClick={() => void openDM({ peer_type: "agent", peer_id: agent.id })}
          >
            {openingDM ? (
              <Loader2 className="size-4 shrink-0 animate-spin" aria-hidden />
            ) : (
              <MessageSquare className="size-4 shrink-0" aria-hidden />
            )}
            {openingDM
              ? t(($) => $.side_panel.message_opening)
              : t(($) => $.side_panel.message_button)}
          </ActionButton>
        ) : null}

        {canManage && !isArchived ? (
          <ActionButton
            testId="agent-profile-action-reset"
            disabled={resetting}
            onClick={() => setConfirmReset(true)}
          >
            <RotateCcw className="size-4 shrink-0" aria-hidden />
            {t(($) => $.side_panel.actions_restart)}
          </ActionButton>
        ) : null}

        <ActionButton
          testId="agent-profile-action-copy"
          onClick={() => void handleCopyDiagnostic()}
        >
          <ClipboardList className="size-4 shrink-0" aria-hidden />
          {t(($) => $.side_panel.actions_copy_diagnostic)}
        </ActionButton>

        <ActionButton
          variant="warn"
          testId="agent-profile-action-report"
          onClick={handleReport}
        >
          <Bug className="size-4 shrink-0" aria-hidden />
          {t(($) => $.side_panel.actions_report)}
        </ActionButton>

        {canManage && !isArchived ? (
          <div className="mt-1 border-t border-border pt-3">
            <ActionButton
              variant="danger"
              testId="agent-profile-action-archive"
              disabled={archiving}
              onClick={() => setConfirmArchive(true)}
            >
              <Trash2 className="size-4 shrink-0" aria-hidden />
              {t(($) => $.side_panel.actions_archive)}
            </ActionButton>
          </div>
        ) : null}
      </div>

      {confirmReset ? (
        <AlertDialog open onOpenChange={(v) => { if (!v) setConfirmReset(false); }}>
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle>
                {t(($) => $.side_panel.actions_restart_dialog_title, { name: displayName })}
              </AlertDialogTitle>
              <AlertDialogDescription>
                {t(($) => $.side_panel.actions_restart_dialog_description)}
              </AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel>
                {t(($) => $.row_actions.cancel_dialog_keep)}
              </AlertDialogCancel>
              <AlertDialogAction
                onClick={() => {
                  setConfirmReset(false);
                  void handleReset();
                }}
              >
                {t(($) => $.side_panel.actions_restart)}
              </AlertDialogAction>
            </AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>
      ) : null}

      {confirmArchive ? (
        <AlertDialog open onOpenChange={(v) => { if (!v) setConfirmArchive(false); }}>
          <AlertDialogContent>
            <AlertDialogHeader>
              <div className="flex items-start gap-3">
                <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-full bg-destructive/10">
                  <AlertCircle className="h-5 w-5 text-destructive" />
                </div>
                <div className="flex-1">
                  <AlertDialogTitle>
                    {t(($) => $.detail.archive_dialog_title)}
                  </AlertDialogTitle>
                  <AlertDialogDescription>
                    {t(($) => $.detail.archive_dialog_description, { name: displayName })}
                  </AlertDialogDescription>
                </div>
              </div>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel>
                {t(($) => $.detail.archive_dialog_cancel)}
              </AlertDialogCancel>
              <AlertDialogAction
                variant="destructive"
                onClick={() => {
                  setConfirmArchive(false);
                  void handleArchive();
                }}
              >
                {t(($) => $.detail.archive_dialog_confirm)}
              </AlertDialogAction>
            </AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>
      ) : null}
    </section>
  );
}

function ActionButton({
  children,
  onClick,
  disabled,
  variant = "default",
  testId,
}: {
  children: React.ReactNode;
  onClick: () => void;
  disabled?: boolean;
  variant?: "default" | "primary" | "warn" | "danger";
  testId: string;
}) {
  return (
    <button
      type="button"
      data-testid={testId}
      disabled={disabled}
      onClick={onClick}
      className={cn(
        "inline-flex h-10 w-full items-center justify-center gap-2 rounded-lg border text-[13px] font-semibold transition-colors disabled:cursor-wait disabled:opacity-70",
        variant === "default" &&
          "border-border bg-background text-foreground hover:bg-muted/60",
        variant === "primary" &&
          "border-brand/40 bg-brand/10 text-brand hover:bg-brand/15",
        variant === "warn" &&
          "border-amber-600/35 bg-amber-500/10 text-amber-800 hover:bg-amber-500/15 dark:text-amber-300",
        variant === "danger" &&
          "border-destructive/40 bg-destructive/10 text-destructive hover:bg-destructive/15",
      )}
    >
      {children}
    </button>
  );
}
