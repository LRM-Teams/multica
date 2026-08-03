/**
 * LRM-1174 gate-shot harness (temporary tooling; delete after the shots are
 * attached to the issue).
 *
 * Why a harness and not the app: the real dead-zone cell is a device that
 * reports `(pointer: fine)` AND a narrow window. jsdom never applies media
 * queries, so Vitest can only assert the class contract; only a real browser
 * shows the actual failure — `showModal()` puts the sheet in the top layer
 * (page inert) while `[@media(pointer:fine)]:hidden` paints nothing.
 *
 * Both class strings below are asserted against the real component source by
 * `scripts/lrm1174-gate-shots.mjs` (AFTER === working tree, BEFORE ===
 * origin/dev), so this harness cannot drift into testing a fiction.
 */
import { StrictMode, useEffect, useRef } from "react";
import { createRoot } from "react-dom/client";
import "./harness.css";

// origin/dev — visibility (CSS) and modality (JS) came from two sources.
const BEFORE_DIALOG_CLASS =
  "fixed inset-0 z-50 m-0 h-dvh max-h-none w-screen max-w-none border-0 bg-transparent p-0 backdrop:bg-black/10 [@media(pointer:fine)]:hidden";
// This branch — one source of truth: the JS gate alone.
const AFTER_DIALOG_CLASS =
  "fixed inset-0 z-50 m-0 h-dvh max-h-none w-screen max-w-none border-0 bg-transparent p-0 backdrop:bg-black/10";

const gate = new URLSearchParams(window.location.search).get("gate") === "before"
  ? "before"
  : "after";

function Harness() {
  const dialogRef = useRef<HTMLDialogElement | null>(null);
  useEffect(() => {
    const dialog = dialogRef.current;
    if (dialog && !dialog.open) dialog.showModal();
  }, []);
  return (
    <div className="flex h-dvh flex-col bg-background">
      <div className="border-b border-border px-3 py-2 text-[13.5px] font-medium">#调研模块开发</div>
      <div className="flex-1 space-y-1 overflow-hidden p-2">
        {["先把断点统一收口。", "长按这条消息打开操作面板。", "收到，我这边接着做。"].map(
          (line, index) => (
            <div
              key={line}
              className="group relative grid grid-cols-[28px_minmax(0,1fr)] gap-2.5 rounded-lg px-2 py-1 outline-none hover:bg-muted/35 focus-within:bg-muted/35"
              data-testid={index === 1 ? "probe-bubble" : undefined}
              tabIndex={-1}
            >
              <div className="mt-0.5 size-7 rounded-md bg-muted" />
              <div className="min-w-0 max-w-full">
                <div className="mb-0.5 text-[13.5px] font-medium">Alice Display</div>
                <div className="text-[13.5px] leading-6 text-foreground">{line}</div>
              </div>
            </div>
          ),
        )}
        {/* Inertness probe: while a modal <dialog> is open this must be
            unclickable. If the sheet is also invisible, the app looks frozen. */}
        <button
          type="button"
          data-testid="background-target"
          className="mt-4 inline-flex h-9 items-center rounded-md border border-border px-3 text-sm"
          onClick={() => {
            document.body.dataset.backgroundClicked = "true";
          }}
        >
          背景按钮（模态打开时应不可点）
        </button>
      </div>

      <dialog
        ref={dialogRef}
        data-testid="mobile-sheet"
        aria-label="Message actions"
        className={gate === "before" ? BEFORE_DIALOG_CLASS : AFTER_DIALOG_CLASS}
      >
        <form method="dialog" className="absolute inset-0">
          <button type="submit" aria-label="Message actions" className="h-full w-full cursor-default" />
        </form>
        <div
          data-testid="mobile-message-actions"
          data-message-action-surface="true"
          className="absolute inset-x-0 bottom-0 rounded-t-2xl border-t border-border bg-popover p-3 pb-[calc(env(safe-area-inset-bottom)+0.75rem)] text-popover-foreground shadow-2xl"
        >
          <div className="mx-auto mb-2 h-1 w-10 rounded-full bg-muted-foreground/25" />
          <div className="flex flex-col gap-1">
            {["添加表情回应", "复制", "引用回复"].map((label) => (
              <button
                key={label}
                type="button"
                className="inline-flex h-11 items-center gap-3 rounded-xl px-3 text-sm text-popover-foreground hover:bg-muted"
              >
                <span className="size-4 rounded bg-muted-foreground/30" />
                <span>{label}</span>
              </button>
            ))}
          </div>
        </div>
      </dialog>
    </div>
  );
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <Harness />
  </StrictMode>,
);
