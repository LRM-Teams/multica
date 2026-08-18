"use client";

import { useState } from "react";
import { FilePlus, FilePlus2, ListPlus, Loader2 } from "lucide-react";
import { api } from "@multica/core/api";
import { noteKeys } from "@multica/core/notes/queries";
import { createTopLevelNoteFromChatText } from "./create-note-from-chat";
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
  /** Existing note page. Omit to confirm creating a new top-level note. */
  pageId?: string | null;
}) {
  const { t } = useT("channels");
  const wsId = useWorkspaceId();
  const paths = useWorkspacePaths();
  const navigation = useNavigation();
  const queryClient = useQueryClient();
  const [busy, setBusy] = useState<"insert" | "child" | "create" | null>(null);

  const replyText = noteWorkerReplyPlainText(message);
  if (!replyText || message.deleted_at) return null;

  const targetPageId = pageId?.trim() || "";

  const run = async (mode: "insert" | "child" | "create") => {
    if (busy) return;
    setBusy(mode);
    try {
      const title = deriveNoteWorkerReplyTitle(
        replyText,
        t(($) => $.message.note_worker_reply_title_fallback),
      );
      if (mode === "create") {
        const updated = await createTopLevelNoteFromChatText({
          title,
          content: replyText,
          wsId,
          queryClient,
        });
        toast.success(t(($) => $.message.note_worker_create_note_success), {
          action: {
            label: t(($) => $.message.note_worker_open_note),
            onClick: () => navigation.push(paths.noteDetail(updated.id)),
          },
        });
        return;
      }

      const parent = await api.getNotePage(targetPageId);
      if (mode === "insert") {
        const nextContent = appendWorkerReplyBelowNote(parent.content ?? "", title, replyText);
        const updated = await api.updateNotePage(targetPageId, { content: nextContent });
        if (wsId) {
          queryClient.setQueryData(noteKeys.detail(wsId, targetPageId), updated);
          void queryClient.invalidateQueries({ queryKey: noteKeys.list(wsId) });
        }
        toast.success(t(($) => $.message.note_worker_insert_success), {
          action: {
            label: t(($) => $.message.note_worker_open_note),
            onClick: () => navigation.push(paths.noteDetail(targetPageId)),
          },
        });
        return;
      }

      const child = await api.createNotePage({ parent_id: targetPageId, title });
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
      const fallback =
        mode === "insert"
          ? t(($) => $.message.note_worker_insert_failed)
          : mode === "child"
            ? t(($) => $.message.note_worker_child_failed)
            : t(($) => $.message.note_worker_create_note_failed);
      showErrorToast(error instanceof Error ? error.message : fallback);
    } finally {
      setBusy(null);
    }
  };

  if (!targetPageId) {
    return (
      <div className="mt-2 flex flex-wrap gap-2" data-testid="note-worker-reply-actions">
        <Button
          type="button"
          size="sm"
          variant="outline"
          disabled={busy !== null}
          data-testid="note-worker-create-note"
          onClick={(event) => {
            event.stopPropagation();
            void run("create");
          }}
        >
          {busy === "create" ? (
            <Loader2 className="size-3.5 animate-spin" aria-hidden />
          ) : (
            <FilePlus className="size-3.5" aria-hidden />
          )}
          {t(($) => $.message.note_worker_create_note)}
        </Button>
      </div>
    );
  }

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
