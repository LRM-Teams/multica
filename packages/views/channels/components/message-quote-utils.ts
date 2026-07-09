import type { ChannelMessage } from "@multica/core/types";
import { formatMessagePartsPreview, unwrapStructuredPreviewContent } from "./message-parts-preview";
import type { QuotePreviewSource, QuoteSource } from "./message-quote-types";

function attachmentSummary(attachments: ChannelMessage["attachments"] | undefined) {
  if (!attachments || attachments.length === 0) return null;
  if (attachments.length === 1) return attachments[0]?.filename ?? "Attachment";
  return `${attachments.length} attachments`;
}

export function getMessageQuotePreview(source: QuotePreviewSource) {
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
