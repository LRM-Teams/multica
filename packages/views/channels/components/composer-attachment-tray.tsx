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
 * Slack-style attachment tray: image thumbs + file chips above the editor.
 * Fills the Composer `tray` slot; does not own upload state.
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
    <ul
      className={cn(
        "flex max-h-36 flex-wrap gap-2 overflow-y-auto overscroll-contain",
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
            className={cn(
              "group relative flex shrink-0 items-center gap-2 rounded-md border border-border/50 bg-muted/30",
              showImage ? "size-[7.5rem] flex-col justify-end p-0" : "max-w-[14rem] px-2 py-1.5",
              item.status === "error" && "border-destructive/50 bg-destructive/5",
            )}
          >
            {showImage ? (
              <img
                src={item.previewUrl}
                alt={item.filename}
                className="absolute inset-0 size-full rounded-[5px] object-cover"
              />
            ) : (
              <FileIcon className="size-4 shrink-0 text-muted-foreground" aria-hidden />
            )}

            {!showImage ? (
              <div className="min-w-0 flex-1">
                <p className="truncate text-xs font-medium text-foreground">{item.filename}</p>
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
                <Loader2 className="size-4 animate-spin text-muted-foreground" />
              </div>
            ) : null}

            {showImage && item.status === "error" ? (
              <div className="absolute inset-x-0 bottom-0 rounded-b-[5px] bg-destructive/90 px-1 py-0.5 text-center text-[10px] text-destructive-foreground">
                {t(($) => $.composer.tray_upload_failed)}
              </div>
            ) : null}

            <div
              className={cn(
                "flex items-center gap-0.5",
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
                  showImage ? "bg-background/90 opacity-0 transition-opacity group-hover:opacity-100 group-focus-within:opacity-100" : "bg-transparent",
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
  );
}
