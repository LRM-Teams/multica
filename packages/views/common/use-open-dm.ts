"use client";

import { useCallback } from "react";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { ApiError } from "@multica/core/api";
import { useCreateOrFindDM } from "@multica/core/dm";
import type { CreateOrFindDMBody, DMItem } from "@multica/core/dm";
import { useWorkspacePaths } from "@multica/core/paths";
import { useNavigation } from "../navigation";
import { useT } from "../i18n";

/**
 * Shared "Send message" affordance behaviour for the three DM entry points
 * (Cmd+K results, agent hover card, channel member row): create-or-find the DM
 * with the peer, then open it in the Messages view via the channel detail path.
 *
 * Idempotent on the server, so repeated clicks resolve to the same DM. The
 * returned promise resolves with the DMItem in case the caller wants to react
 * (e.g. close a popover) after navigation.
 */
export function useOpenDM(): {
  openDM: (body: CreateOrFindDMBody) => Promise<DMItem | null>;
  isPending: boolean;
} {
  const createOrFind = useCreateOrFindDM();
  const paths = useWorkspacePaths();
  const { push } = useNavigation();
  const { t } = useT("channels");

  const openDM = useCallback(
    async (body: CreateOrFindDMBody): Promise<DMItem | null> => {
      try {
        const dm = await createOrFind.mutateAsync(body);
        push(paths.channelDetail(dm.id));
        return dm;
      } catch (err) {
        if (err instanceof ApiError && err.status === 403) {
          showErrorToast(t(($) => $.dm.open_forbidden));
        } else {
          showErrorToast(t(($) => $.dm.open_failed));
        }
        return null;
      }
    },
    [createOrFind, paths, push, t],
  );

  return { openDM, isPending: createOrFind.isPending };
}
