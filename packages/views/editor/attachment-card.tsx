"use client";

/**
 * AttachmentCard — shared file-tile UI for non-image / non-html attachments.
 *
 * Subcomponent of the unified `<Attachment>` dispatcher (see attachment.tsx).
 * Rendered for every attachment kind that does not have a richer inline
 * renderer (image / html). Kind-aware routing lives in `<Attachment>` — keep
 * that decision out of this file so this stays a single-purpose tile UI.
 *
 * Layout (Slack-style, per task #339 design): a fixed-width card with a
 * type-colored react-file-icon glyph, a two-line meta column (filename + size ·
 * type), and hover-revealed actions. The whole card is clickable — preview when
 * previewable, otherwise download.
 */

import type { KeyboardEvent as ReactKeyboardEvent } from "react";
import { Download, Eye, Loader2, Trash2 } from "lucide-react";
import { FileIcon, defaultStyles } from "react-file-icon";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../i18n";
import { getPreviewKind } from "./utils/preview";
import {
  formatFileSize,
  getFileExtension,
  getFileTypeCategory,
} from "./utils/file-meta";

export interface AttachmentCardProps {
  /** Filename used for icon, label, type detection. */
  filename: string;
  /** Content type used in addition to filename for previewable-kind detection. */
  contentType?: string;
  /** File size in bytes — rendered in the meta line when known (>0). */
  sizeBytes?: number;
  /**
   * Attachment id — required when the preview proxy is ID-keyed (text kinds
   * like markdown / html / text). Media kinds (pdf/video/audio) preview from
   * the URL alone.
   */
  attachmentId?: string;
  /** Download URL — used as a non-null sentinel for the download button. */
  href?: string;
  /** True while a synchronous upload is in flight (file-card NodeView only). */
  uploading?: boolean;
  /** Pressed when the Eye button (or a previewable card) is clicked. */
  onPreview: () => void;
  /** Pressed when the Download button (or a non-previewable card) is clicked. */
  onDownload: () => void;
  /** Optional remove button, used by editable comment/file-card surfaces. */
  onDelete?: () => void;
}

export function AttachmentCard({
  filename,
  contentType = "",
  sizeBytes,
  attachmentId,
  href,
  uploading,
  onPreview,
  onDownload,
  onDelete,
}: AttachmentCardProps) {
  const { t } = useT("editor");

  const kind = filename ? getPreviewKind(contentType, filename) : null;
  // Media kinds (pdf/video/audio) are previewable from a URL alone — the
  // modal renders them as <video>/<audio>/<iframe src=url>. Text kinds
  // (markdown/html/text) need the ID-keyed `/api/attachments/{id}/content`
  // proxy, so they only preview when we have an attachmentId — otherwise
  // the Eye button would call tryOpen, get rejected, and do nothing.
  const isUrlPreviewableKind =
    kind === "pdf" || kind === "video" || kind === "audio";
  const canPreview =
    !!href && kind !== null && (!!attachmentId || isUrlPreviewableKind);
  const canDownload = !!href;
  const canDelete = !!onDelete;

  const ext = getFileExtension(filename);
  const iconStyles = defaultStyles[ext as keyof typeof defaultStyles] ?? {};
  const category = getFileTypeCategory(contentType, filename);
  const typeLabel = t(($) => $.attachment.file_type[category]);
  const sizeLabel =
    typeof sizeBytes === "number" && sizeBytes > 0
      ? formatFileSize(sizeBytes)
      : "";
  const meta = [sizeLabel, typeLabel].filter(Boolean).join(" · ");

  // Whole-card click → primary action. Previewable cards open the preview;
  // everything else downloads. Non-actionable cards (no href) aren't clickable.
  const primaryAction = canPreview
    ? onPreview
    : canDownload
      ? onDownload
      : undefined;
  const clickable = !uploading && !!primaryAction;

  // Interaction props are spread as a unit so the card is either a fully
  // keyboard-accessible `button` (role + tabIndex + Enter/Space) or a plain
  // static div with no click handler — never a click-only div.
  const interactionProps = clickable
    ? {
        role: "button" as const,
        tabIndex: 0,
        onClick: primaryAction,
        onKeyDown: (e: ReactKeyboardEvent) => {
          if (e.key === "Enter" || e.key === " ") {
            e.preventDefault();
            primaryAction?.();
          }
        },
      }
    : {};

  return (
    <div className="my-1">
      <div
        className={cn(
          "group inline-flex max-w-[340px] items-center gap-3 rounded-xl border border-border bg-muted/40 px-3 py-2.5 transition-colors hover:bg-muted/70",
          clickable && "cursor-pointer",
        )}
        onMouseDown={(e) => e.stopPropagation()}
        {...interactionProps}
      >
        {/* Icon slot — fixed size so cards align regardless of glyph aspect. */}
        <span className="flex h-9 w-9 shrink-0 items-center justify-center">
          {uploading ? (
            <Loader2 className="size-5 animate-spin text-muted-foreground" />
          ) : (
            <span className="w-[30px] leading-none">
              <FileIcon extension={ext || undefined} {...iconStyles} />
            </span>
          )}
        </span>

        {/* Two-line meta. */}
        <div className="min-w-0 flex-1">
          <p
            className="truncate text-[13.5px] font-semibold leading-tight"
            title={filename}
          >
            {uploading
              ? t(($) => $.file_card.uploading, { filename })
              : filename}
          </p>
          {!uploading && meta && (
            <p className="mt-0.5 truncate text-[11.5px] leading-tight text-muted-foreground">
              {meta}
            </p>
          )}
        </div>

        {/* Actions — hidden until hover (desktop), grouped on the right. */}
        {!uploading && (canPreview || canDownload || canDelete) && (
          <div className="ml-1.5 flex shrink-0 items-center gap-0.5 opacity-0 transition-opacity group-hover:opacity-100">
            {canPreview && (
              <button
                type="button"
                className="flex size-7 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-secondary hover:text-foreground"
                title={t(($) => $.attachment.preview)}
                aria-label={t(($) => $.attachment.preview)}
                onMouseDown={(e) => {
                  e.preventDefault();
                  e.stopPropagation();
                  onPreview();
                }}
                onClick={(e) => e.stopPropagation()}
              >
                <Eye className="size-3.5" />
              </button>
            )}
            {canDownload && (
              <button
                type="button"
                className="flex size-7 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-brand/10 hover:text-brand"
                title={t(($) => $.image.download)}
                aria-label={t(($) => $.image.download)}
                onMouseDown={(e) => {
                  e.preventDefault();
                  e.stopPropagation();
                  onDownload();
                }}
                onClick={(e) => e.stopPropagation()}
              >
                <Download className="size-3.5" />
              </button>
            )}
            {canDelete && onDelete && (
              <button
                type="button"
                className="flex size-7 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-destructive/10 hover:text-destructive"
                title={t(($) => $.attachment.remove)}
                aria-label={t(($) => $.attachment.remove)}
                onMouseDown={(e) => {
                  e.preventDefault();
                  e.stopPropagation();
                  onDelete();
                }}
                onClick={(e) => e.stopPropagation()}
              >
                <Trash2 className="size-3.5" />
              </button>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
