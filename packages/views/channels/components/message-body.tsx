"use client";

import type { ChannelMessage } from "@multica/core/types";
import { MemoizedMarkdown } from "../../common/markdown";
import { MessagePartsRenderer } from "./message-parts-renderer";
import { MessageAttachmentZone } from "./message-attachment-zone";
import { collectAttachmentParts } from "./message-attachment-zone-items";
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
 * Attachments always render in a Slack-style zone UNDER the text/sticker body
 * (never interleaved). Attachment parts are the source of truth; the zone
 * hydrates `attachments[]` by id.
 *
 * `compact` is the lightweight parent/root-header variant: a sticker-bearing
 * message renders its parts in a height-capped box (matching the full bubble),
 * while sticker-free content collapses to a single clamped preview line so the
 * header stays short. Attachment zone stays light (height-capped) in compact mode.
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
  const hasAttachmentParts = collectAttachmentParts(effectiveParts).length > 0;

  const body = (() => {
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
      // Attachment-only compact messages have no text body chrome.
      if (!compactBody && hasAttachmentParts) {
        return null;
      }
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

    if (effectiveParts) {
      // Text + sticker only in the body stream; attachment parts are rendered
      // exclusively by MessageAttachmentZone under the body.
      const bodyParts = effectiveParts.filter((part) => part.type !== "attachment");
      const hasBodyContent = bodyParts.some(
        (part) =>
          (part.type === "text" && part.text.trim()) || part.type === "sticker",
      );
      if (!hasBodyContent) return null;
      return <MessagePartsRenderer parts={bodyParts} highlightQuery={highlightQuery} />;
    }

    return (
      <MemoizedMarkdown
        attachments={attachments}
        highlightQuery={highlightQuery}
        enableStickerShortcodes={false}
      >
        {content}
      </MemoizedMarkdown>
    );
  })();

  const zone = hasAttachmentParts ? (
    <MessageAttachmentZone
      parts={effectiveParts}
      attachments={attachments}
      compact={compact}
    />
  ) : null;

  if (!body && !zone) {
    // Empty message with neither body nor zone — still render nothing useful.
    // Callers (tombstones etc.) handle truly empty cases elsewhere.
    if (!content && !effectiveParts?.length) return null;
    // Legacy empty-parts envelope path: fall through to markdown of content.
    if (!effectiveParts) {
      return (
        <MemoizedMarkdown
          attachments={attachments}
          highlightQuery={highlightQuery}
          enableStickerShortcodes={false}
        >
          {content}
        </MemoizedMarkdown>
      );
    }
    return null;
  }

  return (
    <>
      {body}
      {zone}
    </>
  );
}
