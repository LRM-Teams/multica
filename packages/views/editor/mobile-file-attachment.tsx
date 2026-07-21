"use client";

/**
 * Mobile file attachment entry + fullscreen detail (LRM-216 / LRM-215).
 *
 * Narrow screens only: compact Slack/Discord-style info card in the message
 * stream (no inline iframe / content preview). Tap pushes a 100vh detail
 * sheet with basic metadata + Download / Open — no preview pane.
 */

import * as React from "react";
import { ChevronRight, ChevronLeft } from "lucide-react";
import { FileIcon, defaultStyles } from "react-file-icon";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../i18n";
import {
  formatFileSize,
  getFileExtension,
  getFileTypeCategory,
} from "./utils/file-meta";

export interface MobileFileAttachmentProps {
  filename: string;
  contentType?: string;
  sizeBytes?: number;
  createdAt?: string;
  /** Display name when known; omitted row value falls back to em dash. */
  uploaderName?: string;
  uploading?: boolean;
  /** False when the file cannot be opened (no href / unavailable). */
  openable?: boolean;
  onDownload: () => void;
  /** Open in new tab / other app. */
  onOpen: () => void;
  className?: string;
}

function formatWhen(iso?: string): string {
  if (!iso) return "—";
  try {
    const d = new Date(iso);
    if (Number.isNaN(d.getTime())) return "—";
    return d.toLocaleString();
  } catch {
    return "—";
  }
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
  createdAt,
  uploaderName,
  uploading,
  openable = true,
  onDownload,
  onOpen,
  className,
}: MobileFileAttachmentProps) {
  const { t } = useT("editor");
  const [open, setOpen] = React.useState(false);
  const dialogRef = React.useRef<HTMLDialogElement | null>(null);
  const ext = getFileExtension(filename);
  const iconStyles = defaultStyles[ext as keyof typeof defaultStyles] ?? {};
  const category = getFileTypeCategory(contentType, filename);
  const typeLabel = t(($) => $.attachment.file_type[category]);
  const sizeLabel =
    typeof sizeBytes === "number" && sizeBytes > 0
      ? formatFileSize(sizeBytes)
      : "";
  const sub = [typeLabel, sizeLabel].filter(Boolean).join(" · ");
  const badge = typeBadge(filename, contentType);
  const tone = badgeTone(filename, contentType);

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
          aria-label={t(($) => $.attachment.file_detail_title)}
          className="fixed inset-0 z-[80] m-0 flex h-dvh max-h-none w-screen max-w-none flex-col border-0 bg-background p-0 open:flex animate-in slide-in-from-right duration-300"
          onCancel={(event) => {
            event.preventDefault();
            closeDetail();
          }}
          onClose={() => setOpen(false)}
        >
          <div className="flex min-h-12 shrink-0 items-center gap-1 border-b border-border px-1">
            <button
              type="button"
              data-testid="mobile-file-detail-back"
              className="grid size-11 place-items-center rounded-md text-foreground hover:bg-muted"
              aria-label={t(($) => $.attachment.back)}
              onClick={closeDetail}
            >
              <ChevronLeft className="size-6" />
            </button>
            <span className="text-[15px] font-semibold">
              {t(($) => $.attachment.file_detail_title)}
            </span>
          </div>

          <div className="flex min-h-0 flex-1 flex-col items-center overflow-y-auto px-5 pb-6 pt-8">
            <div
              className={cn(
                "mb-4 grid size-[72px] place-items-center rounded-2xl text-sm font-extrabold",
                tone,
              )}
              aria-hidden
            >
              {badge}
            </div>
            {/* Keep FileIcon available for a11y/tests but prefer badge for visual parity with design */}
            <span className="sr-only">
              <FileIcon extension={ext || undefined} {...iconStyles} />
            </span>
            <h2 className="mb-2 max-w-full break-all text-center text-[17px] font-bold leading-snug">
              {filename}
            </h2>

            <dl className="mt-4 w-full border-t border-border">
              <Fact
                label={t(($) => $.attachment.meta_type)}
                value={contentType || typeLabel}
              />
              <Fact
                label={t(($) => $.attachment.meta_size)}
                value={sizeLabel || "—"}
              />
              <Fact
                label={t(($) => $.attachment.meta_sender)}
                value={uploaderName?.trim() || "—"}
              />
              <Fact
                label={t(($) => $.attachment.meta_time)}
                value={formatWhen(createdAt)}
              />
            </dl>

            <div className="mt-auto flex w-full flex-col gap-2 pt-8">
              <button
                type="button"
                data-testid="mobile-file-detail-download"
                className="h-11 rounded-[10px] bg-[#007a5a] text-[15px] font-bold text-white hover:bg-[#006b4e]"
                onClick={() => {
                  try {
                    onDownload();
                  } catch {
                    /* toast handled by download helper */
                  }
                }}
              >
                {t(($) => $.image.download)}
              </button>
              <button
                type="button"
                data-testid="mobile-file-detail-open"
                className="h-11 rounded-[10px] border border-border bg-background text-[14px] font-semibold text-foreground hover:bg-muted/50"
                onClick={onOpen}
              >
                {t(($) => $.attachment.open_elsewhere)}
              </button>
            </div>
          </div>
        </dialog>
      )}
    </>
  );
}

function Fact({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-start justify-between gap-3 border-b border-border py-3 text-[13px]">
      <dt className="shrink-0 text-muted-foreground">{label}</dt>
      <dd className="min-w-0 text-right font-semibold break-all">{value}</dd>
    </div>
  );
}
