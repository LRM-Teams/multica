"use client";

import { useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import type { Agent, UpdateAgentRequest } from "@multica/core/types";
import { api } from "@multica/core/api";
import {
  agentDetailKeys,
  agentLifecycleActionState,
} from "@multica/core/agents";
import { workspaceKeys } from "@multica/core/workspace/queries";
import { useT } from "../../i18n";

/** Fields that change the running code-agent session (Frank/Raft: restart after save). */
const EXECUTION_CONFIG_KEYS = new Set([
  "runtime_id",
  "model",
  "thinking_level",
]);

function touchesExecutionConfig(data: Record<string, unknown>): boolean {
  for (const key of Object.keys(data)) {
    if (EXECUTION_CONFIG_KEYS.has(key)) return true;
  }
  return false;
}

/**
 * Shared agent-update mutation used by BOTH the agent detail inspector and
 * the compact profile card / side panel. Extracted from `agent-detail-page`
 * so every surface that flips runtime / model / thinking / visibility /
 * concurrency goes through ONE optimistic code path — no duplicated
 * cache-patch or rollback logic.
 *
 * After LRM-292, profile/panel body is authoritative on
 * `agentDetailKeys.detail` (GET /api/agents/:id), not ListAgents. This hook
 * must patch BOTH caches so picker chips refresh without a full page reload.
 *
 * Returns `handleUpdate(id, data)`:
 *  - Cancels in-flight list/detail fetches, then optimistically patches
 *    `workspaceKeys.agents(wsId)` AND `agentDetailKeys.detail(wsId, id)`
 *    BEFORE the network round-trip so the picker chips flip immediately.
 *  - On success: write the server's canonical touched fields into both
 *    caches. Detail uses the PATCH payload as authority — we do NOT
 *    invalidate+refetch detail (that race can briefly restore the pre-PATCH
 *    agent and leave Runtime Config stale until a hard reload — LRM-296).
 *    List is still invalidated so directory consumers converge.
 *  - When the patch touches runtime/model/thinking (execution config), also
 *    kick the existing agent lifecycle `restart` (same path as Restart
 *    button — Parker A2) so the new config applies immediately (Frank
 *    2026-08-04 / Raft). Preflight gates offline / unsupported (A3/A4);
 *    restart failure does not roll back the saved config.
 *  - On error: rolls back ONLY the fields THIS call wrote, invalidates so
 *    the cache converges with the server, shows an error toast, and
 *    rethrows so the caller's own catch can react. Never silently keep
 *    pre-mutation values without an error (LRM-238).
 */
export function useUpdateAgent(wsId: string) {
  const qc = useQueryClient();
  const { t } = useT("agents");

  return async (id: string, data: Record<string, unknown>): Promise<void> => {
    const listKey = workspaceKeys.agents(wsId);
    const detailKey = agentDetailKeys.detail(wsId, id);
    const prevAgents = qc.getQueryData<Agent[]>(listKey);
    const prevAgentFromList = prevAgents?.find((a) => a.id === id);
    const prevAgentFromDetail = qc.getQueryData<Agent>(detailKey);
    const prevAgent = prevAgentFromList ?? prevAgentFromDetail;

    const optimistic: Record<string, unknown> = {};
    const prevFields: Record<string, unknown> = {};
    for (const key of Object.keys(data)) {
      optimistic[key] = data[key];
      if (prevAgent) {
        prevFields[key] = (prevAgent as unknown as Record<string, unknown>)[
          key
        ];
      }
    }

    const patchList = (patch: Record<string, unknown>) => {
      qc.setQueryData<Agent[]>(listKey, (old) =>
        old?.map((a) => (a.id === id ? ({ ...a, ...patch } as Agent) : a)),
      );
    };
    const patchDetail = (patch: Record<string, unknown>) => {
      qc.setQueryData<Agent>(detailKey, (old) =>
        old ? ({ ...old, ...patch } as Agent) : old,
      );
    };

    // Drop in-flight GET /agents/:id so a late response cannot overwrite the
    // optimistic / PATCH write (toast success + stale Runtime chip). Fire-and-
    // forget so the chip flip stays synchronous for the click frame.
    void qc.cancelQueries({ queryKey: listKey });
    void qc.cancelQueries({ queryKey: detailKey });

    patchList(optimistic);
    patchDetail(optimistic);
    try {
      // The server returns the persisted `Agent`; write back its canonical
      // values for the fields THIS call touched so the UI matches the server exactly — no stale
      // flash before the refetch, and no dependence on the request value
      // equalling what the server ultimately stored. Only the touched fields
      // are patched (not the whole agent) so a concurrent successful mutation
      // to a DIFFERENT field on the same agent is never clobbered.
      const updated = await api.updateAgent(id, data as UpdateAgentRequest);
      const serverPatch: Record<string, unknown> = {};
      for (const field of Object.keys(optimistic)) {
        const serverValue = (updated as unknown as Record<string, unknown>)[
          field
        ];
        // Prefer server echo; if a field is omitted, keep the optimistic
        // value rather than writing `undefined` over a good chip.
        serverPatch[field] =
          serverValue !== undefined ? serverValue : optimistic[field];
      }
      await qc.cancelQueries({ queryKey: detailKey });
      patchList(serverPatch);
      // Detail cache may be empty for list-only surfaces — seed it from the
      // server payload so a later open of the profile card is already fresh.
      qc.setQueryData<Agent>(detailKey, (old) =>
        old
          ? ({ ...old, ...serverPatch } as Agent)
          : ({ ...updated } as Agent),
      );
      // List directory can refetch; panel body must NOT — PATCH is authority.
      qc.invalidateQueries({ queryKey: listKey });

      if (!touchesExecutionConfig(data)) {
        toast.success(t(($) => $.detail.agent_updated_toast));
        return;
      }

      // A1/A2/A3/A4 — save first, then reuse lifecycle restart when preflight allows.
      try {
        const preflight = await api.getAgentLifecyclePreflight(id);
        const restartState = agentLifecycleActionState(preflight, "restart");
        // Same gate as the profile Restart button: capability + supported.
        const canForce =
          preflight.provider_capabilities?.force_restart === true;
        if (!canForce || !restartState.supported) {
          toast.success(t(($) => $.detail.agent_updated_next_run_toast));
          return;
        }
        await api.startAgentLifecycleAction(
          id,
          "restart",
          crypto.randomUUID(),
        );
        if (restartState.execution_mode === "after_current_run") {
          // A3: scheduled after busy run — don't claim "already restarted".
          toast.success(t(($) => $.detail.agent_updated_restart_scheduled_toast));
        } else {
          toast.success(t(($) => $.detail.agent_updated_restart_toast));
        }
      } catch (restartErr) {
        // Config already saved — surface restart failure without rolling back.
        toast.success(t(($) => $.detail.agent_updated_toast));
        showErrorToast(
          restartErr instanceof Error
            ? restartErr.message
            : t(($) => $.detail.restart_after_update_failed_toast),
        );
      }
    } catch (e) {
      if (prevAgentFromList || prevAgentFromDetail) {
        patchList(prevFields);
        if (prevAgentFromDetail) {
          patchDetail(prevFields);
        }
      }
      qc.invalidateQueries({ queryKey: listKey });
      qc.invalidateQueries({ queryKey: detailKey });
      showErrorToast(
        e instanceof Error ? e.message : t(($) => $.detail.update_failed_toast),
      );
      throw e;
    }
  };
}
