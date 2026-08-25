"use client";

import { useState } from "react";
import { FilePlus2, ListPlus, Loader2 } from "lucide-react";
import { api } from "@multica/core/api";
import { chatKeys } from "@multica/core/chat/queries";
import { noteKeys } from "@multica/core/notes/queries";
import { useWorkspaceId } from "@multica/core/hooks";
import type { MessagePart, NotePeriodBriefInsertMode } from "@multica/core/types";
import { useQueryClient } from "@tanstack/react-query";
import { Button } from "@multica/ui/components/ui/button";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { toast } from "sonner";
import { useT } from "../i18n/use-t";

export function PeriodBriefInsertActions({
  part,
}: {
  part: Extract<MessagePart, { type: "period_brief_insert" }>;
}) {
  const { t } = useT("channels");
  const wsId = useWorkspaceId();
  const queryClient = useQueryClient();
  const [busy, setBusy] = useState<NotePeriodBriefInsertMode | null>(null);

  const run = async (mode: NotePeriodBriefInsertMode) => {
    if (busy !== null) return;
    setBusy(mode);
    try {
      const res = await api.insertNotePeriodBrief(part.ref_id, { mode });
      if (wsId) {
        void queryClient.invalidateQueries({ queryKey: noteKeys.all(wsId) });
        void queryClient.invalidateQueries({ queryKey: chatKeys.all(wsId) });
      }
      toast.success(
        mode === "append"
          ? t(($) => $.message.period_brief_insert_below_success)
          : t(($) => $.message.period_brief_insert_child_success, { title: res.title ?? "" }),
      );
    } catch (err) {
      showErrorToast(
        err instanceof Error ? err.message : t(($) => $.message.period_brief_insert_failed),
      );
    } finally {
      setBusy(null);
    }
  };

  return (
    <div className="mt-2 flex flex-wrap gap-2" data-testid="period-brief-insert-actions">
      <Button
        type="button"
        size="sm"
        variant="outline"
        disabled={busy !== null}
        data-testid="period-brief-insert-below"
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
        {t(($) => $.message.period_brief_insert_below)}
      </Button>
      <Button
        type="button"
        size="sm"
        variant="outline"
        disabled={busy !== null}
        data-testid="period-brief-insert-child"
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
        {t(($) => $.message.period_brief_insert_child)}
      </Button>
    </div>
  );
}
