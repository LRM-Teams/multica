"use client";

import { X } from "lucide-react";
import type { Attachment, ChannelMessage, ChannelMessageReply } from "@multica/core/types";
import { cn } from "@multica/ui/lib/utils";
import { useActorName } from "@multica/core/workspace/hooks";
import { useT } from "../../i18n/use-t";
import { resolveChannelAuthorDisplayName } from "./message-preview";
import {
  formatMessagePartsPreview,
  unwrapStructuredPreviewContent,
} from "./message-parts-preview";

type QuoteMessage = Pick<
  ChannelMessage | ChannelMessageReply,
  "id" | "type" | "author_id" | "author_name" | "content" | "parts" | "created_at"
> & {
  attachments?: Attachment[];
};

function normalizePreview(value: string): string {
  return value.replace(/\s+/g, " ").trim();
}

function isImageAttachment(attachment: Attachment): boolean {
  return attachment.content_type?.startsWith("image/") ?? false;
}

function quoteSummary(
  message: QuoteMessage,
  labels: {
    attachment: string;
    attachments: (count: number) => string;
    image: string;
    images: (count: number) => string;
    empty: string;
  },
): string {
  const text = normalizePreview(
    formatMessagePartsPreview(message.parts) ??
      unwrapStructuredPreviewContent(message.content) ??
      message.content,
  );
  if (text) return text;

  const attachments = message.attachments ?? [];
  if (attachments.length === 1) {
    const [attachment] = attachments;
    if (attachment) {
      const label = isImageAttachment(attachment) ? labels.image : labels.attachment;
      return attachment.filename ? `${label}: ${attachment.filename}` : label;
    }
  }
  if (attachments.length > 1) {
    const allImages = attachments.every(isImageAttachment);
    return allImages ? labels.images(attachments.length) : labels.attachments(attachments.length);
  }

  return labels.empty;
}

function quoteTypeLabel(type: QuoteMessage["type"], labels: {
  user: string;
  agent: string;
  lark: string;
  system: string;
  unknown: string;
}): string {
  switch (type) {
    case "user":
      return labels.user;
    case "agent":
      return labels.agent;
    case "lark":
      return labels.lark;
    case "system":
      return labels.system;
    default:
      return labels.unknown;
  }
}

function useQuotePresentation(
  message: QuoteMessage,
  currentUserId: string | null,
  ownName?: string,
) {
  const { t } = useT("channels");
  const { getActorName } = useActorName();
  const author = resolveChannelAuthorDisplayName(message, {
    currentUserId,
    ownName,
    getActorName,
  });
  const typeLabel = quoteTypeLabel(message.type, {
    user: t(($) => $.quote.type_user),
    agent: t(($) => $.quote.type_agent),
    lark: t(($) => $.quote.type_lark),
    system: t(($) => $.quote.type_system),
    unknown: t(($) => $.quote.type_unknown),
  });
  const summary = quoteSummary(message, {
    attachment: t(($) => $.quote.attachment_summary),
    attachments: (count) => t(($) => $.quote.attachments_summary, { count }),
    image: t(($) => $.quote.image_summary),
    images: (count) => t(($) => $.quote.images_summary, { count }),
    empty: t(($) => $.quote.empty_summary),
  });
  return { author, typeLabel, summary };
}

export function ComposerQuotePreview({
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
  const { author, typeLabel, summary } = useQuotePresentation(message, currentUserId, ownName);

  return (
    <div className="border-b border-border/35 px-4 py-2" data-testid="composer-quote-preview">
      <div className="flex min-w-0 items-start gap-2 rounded-md border-l-2 border-primary/45 bg-muted/30 px-2.5 py-2">
        <div className="min-w-0 flex-1">
          <div className="flex min-w-0 items-center gap-2">
            <span className="truncate text-xs font-semibold text-foreground">{author}</span>
            <span className="shrink-0 rounded-full bg-background/80 px-1.5 py-0.5 text-[10px] leading-none text-muted-foreground">
              {typeLabel}
            </span>
          </div>
          <p className="mt-0.5 line-clamp-1 text-xs text-muted-foreground">{summary}</p>
        </div>
        <button
          type="button"
          onClick={onCancel}
          className="inline-flex size-6 shrink-0 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-background hover:text-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
          aria-label={t(($) => $.quote.cancel)}
        >
          <X className="size-3.5" />
        </button>
      </div>
    </div>
  );
}

export function InlineMessageQuote({
  replyTo,
  replyToMessageId,
  currentUserId,
  ownName,
  onJump,
}: {
  replyTo?: ChannelMessageReply | null;
  replyToMessageId?: string | null;
  currentUserId: string | null;
  ownName?: string;
  onJump?: (messageId: string) => void;
}) {
  const { t } = useT("channels");
  const fallbackReply = replyTo ?? {
    id: replyToMessageId ?? "",
    type: "user" as const,
    author_id: null,
    author_name: "",
    content: "",
    created_at: "",
  };
  const { author, typeLabel, summary } = useQuotePresentation(fallbackReply, currentUserId, ownName);
  const canJump = !!replyToMessageId && !!replyTo;

  if (!replyToMessageId) return null;

  if (!replyTo) {
    return (
      <div
        data-testid="message-quote-card"
        data-quote-unavailable="true"
        className="mb-2 w-full rounded border-l-2 border-muted-foreground/20 bg-muted/20 px-2 py-1 text-left"
      >
        <p className="truncate text-[11px] font-semibold text-foreground/60">
          {t(($) => $.quote.unavailable_title)}
        </p>
        <p className="line-clamp-1 text-[11px] text-muted-foreground">
          {t(($) => $.quote.unavailable_summary)}
        </p>
      </div>
    );
  }

  return (
    <button
      type="button"
      onClick={() => {
        if (canJump) onJump?.(replyToMessageId);
      }}
      disabled={!canJump}
      data-testid="message-quote-card"
      className={cn(
        "mb-2 w-full rounded border-l-2 border-muted-foreground/30 bg-muted/30 px-2 py-1 text-left transition-opacity",
        canJump ? "cursor-pointer hover:opacity-80" : "cursor-default",
      )}
      aria-label={t(($) => $.quote.jump_to)}
    >
      <span className="flex min-w-0 items-center gap-1.5">
        <span className="truncate text-[11px] font-semibold text-foreground/70">{author}</span>
        <span className="shrink-0 rounded-full bg-background/70 px-1 py-0.5 text-[9px] leading-none text-muted-foreground">
          {typeLabel}
        </span>
      </span>
      <span className="block line-clamp-1 text-[11px] text-muted-foreground">{summary}</span>
    </button>
  );
}
