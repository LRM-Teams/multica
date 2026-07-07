"use client";

import type { ChannelMessage } from "@multica/core/types";
import { MemoizedMarkdown } from "../../common/markdown";
import { MessagePartsRenderer } from "./message-parts-renderer";
import {
  formatMessagePartsPreview,
  resolveMessageParts,
  unwrapStructuredPreviewContent,
} from "./message-parts-preview";

/**
 * Shared renderer for a channel/thread/DM message body. Resolves the message's
 * structured parts (own `parts`, or a historical structured-action envelope
 * unwrapped from `content`) and renders them through `MessagePartsRenderer` — so
 * a sticker shows as an IMAGE wherever the same message appears (channel bubble,
 * thread root preview, DM parent), never as a flattened `[Sticker] …` label.
 *
 * `compact` is the lightweight parent/root-header variant: a sticker-bearing
 * message renders its parts in a height-capped box (matching the full bubble),
 * while sticker-free content collapses to a single clamped preview line so the
 * header stays short.
 */
export function MessageBody({
  content,
  parts,
  attachments,
  highlightQuery,
  compact = false,
}: {
  content: string;
  parts?: ChannelMessage["parts"];
  attachments?: ChannelMessage["attachments"];
  highlightQuery?: string;
  compact?: boolean;
}) {
  const effectiveParts = resolveMessageParts(content, parts);

  if (compact) {
    // A sticker-bearing message renders its parts (image + any text) height-
    // capped; sticker-free content stays a clamped text preview so a long agent
    // message never expands the parent header.
    if (effectiveParts?.some((part) => part.type === "sticker")) {
      return (
        <div className="max-h-40 overflow-hidden">
          <MessagePartsRenderer parts={effectiveParts} />
        </div>
      );
    }
    const compactBody =
      formatMessagePartsPreview(effectiveParts) ?? unwrapStructuredPreviewContent(content);
    return (
      <div className="line-clamp-3">
        {compactBody ? (
          <span>{compactBody}</span>
        ) : (
          <MemoizedMarkdown attachments={attachments} enableStickerShortcodes={false}>
            {content}
          </MemoizedMarkdown>
        )}
      </div>
    );
  }

  return effectiveParts ? (
    <MessagePartsRenderer parts={effectiveParts} highlightQuery={highlightQuery} />
  ) : (
    <MemoizedMarkdown
      attachments={attachments}
      highlightQuery={highlightQuery}
      enableStickerShortcodes={false}
    >
      {content}
    </MemoizedMarkdown>
  );
}
