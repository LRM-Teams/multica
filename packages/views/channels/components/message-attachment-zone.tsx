"use client";

import * as React from "react";
import type { Attachment, MessagePart } from "@multica/core/types";
import { cn } from "@multica/ui/lib/utils";
import { Attachment as AttachmentRenderer } from "../../editor/attachment";
import { AttachmentDownloadProvider } from "../../editor/attachment-download-context";
import { useT } from "../../i18n/use-t";
import {
  resolveGalleryLayout,
  type GalleryLayoutMode,
} from "./message-attachment-gallery";
import {
  resolveAttachmentZoneItems,
  type ResolvedAttachmentItem,
} from "./message-attachment-zone-items";

/**
 * Slack-style attachment zone under a message body: gallery thumbs for images,
 * file tiles for everything else, PRD-safe placeholder when hydration is missing.
 * Never interleaves with text — callers always mount this after the body.
 *
 * LRM-1098: two+ images use an equal-cell cover grid; when measured aspect
 * ratios differ by >2, switch to a single-column stack.
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

  const imageItems: ResolvedAttachmentItem[] = [];
  const otherItems: ResolvedAttachmentItem[] = [];
  for (const item of items) {
    if (item.kind === "record" && isImageAttachment(item.attachment)) {
      imageItems.push(item);
    } else {
      otherItems.push(item);
    }
  }

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
        {imageItems.length >= 2 ? (
          <ImageGallery items={imageItems} />
        ) : imageItems.length === 1 ? (
          <div className="min-w-0 max-w-full">
            <AttachmentSlot item={imageItems[0]!} />
          </div>
        ) : null}

        {otherItems.length > 0 ? (
          <div className="flex min-w-0 flex-col gap-1.5 sm:flex-row sm:flex-wrap sm:items-start">
            {otherItems.map((item) => (
              <div
                key={item.attachmentId}
                className="min-w-0 w-full max-w-full sm:max-w-[340px]"
              >
                <AttachmentSlot item={item} />
              </div>
            ))}
          </div>
        ) : null}
      </div>
    </AttachmentDownloadProvider>
  );
}

function ImageGallery({ items }: { items: ResolvedAttachmentItem[] }) {
  const itemKey = items.map((item) => item.attachmentId).join("|");
  const [tracked, setTracked] = React.useState(() => ({
    key: itemKey,
    aspects: items.map(() => undefined as number | undefined),
  }));

  // Reset measured aspects when the attachment set changes (render-time
  // adjust — avoids a derived-state useEffect that React Doctor blocks).
  if (tracked.key !== itemKey) {
    setTracked({
      key: itemKey,
      aspects: items.map(() => undefined),
    });
  }

  const aspects = tracked.key === itemKey ? tracked.aspects : items.map(() => undefined);
  const layout: GalleryLayoutMode = resolveGalleryLayout(aspects, items.length);
  const rootRef = React.useRef<HTMLDivElement>(null);

  // Parent observes cell <img> natural sizes directly — no child→parent
  // callback-in-effect (react-doctor/no-pass-data-to-parent).
  React.useEffect(() => {
    const root = rootRef.current;
    if (!root) return;

    const cleanups: Array<() => void> = [];

    const readAspects = () => {
      const cells = root.querySelectorAll<HTMLElement>("[data-testid='gallery-cell']");
      const next: Array<number | undefined> = Array.from(cells, (cell) => {
        const img = cell.querySelector("img");
        if (
          img instanceof HTMLImageElement &&
          img.naturalWidth > 0 &&
          img.naturalHeight > 0
        ) {
          return img.naturalWidth / img.naturalHeight;
        }
        return undefined;
      });
      setTracked((prev) => {
        if (prev.key !== itemKey) return prev;
        if (
          prev.aspects.length === next.length &&
          prev.aspects.every((value, index) => value === next[index])
        ) {
          return prev;
        }
        return { key: itemKey, aspects: next };
      });
    };

    const attachImg = (img: HTMLImageElement) => {
      if (img.complete) readAspects();
      else {
        const onLoad = () => readAspects();
        img.addEventListener("load", onLoad, { once: true });
        cleanups.push(() => img.removeEventListener("load", onLoad));
      }
    };

    root.querySelectorAll("img").forEach((node) => {
      if (node instanceof HTMLImageElement) attachImg(node);
    });

    const mo = new MutationObserver(() => {
      root.querySelectorAll("img").forEach((node) => {
        if (node instanceof HTMLImageElement) attachImg(node);
      });
      readAspects();
    });
    mo.observe(root, { childList: true, subtree: true });
    cleanups.push(() => mo.disconnect());

    return () => {
      for (const cleanup of cleanups) cleanup();
    };
  }, [itemKey]);

  return (
    <div
      ref={rootRef}
      data-testid="message-attachment-gallery"
      data-layout={layout}
      data-count={items.length}
      className={cn(
        "message-attachment-gallery min-w-0",
        layout === "grid" ? "gallery-layout-grid" : "gallery-layout-stack",
      )}
    >
      {items.map((item) => (
        <div
          key={item.attachmentId}
          className="gallery-cell min-w-0"
          data-testid="gallery-cell"
        >
          <AttachmentSlot item={item} />
        </div>
      ))}
    </div>
  );
}

function AttachmentSlot({ item }: { item: ResolvedAttachmentItem }) {
  if (item.kind === "record") {
    return (
      <AttachmentRenderer
        attachment={{ kind: "record", attachment: item.attachment }}
        // LRM-285 — message stream: HTML is a file card, never an
        // in-bubble iframe preview (issue comments keep default).
        inlineHtmlPreview={false}
      />
    );
  }
  return <AttachmentUnavailablePlaceholder />;
}

function isImageAttachment(attachment: Attachment): boolean {
  return attachment.content_type?.startsWith("image/") ?? false;
}

function AttachmentUnavailablePlaceholder() {
  const { t } = useT("channels");
  return (
    <span
      data-testid="attachment-unavailable"
      className="inline-flex min-h-9 max-w-full items-center border border-dashed border-border/80 bg-muted/30 px-2.5 py-1.5 text-xs text-muted-foreground"
    >
      {t(($) => $.message.attachment_unavailable)}
    </span>
  );
}
