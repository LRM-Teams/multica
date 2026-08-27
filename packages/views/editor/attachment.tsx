"use client";

/**
 * Attachment — single unified renderer for every attachment surface.
 *
 * Takes one attachment-shaped input (a full record, a URL-only reference, or
 * an in-flight upload) and dispatches by PreviewKind:
 *
 *   - image  → ImageAttachmentView (figure + click-to-preview lightbox via
 *              the shared AttachmentPreviewModal; compose-only hover toolbar)
 *   - html   → HtmlAttachmentPreview (inline iframe + hover toolbar), unless
 *              `inlineHtmlPreview={false}` (channel/thread message stream —
 *              LRM-285: Slack file-card in the stream; click still previews)
 *   - others → AttachmentCard (icon + filename + Eye/Download row)
 *
 * Call sites:
 *   - extensions/file-card.tsx FileCardView (Tiptap NodeView)
 *   - extensions/image-view.tsx ImageView (Tiptap NodeView)
 *   - readonly-content.tsx (markdown img + fileCard div renderers)
 *   - issues/components/comment-card.tsx AttachmentList (standalone fallback)
 *   - common/markdown.tsx (chat / skill viewer Markdown wrapper)
 *
 * The component owns its own preview modal and download dispatcher — callers
 * just pass `attachment` and (for editor surfaces) optional editor chrome
 * hints (selected, editable, onDelete).
 */

import type { CSSProperties } from "react";
import {
  Download,
  Link as LinkIcon,
  Maximize2,
  Trash2,
} from "lucide-react";
import { toast } from "sonner";
import { Tooltip, TooltipTrigger, TooltipContent } from "@multica/ui/components/ui/tooltip";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { cn } from "@multica/ui/lib/utils";
import { copyText } from "@multica/ui/lib/clipboard";
import { api } from "@multica/core/api";
import { useConfigStore } from "@multica/core/config";
import { paths, useWorkspaceSlug } from "@multica/core/paths";
import type { Attachment as AttachmentRecord } from "@multica/core/types";
import {
  attachmentDownloadPath,
  attachmentIdFromRef,
} from "@multica/core/types/attachment-url";
import { useT } from "../i18n";
import { useNavigation } from "../navigation";
import { useAttachmentDownloadResolver } from "./attachment-download-context";
import { useAttachmentPreview } from "./attachment-preview-modal";
import { useDownloadAttachment } from "./use-download-attachment";
import { useIsMobile } from "@multica/ui/hooks/use-mobile";
import { AttachmentCard } from "./attachment-card";
import { HtmlAttachmentPreview } from "./html-attachment-preview";
import { MobileFileAttachment } from "./mobile-file-attachment";
import { getPreviewKind, type PreviewKind } from "./utils/preview";
import "./styles/attachment.css";

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

export type AttachmentInput =
  // Server response in hand — full record. Used by AttachmentList and any
  // caller iterating a server-returned attachments[] array.
  | { kind: "record"; attachment: AttachmentRecord }
  // Markdown / Tiptap inline: only a URL + filename. Resolves to a full
  // record via the surrounding AttachmentDownloadProvider when available;
  // otherwise renders in URL-only mode (media types still preview from URL,
  // text types fall back to a download CTA).
  | {
      kind: "url";
      url: string;
      filename: string;
      contentType?: string;
      /** Editor in-flight state. Renders a loader placeholder. */
      uploading?: boolean;
      /**
       * Intrinsic pixel dimensions. Rendered as `<img width height>` so the
       * browser reserves the box before the image decodes — prevents the
       * layout shift that would otherwise push the caret out of view on paste.
       */
      width?: number;
      height?: number;
      /**
       * Structural hint from the call site: "this slot is definitionally an
       * image / file / ...". Bypasses `getPreviewKind` autodetect, which
       * needs a filename or content-type and falls back to the file-card
       * chrome when neither is available. Required for callers that KNOW
       * the kind from context (markdown `![]()` is always an image; Tiptap
       * image NodeView is always an image) but receive only a URL with an
       * empty `alt`/`filename`.
       */
      forceKind?: PreviewKind;
    };

export interface AttachmentProps {
  attachment: AttachmentInput;
  /** Editor hint — when true, the image toolbar exposes Trash. */
  editable?: boolean;
  /** Editor hint — applies the "selected" visual to the image figure. */
  selected?: boolean;
  /** Editor hint — wired to Tiptap deleteNode(). */
  onDelete?: () => void;
  /**
   * When false, HTML attachments render as a Slack-style file card in the
   * message stream (no in-bubble iframe). Click-to-preview (fullscreen on
   * mobile, attachment preview route on desktop) is unchanged. Channel /
   * thread message streams pass false (LRM-285).
   */
  inlineHtmlPreview?: boolean;
  className?: string;
}

interface Normalized {
  filename: string;
  contentType: string;
  url: string;
  attachmentId?: string;
  sizeBytes?: number;
  record?: AttachmentRecord;
  uploading: boolean;
  width?: number;
  height?: number;
}

function normalize(
  input: AttachmentInput,
  resolve: (url: string) => AttachmentRecord | undefined,
  cdnDomain: string,
): Normalized {
  if (input.kind === "record") {
    return {
      filename: input.attachment.filename,
      contentType: input.attachment.content_type,
      url: absolutizeMediaURL(
        pickInlineMediaURL(input.attachment, input.attachment.url, cdnDomain),
      ),
      attachmentId: input.attachment.id,
      sizeBytes: input.attachment.size_bytes,
      record: input.attachment,
      uploading: false,
    };
  }
  const record = input.url ? resolve(input.url) : undefined;
  // LRM-1130: `attachment:<uuid>` is the same id as the stable download
  // path — rewrite before falling through so <img> never gets a bare
  // scheme that the browser can't load.
  const idFromRef = input.url ? attachmentIdFromRef(input.url) : undefined;
  const fallbackUrl =
    !record && idFromRef ? attachmentDownloadPath(idFromRef) : input.url;
  return {
    filename: input.filename || record?.filename || "",
    contentType: input.contentType || record?.content_type || "",
    // When the markdown URL resolved to an attachment record, swap to
    // the record's freshly-loadable URL. The persisted markdown URL
    // (`/api/attachments/<id>/download` for new content; raw stored URL
    // for legacy) is correct as a stable reference but doesn't
    // necessarily load as a native <img>/<video> resource for every
    // client — token-mode clients can't attach an Authorization header
    // to bare /api/* fetches, and a CloudFront-signed `download_url`
    // is the only working media src in that mode. `pickInlineMediaURL`
    // picks the URL with embedded credentials when one exists and
    // falls back to the input URL otherwise so legacy / unresolved
    // markdown stays on its existing path. See MUL-3130 review.
    //
    // After picking the credential-bearing URL we run the absolutize
    // pass so a site-relative `/api/attachments/...` or `/uploads/...`
    // path becomes a proper origin-bearing URL when the renderer's
    // document origin doesn't proxy /api or /uploads to the API host
    // (Electron desktop, mobile webview). Web with a same-origin
    // proxy keeps `apiBaseUrl=""` and the helper is a no-op there.
    // See MUL-3192 — quick-create modal regressed because the freshly-
    // uploaded image URL stayed site-relative and Electron's renderer
    // origin (file://) couldn't load it.
    url: absolutizeMediaURL(
      record ? pickInlineMediaURL(record, fallbackUrl, cdnDomain) : fallbackUrl,
    ),
    // #831: fall back to the id embedded in a stable
    // `/api/attachments/<id>/download` URL (or `attachment:<uuid>`).
    // `resolve()` only finds records present in the surrounding
    // `attachments` prop, so a URL pasted across comments (or a surface
    // that passes no attachments at all) used to yield `undefined` here
    // — even though the id was sitting in the URL. That lost id is what
    // silently downgraded markdown/txt previews to a download and
    // disabled the card's preview affordance.
    attachmentId: record?.id ?? idFromRef,
    sizeBytes: record?.size_bytes,
    record,
    uploading: !!input.uploading,
    width: input.width,
    height: input.height,
  };
}

// absolutizeMediaURL is the legacy-compat fallback for old markdown bodies
// that persisted a site-relative `/api/attachments/<id>/download` or
// `/uploads/<key>` URL.
//
// The current (post-MUL-3192) write path persists an absolute URL chosen
// server-side by `buildMarkdownURL` (see server/internal/handler/file.go),
// so new content already loads natively on every client. This helper only
// matters for content written BEFORE MUL-3192 — those bodies still carry
// the old relative shape, and rendering them on a surface whose document
// origin is NOT the API host (Electron desktop, mobile webview) needs the
// API base URL pinned in at render time.
//
// On web, `api.getBaseUrl()` is empty (the Next.js rewrite proxies /api/*
// to the API host server-side), so this is a no-op there.
//
// http(s)://, blob:, and data: URLs are passed through unchanged — they
// already carry their own origin.
function absolutizeMediaURL(rawUrl: string): string {
  if (!rawUrl) return rawUrl;
  if (/^https?:\/\//i.test(rawUrl)) return rawUrl;
  if (/^blob:/i.test(rawUrl) || /^data:/i.test(rawUrl)) return rawUrl;
  if (!rawUrl.startsWith("/")) return rawUrl;
  // The api singleton is a Proxy that returns `undefined` for any property
  // access before `setApiInstance()` runs (boot ordering, SSR). Optional
  // chaining lets us cope with that without throwing — pre-init renders
  // simply keep the site-relative path.
  const baseUrl = (api.getBaseUrl?.() ?? "").replace(/\/+$/, "");
  if (!baseUrl) return rawUrl;
  return `${baseUrl}${rawUrl}`;
}

// pickInlineMediaURL returns the URL most likely to load successfully
// inside a native <img>/<video>/<iframe> resource fetch — i.e. without
// the calling client attaching an Authorization header.
//
// The metadata response carries three URL fields per attachment row,
// each with a different lifetime / accessibility:
//
//   - `record.download_url` — this-response click-time URL. In
//                             CloudFront-signed mode this is the
//                             signed redirect (works as a native img
//                             src for the duration of the TTL); in
//                             other modes it's the bare API endpoint
//                             (`/api/attachments/<id>/download`) which
//                             requires per-request auth and does NOT
//                             load as a native img on a non-same-site
//                             origin like Desktop's file://.
//   - `record.markdown_url` — the durable URL the server picked for
//                             persistence (MUL-3192 / `buildMarkdownURL`):
//                             public CDN passthrough when the storage is
//                             public-readable, or `MULTICA_PUBLIC_URL +
//                             /api/attachments/<id>/download` for
//                             private-bucket modes. Aligned with the
//                             server-side policy by construction, so it
//                             beats `record.url` whenever both exist.
//   - `record.url`          — raw storage URL. May be private (S3 /
//                             CloudFront-signed, R2, MinIO) and unable
//                             to load directly. Last-resort fallback
//                             for legacy responses that omit
//                             `markdown_url`.
//
// Order:
//
//  1. Signed `download_url` — when CloudFront has minted a signed
//     redirect for THIS response, use it; the TTL means the signed URL
//     beats `markdown_url` on first paint (no extra hop through the
//     API endpoint), and the renderer doesn't persist it so the TTL is
//     not a problem.
//  2. Known CDN `record.url` — when `/api/config` exposes the same CDN
//     host as the attachment record, the browser can load the object
//     directly (public CDN, or CloudFront cookie mode). Prefer it over
//     an API-shaped `markdown_url` so the rendered `<img src>` and Copy
//     Link affordance expose the CDN URL while the persisted markdown
//     can remain the stable attachment endpoint.
//  3. `record.markdown_url` — the durable, server-policy-aligned URL.
//     Beats raw `record.url` because it never points at a private
//     bucket (must-fix 2 from MUL-3192 review).
//  4. `record.url` — legacy fallback for responses that omit
//     `markdown_url` (a backend old enough to predate MUL-3192).
//  5. The input URL — when there's no record at all.
function pickInlineMediaURL(
  record: AttachmentRecord,
  fallback: string,
  cdnDomain: string,
): string {
  const dl = record.download_url ?? "";
  if (
    /^https?:\/\//i.test(dl) &&
    /[?&](Signature|X-Amz-Signature|Key-Pair-Id|Expires|X-Amz-Expires)=/i.test(dl)
  ) {
    return dl;
  }
  if (storageURLMatchesCdnDomain(record.url, cdnDomain)) return record.url;
  if (record.markdown_url) return record.markdown_url;
  if (record.url) return record.url;
  return fallback;
}

function storageURLMatchesCdnDomain(rawURL: string, cdnDomain: string): boolean {
  const expected = normalizeHost(cdnDomain);
  if (!rawURL || !expected) return false;
  try {
    const u = new URL(rawURL);
    if (u.protocol !== "http:" && u.protocol !== "https:") return false;
    if (normalizeHost(u.hostname) !== expected) return false;
    return !hasExpiringSignatureQuery(u.searchParams);
  } catch {
    return false;
  }
}

function normalizeHost(host: string): string {
  return host.trim().toLowerCase().replace(/\.$/, "");
}

function hasExpiringSignatureQuery(q: URLSearchParams): boolean {
  for (const key of [
    "Signature",
    "X-Amz-Signature",
    "Key-Pair-Id",
    "Expires",
    "X-Amz-Expires",
  ]) {
    if (q.has(key)) return true;
  }
  return false;
}

// ---------------------------------------------------------------------------
// Dispatcher
// ---------------------------------------------------------------------------

export function Attachment({
  attachment,
  editable,
  selected,
  onDelete,
  inlineHtmlPreview = true,
  className,
}: AttachmentProps) {
  const { resolveAttachment, openByUrl } = useAttachmentDownloadResolver();
  const cdnDomain = useConfigStore((s) => s.cdnDomain);
  const download = useDownloadAttachment();
  const preview = useAttachmentPreview();
  const isMobile = useIsMobile();
  const slug = useWorkspaceSlug();
  const navigation = useNavigation();

  const state = normalize(attachment, resolveAttachment, cdnDomain);
  const forceKind =
    attachment.kind === "url" ? attachment.forceKind : undefined;
  const kind =
    forceKind ??
    (state.filename || state.contentType
      ? getPreviewKind(state.contentType, state.filename)
      : null);

  const openPreview = () => {
    if (state.record) {
      preview.tryOpen({ kind: "full", attachment: state.record });
      return;
    }
    if (state.url) {
      preview.tryOpen({
        kind: "url",
        url: state.url,
        filename: state.filename,
        // #831: carry the URL-recovered id so text kinds reach the /content
        // proxy instead of falling through to a download.
        attachmentId: state.attachmentId,
      });
    }
  };

  // LRM-285 — message-stream HTML: open in an independent tab (or download),
  // never launch the in-app preview modal from the file card body.
  const openHtmlInNewTabOrDownload = () => {
    if (slug && state.attachmentId) {
      const nameQuery = state.filename
        ? `?name=${encodeURIComponent(state.filename)}`
        : "";
      const path = `${paths.workspace(slug).attachmentPreview(state.attachmentId)}${nameQuery}`;
      if (navigation.openInNewTab) {
        navigation.openInNewTab(path, state.filename, { activate: true });
        return;
      }
      const url = navigation.getShareableUrl(path);
      window.open(url, "_blank", "noopener,noreferrer");
      return;
    }
    if (state.attachmentId) {
      download(state.attachmentId);
      return;
    }
    if (state.url) openByUrl(state.url);
  };

  const handleDownload = () => {
    if (state.attachmentId) {
      download(state.attachmentId);
      return;
    }
    if (state.url) openByUrl(state.url);
  };

  const handleOpenElsewhere = () => {
    if (state.url) {
      window.open(state.url, "_blank", "noopener,noreferrer");
      return;
    }
    handleDownload();
  };

  // LRM-216 / LRM-219 / LRM-230 / LRM-285 — narrow/mobile:
  //   image → stream thumb → fullscreen big image
  //   html  → compact card in message stream → fullscreen sandboxed HTML on tap
  //           (`inlineHtmlPreview` only gates the in-stream iframe, not tap preview)
  //   else  → compact card → fullscreen download guidance (never blank)
  if (isMobile) {
    const canOpen = !!state.url || !!state.attachmentId;
    const previewMode =
      kind === "image"
        ? "image"
        : kind === "html" && state.attachmentId
          ? "html"
          : "none";
    return (
      <MobileFileAttachment
        filename={state.filename}
        contentType={state.contentType}
        sizeBytes={state.sizeBytes}
        createdAt={state.record?.created_at}
        uploading={state.uploading}
        openable={canOpen && !state.uploading}
        previewUrl={state.url}
        attachmentId={state.attachmentId}
        previewMode={previewMode}
        onDownload={handleDownload}
        onOpen={
          kind === "html" && !inlineHtmlPreview
            ? openHtmlInNewTabOrDownload
            : handleOpenElsewhere
        }
        className={className}
      />
    );
  }

  if (kind === "image") {
    return (
      <>
        <ImageAttachmentView
          src={state.url}
          alt={state.filename}
          uploading={state.uploading}
          width={state.width}
          height={state.height}
          editable={editable}
          selected={selected}
          onView={openPreview}
          onDownload={handleDownload}
          onDelete={onDelete}
          className={className}
        />
        {preview.modal}
      </>
    );
  }

  if (
    kind === "html" &&
    inlineHtmlPreview &&
    state.attachmentId &&
    !state.uploading
  ) {
    return (
      <>
        <HtmlAttachmentPreview
          attachmentId={state.attachmentId}
          filename={state.filename}
          onPreview={openPreview}
          onDownload={handleDownload}
          onDelete={editable ? onDelete : undefined}
        />
        {preview.modal}
      </>
    );
  }

  const cardOpen =
    kind === "html" && !inlineHtmlPreview
      ? openHtmlInNewTabOrDownload
      : openPreview;

  return (
    <>
      <AttachmentCard
        filename={state.filename}
        contentType={state.contentType}
        sizeBytes={state.sizeBytes}
        attachmentId={state.attachmentId}
        href={state.url || undefined}
        uploading={state.uploading}
        onPreview={cardOpen}
        onDownload={handleDownload}
        onDelete={editable ? onDelete : undefined}
      />
      {preview.modal}
    </>
  );
}

// ---------------------------------------------------------------------------
// ImageAttachmentView — inline image; compose hover toolbar only
// ---------------------------------------------------------------------------
//
// DOM and styling are intentionally a direct port of the original
// extensions/image-view.tsx <figure> structure. Shared visual styles live in
// styles/attachment.css under `.image-figure / .image-content / .image-toolbar`
// so standalone surfaces (chat messages, AttachmentList) get identical visuals
// without depending on the editor stylesheet being imported elsewhere.
//
// LRM-546 — read-only display surfaces (channel/DM/comment message stream) do
// NOT render the dark expand+download floating bar. Click the thumb to open
// the lightbox. The hover toolbar is compose-only (View / Download / Copy /
// Delete).

interface ImageAttachmentViewProps {
  src: string;
  alt: string;
  uploading: boolean;
  width?: number;
  height?: number;
  editable?: boolean;
  selected?: boolean;
  onView: () => void;
  onDownload: () => void;
  onDelete?: () => void;
  className?: string;
}

function ImageAttachmentView({
  src,
  alt,
  uploading,
  width,
  height,
  editable,
  selected,
  onView,
  onDownload,
  onDelete,
  className,
}: ImageAttachmentViewProps) {
  const { t } = useT("editor");

  const handleCopyLink = async () => {
    if (await copyText(src)) {
      toast.success(t(($) => $.image.link_copied));
    } else {
      showErrorToast(t(($) => $.image.copy_link_failed));
    }
  };

  // Click on figure opens the preview only in non-editor / non-uploading
  // surfaces — inside the editor we let ProseMirror own the click for
  // selection / cursor placement. Preview is the Maximize button *or*
  // a double-click on the image itself. The CSS rule
  // `.image-figure[data-clickable="true"] { cursor: zoom-in }` keys off
  // this same flag for the single-click cursor affordance.
  const clickable = !editable && !uploading;
  const canPreview = !uploading && !!src;

  const aspectRatioStyle =
    width && height && width > 0 && height > 0
      ? ({ aspectRatio: `${width} / ${height}` } as CSSProperties)
      : undefined;

  // DOM mirrors the original ReadonlyImage (span-only chain so it stays
  // valid HTML5 when rendered inside a markdown <p>). In editor surfaces
  // the NodeViewWrapper still emits its own outer .image-node div around
  // this — the duplicate `image-node` class is harmless.
  return (
    <span className="image-node">
      <span
        className={cn(
          "image-figure",
          selected && editable && "image-selected",
          className,
        )}
        style={aspectRatioStyle}
        data-clickable={clickable || undefined}
        contentEditable={false}
        onClick={clickable ? onView : undefined}
        onDoubleClick={
          canPreview
            ? (event) => {
                event.preventDefault();
                event.stopPropagation();
                onView();
              }
            : undefined
        }
      >
        <img
          src={src || undefined}
          alt={alt}
          width={width}
          height={height}
          className={cn("image-content", uploading && "image-uploading")}
          draggable={false}
        />
        {!uploading && src && editable && (
          <span
            className="image-toolbar"
            onMouseDown={(e) => e.stopPropagation()}
            onClick={(e) => e.stopPropagation()}
            onDoubleClick={(e) => e.stopPropagation()}
          >
            <Tooltip>
              <TooltipTrigger type="button" aria-label={t(($) => $.image.view)} onClick={onView}>
                <Maximize2 className="size-3.5" />
              </TooltipTrigger>
              <TooltipContent side="top">{t(($) => $.image.view)}</TooltipContent>
            </Tooltip>
            <Tooltip>
              <TooltipTrigger type="button" aria-label={t(($) => $.image.download)} onClick={onDownload}>
                <Download className="size-3.5" />
              </TooltipTrigger>
              <TooltipContent side="top">{t(($) => $.image.download)}</TooltipContent>
            </Tooltip>
            <Tooltip>
              <TooltipTrigger type="button" aria-label={t(($) => $.image.copy_link)} onClick={handleCopyLink}>
                <LinkIcon className="size-3.5" />
              </TooltipTrigger>
              <TooltipContent side="top">{t(($) => $.image.copy_link)}</TooltipContent>
            </Tooltip>
            {onDelete && (
              <Tooltip>
                <TooltipTrigger type="button" aria-label={t(($) => $.image.delete)} onClick={onDelete}>
                  <Trash2 className="size-3.5" />
                </TooltipTrigger>
                <TooltipContent side="top">{t(($) => $.image.delete)}</TooltipContent>
              </Tooltip>
            )}
          </span>
        )}
      </span>
    </span>
  );
}
