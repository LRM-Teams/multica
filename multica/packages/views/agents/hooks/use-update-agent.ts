"use client";

import { useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import type { Agent, UpdateAgentRequest } from "@multica/core/types";
import { api } from "@multica/core/api";
import { agentDetailKeys } from "@multica/core/agents";
import { workspaceKeys } from "@multica/core/workspace/queries";
import { useT } from "../../i18n";

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
 *    The request's `username` key maps to the cached `Agent.name` (the
 *    @handle the UI reads), not a literal `agent.username`.
 *  - On success: write the server's canonical touched fields into both
 *    caches. Detail uses the PATCH payload as authority — we do NOT
 *    invalidate+refetch detail (that race can briefly restore the pre-PATCH
 *    agent and leave Runtime Config stale until a hard reload — LRM-296).
 *    List is still invalidated so directory consumers converge.
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

    // Request keys are API field names; the cached `Agent` is keyed by its
    // OWN field names, which are 1:1 EXCEPT for the @handle: the request sends
    // `username`, but the server (and the cached Agent the UI renders) stores
    // it under `name`. So translate `username` → `name` for the optimistic
    // patch AND its rollback. Merging the request verbatim would write a stray
    // `agent.username` that nothing reads — leaving the UI (which shows
    // `agent.name`) on the OLD handle until the refetch converges (a stale
    // flash), and reverting the WRONG field if the update fails.
    const cacheField = (key: string) => (key === "username" ? "name" : key);
    const optimistic: Record<string, unknown> = {};
    const prevFields: Record<string, unknown> = {};
    for (const key of Object.keys(data)) {
      const field = cacheField(key);
      optimistic[field] = data[key];
      if (prevAgent) {
        prevFields[field] = (prevAgent as unknown as Record<string, unknown>)[
          field
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
      // values for the fields THIS call touched (e.g. `name` for a `username`
      // edit) so the displayed handle matches the server exactly — no stale
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
      toast.success(t(($) => $.detail.agent_updated_toast));
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
