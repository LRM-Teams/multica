"use client";

import * as React from "react";
import { ThumbsDown, ThumbsUp } from "lucide-react";
import { api } from "@multica/core/api";
import type { ReplyFeedback } from "@multica/core/types";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";

export function ReplyFeedbackBar({
  channelId,
  messageId,
  initial,
}: {
  channelId: string;
  messageId: string;
  initial?: ReplyFeedback | null;
}) {
  const { t } = useT("channels");
  const serverValue = initial?.value ?? null;
  const [optimistic, setOptimistic] = React.useState<1 | -1 | null | undefined>(undefined);
  const [busy, setBusy] = React.useState(false);

  // Clear optimistic once the server snapshot catches up (adjust state while rendering).
  if (optimistic !== undefined && optimistic === serverValue) {
    setOptimistic(undefined);
  }
  const value = optimistic !== undefined ? optimistic : serverValue;

  const toggle = async (next: 1 | -1) => {
    if (busy) return;
    setBusy(true);
    const prev = value;
    const nextValue: 1 | -1 | null = prev === next ? null : next;
    setOptimistic(nextValue);
    try {
      if (nextValue === null) {
        await api.deleteChannelReplyFeedback(channelId, messageId);
      } else {
        await api.upsertChannelReplyFeedback(channelId, messageId, nextValue);
      }
    } catch {
      setOptimistic(prev);
    } finally {
      setBusy(false);
    }
  };

  return (
    <fieldset
      className="mt-1 flex items-center gap-1 border-0 p-0"
      data-testid="reply-feedback-bar"
    >
      <legend className="sr-only">{t(($) => $.message.reply_feedback_legend)}</legend>
      <button
        type="button"
        aria-label={t(($) => $.message.reply_feedback_up)}
        aria-pressed={value === 1}
        disabled={busy}
        onClick={() => void toggle(1)}
        className={cn(
          "inline-flex size-8 touch-manipulation items-center justify-center rounded-md border border-transparent text-muted-foreground transition-colors",
          "hover:bg-accent hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
          value === 1 && "border-border bg-accent text-foreground",
        )}
      >
        <ThumbsUp className="size-3.5" />
      </button>
      <button
        type="button"
        aria-label={t(($) => $.message.reply_feedback_down)}
        aria-pressed={value === -1}
        disabled={busy}
        onClick={() => void toggle(-1)}
        className={cn(
          "inline-flex size-8 touch-manipulation items-center justify-center rounded-md border border-transparent text-muted-foreground transition-colors",
          "hover:bg-accent hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
          value === -1 && "border-border bg-accent text-foreground",
        )}
      >
        <ThumbsDown className="size-3.5" />
      </button>
    </fieldset>
  );
}
