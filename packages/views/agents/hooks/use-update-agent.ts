"use client";

import { useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import type { Agent, UpdateAgentRequest } from "@multica/core/types";
import { api } from "@multica/core/api";
import { workspaceKeys } from "@multica/core/workspace/queries";
import { useT } from "../../i18n";

/**
 * Shared agent-update mutation used by BOTH the agent detail inspector and
 * the compact profile card. Extracted from `agent-detail-page` so every
 * surface that flips runtime / model / thinking / visibility / concurrency
 * goes through ONE optimistic code path — no duplicated cache-patch or
 * rollback logic.
 *
 * Returns `handleUpdate(id, data)`:
 *  - Optimistically patches the matching agent in the cached
 *    `workspaceKeys.agents(wsId)` list BEFORE the network round-trip so the
 *    picker chips flip to the new value immediately on click, instead of
 *    waiting 0.5-2s for the API response + invalidate + refetch.
 *  - On success: `api.updateAgent` + invalidate + success toast.
 *  - On error: rolls back ONLY the fields THIS call wrote (leaving any
 *    other concurrently-mutated fields untouched), invalidates so the cache
 *    converges with the server, shows an error toast, and rethrows so the
 *    caller's own catch (e.g. an edit dialog) can react. A whole-list
 *    snapshot rollback would clobber a concurrent successful mutation if the
 *    failing call resolves last (e.g. flipping visibility then runtime
 *    simultaneously and only the visibility PATCH fails).
 */
export function useUpdateAgent(wsId: string) {
  const qc = useQueryClient();
  const { t } = useT("agents");

  return async (id: string, data: Record<string, unknown>): Promise<void> => {
    const queryKey = workspaceKeys.agents(wsId);
    const prevAgents = qc.getQueryData<Agent[]>(queryKey);
    const prevAgent = prevAgents?.find((a) => a.id === id);
    const prevFields: Record<string, unknown> = {};
    if (prevAgent) {
      for (const key of Object.keys(data)) {
        prevFields[key] = (prevAgent as unknown as Record<string, unknown>)[key];
      }
    }
    qc.setQueryData<Agent[]>(queryKey, (old) =>
      old?.map((a) => (a.id === id ? ({ ...a, ...data } as Agent) : a)),
    );
    try {
      await api.updateAgent(id, data as UpdateAgentRequest);
      qc.invalidateQueries({ queryKey });
      toast.success(t(($) => $.detail.agent_updated_toast));
    } catch (e) {
      if (prevAgent) {
        qc.setQueryData<Agent[]>(queryKey, (old) =>
          old?.map((a) =>
            a.id === id ? ({ ...a, ...prevFields } as Agent) : a,
          ),
        );
      }
      qc.invalidateQueries({ queryKey });
      toast.error(
        e instanceof Error ? e.message : t(($) => $.detail.update_failed_toast),
      );
      throw e;
    }
  };
}
