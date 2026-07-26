"use client";

import * as React from "react";
import { ThumbsDown, ThumbsUp } from "lucide-react";
import { api } from "@multica/core/api";
import type { ReplyFeedback } from "@multica/core/types";
import { cn } from "@multica/ui/lib/utils";

export function ReplyFeedbackBar({
  channelId,
  messageId,
  initial,
}: {
  channelId: string;
  messageId: string;
  initial?: ReplyFeedback | null;
}) {
  const [value, setValue] = React.useState<1 | -1 | null>(initial?.value ?? null);
  const [busy, setBusy] = React.useState(false);

  React.useEffect(() => {
    setValue(initial?.value ?? null);
  }, [initial?.value, initial?.id]);

  const toggle = async (next: 1 | -1) => {
    if (busy) return;
    setBusy(true);
    const prev = value;
    try {
      if (prev === next) {
        setValue(null);
        await api.deleteChannelReplyFeedback(channelId, messageId);
      } else {
        setValue(next);
        await api.upsertChannelReplyFeedback(channelId, messageId, next);
      }
    } catch {
      setValue(prev);
    } finally {
      setBusy(false);
    }
  };

  return (
    <fieldset
      className="mt-1 flex items-center gap-1 border-0 p-0"
      data-testid="reply-feedback-bar"
    >
      <legend className="sr-only">回复反馈</legend>
      <button
        type="button"
        aria-label="点赞"
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
        aria-label="点踩"
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
