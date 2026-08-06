"use client";

/**
 * Mobile file attachment entry + fullscreen detail (LRM-216 / LRM-219 / LRM-230).
 *
 * - Image: stream thumbnail → tap → fullscreen big image (Slack chrome)
 * - HTML: compact card → fullscreen sandboxed HTML preview (srcDoc) + Download
 * - Other files (incl. PDF): compact card → fullscreen filename + Download with
 *   a readable "can't preview — download / open locally" pane (never blank)
 */

import * as React from "react";
import { ChevronRight, ChevronLeft, FileWarning } from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../i18n";
import { HtmlPreviewBody } from "./html-preview-body";
import {
  formatFileSize,
  getFileExtension,
  getFileTypeCategory,
} from "./utils/file-meta";
import {
  resolveMobilePreviewMode,
  type MobilePreviewMode,
} from "./mobile-preview-mode";

export interface MobileFileAttachmentProps {
  filename: string;
  contentType?: string;
  sizeBytes?: number;
  /** @deprecated Unused in Slack chrome; kept for call-site compat. */
  createdAt?: string;
  /** @deprecated Unused in Slack chrome; kept for call-site compat. */
  uploaderName?: string;
  uploading?: boolean;
  openable?: boolean;
  previewUrl?: string | null;
  attachmentId?: string | null;
  previewMode?: MobilePreviewMode;
  onDownload: () => void;
  /** @deprecated Unused in Slack chrome; kept for call-site compat. */
  onOpen?: () => void;
  className?: string;
}

function typeBadge(filename: string, contentType: string): string {
  const ext = getFileExtension(filename);
  if (ext) return ext.toUpperCase().slice(0, 5);
  if (contentType.includes("html")) return "HTML";
  if (contentType.startsWith("image/")) return "IMG";
  if (contentType.startsWith("video/")) return "VID";
  if (contentType.startsWith("audio/")) return "AUD";
  if (contentType.includes("pdf")) return "PDF";
  return "FILE";
}

function badgeTone(filename: string, contentType: string): string {
  const ext = getFileExtension(filename);
  if (ext === "html" || ext === "htm" || contentType.includes("html")) {
    return "bg-[#e67e22] text-white";
  }
  if (ext === "pdf" || contentType.includes("pdf")) {
    return "bg-[#e01e5a] text-white";
  }
  if (contentType.startsWith("image/")) return "bg-[#1264a3] text-white";
  return "bg-[#2b2d31] text-white";
}

export function MobileFileAttachment({
  filename,
  contentType = "",
  sizeBytes,
  uploading,
  openable = true,
  previewUrl,
  attachmentId,
  previewMode,
  onDownload,
  className,
}: MobileFileAttachmentProps) {
  const { t } = useT("editor");
  const [open, setOpen] = React.useState(false);
  const dialogRef = React.useRef<HTMLDialogElement | null>(null);
  const category = getFileTypeCategory(contentType, filename);
  const typeLabel = t(($) => $.attachment.file_type[category]);
  const sizeLabel =
    typeof sizeBytes === "number" && sizeBytes > 0
      ? formatFileSize(sizeBytes)
      : "";
  const sub = [typeLabel, sizeLabel].filter(Boolean).join(" · ");
  const badge = typeBadge(filename, contentType);
  const tone = badgeTone(filename, contentType);
  const mode = resolveMobilePreviewMode(previewMode, contentType, filename);
  const showImageThumb =
    mode === "image" && !!previewUrl && !uploading;

  const bindDialog = React.useCallback((dialog: HTMLDialogElement | null) => {
    dialogRef.current = dialog;
    if (!dialog || dialog.open) return;
    if (typeof dialog.showModal === "function") dialog.showModal();
    else dialog.setAttribute("open", "");
  }, []);

  const closeDetail = React.useCallback(() => {
    const dialog = dialogRef.current;
    if (dialog?.open) {
      if (typeof dialog.close === "function") dialog.close();
      else dialog.removeAttribute("open");
    }
    setOpen(false);
  }, []);

  const openDetail = () => {
    if (!openable || uploading) return;
    setOpen(true);
  };

  const runDownload = () => {
    try {
      onDownload();
    } catch {
      /* toast handled by download helper */
    }
  };

  return (
    <>
      <div className={cn("my-1 min-w-0 w-full max-w-full", className)}>
        {showImageThumb ? (
          <button
            type="button"
            data-testid="mobile-file-entry"
            data-preview="image-thumb"
            disabled={!openable}
            aria-label={t(($) => $.attachment.open_file, { filename })}
            onClick={openDetail}
            className={cn(
              "block w-full max-w-full overflow-hidden rounded-lg border border-border bg-muted/30 text-left",
              openable
                ? "cursor-zoom-in hover:bg-muted/50 active:bg-muted/70"
                : "cursor-default opacity-70",
            )}
          >
            <img
              data-testid="mobile-file-stream-thumb"
              src={previewUrl}
              alt={filename}
              className="max-h-56 w-full object-contain"
            />
          </button>
        ) : (
          <button
            type="button"
            data-testid="mobile-file-entry"
            data-preview="compact-card"
            disabled={!openable || uploading}
            aria-label={t(($) => $.attachment.open_file, { filename })}
            onClick={openDetail}
            className={cn(
              // LRM-359: chip surface uses muted + border tokens; filename is
              // text-foreground (no Slack link hex — contrast ≥4.5:1 light/dark).
              "flex w-full min-h-14 max-w-full items-center gap-2.5 rounded-lg border border-border bg-muted px-3 py-2.5 text-left text-foreground transition-colors",
              openable && !uploading
                ? "cursor-pointer hover:bg-accent active:bg-accent"
                : "cursor-default opacity-70",
            )}
          >
            <span
              className={cn(
                "grid size-10 shrink-0 place-items-center rounded-lg text-[10px] font-extrabold tracking-wide",
                tone,
              )}
              aria-hidden
            >
              {uploading ? "…" : badge}
            </span>
            <span className="min-w-0 flex-1">
              <span
                className="block truncate text-[13px] font-semibold leading-tight text-foreground"
                title={filename}
                data-testid="mobile-file-filename"
              >
                {uploading
                  ? t(($) => $.file_card.uploading, { filename })
                  : filename}
              </span>
              {sub && !uploading && (
                <span className="mt-0.5 block truncate text-[11px] leading-tight text-muted-foreground">
                  {sub}
                </span>
              )}
            </span>
            <ChevronRight
              className="size-5 shrink-0 text-muted-foreground"
              aria-hidden
            />
          </button>
        )}
      </div>

      {open && (
        <dialog
          ref={bindDialog}
          data-testid="mobile-file-detail"
          aria-label={filename}
          className="fixed inset-0 z-[80] m-0 flex h-dvh max-h-none w-screen max-w-none flex-col border-0 bg-background p-0 open:flex animate-in slide-in-from-right duration-300"
          onCancel={(event) => {
            event.preventDefault();
            closeDetail();
          }}
          onClose={() => setOpen(false)}
        >
          {/* Slack chrome: back · filename · one Download */}
          <div className="flex min-h-12 shrink-0 items-center gap-0.5 border-b border-border bg-background px-1">
            <button
              type="button"
              data-testid="mobile-file-detail-back"
              className="grid size-11 place-items-center rounded-md text-foreground hover:bg-muted"
              aria-label={t(($) => $.attachment.back)}
              onClick={closeDetail}
            >
              <ChevronLeft className="size-6" />
            </button>
            <div className="min-w-0 flex-1 truncate py-1 text-[14px] font-bold leading-tight">
              {filename}
            </div>
            <button
              type="button"
              data-testid="mobile-file-detail-download"
              className="shrink-0 px-3 py-2 text-[13px] font-bold text-brand hover:bg-muted"
              onClick={runDownload}
            >
              {t(($) => $.image.download)}
            </button>
          </div>

          <div
            data-testid="mobile-file-preview-pane"
            className="flex min-h-0 flex-1 flex-col overflow-hidden bg-[#f0f0ee]"
          >
            {mode === "image" && previewUrl ? (
              <div className="flex h-full min-h-0 flex-1 items-center justify-center overflow-auto p-3">
                <img
                  data-testid="mobile-file-preview-image"
                  src={previewUrl}
                  alt={filename}
                  className="max-h-full max-w-full object-contain"
                />
              </div>
            ) : mode === "html" && attachmentId ? (
              <div
                data-testid="mobile-file-preview-html-body"
                className="flex min-h-0 flex-1 flex-col bg-background"
              >
                <HtmlPreviewBody
                  source={{ kind: "attachment", attachmentId }}
                  title={filename}
                  className="h-full min-h-0 w-full flex-1"
                  iframeClassName="rounded-none border-0"
                  placeholderClassName="h-full min-h-0 w-full flex-1"
                  errorTestId="mobile-file-preview-html-error"
                />
              </div>
            ) : (
              <DownloadGuidancePane badge={badge} tone={tone} />
            )}
          </div>
        </dialog>
      )}
    </>
  );
}

/** Non-blank fallback when content preview isn't available (PDF / zip / …). */
function DownloadGuidancePane({
  badge,
  tone,
}: {
  badge: string;
  tone: string;
}) {
  const { t } = useT("editor");
  return (
    <div
      data-testid="mobile-file-preview-unavailable"
      className="flex min-h-0 flex-1 flex-col items-center justify-center gap-2 px-6 py-10 text-center"
    >
      <span
        className={cn(
          "grid size-12 place-items-center rounded-xl text-[11px] font-extrabold",
          tone,
        )}
        aria-hidden
      >
        {badge}
      </span>
      <FileWarning className="size-5 text-muted-foreground" aria-hidden />
      <p className="text-[13px] font-semibold text-foreground">
        {t(($) => $.attachment.preview_unavailable)}
      </p>
      <p className="max-w-[18rem] text-[12px] leading-snug text-muted-foreground">
        {t(($) => $.attachment.preview_unavailable_hint)}
      </p>
    </div>
  );
}
