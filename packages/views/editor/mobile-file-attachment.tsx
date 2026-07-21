"use client";

/**
 * Mobile file attachment entry + fullscreen detail (LRM-216 / LRM-215).
 *
 * Narrow screens only: compact Slack/Discord-style info card in the message
 * stream (no inline iframe / content preview). Tap pushes a 100vh detail
 * sheet with basic metadata + Download / Open — no preview pane.
 */

import * as React from "react";
import { createPortal } from "react-dom";
import { ChevronLeft, ChevronRight, Trash2 } from "lucide-react";
import { useActorName } from "@multica/core/workspace/hooks";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../i18n";
import { useMessageTime } from "../i18n/use-message-time";
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
  /** Attachment uploader_type from the record (`member` / `user` / `agent`). */
  uploaderType?: string;
  uploaderId?: string;
  /** Explicit display name overrides resolver lookup when provided. */
  uploaderName?: string;
  uploading?: boolean;
  /** False when the file cannot be opened (no href / unavailable). */
  openable?: boolean;
  onDownload: () => void;
  /** Open in new tab / other app. */
  onOpen: () => void;
  /** Optional remove — editor compose surfaces only. */
  onDelete?: () => void;
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
  if (contentType.startsWith("image/")) return "bg-brand text-brand-foreground";
  return "bg-[#2b2d31] text-white";
}

export function MobileFileAttachment({
  filename,
  contentType = "",
  sizeBytes,
  createdAt,
  uploaderType,
  uploaderId,
  uploaderName: uploaderNameProp,
  uploading,
  openable = true,
  onDownload,
  onOpen,
  onDelete,
  className,
}: MobileFileAttachmentProps) {
  const { t } = useT("editor");
  const messageTime = useMessageTime();
  const { getActorName } = useActorName();
  const [open, setOpen] = React.useState(false);
  const category = getFileTypeCategory(contentType, filename);
  const typeLabel = t(($) => $.attachment.file_type[category]);
  const sizeLabel =
    typeof sizeBytes === "number" && sizeBytes > 0
      ? formatFileSize(sizeBytes)
      : "";
  const badge = typeBadge(filename, contentType);
  const tone = badgeTone(filename, contentType);
  // Design (LRM-215): "HTML · 606 B" — type badge + size, not localized category first.
  const sub = [badge, sizeLabel].filter(Boolean).join(" · ");
  const timeLabel = createdAt ? messageTime.format(createdAt) || "—" : "—";
  const actorType =
    uploaderType === "user" ? "member" : (uploaderType ?? "");
  const resolvedUploader =
    uploaderNameProp?.trim() ||
    (actorType && uploaderId ? getActorName(actorType, uploaderId) : "") ||
    "";

  const openDetail = () => {
    if (!openable || uploading) return;
    setOpen(true);
  };

  React.useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    const prevOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    document.addEventListener("keydown", onKey);
    return () => {
      document.body.style.overflow = prevOverflow;
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  return (
    <>
      <div className={cn("my-1 min-w-0 w-full max-w-full", className)}>
        <div
          className={cn(
            "flex w-full min-h-14 max-w-full items-center gap-1 rounded-lg border border-border bg-background",
            openable && !uploading ? "" : "opacity-70",
          )}
        >
          <button
            type="button"
            data-testid="mobile-file-entry"
            disabled={!openable || uploading}
            aria-label={t(($) => $.attachment.open_file, { filename })}
            onClick={openDetail}
            className={cn(
              "flex min-h-14 min-w-0 flex-1 items-center gap-2.5 px-3 py-2.5 text-left transition-colors",
              openable && !uploading
                ? "cursor-pointer hover:bg-muted/50 active:bg-muted/70"
                : "cursor-default",
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
                className="block truncate text-[13px] font-semibold leading-tight text-brand"
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
          {onDelete && !uploading && (
            <button
              type="button"
              className="mr-1 flex size-11 shrink-0 items-center justify-center rounded-md text-muted-foreground hover:bg-destructive/10 hover:text-destructive"
              title={t(($) => $.attachment.remove)}
              aria-label={t(($) => $.attachment.remove)}
              onClick={(e) => {
                e.stopPropagation();
                onDelete();
              }}
            >
              <Trash2 className="size-3.5" />
            </button>
          )}
        </div>
      </div>

      {open &&
        typeof document !== "undefined" &&
        createPortal(
          <div
            data-testid="mobile-file-detail"
            role="dialog"
            aria-modal="true"
            aria-label={t(($) => $.attachment.file_detail_title)}
            className="fixed inset-0 z-[80] flex flex-col bg-background animate-in slide-in-from-right duration-300"
          >
            <div className="flex min-h-12 shrink-0 items-center gap-1 border-b border-border px-1">
              <button
                type="button"
                data-testid="mobile-file-detail-back"
                className="grid size-11 place-items-center rounded-md text-foreground hover:bg-muted"
                aria-label={t(($) => $.attachment.back)}
                onClick={() => setOpen(false)}
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
                  value={resolvedUploader || "—"}
                />
                <Fact
                  label={t(($) => $.attachment.meta_time)}
                  value={timeLabel}
                />
              </dl>

              <div className="mt-auto flex w-full flex-col gap-2 pt-8">
                <button
                  type="button"
                  data-testid="mobile-file-detail-download"
                  className="h-11 rounded-[10px] bg-[#007a5a] text-[15px] font-bold text-white hover:bg-[#006b4e]"
                  onClick={() => {
                    void onDownload();
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
          </div>,
          document.body,
        )}
    </>
  );
}

function Fact({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-start justify-between gap-3 border-b border-border py-3 text-[13px]">
      <dt className="shrink-0 text-muted-foreground">{label}</dt>
      <dd className="min-w-0 break-all text-right font-semibold">{value}</dd>
    </div>
  );
}
