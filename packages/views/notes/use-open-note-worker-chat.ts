"use client";

import { useCallback } from "react";
import { useWorkspacePaths } from "@multica/core/paths";
import { useOpenDM } from "../common/use-open-dm";
import { useNavigation } from "../navigation";
import type { NoteWorkerChatJob } from "./open-note-worker-chat";

/**
 * Open the Messages conversation where the Worker run was posted.
 * Prefer channel_id (group or agent DM). Fall back to creating/opening the
 * agent DM when older jobs lack channel_id.
 */
export function useOpenNoteWorkerChat(): {
  openNoteWorkerChat: (job: NoteWorkerChatJob) => Promise<void>;
} {
  const { openDM } = useOpenDM();
  const paths = useWorkspacePaths();
  const { push } = useNavigation();

  const open = useCallback(
    async (job: NoteWorkerChatJob) => {
      const channelId = job.channel_id?.trim();
      if (channelId) {
        push(paths.channelDetail(channelId));
        return;
      }
      const agentId = job.agent_id?.trim();
      if (!agentId) return;
      await openDM({ peer_type: "agent", peer_id: agentId });
    },
    [openDM, paths, push],
  );

  return { openNoteWorkerChat: open };
}
