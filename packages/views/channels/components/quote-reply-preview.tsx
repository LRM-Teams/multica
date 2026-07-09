"use client";

import { X } from "lucide-react";
import type { ChannelMessage } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { useActorName } from "@multica/core/workspace/hooks";
import { useT } from "../../i18n/use-t";
import { resolveChannelAuthorDisplayName } from "./message-preview";
import {
  formatMessagePartsPreview,
  unwrapStructuredPreviewContent,
} from "./message-parts-preview";

export function QuoteReplyPreview({
  message,
  currentUserId,
  ownName,
  onCancel,
}: {
  message: ChannelMessage;
  currentUserId: string | null;
  ownName?: string;
  onCancel: () => void;
}) {
  const { t } = useT("channels");
  const { getActorName } = useActorName();
  const authorName = resolveChannelAuthorDisplayName(message, {
    currentUserId,
    ownName,
    getActorName,
  });
  const preview =
    formatMessagePartsPreview(message.parts) ??
    unwrapStructuredPreviewContent(message.content) ??
    message.content;

  return (
    <div
      data-testid="quote-reply-preview"
      className="flex min-w-0 items-start gap-2 border-b border-border/40 px-3 py-2"
    >
      <div className="mt-0.5 h-10 w-0.5 shrink-0 rounded-full bg-primary/60" />
      <div className="min-w-0 flex-1">
        <div className="truncate text-xs font-medium text-foreground">
          {t(($) => $.quote.replying_to, { name: authorName })}
        </div>
        <div className="line-clamp-1 text-xs text-muted-foreground">
          {preview || t(($) => $.quote.empty_preview)}
        </div>
      </div>
      <Button
        type="button"
        variant="ghost"
        size="icon"
        className="-mr-1 size-7 shrink-0"
        aria-label={t(($) => $.quote.cancel_action)}
        onClick={onCancel}
      >
        <X className="size-4" />
      </Button>
    </div>
  );
}
