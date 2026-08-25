"use client";

/**
 * AttachmentPreviewModal — full-screen inline preview for an attachment.
 *
 * Single modal for every previewable kind. Handles 7 PreviewKinds:
 *
 *   - image : <img className="object-contain"> centered in the modal frame.
 *             Replaces the previous standalone ImageLightbox.
 *   - pdf   : <iframe src={download_url}> — relies on Chromium's PDFium
 *             plugin. On desktop, requires webPreferences.plugins=true
 *             (see apps/desktop/src/main/index.ts).
 *   - video : <video controls src={download_url}>
 *   - audio : <audio controls src={download_url}>
 *
 *   - markdown : fetch text via api.getAttachmentTextContent, render via
 *                the existing ReadonlyContent (full mention/mermaid/katex
 *                pipeline included).
 *   - html     : fetch text, hand to <iframe srcdoc={text}
 *                sandbox="allow-scripts">. The iframe runs in an opaque
 *                origin: scripts execute (chart libraries / vanilla SVG
 *                JS work), but cookie / localStorage / parent access /
 *                top-navigation / popups / forms stay blocked because
 *                `allow-same-origin` is intentionally NOT included.
 *   - text     : fetch text, highlight with lowlight if the extension
 *                maps to a known hljs language; otherwise plain <pre>.
 *
 * Media types load directly from the CloudFront signed `download_url`.
 * Text types go through `/api/attachments/{id}/content` to sidestep
 * CloudFront CORS (not configured) + Content-Disposition: attachment.
 */

import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { createPortal } from "react-dom";
import {
  PreviewTooLargeError,
  PreviewUnsupportedError,
} from "@multica/core/api";
import { Download, ExternalLink, FileText, Loader2, X } from "lucide-react";
import { Tooltip, TooltipTrigger, TooltipContent } from "@multica/ui/components/ui/tooltip";
import type { Attachment } from "@multica/core/types";
import { paths, useWorkspaceSlug } from "@multica/core/paths";
import { resolvePublicFileUrl } from "@multica/core/workspace/avatar-url";
import { useT } from "../i18n";
import { useNavigation } from "../navigation";
import { openExternal } from "../platform";
import { ReadonlyContent } from "./readonly-content";
import {
  extensionToLanguage,
  getPreviewKind,
  rendersFromUrlAlone,
  type PreviewKind,
} from "./utils/preview";
import { useDownloadAttachment } from "./use-download-attachment";
import { useAttachmentHtmlText } from "./hooks/use-attachment-html-text";
import { HtmlPreviewBody } from "./html-preview-body";
import { CodeBlockStatic } from "./code-block-static";

// ---------------------------------------------------------------------------
// Preview source — full attachment, or URL-only (media types only)
// ---------------------------------------------------------------------------
//
// `full` carries the resolved Attachment record and supports every PreviewKind
// (text types require the attachment id to call /api/attachments/{id}/content).
//
// `url` carries the signed URL + filename. It is what NodeViews fall back
// to when `resolveAttachment(href)` returns undefined — typical when the URL
// was copy-pasted across comments so the attachment record isn't reachable
// from the current entity's `attachments` prop.
//
// #831: a URL-only source MAY still carry `attachmentId`. A stable
// `/api/attachments/<id>/download` URL contains the id even when the record
// isn't in the current `attachments` prop, and the text `/content` proxy only
// needs that id — not the whole record. Callers pass it (via
// `attachmentIdFromDownloadURL`) so markdown / txt / html previews work from a
// pasted URL instead of silently degrading to a download. Without an id, text
// kinds remain ungated-out because the proxy is unaddressable.

// LRM-1180: a URL-only source MAY also carry the real `contentType`. Without
// it `normalize()` hardcodes `""` and `getPreviewKind` can only fall back to
// the filename extension — a pasted screenshot usually has no extension, so
// the kind resolves to null and the modal degrades to
// "preview unsupported" + Download for a plain PNG. Callers that already hold
// the MIME (e.g. the composer tray) pass it; the 10 existing `kind: "url"`
// call sites omit it and keep the previous behaviour exactly.

export type PreviewSource =
  | { kind: "full"; attachment: Attachment }
  | {
      kind: "url";
      url: string;
      filename: string;
      contentType?: string;
      attachmentId?: string;
    };

// Normalized view used everywhere downstream of `useAttachmentPreview`.
// `attachmentId === null` signals URL-only mode (download falls back to
// `openExternal`, text rendering branches are unreachable by the gate).
interface PreviewState {
  filename: string;
  contentType: string;
  mediaUrl: string;
  attachmentId: string | null;
}

function normalize(source: PreviewSource): PreviewState {
  // Resolve any server-relative URL (e.g. `/api/attachments/{id}/download`
  // returned by the unified-endpoint metadata path when no CloudFront
  // signer is configured) against the configured API base. Web with the
  // default empty base keeps the relative path and resolves it against
  // the page origin — same behaviour as before this PR. Desktop renderer
  // (loaded from `app://` / file: / dev-server origin) needs the absolute
  // form so `<img src>` / `<iframe src>` / `<video src>` actually point at
  // the API server instead of the shell origin.
  if (source.kind === "full") {
    const mediaUrl =
      resolvePublicFileUrl(source.attachment.download_url) ??
      source.attachment.download_url;
    return {
      filename: source.attachment.filename,
      contentType: source.attachment.content_type,
      mediaUrl,
      attachmentId: source.attachment.id,
    };
  }
  return {
    filename: source.filename,
    // LRM-1180: prefer the caller's real MIME; `""` keeps the pre-existing
    // filename-extension fallback for the callers that don't have one.
    contentType: source.contentType ?? "",
    mediaUrl: resolvePublicFileUrl(source.url) ?? source.url,
    // #831: keep the id when the caller could recover one from the URL — it
    // unlocks the text `/content` proxy and the re-signing download path.
    attachmentId: source.attachmentId ?? null,
  };
}

// ---------------------------------------------------------------------------
// Public props
// ---------------------------------------------------------------------------

interface AttachmentPreviewModalProps {
  source: PreviewSource;
  open: boolean;
  onClose: () => void;
}

// ---------------------------------------------------------------------------
// Hook — local state + ready-to-mount modal JSX
// ---------------------------------------------------------------------------
//
// Why no React context / provider: packages/views/ cannot mount a Context.Provider
// inside CoreProvider (in packages/core/), and threading a new provider through
// every app layout is more friction than it's worth for a feature with at most
// one open modal at a time. Instead each entry point gets its own local state
// and renders the returned `modal` node. Multiple entry points coexisting just
// means each carries its own (collapsed) state — they never collide because
// only one preview is open per user click.

export interface AttachmentPreviewHandle {
  /** Try to open a preview for the source. Returns false when the file type
   *  isn't previewable, OR when the source is URL-only but the kind requires
   *  a full attachment (text/markdown/html). Callers can fall back to a
   *  download flow. */
  tryOpen: (source: PreviewSource) => boolean;
  /** Force-open a preview, skipping the previewable() guard. Use for cases
   *  where the caller has already filtered. */
  open: (source: PreviewSource) => void;
  /** Modal node to render somewhere in the caller's tree. Resolves to `null`
   *  when no preview is active. Safe to render inside any container — the
   *  modal portals to document.body. */
  modal: ReactNode;
}

// #591/#799 (Iris): the modal's inline iframe can never render a PDF — the
// app's global CSP blocks it — so a fallback dialog with an "Open in new
// tab" button was just a valueless extra click. PDF never gets the modal,
// full stop: both `open` (force-open) and `tryOpen` route through this one
// dispatcher so a future force-open() caller can't resurrect the removed
// fallback dialog. openExternal fires synchronously in the caller's own
// call stack (still inside the original click's gesture) so the tab isn't
// popup-blocked.
function dispatchPreviewSource(source: PreviewSource, setCurrent: (source: PreviewSource) => void) {
  const state = normalize(source);
  const kind = getPreviewKind(state.contentType, state.filename);
  if (kind === "pdf") {
    openExternal(state.mediaUrl);
    return;
  }
  setCurrent(source);
}

export function useAttachmentPreview(): AttachmentPreviewHandle {
  const [current, setCurrent] = useState<PreviewSource | null>(null);

  const open = useCallback(
    (source: PreviewSource) => dispatchPreviewSource(source, setCurrent),
    [],
  );
  const tryOpen = useCallback((source: PreviewSource) => {
    const state = normalize(source);
    const kind = getPreviewKind(state.contentType, state.filename);
    if (!kind) return false;
    // #831: gate on whether we actually have an attachment id, not on the
    // source shape. Text kinds need the ID-keyed /content proxy; a URL-only
    // source that recovered its id from `/api/attachments/<id>/download` can
    // drive them just fine. Only a truly id-less source is turned away.
    if (!state.attachmentId && !rendersFromUrlAlone(kind)) return false;
    dispatchPreviewSource(source, setCurrent);
    return true;
  }, []);

  return useMemo(() => {
    const modal = current ? (
      <AttachmentPreviewModal
        source={current}
        open
        onClose={() => setCurrent(null)}
      />
    ) : null;

    return { open, tryOpen, modal };
  }, [current, open, tryOpen]);
}

// ---------------------------------------------------------------------------
// LRM-1298 — focus contract for this hand-rolled modal
// ---------------------------------------------------------------------------
//
// The overlay below is a raw `createPortal` div that declares
// `role="dialog"` + `aria-modal="true"`, but it shipped without any focus
// management: opening left focus on whatever the trigger was (or on <body>
// when the trigger unmounted), Tab walked straight out of the "modal" into the
// page behind it, and closing never gave focus back. Escape / backdrop close
// already worked and are left untouched.
//
// Deliberately local to this file rather than reusing
// research/hooks/use-overlay-panel-a11y: that hook serves a *non-modal*
// desktop side panel and explicitly does not trap focus, which is the one
// thing an `aria-modal="true"` dialog must do. Lifting a shared modal-focus
// hook into a common package is worth doing separately; it is not this fix.
//
// The restore strategy follows LRM-1177: remember a stable re-locator (`id`,
// else `data-testid`) next to the node, because a trigger is often rendered
// conditionally (`!open ? <Button/> : null`) and returns as a *different* DOM
// node, so restoring by node identity alone silently no-ops.

const FOCUSABLE_SELECTOR = [
  "a[href]",
  "area[href]",
  "button:not([disabled])",
  "input:not([disabled])",
  "select:not([disabled])",
  "textarea:not([disabled])",
  "iframe",
  "audio[controls]",
  "video[controls]",
  '[contenteditable]:not([contenteditable="false"])',
  '[tabindex]:not([tabindex="-1"])',
].join(", ");

type RestoreKey =
  | { kind: "id"; value: string }
  | { kind: "testId"; value: string };

function restoreKeyFor(element: HTMLElement): RestoreKey | null {
  if (element.id) return { kind: "id", value: element.id };
  const testId = element.dataset.testid;
  if (testId) return { kind: "testId", value: testId };
  return null;
}

// Resolved without building a selector string so arbitrary ids / test ids
// cannot produce an invalid or injected selector.
function resolveRestoreKey(
  doc: Document,
  key: RestoreKey | null,
): HTMLElement | null {
  if (!key) return null;
  if (key.kind === "id") return doc.getElementById(key.value);
  for (const candidate of doc.querySelectorAll<HTMLElement>("[data-testid]")) {
    if (candidate.dataset.testid === key.value) return candidate;
  }
  return null;
}

function focusablesWithin(container: HTMLElement): HTMLElement[] {
  return Array.from(
    container.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTOR),
  ).filter(
    (node) =>
      node.getAttribute("aria-hidden") !== "true" &&
      node.tabIndex !== -1 &&
      !node.hasAttribute("disabled"),
  );
}

function useModalFocusContract(active: boolean) {
  const dialogRef = useRef<HTMLDivElement | null>(null);
  const restoreRef = useRef<HTMLElement | null>(null);
  const restoreKeyRef = useRef<RestoreKey | null>(null);

  // Track the last control focused while the modal is closed. Capturing only
  // at open time is too late when the trigger is removed in the same commit
  // that opens the modal — by then `document.activeElement` is already
  // <body> and there is nothing left to remember.
  useEffect(() => {
    if (active) return;
    const doc = dialogRef.current?.ownerDocument ?? globalThis.document;
    if (!doc) return;
    const remember = () => {
      const el = doc.activeElement;
      if (!(el instanceof HTMLElement) || el === doc.body) return;
      restoreRef.current = el;
      restoreKeyRef.current = restoreKeyFor(el);
    };
    remember();
    doc.addEventListener("focusin", remember);
    return () => doc.removeEventListener("focusin", remember);
  }, [active]);

  // Initial focus in, restore out.
  useEffect(() => {
    if (!active) return;
    const dialog = dialogRef.current;
    const doc = dialog?.ownerDocument ?? globalThis.document;
    const previous = doc.activeElement;
    // Only overwrite when there is still a live focused control; otherwise
    // keep whatever the tracker above captured before the trigger was removed.
    if (previous instanceof HTMLElement && previous !== doc.body) {
      restoreRef.current = previous;
      restoreKeyRef.current = restoreKeyFor(previous);
    }

    // Focus the labelled `role="dialog"` node itself (tabIndex -1) so screen
    // readers announce the filename label on open, and Tab from there lands on
    // the first control inside the frame.
    if (dialog && !dialog.contains(doc.activeElement)) {
      dialog.focus({ preventScroll: true });
    }

    return () => {
      const target = restoreRef.current;
      const key = restoreKeyRef.current;
      restoreRef.current = null;
      restoreKeyRef.current = null;
      const resolved = target?.isConnected
        ? target
        : resolveRestoreKey(doc, key);
      // No re-locator and the original node is gone: leave focus where it is
      // rather than grabbing an unrelated control.
      resolved?.focus({ preventScroll: true });
    };
  }, [active]);

  // Trap: `aria-modal="true"` promises the rest of the page is inert, so Tab
  // must cycle inside the frame. Focusables are re-read on every keystroke
  // because the content area mounts asynchronously (text/markdown/html
  // previews) and the new-tab button appears only for some kinds.
  useEffect(() => {
    if (!active) return;
    const doc = dialogRef.current?.ownerDocument ?? globalThis.document;
    if (!doc) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== "Tab" || event.defaultPrevented) return;
      const dialog = dialogRef.current;
      if (!dialog) return;

      const focusables = focusablesWithin(dialog);
      const activeEl = doc.activeElement;
      const inside = activeEl instanceof HTMLElement && dialog.contains(activeEl);

      const first = focusables[0];
      const last = focusables[focusables.length - 1];
      // Nothing focusable inside (e.g. a content area that renders no
      // controls): keep focus parked on the dialog rather than letting Tab
      // walk out of an `aria-modal` surface.
      if (!first || !last) {
        event.preventDefault();
        dialog.focus({ preventScroll: true });
        return;
      }

      // Focus escaped (or never entered) the dialog — pull it back in.
      if (!inside) {
        event.preventDefault();
        (event.shiftKey ? last : first).focus({ preventScroll: true });
        return;
      }
      if (event.shiftKey && (activeEl === first || activeEl === dialog)) {
        event.preventDefault();
        last.focus({ preventScroll: true });
        return;
      }
      if (!event.shiftKey && (activeEl === last || activeEl === dialog)) {
        event.preventDefault();
        first.focus({ preventScroll: true });
      }
    };
    doc.addEventListener("keydown", onKeyDown, true);
    return () => doc.removeEventListener("keydown", onKeyDown, true);
  }, [active]);

  return dialogRef;
}

// ---------------------------------------------------------------------------
// Modal — frame + dispatch
// ---------------------------------------------------------------------------

export function AttachmentPreviewModal({
  source,
  open,
  onClose,
}: AttachmentPreviewModalProps) {
  const { t } = useT("editor");
  const download = useDownloadAttachment();
  const state = normalize(source);
  // useWorkspaceSlug (not useWorkspacePaths) — returns null outside a
  // workspace route instead of throwing, so the new-tab button just hides.
  const slug = useWorkspaceSlug();
  const navigation = useNavigation();

  useEffect(() => {
    if (!open) return;
    const handler = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    document.addEventListener("keydown", handler);
    return () => document.removeEventListener("keydown", handler);
  }, [open, onClose]);

  const kind = getPreviewKind(state.contentType, state.filename);

  // LRM-1298: initial focus + trap + restore for this hand-rolled dialog.
  const dialogRef = useModalFocusContract(open);

  // Download dispatcher: re-sign through `getAttachment` when an id is
  // available; otherwise fall back to opening the (possibly stale) URL
  // externally — same tradeoff as the file-card NodeView's download path.
  const handleDownload = () => {
    if (state.attachmentId) {
      download(state.attachmentId);
    } else {
      openExternal(state.mediaUrl);
    }
  };

  // Open-in-new-tab mirrors HtmlAttachmentPreview's inline toolbar: the
  // `html` kind has a dedicated full-page route (/attachments/{id}/preview),
  // gated on slug + attachmentId because URL-only sources can't address the
  // /content proxy the page relies on.
  //
  // `pdf` never reaches this modal at all (see tryOpen above) — the app's
  // global CSP `frame-ancestors 'none'` blocks it from loading in an iframe
  // regardless, so useAttachmentPreview hands PDFs straight to the browser's
  // native viewer instead of mounting this dialog.
  const canOpenInNewTab = kind === "html" && !!slug && !!state.attachmentId;
  const handleOpenInNewTab = () => {
    if (!slug || !state.attachmentId) return;
    const nameQuery = state.filename
      ? `?name=${encodeURIComponent(state.filename)}`
      : "";
    const path = `${paths.workspace(slug).attachmentPreview(state.attachmentId)}${nameQuery}`;
    if (navigation.openInNewTab) {
      navigation.openInNewTab(path, state.filename, { activate: true });
    } else {
      const url = navigation.getShareableUrl(path);
      window.open(url, "_blank", "noopener,noreferrer");
    }
    onClose();
  };

  if (!open || typeof document === "undefined") return null;

  return createPortal(
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/80 p-4"
      onClick={onClose}
      role="dialog"
      aria-modal="true"
      aria-label={state.filename}
      ref={dialogRef}
      // Programmatically focusable only — the dialog takes focus on open so
      // the label is announced, but it never joins the Tab order itself.
      tabIndex={-1}
    >
      {/* Larger than the create-issue dialog (max-w-4xl, manualDialogContentClass)
          because PDF / video previews want more room. Capped to viewport
          minus the surrounding p-4 (1rem each side) so it never overflows
          the screen on small displays / split panes. */}
      <div
        className="flex h-[min(90vh,calc(100vh-2rem))] w-full max-w-6xl flex-col overflow-hidden rounded-lg bg-background shadow-xl"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center gap-2 border-b border-border bg-muted/30 px-4 py-2">
          <FileText className="size-4 shrink-0 text-muted-foreground" />
          <p className="truncate text-sm font-medium">{state.filename}</p>
          <span className="ml-1 shrink-0 text-xs text-muted-foreground">
            {state.contentType || "—"}
          </span>
          <div className="ml-auto flex items-center gap-1">
            {canOpenInNewTab && (
              <Tooltip>
                <TooltipTrigger
                  type="button"
                  className="rounded-md p-1.5 text-muted-foreground transition-colors hover:bg-secondary hover:text-foreground"
                  aria-label={t(($) => $.attachment.open_in_new_tab)}
                  onClick={handleOpenInNewTab}
                >
                  <ExternalLink className="size-4" />
                </TooltipTrigger>
                <TooltipContent side="top">
                  {t(($) => $.attachment.open_in_new_tab)}
                </TooltipContent>
              </Tooltip>
            )}
            <Tooltip>
              <TooltipTrigger
                type="button"
                className="rounded-md p-1.5 text-muted-foreground transition-colors hover:bg-secondary hover:text-foreground"
                aria-label={t(($) => $.image.download)}
                onClick={handleDownload}
              >
                <Download className="size-4" />
              </TooltipTrigger>
              <TooltipContent side="top">{t(($) => $.image.download)}</TooltipContent>
            </Tooltip>
            <Tooltip>
              <TooltipTrigger
                type="button"
                className="rounded-md p-1.5 text-muted-foreground transition-colors hover:bg-secondary hover:text-foreground"
                aria-label={t(($) => $.attachment.close)}
                onClick={onClose}
              >
                <X className="size-4" />
              </TooltipTrigger>
              <TooltipContent side="top">{t(($) => $.attachment.close)}</TooltipContent>
            </Tooltip>
          </div>
        </div>
        <div className="min-h-0 flex-1 overflow-auto bg-background">
          <PreviewContent
            kind={kind}
            source={source}
            state={state}
            onDownload={handleDownload}
          />
        </div>
      </div>
    </div>,
    document.body,
  );
}

// ---------------------------------------------------------------------------
// Dispatch
// ---------------------------------------------------------------------------

// Dispatch on PreviewKind. New cases go here; remember that the modal frame
// (header, close, Download CTA, ESC handling) is shared — sub-renderers only
// own the content area.
function PreviewContent({
  kind,
  source,
  state,
  onDownload,
}: {
  kind: PreviewKind | null;
  source: PreviewSource;
  state: PreviewState;
  onDownload: () => void;
}) {
  const { t } = useT("editor");

  if (kind === null) {
    return (
      <UnsupportedFallback
        message={t(($) => $.attachment.preview_unsupported)}
        onDownload={onDownload}
      />
    );
  }

  // Text kinds need the attachment id for the /content proxy. The tryOpen
  // gate prevents URL-only sources from reaching here for text kinds, but
  // be defensive — a direct mount of <AttachmentPreviewModal> with a URL
  // source whose filename later resolves to a text kind would otherwise
  // crash on a null id.
  if (
    (kind === "markdown" || kind === "html" || kind === "text") &&
    !state.attachmentId
  ) {
    return (
      <UnsupportedFallback
        message={t(($) => $.attachment.preview_unsupported)}
        onDownload={onDownload}
      />
    );
  }

  switch (kind) {
    case "image":
      return (
        <div className="flex h-full w-full items-center justify-center bg-black/40 p-4">
          <img
            src={state.mediaUrl}
            alt={state.filename}
            className="h-full w-full rounded-lg object-contain"
          />
        </div>
      );
    case "pdf":
      // #591/#799 (Iris): unreachable — both open() and tryOpen() dispatch
      // pdf sources straight to openExternal via dispatchPreviewSource
      // (above `useAttachmentPreview`) and never call setCurrent, so this
      // modal never mounts for a pdf source. No fallback dialog kept.
      return null;
    case "video":
      return (
        <div className="flex h-full w-full items-center justify-center bg-black">
          <video
            src={state.mediaUrl}
            controls
            className="h-full w-full object-contain"
          />
        </div>
      );
    case "audio":
      return (
        <div className="flex h-full w-full items-center justify-center p-8">
          <audio src={state.mediaUrl} controls className="w-full max-w-xl" />
        </div>
      );
    case "markdown":
      return (
        <TextBackedPreview
          attachmentId={state.attachmentId!}
          onDownload={onDownload}
          render={(text) => (
            <ReadonlyContent
              content={text}
              className="px-6 py-4"
              attachments={source.kind === "full" ? [source.attachment] : []}
            />
          )}
        />
      );
    case "html":
      return (
        <TextBackedPreview
          attachmentId={state.attachmentId!}
          onDownload={onDownload}
          render={(text) => (
            <HtmlPreviewBody
              source={{ kind: "inline", html: text }}
              title={state.filename}
              className="h-full w-full"
              iframeClassName="rounded-none border-0"
            />
          )}
        />
      );
    case "text":
      return (
        <TextBackedPreview
          attachmentId={state.attachmentId!}
          onDownload={onDownload}
          render={(text) => (
            <CodeBlockStatic
              language={extensionToLanguage(state.filename)}
              body={text}
              className="px-6 py-4"
            />
          )}
        />
      );
  }
}

// ---------------------------------------------------------------------------
// Text-backed preview — fetches body once, then hands to the render prop
// ---------------------------------------------------------------------------

// React Query owns server state per the project convention; re-opening the
// same attachment hits the cache instead of re-fetching. Query is keyed on
// the attachment id alone — the 30 min TTL on the server-side signed URL
// is much longer than any plausible preview session.
function TextBackedPreview({
  attachmentId,
  onDownload,
  render,
}: {
  attachmentId: string;
  onDownload: () => void;
  render: (text: string) => ReactNode;
}) {
  const { t } = useT("editor");
  const query = useAttachmentHtmlText(attachmentId);

  if (query.isLoading) {
    return (
      <div className="flex h-full items-center justify-center gap-2 text-sm text-muted-foreground">
        <Loader2 className="size-4 animate-spin" />
        {t(($) => $.attachment.preview_loading)}
      </div>
    );
  }
  if (query.error) {
    if (query.error instanceof PreviewTooLargeError) {
      return (
        <UnsupportedFallback
          message={t(($) => $.attachment.preview_too_large)}
          onDownload={onDownload}
        />
      );
    }
    if (query.error instanceof PreviewUnsupportedError) {
      return (
        <UnsupportedFallback
          message={t(($) => $.attachment.preview_unsupported)}
          onDownload={onDownload}
        />
      );
    }
    return (
      <UnsupportedFallback
        message={t(($) => $.attachment.preview_failed)}
        onDownload={onDownload}
      />
    );
  }
  if (!query.data) return null;
  return <>{render(query.data.text)}</>;
}

// ---------------------------------------------------------------------------
// Fallback — used for 413 / 415 / unknown kinds
// ---------------------------------------------------------------------------

function UnsupportedFallback({
  message,
  onDownload,
}: {
  message: string;
  onDownload: () => void;
}) {
  const { t } = useT("editor");
  return (
    <div className="flex h-full flex-col items-center justify-center gap-3 px-8 text-center">
      <FileText className="size-8 text-muted-foreground" />
      <p className="text-sm text-muted-foreground">{message}</p>
      <div className="flex items-center gap-2">
        <button
          type="button"
          className="inline-flex items-center gap-2 rounded-md border border-border bg-background px-3 py-1.5 text-sm transition-colors hover:bg-muted"
          onClick={onDownload}
        >
          <Download className="size-4" />
          {t(($) => $.image.download)}
        </button>
      </div>
    </div>
  );
}

// Re-export the predicate from the dispatch util so entry-point components
// only need a single import to gate the Eye button.
export { isPreviewable } from "./utils/preview";
