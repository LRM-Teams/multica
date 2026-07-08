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
 * type), and hover/focus-revealed actions.
 *
 * Accessibility (design owner: Iris):
 *   - The icon+meta region is the PRIMARY control for previewable files: a real
 *     `<button>` (Slack's "click the name to open") whose aria-label carries
 *     the whole file identity — "Open {name} · {size} · {type}" — so a screen
 *     reader announces it once as a single, actionable file item. For
 *     non-previewable files (zip, …) it is inert text (name + size · type stay
 *     SR-readable) and download is the only action, on the right.
 *   - Secondary actions (preview / download / delete) are SIBLING buttons,
 *     never nested inside the primary one (a button may not contain another
 *     interactive element), revealed on hover or keyboard focus.
 */

import { Download, Loader2, Trash2 } from "lucide-react";
import { FileIcon, defaultStyles } from "react-file-icon";
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
  /** Pressed when the Eye button (or a previewable card body) is clicked. */
  onPreview: () => void;
  /** Pressed when the Download button is clicked. */
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

  // Previewable files get a primary "open" affordance on the body. Uploading
  // and non-previewable files render the body as inert text.
  const openable = !uploading && canPreview;
  const hasActions = !uploading && (canDownload || canDelete);
  // Primary-button accessible name: open verb + full file identity, so a
  // screen reader hears "Open report.pdf · 1.4 MB · PDF" as one item.
  const openLabelBase = t(($) => $.attachment.open_file, { filename });
  const openLabel = meta ? `${openLabelBase} · ${meta}` : openLabelBase;

  const body = (
    <>
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
      {/* Two-line meta — phrasing-only markup so it can live inside a button. */}
      <span className="min-w-0 flex-1 text-left">
        <span
          className="block truncate text-[13.5px] font-semibold leading-tight"
          title={filename}
        >
          {uploading
            ? t(($) => $.file_card.uploading, { filename })
            : filename}
        </span>
        {!uploading && meta && (
          <span className="mt-0.5 block truncate text-[11.5px] leading-tight text-muted-foreground">
            {meta}
          </span>
        )}
      </span>
    </>
  );

  return (
    <div className="my-1">
      <div
        className="group inline-flex max-w-[340px] items-center gap-2 rounded-xl border border-border bg-muted/40 px-3 py-2.5 transition-colors hover:bg-muted/70"
        onMouseDown={(e) => e.stopPropagation()}
      >
        {openable ? (
          <button
            type="button"
            className="flex min-w-0 flex-1 cursor-pointer items-center gap-3 rounded-lg text-left outline-none focus-visible:ring-2 focus-visible:ring-brand/50"
            aria-label={openLabel}
            onClick={onPreview}
          >
            {body}
          </button>
        ) : (
          <div className="flex min-w-0 flex-1 items-center gap-3">{body}</div>
        )}

        {/* Actions — hidden until hover / keyboard focus, grouped on the right.
            Preview is NOT a button here: previewable files open from the body
            control above; the hover toolbar stays minimal (download / delete),
            matching the design + Slack parity. */}
        {hasActions && (
          <div className="flex shrink-0 items-center gap-0.5 opacity-0 transition-opacity group-hover:opacity-100 group-focus-within:opacity-100">
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
