import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { useWorkspaceId } from "../hooks";
import { dmKeys } from "./queries";
import type { CreateOrFindDMBody } from "./types";

/**
 * Create-or-find a 1-on-1 DM with a peer (idempotent). On success the DM list
 * is invalidated so a freshly created DM appears in the sidebar. The returned
 * `DMItem` is what callers select/open in the Messages view.
 */
export function useCreateOrFindDM() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: (body: CreateOrFindDMBody) => api.createOrFindDM(body),
    onSuccess: () => qc.invalidateQueries({ queryKey: dmKeys.list(wsId) }),
  });
}
