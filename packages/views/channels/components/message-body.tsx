"use client";

import { memo } from "react";
import type { ChannelMessage } from "@multica/core/types";
import { useActorName } from "@multica/core/workspace/hooks";
import { MemoizedMarkdown } from "../../common/markdown";
import { mentionResolverFrom, projectReferencesToText } from "./message-preview";
import { InlineReferenceContent } from "../../common/inline-reference-content";
import { MessagePartsRenderer } from "./message-parts-renderer";
import { MessageAttachmentZone } from "./message-attachment-zone";
import { collectAttachmentParts } from "./message-attachment-zone-items";
import {
  formatMessagePartsPreview,
  resolveMessageParts,
  unwrapStructuredPreviewContent,
} from "./message-parts-preview";
import { areMessageBodyPropsEqual } from "./channel-message-render-equality";

type MessageBodyContentMode = "all" | "transcript" | "non-transcript";

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
// react-doctor-disable-next-line react-doctor/no-multi-comp -- memo export keeps MessageBody name; Inner is file-local for equality compare
function MessageBodyInner({
  content,
  parts,
  attachments,
  highlightQuery,
  compact = false,
  sourceMessageId,
  consumedAttachmentIds,
  contentMode = "all",
  choiceContext,
}: {
  content: string;
  parts?: ChannelMessage["parts"];
  attachments?: ChannelMessage["attachments"];
  highlightQuery?: string;
  compact?: boolean;
  sourceMessageId?: string;
  consumedAttachmentIds?: readonly string[];
  contentMode?: MessageBodyContentMode;
  choiceContext?: { channelId: string; messageId: string };
}) {
  const { getActorName } = useActorName();
  const resolveMentionPreview = mentionResolverFrom(getActorName);
  const resolvedParts = resolveMessageParts(content, parts);
  const consumedAttachmentIdSet = new Set(consumedAttachmentIds ?? []);
  const effectiveParts = resolvedParts?.filter(
    (part) =>
      part.type !== "attachment" ||
      !consumedAttachmentIdSet.has(part.attachment_id),
  );
  const presentedParts = effectiveParts?.filter((part) => {
    if (contentMode === "all") return true;
    const isTranscriptPart = part.type === "text" || part.type === "reference";
    return contentMode === "transcript"
      ? isTranscriptPart
      : !isTranscriptPart && part.type !== "voice";
  });
  const hasAttachmentParts = collectAttachmentParts(presentedParts).length > 0;
  const hasHiringProposal =
    presentedParts?.some(
      (part) => part.type === "reference" && part.ref_type === "agent:create",
    ) ?? false;
  const suppressHiringProtocolFallback =
    hasHiringProposal && /^\s*\[agent:create proposal\](?:\s|$)/u.test(content);

  const body = (() => {
    if (compact) {
      // A sticker-bearing message renders its parts (image + any text) height-
      // capped; sticker-free content stays a clamped text preview so a long agent
      // message never expands the parent header.
      if (presentedParts?.some((part) => part.type === "sticker" || part.type === "choice" || part.type === "note_brief")) {
        return (
          <div className="max-h-40 overflow-hidden">
            <MessagePartsRenderer parts={presentedParts} choiceContext={choiceContext} />
          </div>
        );
      }
      if (contentMode === "non-transcript") return null;
      if (suppressHiringProtocolFallback) {
        return null;
      }
      // Project reference spans first (#530): post-#463 a mention lives in `parts`
      // with a span into `content`, so formatMessagePartsPreview yields nothing and
      // the raw `content` below would render the internal handle — a thread root or
      // parent header would read `@actor_14` while the message itself reads `@小雅`.
      const compactBody =
        projectReferencesToText(
          content,
          contentMode === "all" ? parts : presentedParts,
          resolveMentionPreview,
        ) ??
        formatMessagePartsPreview(presentedParts) ??
        unwrapStructuredPreviewContent(content);
      // Attachment-only compact messages have no text body chrome.
      if (!compactBody && hasAttachmentParts) {
        return null;
      }
      return (
        <div className="line-clamp-3">
          {compactBody ? (
            <span>{compactBody}</span>
          ) : (
            <MemoizedMarkdown attachments={attachments} enableStickerShortcodes={false} mentionVariant="plain">
              {content}
            </MemoizedMarkdown>
          )}
        </div>
      );
    }

    if (presentedParts) {
      // Text + sticker only in the body stream; attachment parts are rendered
      // exclusively by MessageAttachmentZone under the body.
      const bodyParts = presentedParts.filter((part) => part.type !== "attachment");
      const hasReferenceParts = bodyParts.some((part) => part.type === "reference");
      // Reference parts (#463 structured mentions / issue-refs) are overlays on
      // the canonical `content`, so a message can have reference-only `parts` yet
      // carry its full text in `content` (e.g. agent @mentions). Treat that
      // as body content so it renders through InlineReferenceContent below instead
      // of collapsing to an empty bubble.
      const hasNoteBrief = bodyParts.some((part) => part.type === "note_brief");
      const hasBodyContent =
        bodyParts.some(
          (part) =>
            (part.type === "text" && part.text.trim()) ||
            part.type === "sticker" ||
            part.type === "choice" ||
            part.type === "choice_reply" ||
            part.type === "note_brief" ||
            part.type === "period_brief_insert",
        ) || (hasReferenceParts && content.trim() !== "" && !suppressHiringProtocolFallback);
      if (!hasBodyContent && !hasHiringProposal) return null;
      // Structured mention / issue-ref parts (#463): the canonical `content` now
      // carries bare `@Label` / `MUL-123` text with the refs anchored to spans,
      // so render the text body through the shared inline-reference projector —
      // that's what turns those bare tokens back into hover-card mentions +
      // issue links (the bare-text migration window dropped them). Stickers still
      // render as images alongside. No refs → the existing parts renderer.
      if (hasReferenceParts) {
        const stickerChoiceAndCardParts = bodyParts.filter(
          (part) =>
            part.type === "sticker" ||
            part.type === "choice" ||
            part.type === "choice_reply" ||
            part.type === "note_brief" ||
            (part.type === "reference" && part.ref_type === "agent:create"),
        );
        return (
          <>
            {!suppressHiringProtocolFallback ? (
              <InlineReferenceContent
                content={content}
                parts={presentedParts}
                highlightQuery={highlightQuery}
                sourceMessageId={sourceMessageId}
                mentionVariant="plain"
              />
            ) : null}
            {stickerChoiceAndCardParts.length > 0 && (
              <MessagePartsRenderer parts={stickerChoiceAndCardParts} choiceContext={choiceContext} />
            )}
          </>
        );
      }
      // note_brief-only parts keep instruction text in `content` (no text part).
      if (hasNoteBrief && !bodyParts.some((part) => part.type === "text" || part.type === "sticker" || part.type === "choice" || part.type === "choice_reply")) {
        return (
          <>
            {content.trim() ? (
              <MemoizedMarkdown
                attachments={attachments}
                highlightQuery={highlightQuery}
                enableStickerShortcodes={false}
                sourceMessageId={sourceMessageId}
                mentionVariant="plain"
              >
                {content}
              </MemoizedMarkdown>
            ) : null}
            <MessagePartsRenderer
              parts={bodyParts.filter((part) => part.type === "note_brief")}
              highlightQuery={highlightQuery}
              choiceContext={choiceContext}
            />
          </>
        );
      }
      return (
        <MessagePartsRenderer
          parts={bodyParts}
          highlightQuery={highlightQuery}
          choiceContext={choiceContext}
        />
      );
    }

    if (contentMode !== "all") return null;
    return (
      <MemoizedMarkdown
        attachments={attachments}
        highlightQuery={highlightQuery}
        enableStickerShortcodes={false}
        sourceMessageId={sourceMessageId}
        mentionVariant="plain"
      >
        {content}
      </MemoizedMarkdown>
    );
  })();

  const zone = hasAttachmentParts ? (
    <MessageAttachmentZone
      parts={presentedParts}
      attachments={attachments}
      compact={compact}
    />
  ) : null;

  if (!body && !zone) {
    // Empty message with neither body nor zone — still render nothing useful.
    // Callers (tombstones etc.) handle truly empty cases elsewhere.
    if (!content && !presentedParts?.length) return null;
    // Legacy empty-parts envelope path: fall through to markdown of content.
    if (!presentedParts && contentMode === "all") {
      return (
        <MemoizedMarkdown
          attachments={attachments}
          highlightQuery={highlightQuery}
          enableStickerShortcodes={false}
          sourceMessageId={sourceMessageId}
          mentionVariant="plain"
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

export const MessageBody = memo(MessageBodyInner, areMessageBodyPropsEqual);
