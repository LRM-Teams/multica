"use client";

import { useEffect, useRef } from "react";
import { FileIcon, Loader2, RotateCcw, X, ZoomIn } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { Tooltip, TooltipTrigger, TooltipContent } from "@multica/ui/components/ui/tooltip";
import { cn } from "@multica/ui/lib/utils";
import { useAttachmentPreview } from "../../editor/attachment-preview-modal";
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

/**
 * Slack-style composer tray: a **single horizontal strip** of pending
 * thumbs/chips above the editor. Never stacks as a vertical list.
 *
 * Mobile web: same one-row model + horizontal pan; larger hit targets; remove
 * always visible (no hover-gated controls).
 *
 * LRM-1180 (frozen design v2 on parent LRM-1150) — two changes, both about the
 * remove button no longer smothering the thumb it belongs to:
 *
 *  1. The image remove button is 20px (`size-5`) and sits in the **overflow
 *     corner** (`-right-2 -top-2`), so only a 12×12 sliver overlaps the thumb:
 *     41.3% → 4.6% occlusion on mobile (36px in-image button on a 56px thumb),
 *     25.0% → 6.25% on desktop. `after:-inset-0.5` restores a 24px pointer
 *     target so WCAG SC 2.5.8 still passes at a 20px visual size.
 *     LRM-1228 extends the same corner rule to file/stale chips: they were the
 *     other half of “手机端 button 太大” (a 36px `size-9` in-chip button on
 *     mobile web). Nothing sits under the button there, but it ate ~42px of a
 *     176px chip; moving it out gives the filename 98px → 136px and the visual
 *     drops 36px → 20px. The chip reserves `pr-3` (the outdented button's inner
 *     half is 12px) so `truncate` text never runs underneath. Retry keeps the
 *     layout — centered on a thumb, inline in a chip — so only remove outdents.
 *  2. The thumb itself becomes the preview entry point (`<button>` + Enter /
 *     Space), reusing the shared `useAttachmentPreview()` modal rather than
 *     introducing a second lightbox. Desktop hover/keyboard-focus reveals a
 *     centered zoom glyph; the overflow corner leaves room for it (a centered
 *     24px block and an in-image 24px corner button used to overlap 12×12).
 *
 * Both depend on the tray reserving `pt-2 pr-2` (the `<ul>` has
 * `overflow-y-hidden`, whose clip region is the *padding* box — without it the
 * outdented button's top half is cut off) and `gap-3` (gap-2 == the 8px
 * outdent, so the button would touch the next item). `-mt-2` gives the
 * reserved top padding back to the layout so the composer doesn't grow.
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
  const preview = useAttachmentPreview();

  // Focus return: the shared handle exposes no onClose, so the tray watches
  // `modal` flipping back to null and restores focus to the thumb that opened
  // it. Keyboard users would otherwise land back at document start.
  const thumbRefs = useRef<Record<string, HTMLButtonElement | null>>({});
  const lastZoomedRef = useRef<string | null>(null);
  const previewOpen = preview.modal !== null;
  const wasPreviewOpen = useRef(false);
  useEffect(() => {
    if (previewOpen) {
      wasPreviewOpen.current = true;
      return;
    }
    if (!wasPreviewOpen.current) return;
    wasPreviewOpen.current = false;
    const id = lastZoomedRef.current;
    // No-op when the item was removed while the preview was open.
    if (id) thumbRefs.current[id]?.focus();
  }, [previewOpen]);

  if (pending.length === 0) return null;

  // Image thumbs and file chip outer height stay aligned.
  const thumb = isMobile ? "size-14" : "size-12";
  const chipH = isMobile ? "h-14" : "h-12";
  // ≥44px hit target on mobile web; desktop can stay compact. File chips only —
  // image items use the 20px overflow-corner button (LRM-1180).
  const iconBtn = isMobile ? "size-9" : "size-6";
  const iconGlyph = isMobile ? "size-3.5" : "size-2.5";

  const openPreview = (item: PendingAttachment) => {
    if (!item.previewUrl) return;
    lastZoomedRef.current = item.localId;
    // `open`, not `tryOpen`: tryOpen returns false when the kind can't be
    // resolved and the visible result is a click that does nothing.
    // `contentType` is the real MIME the tray already holds — pasted
    // screenshots often have no extension, and getPreviewKind checks
    // contentType before falling back to the filename.
    preview.open({
      kind: "url",
      url: item.previewUrl,
      filename: item.filename,
      contentType: item.contentType,
      attachmentId: item.attachmentId,
    });
  };

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
          // Error keeps the centered slot for retry (retry-or-drop is the only
          // meaningful action on a failed upload), so no zoom there. Uploading
          // stays zoomable — previewUrl is a local blob, viewable before the
          // upload lands.
          const canZoom = showImage && item.status !== "error";
          // Two centered elements would collide with the uploading spinner.
          const showZoomHint = canZoom && item.status !== "uploading";

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
                canZoom ? (
                  <button
                    type="button"
                    ref={(node) => {
                      thumbRefs.current[item.localId] = node;
                    }}
                    className={cn(
                      thumb,
                      "relative block cursor-zoom-in overflow-hidden rounded-[7px] p-0 outline-none focus-visible:ring-2 focus-visible:ring-ring",
                    )}
                    aria-label={previewLabel}
                    data-testid={`composer-tray-zoom-${item.localId}`}
                    onClick={() => openPreview(item)}
                  >
                    {/* Blob/remote tray preview only; shared package cannot use next/image.
                        react-doctor-disable-next-line react-doctor/nextjs-no-img-element */}
                    <img
                      src={item.previewUrl}
                      alt={item.filename}
                      className={cn(thumb, "rounded-[7px] object-cover")}
                      draggable={false}
                    />
                    {showZoomHint ? (
                      <span
                        data-testid={`composer-tray-zoom-hint-${item.localId}`}
                        className="pointer-events-none absolute inset-0 hidden items-center justify-center rounded-[7px] bg-black/28 group-focus-within:flex group-hover:flex"
                        aria-hidden
                      >
                        <span className="flex size-6 items-center justify-center rounded-md bg-background/90 text-foreground">
                          <ZoomIn className="size-3.5" />
                        </span>
                      </span>
                    ) : null}
                  </button>
                ) : (
                  // Failed upload: the image stays visible for context but is
                  // not an entry point — the centered slot is retry's.
                  // react-doctor-disable-next-line react-doctor/nextjs-no-img-element
                  <img
                    src={item.previewUrl}
                    alt={item.filename}
                    className={cn(thumb, "rounded-[7px] object-cover")}
                    draggable={false}
                  />
                )
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

              {/* Retry is the primary recovery action and covers nothing, so it
                  stays in the layout: centered over an image thumb, inline in a
                  file chip's control row. Only remove goes to the corner. */}
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

              {/* LRM-1228: one remove rule for every chip kind — 20px visual in
                  the overflow corner, 24px pointer target via `after:-inset-0.5`.
                  File chips reserve `pr-3` (the button's inner half is 12px) so
                  the truncated filename never runs underneath. */}
              <div className="absolute -right-2 -top-2 z-30 flex shrink-0 items-center">
                <Button
                  type="button"
                  variant="secondary"
                  size="icon"
                  className={cn(
                    // 20px visual, 24px hit target via the ::after pad.
                    "relative size-5 rounded-full border border-border bg-background/95 shadow-sm",
                    'after:absolute after:-inset-0.5 after:content-[""]',
                    // Only an image has something worth un-covering on desktop
                    // hover. A file chip's remove is the chip's only control, so
                    // hiding it until hover would just cost discoverability.
                    showImage && !isMobile
                      ? "opacity-0 transition-opacity group-focus-within:opacity-100 group-hover:opacity-100"
                      : "opacity-100",
                  )}
                  aria-label={removeLabel}
                  onClick={() => onRemove(item.localId)}
                >
                  <X className="size-3" />
                </Button>
              </div>
            </li>
          );
        })}
      </ul>
      {/* Portals to document.body; mounted here so all four tray call sites
          (channel + thread, DM ×2) get zoom with zero prop changes. */}
      {preview.modal}
    </>
  );
}
