"use client";

import { useState } from "react";
import { FilePlus2, ListPlus, Loader2 } from "lucide-react";
import { api } from "@multica/core/api";
import { noteKeys } from "@multica/core/notes/queries";
import {
  appendWorkerReplyBelowNote,
  deriveNoteWorkerReplyTitle,
  noteWorkerReplyPlainText,
} from "@multica/core/notes/worker-reply-actions";
import { useWorkspaceId } from "@multica/core/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import type { ChannelMessage } from "@multica/core/types";
import { useQueryClient } from "@tanstack/react-query";
import { Button } from "@multica/ui/components/ui/button";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { toast } from "sonner";
import { useNavigation } from "../navigation";
import { useT } from "../i18n/use-t";

export function NoteWorkerReplyActions({
  message,
  pageId,
}: {
  message: ChannelMessage;
  pageId: string;
}) {
  const { t } = useT("channels");
  const wsId = useWorkspaceId();
  const paths = useWorkspacePaths();
  const navigation = useNavigation();
  const queryClient = useQueryClient();
  const [busy, setBusy] = useState<"insert" | "child" | null>(null);

  const replyText = noteWorkerReplyPlainText(message);
  if (!replyText || message.deleted_at) return null;

  const run = async (mode: "insert" | "child") => {
    if (busy) return;
    setBusy(mode);
    try {
      const parent = await api.getNotePage(pageId);
      const title = deriveNoteWorkerReplyTitle(
        replyText,
        t(($) => $.message.note_worker_reply_title_fallback),
      );
      if (mode === "insert") {
        const nextContent = appendWorkerReplyBelowNote(parent.content ?? "", title, replyText);
        const updated = await api.updateNotePage(pageId, { content: nextContent });
        if (wsId) {
          queryClient.setQueryData(noteKeys.detail(wsId, pageId), updated);
          void queryClient.invalidateQueries({ queryKey: noteKeys.list(wsId) });
        }
        toast.success(t(($) => $.message.note_worker_insert_success), {
          action: {
            label: t(($) => $.message.note_worker_open_note),
            onClick: () => navigation.push(paths.noteDetail(pageId)),
          },
        });
        return;
      }

      const child = await api.createNotePage({ parent_id: pageId, title });
      const updated = await api.updateNotePage(child.id, { content: replyText });
      if (wsId) {
        queryClient.setQueryData(noteKeys.detail(wsId, updated.id), updated);
        void queryClient.invalidateQueries({ queryKey: noteKeys.list(wsId) });
      }
      toast.success(t(($) => $.message.note_worker_child_success), {
        action: {
          label: t(($) => $.message.note_worker_open_note),
          onClick: () => navigation.push(paths.noteDetail(updated.id)),
        },
      });
    } catch (error: unknown) {
      showErrorToast(
        error instanceof Error
          ? error.message
          : mode === "insert"
            ? t(($) => $.message.note_worker_insert_failed)
            : t(($) => $.message.note_worker_child_failed),
      );
    } finally {
      setBusy(null);
    }
  };

  return (
    <div
      className="mt-2 flex flex-wrap gap-2"
      data-testid="note-worker-reply-actions"
    >
      <Button
        type="button"
        size="sm"
        variant="outline"
        disabled={busy !== null}
        data-testid="note-worker-insert-below"
        onClick={(event) => {
          event.stopPropagation();
          void run("insert");
        }}
      >
        {busy === "insert" ? (
          <Loader2 className="size-3.5 animate-spin" aria-hidden />
        ) : (
          <ListPlus className="size-3.5" aria-hidden />
        )}
        {t(($) => $.message.note_worker_insert_below)}
      </Button>
      <Button
        type="button"
        size="sm"
        variant="outline"
        disabled={busy !== null}
        data-testid="note-worker-create-child"
        onClick={(event) => {
          event.stopPropagation();
          void run("child");
        }}
      >
        {busy === "child" ? (
          <Loader2 className="size-3.5 animate-spin" aria-hidden />
        ) : (
          <FilePlus2 className="size-3.5" aria-hidden />
        )}
        {t(($) => $.message.note_worker_create_child)}
      </Button>
    </div>
  );
}
