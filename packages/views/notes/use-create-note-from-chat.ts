"use client";

import { useState } from "react";
import { deriveNoteWorkerReplyTitle } from "@multica/core/notes/worker-reply-actions";
import { paths, useWorkspaceSlug } from "@multica/core/paths";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { toast } from "sonner";
import { useOptionalNavigation } from "../navigation/context";
import { useT } from "../i18n/use-t";
import { createTopLevelNoteFromChatText } from "./create-note-from-chat";

/**
 * Create a product note from visible chat text (hover bar / mobile sheet).
 * Same write path as the Worker "Create note" confirmation.
 *
 * Navigation / workspace slug are optional so message rows can mount in tests
 * and overlays that do not wrap the full app shell.
 */
export function useCreateNoteFromChat() {
  const { t } = useT("channels");
  const slug = useWorkspaceSlug();
  const navigation = useOptionalNavigation();
  const [busy, setBusy] = useState(false);

  const createNoteFromText = async (content: string) => {
    const trimmed = content.trim();
    if (!trimmed || busy) return;
    setBusy(true);
    try {
      const title = deriveNoteWorkerReplyTitle(
        trimmed,
        t(($) => $.message.note_brief_untitled),
      );
      const updated = await createTopLevelNoteFromChatText({
        title,
        content: trimmed,
      });
      const openPath = slug ? paths.workspace(slug).noteDetail(updated.id) : null;
      toast.success(t(($) => $.message.note_worker_create_note_success), {
        action:
          navigation && openPath
            ? {
                label: t(($) => $.message.note_worker_open_note),
                onClick: () => navigation.push(openPath),
              }
            : undefined,
      });
    } catch (error: unknown) {
      showErrorToast(
        error instanceof Error
          ? error.message
          : t(($) => $.message.note_worker_create_note_failed),
      );
    } finally {
      setBusy(false);
    }
  };

  return { createNoteFromText, busy };
}
