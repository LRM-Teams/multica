"use client";

import * as React from "react";
import type { Attachment, MessagePart } from "@multica/core/types";
import { cn } from "@multica/ui/lib/utils";
import { Attachment as AttachmentRenderer, AttachmentDownloadProvider } from "../../editor";
import { useT } from "../../i18n/use-t";
import {
  resolveAttachmentZoneItems,
  type ResolvedAttachmentItem,
} from "./message-attachment-zone-items";

/**
 * Slack-style attachment zone under a message body: gallery thumbs for images,
 * file tiles for everything else, PRD-safe placeholder when hydration is missing.
 * Never interleaves with text — callers always mount this after the body.
 */
export function MessageAttachmentZone({
  parts,
  attachments,
  className,
  compact = false,
}: {
  parts?: MessagePart[] | null;
  attachments?: Attachment[] | null;
  className?: string;
  compact?: boolean;
}) {
  const items = React.useMemo(
    () => resolveAttachmentZoneItems(parts, attachments),
    [parts, attachments],
  );

  if (items.length === 0) return null;

  const records = items
    .filter((item): item is Extract<ResolvedAttachmentItem, { kind: "record" }> => item.kind === "record")
    .map((item) => item.attachment);

  return (
    <AttachmentDownloadProvider attachments={records}>
      <div
        data-testid="message-attachment-zone"
        data-compact={compact ? "true" : undefined}
        className={cn(
          "mt-1.5 flex min-w-0 flex-col gap-1.5",
          compact && "max-h-16 overflow-hidden opacity-80",
          className,
        )}
      >
        <div className="flex min-w-0 flex-col gap-1.5 sm:flex-row sm:flex-wrap sm:items-start">
          {items.map((item) =>
            item.kind === "record" ? (
              <div
                key={item.attachmentId}
                className={cn(
                  "min-w-0",
                  isImageAttachment(item.attachment)
                    ? "max-w-full"
                    : "w-full max-w-full sm:max-w-[340px]",
                )}
              >
                <AttachmentRenderer
                  attachment={{ kind: "record", attachment: item.attachment }}
                  // LRM-285 — message stream: HTML is a file card, never an
                  // in-bubble iframe preview (issue comments keep default).
                  inlineHtmlPreview={false}
                />
              </div>
            ) : (
              <AttachmentUnavailablePlaceholder key={item.attachmentId} />
            ),
          )}
        </div>
      </div>
    </AttachmentDownloadProvider>
  );
}

function isImageAttachment(attachment: Attachment): boolean {
  return attachment.content_type?.startsWith("image/") ?? false;
}

function AttachmentUnavailablePlaceholder() {
  const { t } = useT("channels");
  return (
    <span
      data-testid="attachment-unavailable"
      className="inline-flex min-h-9 max-w-full items-center rounded-md border border-dashed border-border/80 bg-muted/30 px-2.5 py-1.5 text-xs text-muted-foreground"
    >
      {t(($) => $.message.attachment_unavailable)}
    </span>
  );
}
