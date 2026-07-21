"use client";

/**
 * Mobile file attachment entry + fullscreen detail (LRM-216 / LRM-217).
 *
 * Narrow screens only: compact Slack/Discord-style info card in the message
 * stream (no inline iframe / content preview). Tap pushes a 100vh detail
 * sheet with an embedded preview zone (HTML / image / PDF same shell) +
 * metadata + Download / Open. Non-previewable types show an unavailable
 * placeholder in the same shell.
 */

import * as React from "react";
import { ChevronLeft, ChevronRight, FileWarning } from "lucide-react";
import { useActorName } from "@multica/core/workspace/hooks";
import { useWorkspaceSlug } from "@multica/core/paths";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../i18n";
import { HtmlPreviewBody } from "./html-preview-body";
import { getPreviewKind } from "./utils/preview";
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
  /** Display name when known; omitted row value falls back to resolver / em dash. */
  uploaderName?: string;
  uploaderType?: string;
  uploaderId?: string;
  /** ID-keyed HTML preview + same-origin PDF iframe. */
  attachmentId?: string;
  /** Media URL for image / PDF iframe / open-elsewhere. */
  mediaUrl?: string;
  uploading?: boolean;
  /** False when the file cannot be opened (no href / unavailable). */
  openable?: boolean;
  onDownload: () => void;
  /** Open in new tab / other app. */
  onOpen: () => void;
  className?: string;
}

type ShellPreviewKind = "html" | "image" | "pdf" | "none";

function shellPreviewKind(
  contentType: string,
  filename: string,
): ShellPreviewKind {
  const kind = getPreviewKind(contentType, filename);
  if (kind === "html") return "html";
  if (kind === "image") return "image";
  if (kind === "pdf") return "pdf";
  return "none";
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
  if (contentType.startsWith("image/")) return "bg-brand text-brand-foreground";
  return "bg-[#2b2d31] text-white";
}

function sameOriginPdfSrc(
  attachmentId: string | undefined,
  mediaUrl: string | undefined,
  workspaceSlug: string | null,
): string | undefined {
  if (attachmentId && workspaceSlug) {
    const params = new URLSearchParams({ workspace_slug: workspaceSlug });
    return `/api/attachments/${encodeURIComponent(attachmentId)}/download?${params}`;
  }
  return mediaUrl || undefined;
}

export function MobileFileAttachment({
  filename,
  contentType = "",
  sizeBytes,
  createdAt,
  uploaderName: uploaderNameProp,
  uploaderType,
  uploaderId,
  attachmentId,
  mediaUrl,
  uploading,
  openable = true,
  onDownload,
  onOpen,
  className,
}: MobileFileAttachmentProps) {
  const { t } = useT("editor");
  const workspaceSlug = useWorkspaceSlug();
  const { getActorName } = useActorName();
  const [open, setOpen] = React.useState(false);
  const dialogRef = React.useRef<HTMLDialogElement | null>(null);
  const category = getFileTypeCategory(contentType, filename);
  const typeLabel = t(($) => $.attachment.file_type[category]);
  const sizeLabel =
    typeof sizeBytes === "number" && sizeBytes > 0
      ? formatFileSize(sizeBytes)
      : "";
  const badge = typeBadge(filename, contentType);
  const tone = badgeTone(filename, contentType);
  const sub = [badge, sizeLabel].filter(Boolean).join(" · ");
  const previewKind = shellPreviewKind(contentType, filename);
  const pdfSrc = sameOriginPdfSrc(attachmentId, mediaUrl, workspaceSlug);
  const actorType =
    uploaderType === "user" ? "member" : (uploaderType ?? "");
  const uploaderName =
    uploaderNameProp?.trim() ||
    (actorType && uploaderId ? getActorName(actorType, uploaderId) : "") ||
    "";

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
      </div>

      {open && (
        <dialog
          ref={bindDialog}
          data-testid="mobile-file-detail"
          aria-label={filename || t(($) => $.attachment.file_detail_title)}
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
            <span className="min-w-0 flex-1 truncate text-[15px] font-semibold">
              {filename || t(($) => $.attachment.file_detail_title)}
            </span>
          </div>

          <MobileFilePreviewZone
            previewKind={previewKind}
            filename={filename}
            attachmentId={attachmentId}
            mediaUrl={mediaUrl}
            pdfSrc={pdfSrc}
            badge={badge}
            tone={tone}
          />

          <div className="flex min-h-0 shrink-0 flex-col overflow-y-auto px-4 pb-5 pt-3">
            <h2 className="break-all text-[15px] font-bold leading-snug">
              {filename}
            </h2>
            {(typeLabel || sizeLabel) && (
              <p className="mt-1 text-[12px] text-muted-foreground">
                {[typeLabel, sizeLabel].filter(Boolean).join(" · ")}
              </p>
            )}

            <dl className="mt-3 w-full border-t border-border">
              <Fact
                label={t(($) => $.attachment.meta_sender)}
                value={uploaderName || "—"}
              />
              <Fact
                label={t(($) => $.attachment.meta_time)}
                value={formatWhen(createdAt)}
              />
            </dl>

            <div className="mt-4 flex w-full flex-col gap-2">
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
        </dialog>
      )}
    </>
  );
}

function MobileFilePreviewZone({
  previewKind,
  filename,
  attachmentId,
  mediaUrl,
  pdfSrc,
  badge,
  tone,
}: {
  previewKind: ShellPreviewKind;
  filename: string;
  attachmentId?: string;
  mediaUrl?: string;
  pdfSrc?: string;
  badge: string;
  tone: string;
}) {
  const { t } = useT("editor");
  const chip =
    previewKind === "html"
      ? t(($) => $.attachment.preview_chip_html)
      : previewKind === "image"
        ? t(($) => $.attachment.preview_chip_image)
        : previewKind === "pdf"
          ? t(($) => $.attachment.preview_chip_pdf)
          : t(($) => $.attachment.preview_unavailable_chip);

  return (
    <div
      data-testid="mobile-file-preview"
      data-preview-kind={previewKind}
      className={cn(
        "relative flex min-h-[168px] max-h-[52%] flex-1 flex-col overflow-hidden border-b border-border bg-muted/40",
        previewKind === "html" && "bg-background",
      )}
    >
      <span
        className={cn(
          "absolute left-2 top-2 z-[1] rounded-full border border-border bg-background/95 px-2 py-0.5 text-[10px] font-bold text-muted-foreground",
          previewKind === "none" &&
            "border-amber-500/40 bg-amber-500/10 text-amber-800 dark:text-amber-200",
        )}
      >
        {chip}
      </span>

      {previewKind === "html" && attachmentId ? (
        <HtmlPreviewBody
          source={{ kind: "attachment", attachmentId }}
          title={filename}
          className="h-full min-h-[168px] w-full flex-1"
          iframeClassName="rounded-none border-0"
          errorTestId="mobile-file-preview-html-error"
        />
      ) : previewKind === "html" ? (
        <UnavailablePreview badge={badge} tone={tone} />
      ) : previewKind === "image" && mediaUrl ? (
        <img
          src={mediaUrl}
          alt={filename}
          className="mx-auto h-full max-h-full w-full object-contain p-2.5"
          data-testid="mobile-file-preview-image"
        />
      ) : previewKind === "pdf" && pdfSrc ? (
        <iframe
          src={pdfSrc}
          title={filename}
          className="h-full min-h-[168px] w-full flex-1 border-0 bg-background"
          data-testid="mobile-file-preview-pdf"
        />
      ) : (
        <UnavailablePreview badge={badge} tone={tone} />
      )}
    </div>
  );
}

function UnavailablePreview({
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
      className="flex flex-1 flex-col items-center justify-center gap-2 px-4 py-8 text-center"
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
      <p className="max-w-[16rem] text-[12px] leading-snug text-muted-foreground">
        {t(($) => $.attachment.preview_unavailable_hint)}
      </p>
    </div>
  );
}

function Fact({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-start justify-between gap-3 border-b border-border py-2.5 text-[12px] last:border-b-0">
      <dt className="shrink-0 text-muted-foreground">{label}</dt>
      <dd className="min-w-0 break-all text-right font-semibold">{value}</dd>
    </div>
  );
}
