"use client";

import { Trash2, Undo2 } from "lucide-react";
import type { NotePage } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { useT } from "../i18n/use-t";

type NoteTrashViewProps = {
  pages: NotePage[];
  emptying: boolean;
  restoring: boolean;
  deleting: boolean;
  onEmpty: () => void;
  onRestore: (page: NotePage) => void;
  onPermanentDelete: (page: NotePage) => void;
};

export function NoteTrashView({
  pages,
  emptying,
  restoring,
  deleting,
  onEmpty,
  onRestore,
  onPermanentDelete,
}: NoteTrashViewProps) {
  const { t } = useT("layout");
  const busy = emptying || restoring || deleting;

  return (
    <div className="mx-auto max-w-3xl px-8 py-8">
      <div className="flex items-start justify-between gap-4">
        <div className="min-w-0">
          <h1 className="text-2xl font-semibold">{t(($) => $.notes_page.trash)}</h1>
          <p className="mt-1 text-sm text-muted-foreground">{t(($) => $.notes_page.trash_description)}</p>
        </div>
        {pages.length > 0 && (
          <Button size="sm" variant="destructive" onClick={onEmpty} disabled={busy}>
            <Trash2 className="size-4" />
            {t(($) => $.notes_page.empty_trash_action)}
          </Button>
        )}
      </div>
      <div className="mt-6 space-y-2">
        {pages.length === 0 ? (
          <div className="rounded-xl border border-dashed p-6 text-sm text-muted-foreground">{t(($) => $.notes_page.trash_empty)}</div>
        ) : (
          pages.map((page) => (
            <div key={page.id} className="flex items-center gap-3 rounded-xl border p-3">
              <div className="min-w-0 flex-1">
                <div className="truncate text-sm font-medium">{page.title}</div>
                {page.deleted_at && <div className="text-xs text-muted-foreground">{page.deleted_at}</div>}
              </div>
              <Button size="sm" variant="outline" onClick={() => onRestore(page)} disabled={busy}>
                <Undo2 className="size-4" />
                {t(($) => $.notes_page.restore_action)}
              </Button>
              <Button size="sm" variant="destructive" onClick={() => onPermanentDelete(page)} disabled={busy}>
                <Trash2 className="size-4" />
                {t(($) => $.notes_page.permanent_delete_action)}
              </Button>
            </div>
          ))
        )}
      </div>
    </div>
  );
}
