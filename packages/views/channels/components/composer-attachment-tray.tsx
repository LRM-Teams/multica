"use client";

import { Download, FileIcon, Loader2, RotateCcw, Trash2, X } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@multica/ui/components/ui/popover";
import { Tooltip, TooltipTrigger, TooltipContent } from "@multica/ui/components/ui/tooltip";
import { cn } from "@multica/ui/lib/utils";
import { useDownloadAttachment } from "../../editor/use-download-attachment";
import { formatFileSize, getFileExtension } from "../../editor/utils/file-meta";
import { useT } from "../../i18n/use-t";
import type { PendingAttachment } from "../hooks/use-composer-pending-attachments";

export type ComposerAttachmentTrayProps = {
  pending: PendingAttachment[];
  onRemove: (localId: string) => void;
  onRetry: (localId: string) => void;
  /**
   * Web responsive “mobile” (useIsMobile / coarse pointer layout), not Expo.
   * Enlarges touch targets and always shows remove (no hover-only chrome).
   */
  isMobile?: boolean;
  className?: string;
};

function isImagePending(item: PendingAttachment): boolean {
  return item.contentType.startsWith("image/");
}

function imageMeta(item: PendingAttachment): string {
  const type = getFileExtension(item.filename).toUpperCase() || "IMAGE";
  return [type, formatFileSize(item.sizeBytes)].filter(Boolean).join(" · ");
}

/**
 * Slack-style composer tray: a **single horizontal strip** of pending
 * thumbs/chips above the editor. Never stacks as a vertical list.
 *
 * Mobile web: same one-row model + horizontal pan; larger hit targets; remove
 * actions use 40px touch targets inside the image detail popover.
 *
 * Images stay as clean thumbnails in the tray. Clicking one opens a compact,
 * anchored popover with the contained image, filename, file metadata, download,
 * and remove actions. Non-image and stale attachments keep the compact file
 * chip with its overflow-corner remove control.
 *
 * Spec: docs/superpowers/specs/2026-07-13-chat-attachment-presentation-design.md
 */
export function ComposerAttachmentTray({
  pending,
  onRemove,
  onRetry,
  isMobile = false,
  className,
}: ComposerAttachmentTrayProps) {
  const { t } = useT("channels");
  const { t: editorT } = useT("editor");
  const download = useDownloadAttachment();

  if (pending.length === 0) return null;

  // Image thumbs and file chip outer height stay aligned.
  const thumb = isMobile ? "size-14" : "size-12";
  const chipH = isMobile ? "h-14" : "h-12";
  // File-chip recovery controls retain their existing responsive sizes.
  const iconBtn = isMobile ? "size-9" : "size-6";
  const iconGlyph = isMobile ? "size-3.5" : "size-2.5";

  return (
    <>
      {/* Native list semantics (prefer-tag-over-role). list-none keeps layout
          as a horizontal strip, not a document bullet list. */}
      <ul
        className={cn(
          // One row only + horizontal scroll. flex-wrap was wrapping full-width
          // chips into a vertical stack — that is explicitly forbidden here.
          "m-0 flex list-none flex-row flex-nowrap items-center gap-3 overflow-x-auto overflow-y-hidden overscroll-x-contain p-0 pb-0.5",
          // LRM-1180: reserve the overflow corner (clip region is the padding
          // box) and hand the reserved top space back with a negative margin.
          "-mt-2 pr-2 pt-2",
          // Touch: allow horizontal pan without fighting vertical page scroll.
          "touch-pan-x momentum-scroll [scrollbar-width:thin]",
          className,
        )}
        data-slot="composer-attachment-tray"
        data-testid="composer-attachment-tray"
        data-mobile={isMobile ? "true" : undefined}
      >
        {pending.map((item) => {
          const removeLabel = t(($) => $.composer.tray_remove_aria, {
            filename: item.filename,
          });
          const retryLabel = t(($) => $.composer.tray_retry_aria, {
            filename: item.filename,
          });
          const previewLabel = t(($) => $.composer.tray_preview_aria, {
            filename: item.filename,
          });
          const showImage = isImagePending(item) && !!item.previewUrl;
          const canDownload = item.status === "ready" && !!item.attachmentId;

          return (
            <li
              key={item.localId}
              data-testid={`composer-tray-item-${item.localId}`}
              data-status={item.status}
              data-kind={showImage ? "image" : "file"}
              className={cn(
                "group relative flex w-fit max-w-[11rem] shrink-0 list-none flex-row items-center gap-1.5 rounded-lg border border-border/50 bg-muted/35",
                chipH,
                showImage ? cn(thumb, "max-w-none p-0") : "min-w-0 pl-2 pr-3",
                item.status === "error" && "border-destructive/50 bg-destructive/5",
              )}
            >
              {showImage ? (
                <Popover>
                  <PopoverTrigger
                    render={
                      <button
                        type="button"
                        className={cn(
                          thumb,
                          "relative block cursor-zoom-in overflow-hidden rounded-[7px] p-0 outline-none focus-visible:ring-2 focus-visible:ring-ring",
                        )}
                        aria-label={previewLabel}
                        data-testid={`composer-tray-zoom-${item.localId}`}
                      >
                        {/* Blob/remote tray preview only; shared package cannot use next/image.
                            react-doctor-disable-next-line react-doctor/nextjs-no-img-element */}
                        <img
                          src={item.previewUrl}
                          alt={item.filename}
                          className={cn(thumb, "rounded-[7px] object-cover")}
                          draggable={false}
                        />
                      </button>
                    }
                  />
                  <PopoverContent
                    side="top"
                    align="start"
                    sideOffset={8}
                    data-testid={`composer-image-popover-${item.localId}`}
                    className="w-[min(28.75rem,calc(100vw-1.5rem))] gap-0 overflow-hidden p-0"
                  >
                    <div className="flex aspect-[16/10] max-h-[38dvh] min-h-0 items-center justify-center bg-muted/50 p-3">
                      {/* Local blob or uploaded URL; shared package cannot use next/image.
                          react-doctor-disable-next-line react-doctor/nextjs-no-img-element */}
                      <img
                        src={item.previewUrl}
                        alt={item.filename}
                        className="max-h-full max-w-full rounded-md object-contain shadow-sm"
                        draggable={false}
                      />
                    </div>
                    <div className="flex min-h-14 items-center gap-3 px-3 py-2">
                      <div className="min-w-0 flex-1">
                        <p className="truncate text-sm font-medium text-foreground">
                          {item.filename}
                        </p>
                        <p className="text-xs text-muted-foreground">{imageMeta(item)}</p>
                      </div>
                      <div className="flex shrink-0 items-center gap-1">
                        <Button
                          type="button"
                          variant="ghost"
                          size="icon"
                          className={cn(isMobile ? "size-10" : "size-8")}
                          aria-label={`${editorT(($) => $.image.download)} ${item.filename}`}
                          disabled={!canDownload}
                          onClick={() => {
                            if (item.attachmentId) void download(item.attachmentId);
                          }}
                        >
                          <Download className="size-4" />
                        </Button>
                        <Button
                          type="button"
                          variant="ghost"
                          size="icon"
                          className={cn(
                            isMobile ? "size-10" : "size-8",
                            "hover:bg-destructive/10 hover:text-destructive",
                          )}
                          aria-label={removeLabel}
                          onClick={() => onRemove(item.localId)}
                        >
                          <Trash2 className="size-4" />
                        </Button>
                      </div>
                    </div>
                  </PopoverContent>
                </Popover>
              ) : (
                <FileIcon
                  className="size-3.5 shrink-0 text-muted-foreground"
                  aria-hidden
                />
              )}

              {!showImage ? (
                <div className="min-w-0 flex-1">
                  <Tooltip>
                    <TooltipTrigger
                      render={
                        <p className="truncate text-xs font-medium leading-tight text-foreground" />
                      }
                    >
                      {item.filename}
                    </TooltipTrigger>
                    <TooltipContent side="top">{item.filename}</TooltipContent>
                  </Tooltip>
                  {item.status === "error" ? (
                    <p className="truncate text-[10px] leading-tight text-destructive">
                      {item.errorMessage || t(($) => $.composer.tray_upload_failed)}
                    </p>
                  ) : item.status === "stale" ? (
                    <p className="truncate text-[10px] leading-tight text-muted-foreground">
                      {t(($) => $.composer.tray_reselect)}
                    </p>
                  ) : item.status === "uploading" ? (
                    <p className="truncate text-[10px] leading-tight text-muted-foreground">
                      {t(($) => $.composer.tray_uploading)}
                    </p>
                  ) : null}
                </div>
              ) : null}

              {item.status === "uploading" ? (
                <div
                  className={cn(
                    "pointer-events-none flex items-center justify-center",
                    showImage
                      ? "absolute inset-0 z-10 rounded-lg bg-background/55"
                      : "shrink-0",
                  )}
                  aria-hidden
                >
                  <Loader2 className="size-3.5 animate-spin text-muted-foreground" />
                </div>
              ) : null}

              {showImage && item.status === "error" ? (
                <div className="absolute inset-x-0 bottom-0 rounded-b-lg bg-destructive/90 px-0.5 py-0.5 text-center text-[9px] leading-none text-destructive-foreground">
                  {t(($) => $.composer.tray_upload_failed)}
                </div>
              ) : null}

              {/* Retry remains centered over a failed image or inline in a file
                  chip. Image removal lives in the detail popover. */}
              {showImage && item.status === "error" ? (
                <Button
                  type="button"
                  variant="secondary"
                  size="icon"
                  className="absolute left-1/2 top-1/2 z-20 size-6 -translate-x-1/2 -translate-y-1/2 rounded-full bg-background/95 shadow-sm"
                  aria-label={retryLabel}
                  onClick={() => onRetry(item.localId)}
                >
                  <RotateCcw className="size-3" />
                </Button>
              ) : null}

              {!showImage && item.status === "error" ? (
                <Button
                  type="button"
                  variant="secondary"
                  size="icon"
                  className={cn(iconBtn, "shrink-0 bg-background/95 shadow-sm")}
                  aria-label={retryLabel}
                  onClick={() => onRetry(item.localId)}
                >
                  <RotateCcw className={iconGlyph} />
                </Button>
              ) : null}

              {/* File and stale chips retain the compact overflow-corner remove.
                  Images intentionally have no inline remove chrome. */}
              {!showImage ? (
                <div className="absolute -right-2 -top-2 z-30 flex shrink-0 items-center">
                  <Button
                    type="button"
                    variant="secondary"
                    size="icon"
                    className={cn(
                      "relative size-5 rounded-full border border-border bg-background/95 shadow-sm",
                      'after:absolute after:-inset-0.5 after:content-[""]',
                      "opacity-100",
                    )}
                    aria-label={removeLabel}
                    onClick={() => onRemove(item.localId)}
                  >
                    <X className="size-3" />
                  </Button>
                </div>
              ) : null}
            </li>
          );
        })}
      </ul>
    </>
  );
}
