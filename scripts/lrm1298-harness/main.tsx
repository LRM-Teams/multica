/**
 * LRM-1298 focus-contract harness.
 *
 * Why a harness on top of the Vitest suite: jsdom never moves focus for a
 * real Tab keypress, so the jsdom tests can only assert what our own handler
 * does when it intercepts the key. They cannot show what the browser does
 * with the frames we do NOT intercept — i.e. whether the dialog is genuinely
 * closed off. This harness presses real Tab keys in real Chromium.
 *
 * `?variant=after` mounts the REAL `AttachmentPreviewModal`.
 * `?variant=before` mounts `LegacyPreviewFrame`, whose JSX is the overlay from
 * `origin/dev` verbatim (portal + role=dialog + aria-modal + Escape/backdrop
 * close, no focus management at all) — the shipped defect, for contrast.
 *
 * Temporary tooling: delete after the shots are attached to LRM-1298.
 */
import { StrictMode, useCallback, useEffect, useState } from "react";
import { createPortal } from "react-dom";
import { createRoot } from "react-dom/client";
import { I18nextProvider } from "react-i18next";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { Download, FileText, X } from "lucide-react";
import { createI18n } from "../../packages/core/i18n/create-i18n";
import { NavigationProvider } from "../../packages/views/navigation";
import { AttachmentPreviewModal } from "../../packages/views/editor/attachment-preview-modal";
import type { Attachment } from "../../packages/core/types";
import zhEditor from "../../packages/views/locales/zh-Hans/editor.json";
import zhCommon from "../../packages/views/locales/zh-Hans/common.json";
import "./harness.css";

const i18n = createI18n("zh-Hans", {
  "zh-Hans": { editor: zhEditor, common: zhCommon },
});

const params = new URLSearchParams(window.location.search);
const theme = params.get("theme") === "dark" ? "dark" : "light";
const variant = params.get("variant") === "before" ? "before" : "after";
const autoOpen = params.get("open") !== "0";
// LRM-1177 shape: the trigger is conditionally rendered, so it is detached the
// whole time the modal is open and returns as a *different* DOM node.
const unmountTrigger = params.get("unmountTrigger") === "1";
document.documentElement.classList.add(theme);

// Inline SVG so the frame never depends on the network (a broken image would
// change layout and make the shots unreadable).
const IMAGE_DATA_URL = `data:image/svg+xml;utf8,${encodeURIComponent(
  `<svg xmlns="http://www.w3.org/2000/svg" width="640" height="400">
     <rect width="640" height="400" fill="#1264a3"/>
     <text x="50%" y="50%" fill="#ffffff" font-family="sans-serif"
           font-size="28" text-anchor="middle">preview payload</text>
   </svg>`,
)}`;

const ATTACHMENT: Attachment = {
  id: "3f2504e0-4f89-11d3-9a0c-0305e82c3301",
  workspace_id: "ws-1",
  issue_id: null,
  comment_id: null,
  chat_session_id: null,
  chat_message_id: null,
  uploader_type: "member",
  uploader_id: "u-1",
  filename: "quarterly-chart.png",
  url: IMAGE_DATA_URL,
  download_url: IMAGE_DATA_URL,
  markdown_url: IMAGE_DATA_URL,
  content_type: "image/png",
  size_bytes: 12_345,
  created_at: "2026-08-04T00:00:00Z",
};

const navigation = {
  push: () => {},
  replace: () => {},
  back: () => {},
  pathname: "/acme/channels",
  searchParams: new URLSearchParams(),
  getShareableUrl: (p: string) => `https://app.example${p}`,
};

/**
 * `origin/dev` frame, copied verbatim (minus the kind dispatch, which is
 * irrelevant to focus): portal + role="dialog" + aria-modal="true", Escape and
 * backdrop close, and zero `.focus()` calls anywhere.
 */
function LegacyPreviewFrame({
  open,
  onClose,
}: {
  open: boolean;
  onClose: () => void;
}) {
  useEffect(() => {
    if (!open) return;
    const handler = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    document.addEventListener("keydown", handler);
    return () => document.removeEventListener("keydown", handler);
  }, [open, onClose]);

  if (!open) return null;

  return createPortal(
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/80 p-4"
      onClick={onClose}
      role="dialog"
      aria-modal="true"
      aria-label={ATTACHMENT.filename}
    >
      <div
        className="flex h-[min(90vh,calc(100vh-2rem))] w-full max-w-6xl flex-col overflow-hidden rounded-lg bg-background shadow-xl"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center gap-2 border-b border-border bg-muted/30 px-4 py-2">
          <FileText className="size-4 shrink-0 text-muted-foreground" />
          <p className="truncate text-sm font-medium">{ATTACHMENT.filename}</p>
          <span className="ml-1 shrink-0 text-xs text-muted-foreground">
            {ATTACHMENT.content_type}
          </span>
          <div className="ml-auto flex items-center gap-1">
            <button
              type="button"
              className="rounded-md p-1.5 text-muted-foreground transition-colors hover:bg-secondary hover:text-foreground"
              aria-label="下载"
            >
              <Download className="size-4" />
            </button>
            <button
              type="button"
              className="rounded-md p-1.5 text-muted-foreground transition-colors hover:bg-secondary hover:text-foreground"
              aria-label="关闭"
              onClick={onClose}
            >
              <X className="size-4" />
            </button>
          </div>
        </div>
        <div className="min-h-0 flex-1 overflow-auto bg-background">
          <div className="flex h-full w-full items-center justify-center bg-black/40 p-4">
            <img
              src={IMAGE_DATA_URL}
              alt={ATTACHMENT.filename}
              className="h-full w-full rounded-lg object-contain"
            />
          </div>
        </div>
      </div>
    </div>,
    document.body,
  );
}

function Harness() {
  const [open, setOpen] = useState(false);
  const close = useCallback(() => setOpen(false), []);

  // Auto-open through the real trigger click so the "restore focus to the
  // trigger" path is exercised exactly like a user's click would.
  useEffect(() => {
    if (!autoOpen) return;
    const trigger = document.getElementById("preview-trigger");
    trigger?.focus();
    trigger?.click();
  }, []);

  return (
    <div className="min-h-screen space-y-4 p-8">
      <header className="space-y-1">
        <h1 className="text-lg font-semibold">
          LRM-1298 · 附件预览弹层焦点合同 · {variant.toUpperCase()}
        </h1>
        <p className="text-sm text-muted-foreground">
          {variant === "after"
            ? "真实 AttachmentPreviewModal（初始焦点 + Tab 陷阱 + 关闭回焦）"
            : "origin/dev 原样帧：aria-modal 却无任何焦点管理"}
        </p>
      </header>

      <div className="flex flex-wrap items-center gap-3">
        {(!open || !unmountTrigger) && (
          <button
            type="button"
            id="preview-trigger"
            className="rounded-md border border-border bg-background px-3 py-1.5 text-sm"
            onClick={() => setOpen(true)}
          >
            打开预览（触发元素）
          </button>
        )}
        <button type="button" id="bg-control-1" className="rounded-md border border-border px-3 py-1.5 text-sm">
          背景控件 1
        </button>
        <a id="bg-control-2" href="#" className="rounded-md border border-border px-3 py-1.5 text-sm">
          背景链接 2
        </a>
        <input
          id="bg-control-3"
          className="rounded-md border border-border px-3 py-1.5 text-sm"
          placeholder="背景输入框 3"
        />
      </div>

      <p className="text-sm text-muted-foreground">
        弹层打开后，下方背景控件本应完全不可达（aria-modal=true）。
      </p>

      {variant === "after" ? (
        <AttachmentPreviewModal
          source={{ kind: "full", attachment: ATTACHMENT }}
          open={open}
          onClose={close}
        />
      ) : (
        <LegacyPreviewFrame open={open} onClose={close} />
      )}
    </div>
  );
}

const queryClient = new QueryClient({
  defaultOptions: { queries: { retry: false } },
});

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <I18nextProvider i18n={i18n}>
      <QueryClientProvider client={queryClient}>
        <NavigationProvider value={navigation}>
          <Harness />
        </NavigationProvider>
      </QueryClientProvider>
    </I18nextProvider>
  </StrictMode>,
);
