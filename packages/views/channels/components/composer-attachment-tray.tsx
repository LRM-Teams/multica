"use client";

import { FileIcon, Loader2, RotateCcw, X } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import type { PendingAttachment } from "../hooks/use-composer-pending-attachments";

export type ComposerAttachmentTrayProps = {
  pending: PendingAttachment[];
  onRemove: (localId: string) => void;
  onRetry: (localId: string) => void;
  className?: string;
};

function isImagePending(item: PendingAttachment): boolean {
  return item.contentType.startsWith("image/");
}

/**
 * Slack-style attachment tray: image thumbs + file chips **in a horizontal row**
 * (wrap when needed) above the editor. Fills the Composer `tray` slot; does not
 * own upload state.
 */
export function ComposerAttachmentTray({
  pending,
  onRemove,
  onRetry,
  className,
}: ComposerAttachmentTrayProps) {
  const { t } = useT("channels");

  if (pending.length === 0) return null;

  return (
    <div
      role="list"
      className={cn(
        // Explicit row: never stack chips as a vertical list. Wrap only when the
        // row runs out of width (Slack-like grouped tray).
        "flex max-h-36 flex-row flex-wrap items-center gap-2 overflow-y-auto overscroll-contain",
        className,
      )}
      data-slot="composer-attachment-tray"
      data-testid="composer-attachment-tray"
    >
      {pending.map((item) => {
        const removeLabel = t(($) => $.composer.tray_remove_aria, {
          filename: item.filename,
        });
        const retryLabel = t(($) => $.composer.tray_retry_aria, {
          filename: item.filename,
        });
        const showImage = isImagePending(item) && !!item.previewUrl;

        return (
          <div
            role="listitem"
            key={item.localId}
            data-testid={`composer-tray-item-${item.localId}`}
            data-status={item.status}
            className={cn(
              // w-fit + shrink-0 keeps each chip content-sized so siblings sit
              // side-by-side instead of stretching to full tray width (which
              // forced flex-wrap onto one chip per row).
              "group relative flex w-fit shrink-0 flex-row items-center gap-1.5 rounded-md border border-border/50 bg-muted/30",
              showImage
                ? "size-[7.5rem] justify-end p-0"
                : "max-w-[11rem] px-2 py-1.5",
              item.status === "error" && "border-destructive/50 bg-destructive/5",
            )}
          >
            {showImage ? (
              // Blob/remote tray preview only; shared package cannot use next/image.
              // react-doctor-disable-next-line react-doctor/nextjs-no-img-element
              <img
                src={item.previewUrl}
                alt={item.filename}
                className="absolute inset-0 size-full rounded-[5px] object-cover"
              />
            ) : (
              <FileIcon className="size-3.5 shrink-0 text-muted-foreground" aria-hidden />
            )}

            {!showImage ? (
              <div className="min-w-0 max-w-[7.5rem] flex-1">
                <p className="truncate text-xs font-medium text-foreground" title={item.filename}>
                  {item.filename}
                </p>
                {item.status === "error" ? (
                  <p className="truncate text-[10px] text-destructive">
                    {item.errorMessage || t(($) => $.composer.tray_upload_failed)}
                  </p>
                ) : item.status === "uploading" ? (
                  <p className="truncate text-[10px] text-muted-foreground">
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
                    ? "absolute inset-0 rounded-[5px] bg-background/50"
                    : "shrink-0",
                )}
                aria-hidden
              >
                <Loader2 className="size-3.5 animate-spin text-muted-foreground" />
              </div>
            ) : null}

            {showImage && item.status === "error" ? (
              <div className="absolute inset-x-0 bottom-0 rounded-b-[5px] bg-destructive/90 px-1 py-0.5 text-center text-[10px] text-destructive-foreground">
                {t(($) => $.composer.tray_upload_failed)}
              </div>
            ) : null}

            <div
              className={cn(
                "flex shrink-0 items-center gap-0.5",
                showImage && "absolute right-1 top-1 z-10",
              )}
            >
              {item.status === "error" ? (
                <Button
                  type="button"
                  variant="secondary"
                  size="icon"
                  className="size-6 bg-background/90 shadow-sm"
                  aria-label={retryLabel}
                  onClick={() => onRetry(item.localId)}
                >
                  <RotateCcw className="size-3" />
                </Button>
              ) : null}
              <Button
                type="button"
                variant="secondary"
                size="icon"
                className={cn(
                  "size-6 shadow-sm",
                  showImage
                    ? "bg-background/90 opacity-0 transition-opacity group-hover:opacity-100 group-focus-within:opacity-100"
                    : "bg-transparent",
                )}
                aria-label={removeLabel}
                onClick={() => onRemove(item.localId)}
              >
                <X className="size-3" />
              </Button>
            </div>
          </div>
        );
      })}
    </div>
  );
}
