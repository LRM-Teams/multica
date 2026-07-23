"use client";

import { useState } from "react";
import {
  AlertCircle,
  Loader2,
  MessageSquare,
  Trash2,
} from "lucide-react";
import { useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import type { Agent } from "@multica/core/types";
import { api } from "@multica/core/api";
import { workspaceKeys } from "@multica/core/workspace/queries";
import { resolveActorDisplayName } from "@multica/core/identity";
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
 * LRM-448 · Profile v4 Actions stack (Computer IA + Multica tokens).
 * Vertical named actions — no header Message+⋯, no More overflow.
 *
 * LRM-468: Restart/Reset · Copy diagnostic · Report issue are out of
 * scope this period (Frank「这几个功能删掉，先不做」). Keep Message +
 * Archive (danger zone) only — do not leave empty shell buttons.
 */
export function AgentProfileActions({
  agent,
  canManage,
}: {
  agent: Agent;
  canManage: boolean;
}) {
  const { t } = useT("agents");
  const qc = useQueryClient();
  const { openDM, isPending: openingDM } = useOpenDM();
  const [confirmArchive, setConfirmArchive] = useState(false);
  const [archiving, setArchiving] = useState(false);

  const isArchived = !!agent.archived_at;
  const displayName = resolveActorDisplayName(agent, agent.id);

  const invalidateAgents = () => {
    qc.invalidateQueries({ queryKey: workspaceKeys.agents(agent.workspace_id) });
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
  variant,
  testId,
}: {
  children: React.ReactNode;
  onClick: () => void;
  disabled?: boolean;
  variant: "primary" | "danger";
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
        variant === "primary" &&
          "border-brand/40 bg-brand/10 text-brand hover:bg-brand/15",
        variant === "danger" &&
          "border-destructive/40 bg-destructive/10 text-destructive hover:bg-destructive/15",
      )}
    >
      {children}
    </button>
  );
}
