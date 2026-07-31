"use client";

import { useMemo, useState } from "react";
import { Loader2, MessageSquare, RotateCcw, Square, Trash2 } from "lucide-react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import type { Agent } from "@multica/core/types";
import { api } from "@multica/core/api";
import {
  activeChannelTasksKeys,
  activeChannelTasksOptions,
} from "@multica/core/channels";
import { dmKeys, dmListOptions } from "@multica/core/dm";
import { useWorkspaceId } from "@multica/core/hooks";
import { workspaceKeys } from "@multica/core/workspace/queries";
import { resolveActorDisplayName } from "@multica/core/identity";
import { Button } from "@multica/ui/components/ui/button";
import { useOpenDM } from "../../common/use-open-dm";
import { useT } from "../../i18n/use-t";
import { AgentRestartModal } from "./agent-restart-modal";
import { ConfirmDeleteAgent } from "./confirm-delete-agent";
import { pickStoppableDmTask } from "./agent-profile-stoppable-task";

/**
 * LRM-448 · Profile v4 Actions stack (Computer IA + Multica tokens).
 * Vertical named actions — no header Message+⋯, no More overflow.
 *
 * LRM-468: Restart/Reset · Copy diagnostic · Report issue are out of
 * scope this period (Frank「这几个功能删掉，先不做」). Keep Message +
 * Delete (danger zone) only — do not leave empty shell buttons.
 *
 * LRM-448 / Frank 2026-07-23: the destructive action is **Delete**, not
 * Archive (AC#2 "Message + Delete（非 Archive）"). The backend exposes no
 * hard-delete endpoint, so "Delete" deactivates via `archiveAgent`
 * (soft-delete: history preserved, restorable) — the user-facing term is
 * Delete per the locked spec.
 *
 * LRM-480: actions use the standard project Button variants (outline /
 * destructive) — no custom thick-bordered button style.
 *
 * LRM-589: DM Agent Stop lives here (not beside the DM header cue). Show
 * Stop only when this agent has a stoppable 1:1 DM task — never "Stop all".
 *
 * LRM-593 (Frank lock A, carrier LRM-592): pull the three buttons apart by
 * weight so they stop reading as a flat red wall (Frank「就是这几个按钮问题」).
 *   Message = outline · default (lightest)
 *   Stop    = danger wash OUTLINE  (destructive variant wash + destructive
 *             border; subordinate — temporary cancel)
 *   Delete  = the ONLY solid destructive (filled bg-destructive + white),
 *             above a `border-t` danger zone (heaviest — permanent).
 * No second destructive variant is invented: Stop reuses the existing
 * destructive wash + a border; only Delete is promoted to a solid fill.
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
  const wsId = useWorkspaceId();
  const { openDM, isPending: openingDM } = useOpenDM();
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [stopping, setStopping] = useState(false);
  const [restartOpen, setRestartOpen] = useState(false);

  const isArchived = !!agent.archived_at;
  const displayName = resolveActorDisplayName(agent, agent.id);

  const { data: dms = [] } = useQuery(dmListOptions(wsId));
  const dmChannelId = useMemo(() => {
    const dm = dms.find(
      (row) => row.peer.type === "agent" && row.peer.id === agent.id,
    );
    return dm?.id ?? "";
  }, [agent.id, dms]);

  const { data: activeTasks = [] } = useQuery(
    activeChannelTasksOptions(dmChannelId),
  );

  const stoppableTask = useMemo(
    () => pickStoppableDmTask(activeTasks, agent.id),
    [activeTasks, agent.id],
  );

  const invalidateAgents = () => {
    qc.invalidateQueries({ queryKey: workspaceKeys.agents(agent.workspace_id) });
  };

  // LRM-448: "Delete" = deactivate via archiveAgent (soft-delete). No
  // hard-delete endpoint exists; history is preserved and restorable.
  // LRM-865: keep the confirm dialog open until success (or leave it open
  // on failure so the user can retry / cancel).
  const handleDelete = async () => {
    setDeleting(true);
    try {
      await api.archiveAgent(agent.id);
      invalidateAgents();
      toast.success(t(($) => $.side_panel.agent_deleted_toast));
      setConfirmDelete(false);
    } catch (e) {
      showErrorToast(
        e instanceof Error ? e.message : t(($) => $.side_panel.delete_failed_toast),
      );
    } finally {
      setDeleting(false);
    }
  };

  const handleStop = async () => {
    if (!dmChannelId || !stoppableTask || stopping) return;
    const inboxEventId = stoppableTask.inbox_event_id?.trim();
    if (!inboxEventId) {
      showErrorToast(t(($) => $.side_panel.actions_stop_failed));
      return;
    }
    setStopping(true);
    try {
      await api.cancelChannelInboxEvent(dmChannelId, inboxEventId);
      toast.success(
        t(($) => $.side_panel.actions_stop_success, { name: displayName }),
      );
      qc.invalidateQueries({
        queryKey: activeChannelTasksKeys.all(dmChannelId),
      });
      qc.invalidateQueries({ queryKey: dmKeys.list(wsId) });
    } catch {
      showErrorToast(t(($) => $.side_panel.actions_stop_failed));
    } finally {
      setStopping(false);
    }
  };

  return (
    <section aria-label={t(($) => $.side_panel.actions_section)} data-testid="agent-profile-actions">
      <h3 className="mb-2 text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">
        {t(($) => $.side_panel.actions_section)}
      </h3>
      <div className="flex flex-col gap-2">
        {!isArchived ? (
          <Button
            type="button"
            variant="outline"
            size="lg"
            className="w-full gap-2"
            data-testid="agent-profile-action-message"
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
          </Button>
        ) : null}

        {!isArchived && stoppableTask ? (
          <Button
            type="button"
            // LRM-593 lock A: Stop = danger wash OUTLINE (existing destructive
            // wash + destructive border). NOT solid — only Delete is solid.
            variant="destructive"
            size="lg"
            className="w-full gap-2 border-destructive/40"
            data-testid="agent-profile-action-stop"
            disabled={stopping}
            onClick={() => void handleStop()}
            aria-label={t(($) => $.side_panel.actions_stop_aria, {
              name: displayName,
            })}
          >
            {stopping ? (
              <Loader2 className="size-2.5 shrink-0 animate-spin" aria-hidden />
            ) : (
              <Square className="size-2.5 shrink-0 fill-current" aria-hidden />
            )}
            {t(($) => $.side_panel.actions_stop)}
          </Button>
        ) : null}

        {canManage && !isArchived ? (
          <Button
            type="button"
            variant="outline"
            size="lg"
            className="w-full gap-2"
            data-testid="agent-profile-action-restart"
            onClick={() => setRestartOpen(true)}
          >
            <RotateCcw className="size-4 shrink-0" aria-hidden />
            {t(($) => $.restart_modal.trigger)}
          </Button>
        ) : null}

        {canManage && !isArchived ? (
          <div className="mt-1 border-t border-border pt-3">
            <Button
              type="button"
              // LRM-593 lock A: Delete = the ONLY solid destructive (filled
              // bg-destructive + white text) so it outweighs the wash Stop.
              variant="destructive"
              size="lg"
              className="w-full gap-2 bg-destructive text-white hover:bg-destructive/90 dark:bg-destructive dark:hover:bg-destructive/90"
              data-testid="agent-profile-action-delete"
              disabled={deleting}
              onClick={() => setConfirmDelete(true)}
            >
              <Trash2 className="size-4 shrink-0" aria-hidden />
              {t(($) => $.side_panel.actions_delete)}
            </Button>
          </div>
        ) : null}
      </div>

      <ConfirmDeleteAgent
        open={confirmDelete}
        displayName={displayName}
        pending={deleting}
        onConfirm={() => void handleDelete()}
        onOpenChange={setConfirmDelete}
      />

      {canManage && !isArchived ? (
        <AgentRestartModal
          agentId={agent.id}
          agentHandle={agent.name}
          agentName={displayName}
          open={restartOpen}
          onOpenChange={setRestartOpen}
        />
      ) : null}
    </section>
  );
}
