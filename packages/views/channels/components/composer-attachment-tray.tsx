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
 * Slack-style composer tray: a **single horizontal strip** of pending
 * thumbs/chips above the editor. Never stacks as a vertical list.
 *
 * Spec: docs/superpowers/specs/2026-07-13-chat-attachment-presentation-design.md
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
    // Native list semantics (prefer-tag-over-role). list-none keeps layout
    // as a horizontal strip, not a document bullet list.
    <ul
      className={cn(
        // One row only + horizontal scroll. flex-wrap was wrapping full-width
        // chips into a vertical stack — that is explicitly forbidden here.
        "m-0 flex list-none flex-row flex-nowrap items-center gap-2 overflow-x-auto overflow-y-hidden overscroll-x-contain p-0 pb-0.5",
        // Hide scrollbar chrome where supported; still scrollable.
        "[scrollbar-width:thin]",
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
          <li
            key={item.localId}
            data-testid={`composer-tray-item-${item.localId}`}
            data-status={item.status}
            data-kind={showImage ? "image" : "file"}
            className={cn(
              "group relative flex h-12 w-fit max-w-[10.5rem] shrink-0 list-none flex-row items-center gap-1.5 rounded-lg border border-border/50 bg-muted/35",
              showImage ? "w-12 max-w-none p-0" : "min-w-0 px-2",
              item.status === "error" && "border-destructive/50 bg-destructive/5",
            )}
          >
            {showImage ? (
              // Blob/remote tray preview only; shared package cannot use next/image.
              // react-doctor-disable-next-line react-doctor/nextjs-no-img-element
              <img
                src={item.previewUrl}
                alt={item.filename}
                className="size-12 rounded-[7px] object-cover"
                draggable={false}
              />
            ) : (
              <FileIcon
                className="size-3.5 shrink-0 text-muted-foreground"
                aria-hidden
              />
            )}

            {!showImage ? (
              <div className="min-w-0 flex-1">
                <p
                  className="truncate text-xs font-medium leading-tight text-foreground"
                  title={item.filename}
                >
                  {item.filename}
                </p>
                {item.status === "error" ? (
                  <p className="truncate text-[10px] leading-tight text-destructive">
                    {item.errorMessage || t(($) => $.composer.tray_upload_failed)}
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
                    ? "absolute inset-0 rounded-lg bg-background/55"
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

            <div
              className={cn(
                "flex shrink-0 items-center gap-0.5",
                showImage && "absolute right-0.5 top-0.5 z-10",
              )}
            >
              {item.status === "error" ? (
                <Button
                  type="button"
                  variant="secondary"
                  size="icon"
                  className="size-5 bg-background/95 shadow-sm"
                  aria-label={retryLabel}
                  onClick={() => onRetry(item.localId)}
                >
                  <RotateCcw className="size-2.5" />
                </Button>
              ) : null}
              <Button
                type="button"
                variant="secondary"
                size="icon"
                className={cn(
                  "size-5 shadow-sm",
                  showImage
                    ? "bg-background/95 opacity-0 transition-opacity group-hover:opacity-100 group-focus-within:opacity-100"
                    : "bg-transparent hover:bg-background/80",
                )}
                aria-label={removeLabel}
                onClick={() => onRemove(item.localId)}
              >
                <X className="size-2.5" />
              </Button>
            </div>
          </li>
        );
      })}
    </ul>
  );
}
