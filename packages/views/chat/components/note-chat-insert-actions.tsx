"use client";

import { useState } from "react";
import { FilePlus2, ListPlus, Loader2 } from "lucide-react";
import { useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { api } from "@multica/core/api";
import { useWorkspaceId } from "@multica/core/hooks";
import {
  deriveNoteWorkerReplyTitle,
  extractInsertableNoteMarkdown,
} from "@multica/core/notes/worker-reply-actions";
import { noteKeys } from "@multica/core/notes/queries";
import { Button } from "@multica/ui/components/ui/button";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { useT } from "../../i18n";
import { insertMessageIntoNote, type NoteMessageInsertMode } from "./insert-message-into-note";

export function NoteChatInsertActions({
  pageId,
  text,
}: {
  pageId: string;
  text: string;
}) {
  const { t } = useT("chat");
  const wsId = useWorkspaceId();
  const queryClient = useQueryClient();
  const [busy, setBusy] = useState<NoteMessageInsertMode | null>(null);
  const markdown = extractInsertableNoteMarkdown(text);
  if (!markdown || !pageId.trim()) return null;

  const run = async (mode: NoteMessageInsertMode) => {
    if (busy) return;
    setBusy(mode);
    try {
      const titleFallback = t(($) => $.message_list.insert_title_fallback);
      let title = deriveNoteWorkerReplyTitle(markdown, titleFallback);
      if (mode === "append") {
        const parent = await api.getNotePage(pageId);
        const base = (parent.content ?? "").replace(/\s+$/g, "");
        const next = base ? `${base}\n\n${markdown}` : markdown;
        await api.updateNotePage(pageId, { content: next });
      } else {
        const res = await insertMessageIntoNote({
          pageId,
          text: markdown,
          mode,
          titleFallback,
        });
        title = res.title;
      }
      if (wsId) {
        void queryClient.invalidateQueries({ queryKey: noteKeys.all(wsId) });
      }
      toast.success(
        mode === "append"
          ? t(($) => $.message_list.insert_below_success)
          : t(($) => $.message_list.insert_child_success, { title }),
      );
    } catch (error) {
      showErrorToast(
        error instanceof Error ? error.message : t(($) => $.message_list.insert_failed),
      );
    } finally {
      setBusy(null);
    }
  };

  return (
    <div className="mt-2 flex flex-wrap gap-2" data-testid="note-chat-insert-actions">
      <Button
        type="button"
        size="sm"
        variant="outline"
        disabled={busy !== null}
        data-testid="note-chat-insert-below"
        onClick={(event) => {
          event.stopPropagation();
          void run("append");
        }}
      >
        {busy === "append" ? (
          <Loader2 className="size-3.5 animate-spin" aria-hidden />
        ) : (
          <ListPlus className="size-3.5" aria-hidden />
        )}
        {t(($) => $.message_list.insert_below_action)}
      </Button>
      <Button
        type="button"
        size="sm"
        variant="outline"
        disabled={busy !== null}
        data-testid="note-chat-insert-child"
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
        {t(($) => $.message_list.insert_child_action)}
      </Button>
    </div>
  );
}
