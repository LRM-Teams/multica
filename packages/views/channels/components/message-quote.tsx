"use client";

import { X } from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import type { ChannelMessage, ChannelMessageReply } from "@multica/core/types";
import { formatMessagePartsPreview, unwrapStructuredPreviewContent } from "./message-parts-preview";

export type MessageQuoteStatus = "available" | "deleted" | "inaccessible" | (string & {});

export type QuoteTarget = Pick<
  ChannelMessage,
  "id" | "channel_id" | "author_name" | "content" | "parts" | "attachments"
>;

type QuoteSource = (ChannelMessageReply & {
  status?: MessageQuoteStatus | null;
  deleted_at?: string | null;
  attachments?: ChannelMessage["attachments"];
}) | null | undefined;

function attachmentSummary(attachments: ChannelMessage["attachments"] | undefined) {
  if (!attachments || attachments.length === 0) return null;
  if (attachments.length === 1) return attachments[0]?.filename ?? "Attachment";
  return `${attachments.length} attachments`;
}

export function getMessageQuotePreview(source: {
  content?: string | null;
  parts?: ChannelMessage["parts"];
  attachments?: ChannelMessage["attachments"];
}) {
  const text =
    formatMessagePartsPreview(source.parts) ??
    unwrapStructuredPreviewContent(source.content ?? "") ??
    source.content?.trim() ??
    "";
  return text || attachmentSummary(source.attachments) || "Attachment";
}

export function getMessageQuoteStatus(source: QuoteSource, replyToMessageId?: string | null) {
  if (source?.status === "deleted" || source?.deleted_at) return "deleted";
  if (source?.status === "inaccessible") return "inaccessible";
  if (!source && replyToMessageId) return "inaccessible";
  return "available";
}

export function ComposerQuotePreview({
  quote,
  onCancel,
  cancelLabel,
}: {
  quote: QuoteTarget;
  onCancel: () => void;
  cancelLabel: string;
}) {
  return (
    <div
      className="flex items-start gap-2 border-b border-border/35 bg-muted/25 px-3 py-2"
      data-testid="composer-quote-preview"
    >
      <div className="min-w-0 flex-1 border-l-2 border-primary/45 pl-2">
        <p className="truncate text-xs font-medium text-foreground/80">{quote.author_name}</p>
        <p className="line-clamp-2 text-xs leading-5 text-muted-foreground">
          {getMessageQuotePreview(quote)}
        </p>
      </div>
      <button
        type="button"
        onClick={onCancel}
        className="inline-flex size-7 shrink-0 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-background hover:text-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
        aria-label={cancelLabel}
        title={cancelLabel}
      >
        <X className="size-3.5" />
      </button>
    </div>
  );
}

export function MessageQuoteCard({
  quote,
  replyToMessageId,
  authorName,
  onJump,
  labels,
}: {
  quote: QuoteSource;
  replyToMessageId?: string | null;
  authorName: string;
  onJump?: (messageId: string) => void;
  labels: {
    jumpTo: string;
    deleted: string;
    inaccessible: string;
  };
}) {
  const status = getMessageQuoteStatus(quote, replyToMessageId);
  const canJump = status === "available" && !!replyToMessageId && !!onJump;
  const preview =
    status === "deleted"
      ? labels.deleted
      : status === "inaccessible"
        ? labels.inaccessible
        : getMessageQuotePreview({
            content: quote?.content,
            parts: quote?.parts,
            attachments: quote?.attachments,
          });

  return (
    <button
      type="button"
      onClick={() => {
        if (canJump && replyToMessageId) onJump(replyToMessageId);
      }}
      disabled={!canJump}
      data-testid="message-quote-card"
      data-quote-status={status}
      className={cn(
        "mb-2 w-full rounded border-l-2 bg-muted/30 px-2 py-1 text-left transition-colors",
        status === "available" ? "border-muted-foreground/30" : "border-dashed border-muted-foreground/25",
        canJump ? "cursor-pointer hover:bg-muted/45" : "cursor-default opacity-80",
      )}
      aria-label={canJump ? labels.jumpTo : preview}
    >
      <p className="truncate text-[11px] font-semibold text-foreground/70">
        {status === "available" ? authorName : labels.jumpTo}
      </p>
      <p className="line-clamp-2 text-[11px] text-muted-foreground">{preview}</p>
    </button>
  );
}
