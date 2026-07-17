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
 *    The request's `username` key maps to the cached `Agent.name` (the
 *    @handle the UI reads), not a literal `agent.username`.
 *  - On success: `api.updateAgent` + write the server's canonical values for
 *    the touched fields back into the cache + invalidate + success toast.
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

    qc.setQueryData<Agent[]>(queryKey, (old) =>
      old?.map((a) => (a.id === id ? ({ ...a, ...optimistic } as Agent) : a)),
    );
    try {
      // The server returns the persisted `Agent`; write back its canonical
      // values for the fields THIS call touched (e.g. `name` for a `username`
      // edit) so the displayed handle matches the server exactly — no stale
      // flash before the refetch, and no dependence on the request value
      // equalling what the server ultimately stored. Only the touched fields
      // are patched (not the whole agent) so a concurrent successful mutation
      // to a DIFFERENT field on the same agent is never clobbered.
      const updated = await api.updateAgent(id, data as UpdateAgentRequest);
      qc.setQueryData<Agent[]>(queryKey, (old) =>
        old?.map((a) => {
          if (a.id !== id) return a;
          const serverPatch: Record<string, unknown> = {};
          for (const field of Object.keys(optimistic)) {
            serverPatch[field] = (
              updated as unknown as Record<string, unknown>
            )[field];
          }
          return { ...a, ...serverPatch } as Agent;
        }),
      );
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
