"use client";

import { useState } from "react";
import { Loader2, MessageSquare, Play, RotateCcw, Square, Trash2 } from "lucide-react";
import { useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import type { Agent, AgentPresence } from "@multica/core/types";
import { api } from "@multica/core/api";
import { deriveRuntimeHealth } from "@multica/core/runtimes";
import { workspaceKeys } from "@multica/core/workspace/queries";
import { resolveActorDisplayName } from "@multica/core/identity";
import { Button } from "@multica/ui/components/ui/button";
import { useOpenDM } from "../../common/use-open-dm";
import { useT } from "../../i18n/use-t";
import { AgentRestartModal } from "./agent-restart-modal";
import { ConfirmDeleteAgent } from "./confirm-delete-agent";

/**
 * LRM-448 · Profile v4 Actions stack (Computer IA + Multica tokens).
 * Vertical named actions — no header Message+⋯, no More overflow.
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
 * Agent lifecycle uses one Runner-presence-driven Start/Stop button. Restart
 * and reset remain separate operations in the existing restart modal.
 *
 * LRM-593 (Frank lock A): Delete is the only solid destructive, above a
 * `border-t` danger zone.
 */
export function AgentProfileActions({
  agent,
  canManage,
  presence,
}: {
  agent: Agent;
  canManage: boolean;
  presence: AgentPresence | undefined;
}) {
  const { t } = useT("agents");
  const qc = useQueryClient();
  const { openDM, isPending: openingDM } = useOpenDM();
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [lifecyclePending, setLifecyclePending] = useState(false);
  const [restartOpen, setRestartOpen] = useState(false);

  const isArchived = !!agent.archived_at;
  const displayName = resolveActorDisplayName(agent, agent.id);
  const isAgentRunning = presence === "online";
  // Only offer lifecycle actions while the bound computer is
  // reachable — it comes back on its own once the computer reconnects.
  // Derived, staleness-aware health (#10), never the raw `runtime_status`
  // column: that field can say "online" for up to 180s after the daemon
  // actually went silent.
  const isRuntimeOnline =
    deriveRuntimeHealth(
      { status: agent.runtime_status ?? "offline", last_seen_at: agent.runtime_last_seen_at ?? null },
      Date.now(),
    ) === "online";

  const invalidateAgents = () => {
    qc.invalidateQueries({ queryKey: workspaceKeys.agents(agent.workspace_id) });
  };

  const handleLifecycle = async () => {
    setLifecyclePending(true);
    try {
      await (isAgentRunning ? api.stopAgent(agent.id) : api.startAgent(agent.id));
      invalidateAgents();
      toast.success(
        t(($) =>
          isAgentRunning
            ? $.side_panel.actions_stop_agent_success
            : $.side_panel.actions_start_success,
        ),
      );
    } catch (e) {
      showErrorToast(
        e instanceof Error
          ? e.message
          : t(($) =>
              isAgentRunning
                ? $.side_panel.actions_stop_agent_failed
                : $.side_panel.actions_start_failed,
            ),
      );
    } finally {
      setLifecyclePending(false);
    }
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

        {canManage && !isArchived ? (
          <Button
            type="button"
            variant="outline"
            size="lg"
            className="w-full gap-2"
            data-testid="agent-profile-action-start"
            disabled={lifecyclePending || !presence || (!isAgentRunning && !isRuntimeOnline)}
            onClick={() => void handleLifecycle()}
          >
            {lifecyclePending ? (
              <Loader2 className="size-4 shrink-0 animate-spin" aria-hidden />
            ) : isAgentRunning ? (
              <Square className="size-4 shrink-0" aria-hidden />
            ) : (
              <Play className="size-4 shrink-0" aria-hidden />
            )}
            {t(($) =>
              isAgentRunning ? $.side_panel.actions_stop_agent : $.side_panel.actions_start_agent,
            )}
          </Button>
        ) : null}

        {canManage && !isArchived && isRuntimeOnline ? (
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
              // bg-destructive + white text).
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
