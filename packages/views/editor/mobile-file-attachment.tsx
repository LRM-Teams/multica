"use client";

/**
 * Mobile file attachment entry + fullscreen detail (LRM-216 / LRM-217 Slack freeze).
 *
 * Narrow screens only: compact stream card (icon + filename + type · size + ›),
 * no inline iframe. Tap pushes a 100vh sheet aligned to Slack: top bar is
 * back · filename · one Download; the rest is a full-bleed preview (HTML /
 * image / PDF). Other types show「无法预览」in the same shell. No metadata
 * form and no second download / bottom Open CTA on the main surface.
 */

import * as React from "react";
import { ChevronRight, ChevronLeft, Download } from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../i18n";
import {
  formatFileSize,
  getFileExtension,
} from "./utils/file-meta";
import { HtmlPreviewBody } from "./html-preview-body";

export type MobilePreviewMode = "html" | "image" | "pdf" | "none";

export interface MobileFileAttachmentProps {
  filename: string;
  contentType?: string;
  sizeBytes?: number;
  /** @deprecated Slack freeze: not shown on the fullscreen main surface. */
  createdAt?: string;
  /** @deprecated Slack freeze: not shown on the fullscreen main surface. */
  uploaderName?: string;
  uploading?: boolean;
  /** False when the file cannot be opened (no href / unavailable). */
  openable?: boolean;
  /** Direct URL for image / PDF / fallback iframe. */
  previewUrl?: string | null;
  /** Attachment id — preferred for HTML via /content proxy. */
  attachmentId?: string | null;
  previewMode?: MobilePreviewMode;
  onDownload: () => void;
  /** Kept for call-site compatibility; not shown on the Slack main surface. */
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

function resolvePreviewMode(
  mode: MobilePreviewMode | undefined,
  contentType: string,
  filename: string,
): MobilePreviewMode {
  if (mode) return mode;
  const ct = contentType.toLowerCase();
  const ext = getFileExtension(filename);
  if (ct.includes("html") || ext === "html" || ext === "htm") return "html";
  if (
    ct.startsWith("image/") ||
    ["png", "jpg", "jpeg", "gif", "webp", "svg"].includes(ext)
  ) {
    return "image";
  }
  if (ct.includes("pdf") || ext === "pdf") return "pdf";
  return "none";
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
  const sizeLabel =
    typeof sizeBytes === "number" && sizeBytes > 0
      ? formatFileSize(sizeBytes)
      : "";
  const badge = typeBadge(filename, contentType);
  const tone = badgeTone(filename, contentType);
  // Stream card only — one subtitle line「类型 · 大小」(Slack freeze).
  // Prefer the short type badge (HTML/PDF/…) over the coarse category label
  // so the card matches the frozen design copy.
  const sub = [badge, sizeLabel].filter(Boolean).join(" · ");
  const mode = resolvePreviewMode(previewMode, contentType, filename);

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
        <button
          type="button"
          data-testid="mobile-file-entry"
          disabled={!openable || uploading}
          aria-label={t(($) => $.attachment.open_file, { filename })}
          onClick={openDetail}
          className={cn(
            "flex w-full min-h-14 max-w-full items-center gap-2.5 rounded-lg border border-border bg-background px-3 py-2.5 text-left transition-colors",
            openable && !uploading
              ? "cursor-pointer hover:bg-muted/50 active:bg-muted/70"
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
              className="block truncate text-[13px] font-semibold leading-tight text-[#1264a3]"
              title={filename}
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
          {/* Slack freeze: back · filename · one Download — nothing else. */}
          <div className="flex min-h-12 shrink-0 items-center gap-0.5 border-b border-border px-1">
            <button
              type="button"
              data-testid="mobile-file-detail-back"
              className="grid size-11 place-items-center rounded-md text-foreground hover:bg-muted"
              aria-label={t(($) => $.attachment.back)}
              onClick={closeDetail}
            >
              <ChevronLeft className="size-6" />
            </button>
            <div
              className="min-w-0 flex-1 truncate px-1 text-[15px] font-semibold leading-tight"
              title={filename}
            >
              {filename}
            </div>
            <button
              type="button"
              data-testid="mobile-file-detail-download"
              className="mr-1 flex shrink-0 items-center gap-1.5 rounded-md px-3 py-2 text-[13px] font-bold text-[#1264a3] hover:bg-muted/50"
              aria-label={t(($) => $.image.download)}
              onClick={runDownload}
            >
              <Download className="size-4" aria-hidden />
              {t(($) => $.image.download)}
            </button>
          </div>

          <div
            data-testid="mobile-file-preview-pane"
            className="flex min-h-0 flex-1 flex-col bg-[#f0f0ee]"
          >
            <MobilePreviewBody
              mode={mode}
              filename={filename}
              previewUrl={previewUrl}
              attachmentId={attachmentId}
            />
          </div>
        </dialog>
      )}
    </>
  );
}

function MobilePreviewBody({
  mode,
  filename,
  previewUrl,
  attachmentId,
}: {
  mode: MobilePreviewMode;
  filename: string;
  previewUrl?: string | null;
  attachmentId?: string | null;
}) {
  const { t } = useT("editor");

  if (mode === "html" && attachmentId) {
    return (
      <HtmlPreviewBody
        source={{ kind: "attachment", attachmentId }}
        title={filename}
        className="h-full min-h-0 w-full flex-1"
        iframeClassName="rounded-none border-0"
        placeholderClassName="h-full min-h-0 flex-1"
        errorTestId="mobile-file-preview-error"
      />
    );
  }

  if (mode === "html" && previewUrl) {
    return (
      <iframe
        data-testid="mobile-file-preview-html"
        title={filename}
        src={previewUrl}
        sandbox="allow-scripts"
        className="h-full min-h-0 w-full flex-1 border-0 bg-background"
      />
    );
  }

  if (mode === "image" && previewUrl) {
    return (
      <div className="flex h-full min-h-0 flex-1 items-center justify-center overflow-auto p-3">
        <img
          data-testid="mobile-file-preview-image"
          src={previewUrl}
          alt={filename}
          className="max-h-full max-w-full object-contain"
        />
      </div>
    );
  }

  if (mode === "pdf" && previewUrl) {
    // Prefer <object> over <iframe>: app CSP blocks PDF in iframes
    // (see attachment-preview-modal), and react-doctor requires sandboxed
    // iframes which break Chromium's PDF viewer.
    return (
      <object
        data={previewUrl}
        type="application/pdf"
        data-testid="mobile-file-preview-pdf"
        aria-label={filename}
        className="h-full min-h-0 w-full flex-1 bg-background"
      >
        <div className="flex h-full min-h-[12rem] flex-col items-center justify-center gap-2 px-6 text-center">
          <p className="text-[15px] font-semibold">
            {t(($) => $.attachment.cannot_preview)}
          </p>
          <p className="text-[12px] text-muted-foreground">
            {t(($) => $.attachment.preview_unsupported)}
          </p>
        </div>
      </object>
    );
  }

  return (
    <div
      data-testid="mobile-file-preview-unavailable"
      className="flex h-full min-h-0 flex-1 flex-col items-center justify-center gap-2 px-6 text-center"
    >
      <p className="text-[15px] font-semibold text-foreground">
        {t(($) => $.attachment.cannot_preview)}
      </p>
      <p className="text-[12px] text-muted-foreground">
        {t(($) => $.attachment.preview_unsupported)}
      </p>
    </div>
  );
}
