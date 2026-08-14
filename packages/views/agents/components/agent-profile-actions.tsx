"use client";

import { useState } from "react";
import { Loader2, MessageSquare, RotateCcw, Trash2 } from "lucide-react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import type { Agent } from "@multica/core/types";
import { api } from "@multica/core/api";
import {
  agentRestartModeState,
  agentRestartPreflightOptions,
  resolveRestartDisabledReasonKey,
} from "@multica/core/agents";
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
 * LRM-909 (Frank「stop按钮删掉，留restart就行」): Profile ACTIONS no longer
 * renders Stop. Keep Message → Restart… → Delete. DM stop remains on the
 * conversation live cue, not here.
 *
 * LRM-593 (Frank lock A): Delete is the only solid destructive, above a
 * `border-t` danger zone.
 */
export function AgentProfileActions({
  agent,
  canManage,
  forceRestartSupported,
}: {
  agent: Agent;
  canManage: boolean;
  /**
   * From the bound runtime's `provider_capabilities.force_restart`.
   * Task #26 / Parker 2026-08-03: do NOT hide Restart when false — keep
   * the button for canManage users and disable with human copy. Missing
   * capability means false (fail closed for enablement, not visibility).
   */
  forceRestartSupported: boolean;
}) {
  const { t } = useT("agents");
  const qc = useQueryClient();
  const { openDM, isPending: openingDM } = useOpenDM();
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [restartOpen, setRestartOpen] = useState(false);

  const isArchived = !!agent.archived_at;
  const displayName = resolveActorDisplayName(agent, agent.id);
  // Frank, 2026-08-01: only offer restart while the bound computer is
  // reachable — it comes back on its own once the computer reconnects.
  // Derived, staleness-aware health (#10), never the raw `runtime_status`
  // column: that field can say "online" for up to 180s after the daemon
  // actually went silent.
  const isRuntimeOnline =
    deriveRuntimeHealth(
      { status: agent.runtime_status ?? "offline", last_seen_at: agent.runtime_last_seen_at ?? null },
      Date.now(),
    ) === "online";

  // The provider-level gate above (forceRestartSupported) only tells us the
  // agent's provider CAN be force-restarted in principle — not that the
  // daemon it's actually running on is new enough to execute it right now
  // Fetch the server-owned restart capability instead of inferring it from
  // Agent presentation state.
  // real preflight whenever we'd otherwise offer the button, so a stale
  // daemon shows a standing reason instead of a click that silently no-ops.
  // Visibility: managers always see Restart while online (Parker #26) —
  // hiding when force_restart is false made the feature look missing.
  const wantsRestartOffer = canManage && !isArchived && isRuntimeOnline;
  const { data: restartPreflightData, isSuccess: restartPreflightSucceeded } = useQuery(
    agentRestartPreflightOptions(agent.id, wantsRestartOffer && forceRestartSupported),
  );
  const restartState = agentRestartModeState(restartPreflightData, "restart");
  // Only trust preflight disable once resolved — don't flash "unavailable".
  const preflightBlocked = forceRestartSupported && restartPreflightSucceeded && !restartState.supported;
  const restartBlocked = !forceRestartSupported || preflightBlocked;
  const restartDisabledReason = !forceRestartSupported
    ? t(($) => $.restart_modal.disabled_reason.no_force_capability)
    : preflightBlocked
      ? t(($) => $.restart_modal.disabled_reason[resolveRestartDisabledReasonKey(restartState.disabled_reason)])
      : null;

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

        {wantsRestartOffer ? (
          <div className="flex flex-col gap-1">
            <Button
              type="button"
              variant="outline"
              size="lg"
              className="w-full gap-2"
              data-testid="agent-profile-action-restart"
              disabled={restartBlocked}
              onClick={() => {
                if (!restartBlocked) setRestartOpen(true);
              }}
            >
              <RotateCcw className="size-4 shrink-0" aria-hidden />
              {t(($) => $.restart_modal.trigger)}
            </Button>
            {restartDisabledReason && (
              <span
                className="text-xs text-muted-foreground"
                data-testid="agent-profile-action-restart-reason"
              >
                {restartDisabledReason}
              </span>
            )}
          </div>
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
