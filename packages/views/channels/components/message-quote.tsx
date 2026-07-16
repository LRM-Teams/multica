"use client";

import { X } from "lucide-react";
import type { Attachment, ChannelMessage, ChannelMessageQuote, ChannelMessageReply } from "@multica/core/types";
import { cn } from "@multica/ui/lib/utils";
import { useActorName } from "@multica/core/workspace/hooks";
import { useT } from "../../i18n/use-t";
import {
  mentionResolverFrom,
  projectReferencesToText,
  resolveChannelAuthorDisplayName,
  type MentionPreviewResolver,
} from "./message-preview";
import {
  formatMessagePartsPreview,
  unwrapStructuredPreviewContent,
} from "./message-parts-preview";
import type { QuoteTarget } from "./message-quote-types";

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

function isImageContentType(contentType: string | undefined): boolean {
  return contentType?.startsWith("image/") ?? false;
}

/**
 * Build a quote/list summary for attachment-only (or attachment-heavy) messages.
 * Prefers attachment *parts* order + hydration; falls back to `attachments[]`
 * for historical rows that never got attachment parts.
 */
function attachmentQuoteSummary(
  message: Pick<QuoteMessage, "parts" | "attachments">,
  labels: {
    attachment: string;
    attachments: (count: number) => string;
    image: string;
    images: (count: number) => string;
  },
): string | null {
  const byId = new Map((message.attachments ?? []).map((a) => [a.id, a]));
  const attachmentParts = (message.parts ?? []).filter(
    (part): part is Extract<NonNullable<QuoteMessage["parts"]>[number], { type: "attachment" }> =>
      part.type === "attachment" && !!part.attachment_id,
  );

  type SummaryItem = {
    isImage: boolean;
    filename?: string;
  };

  let items: SummaryItem[] = [];
  if (attachmentParts.length > 0) {
    items = attachmentParts.map((part) => {
      const record = byId.get(part.attachment_id);
      return {
        isImage: isImageContentType(record?.content_type ?? part.content_type),
        // Only surface filename when hydrated (PRD: no leak on denied/missing).
        filename: record?.filename,
      };
    });
  } else if ((message.attachments?.length ?? 0) > 0) {
    items = (message.attachments ?? []).map((attachment) => ({
      isImage: isImageAttachment(attachment),
      filename: attachment.filename,
    }));
  }

  if (items.length === 0) return null;

  if (items.length === 1) {
    const [item] = items;
    if (!item) return null;
    const label = item.isImage ? labels.image : labels.attachment;
    return item.filename ? `${label}: ${item.filename}` : label;
  }

  const allImages = items.every((item) => item.isImage);
  return allImages ? labels.images(items.length) : labels.attachments(items.length);
}

function quoteSummary(
  message: Pick<QuoteMessage, "content" | "parts" | "attachments">,
  labels: {
    attachment: string;
    attachments: (count: number) => string;
    image: string;
    images: (count: number) => string;
    empty: string;
  },
  resolveMention: MentionPreviewResolver,
): string {
  const text = normalizePreview(
    // Project the reference spans first (#530). Without this, a quoted message
    // whose mention lives in `parts` (post-#463) produces nothing here and falls
    // through to raw `content` — quoting a message that @'d someone would show
    // `@actor_14` while the message itself reads `@小雅`.
    projectReferencesToText(message.content, message.parts, resolveMention) ??
      formatMessagePartsPreview(message.parts) ??
      unwrapStructuredPreviewContent(message.content) ??
      message.content,
  );
  if (text) return text;

  return (
    attachmentQuoteSummary(message, labels) ?? labels.empty
  );
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
  const summary = quoteSummary(
    message,
    {
      attachment: t(($) => $.quote.attachment_summary),
      attachments: (count) => t(($) => $.quote.attachments_summary, { count }),
      image: t(($) => $.quote.image_summary),
      images: (count) => t(($) => $.quote.images_summary, { count }),
      empty: t(($) => $.quote.empty_summary),
    },
    mentionResolverFrom(getActorName),
  );
  return { author, typeLabel, summary };
}

function messageFromSnapshot(quote: ChannelMessageQuote): QuoteMessage | null {
  if (!quote.snapshot) return null;
  return {
    id: quote.messageId,
    type: quote.snapshot.type,
    author_id: quote.snapshot.authorId ?? null,
    author_name: quote.snapshot.authorName,
    content: quote.snapshot.content,
    parts: quote.snapshot.parts,
    created_at: quote.snapshot.createdAt,
  };
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
  const { t } = useT("channels");
  const { getActorName } = useActorName();

  return (
    <div
      className="flex items-start gap-2 border-b border-border/35 bg-muted/25 px-3 py-2"
      data-testid="composer-quote-preview"
    >
      <div className="min-w-0 flex-1 border-l-2 border-primary/45 pl-2">
        <p className="truncate text-xs font-medium text-foreground/80">{quote.author_name}</p>
        <p className="line-clamp-2 text-xs leading-5 text-muted-foreground">
          {quoteSummary(
            quote,
            {
              attachment: t(($) => $.quote.attachment_summary),
              attachments: (count) => t(($) => $.quote.attachments_summary, { count }),
              image: t(($) => $.quote.image_summary),
              images: (count) => t(($) => $.quote.images_summary, { count }),
              empty: t(($) => $.quote.empty_summary),
            },
            mentionResolverFrom(getActorName),
          )}
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

function AvailableMessageQuoteCard({
  message,
  quoteMessageId,
  currentUserId,
  ownName,
  onJump,
}: {
  message: QuoteMessage;
  quoteMessageId: string;
  currentUserId: string | null;
  ownName?: string;
  onJump?: (messageId: string) => void;
}) {
  const { t } = useT("channels");
  const presentation = useQuotePresentation(message, currentUserId, ownName);
  const canJump = !!onJump;

  return (
    <button
      type="button"
      onClick={() => {
        if (canJump) onJump?.(quoteMessageId);
      }}
      disabled={!canJump}
      data-testid="message-quote-card"
      data-quote-status="active"
      className={cn(
        "mb-2 w-full rounded border-l-2 border-muted-foreground/30 bg-muted/30 px-2 py-1 text-left transition-opacity",
        canJump ? "cursor-pointer hover:opacity-80" : "cursor-default",
      )}
      aria-label={t(($) => $.quote.jump_to)}
    >
      <span className="flex min-w-0 items-center gap-1.5">
        <span className="truncate text-[11px] font-semibold text-foreground/70">{presentation.author}</span>
        <span className="shrink-0 rounded-full bg-background/70 px-1 py-0.5 text-[9px] leading-none text-muted-foreground">
          {presentation.typeLabel}
        </span>
      </span>
      <span className="block line-clamp-1 text-[11px] text-muted-foreground">{presentation.summary}</span>
    </button>
  );
}

export function MessageQuoteCard({
  quote,
  quoteMessageId,
  currentUserId,
  ownName,
  onJump,
}: {
  quote?: ChannelMessageQuote | null;
  quoteMessageId?: string | null;
  currentUserId: string | null;
  ownName?: string;
  onJump?: (messageId: string) => void;
}) {
  const { t } = useT("channels");
  const message = quote ? messageFromSnapshot(quote) : null;

  if (!quoteMessageId) return null;

  if (!quote || !message || quote.status !== "active") {
    return (
      <div
        data-testid="message-quote-card"
        data-quote-status={quote?.status ?? "inaccessible"}
        className="mb-2 w-full rounded border-l-2 border-dashed border-muted-foreground/25 bg-muted/20 px-2 py-1 text-left"
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
    <AvailableMessageQuoteCard
      message={message}
      quoteMessageId={quoteMessageId}
      currentUserId={currentUserId}
      ownName={ownName}
      onJump={onJump}
    />
  );
}
