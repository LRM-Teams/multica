"use client";

import { useCallback, useEffect, useEffectEvent, useRef } from "react";
import { createPortal } from "react-dom";
import { CheckCircle2, Home, Plus, X, FileText, AlertCircle } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import type { CompletionGuideKind } from "../lib/completion-guide";

/**
 * LRM-832 — terminal completion / failure guide card.
 * Narrow: full-width sheet; desktop: centered card. Below Delivery modal (z-80).
 * Native `<dialog>` for focus trap / Escape (react-doctor a11y).
 *
 * LRM-1244 — no full-screen dismiss scrim. A `tabindex="-1"` overlay is still
 * focusable, so native dialog focusing steps parked initial focus on the
 * invisible layer (same root cause as LRM-1243 / #2082). Gutter dismiss is a
 * click on the dialog box itself (`event.target === dialog`).
 */
export function ResearchCompletionCard({
  kind,
  onViewReport,
  onNewResearch,
  onHome,
  onDismiss,
}: {
  kind: CompletionGuideKind;
  onViewReport: () => void;
  onNewResearch: () => void;
  onHome: () => void;
  onDismiss: () => void;
}) {
  const { t } = useT("research");
  const done = kind === "done";
  const dialogRef = useRef<HTMLDialogElement | null>(null);

  const bindDialog = useCallback((dialog: HTMLDialogElement | null) => {
    dialogRef.current = dialog;
    if (!dialog || dialog.open) return;
    if (typeof dialog.showModal === "function") dialog.showModal();
    else dialog.setAttribute("open", "");
  }, []);

  const closeThen = (fn: () => void) => {
    const dialog = dialogRef.current;
    if (dialog?.open) {
      if (typeof dialog.close === "function") dialog.close();
      else dialog.removeAttribute("open");
    }
    fn();
  };

  const onGutterClose = useEffectEvent(() => {
    closeThen(onDismiss);
  });

  useEffect(() => {
    const dialog = dialogRef.current;
    if (!dialog) return;
    const handle = (event: MouseEvent) => {
      if (event.target === dialog) onGutterClose();
    };
    dialog.addEventListener("click", handle);
    return () => dialog.removeEventListener("click", handle);
    // Intentionally omit onGutterClose: useEffectEvent must not be listed in
    // deps (react-doctor: Effect Event listed in effect deps).
    // eslint-disable-next-line react-hooks/exhaustive-deps -- SoT LRM-1243
  }, []);

  if (typeof document === "undefined") return null;

  return createPortal(
    <dialog
      ref={bindDialog}
      data-testid="research-completion-card"
      data-completion-kind={kind}
      className={cn(
        "fixed inset-0 z-[70] m-0 flex h-dvh max-h-none w-screen max-w-none items-end justify-center border-0 bg-transparent p-0 open:flex sm:items-center sm:p-6",
        "backdrop:bg-black/45 backdrop:backdrop-blur-[1px]",
      )}
      aria-labelledby="research-completion-title"
      aria-modal="true"
      onCancel={(event) => {
        event.preventDefault();
        closeThen(onDismiss);
      }}
      onClose={onDismiss}
    >
      <div
        role="document"
        className={cn(
          "relative z-10 flex w-full max-w-lg flex-col gap-4 border bg-card p-4 shadow-2xl",
          // Narrow: edge-to-edge bottom sheet; no horizontal overflow.
          "rounded-t-2xl sm:rounded-2xl",
          "max-h-[min(85dvh,640px)] overflow-y-auto",
        )}
      >
        <div className="flex items-start gap-3">
          <span
            className={cn(
              "mt-0.5 flex size-9 shrink-0 items-center justify-center rounded-full",
              done ? "bg-success/15 text-success-strong" : "bg-destructive/12 text-destructive",
            )}
            aria-hidden
          >
            {done ? (
              <CheckCircle2 className="size-5" strokeWidth={2} aria-hidden />
            ) : (
              <AlertCircle className="size-5" strokeWidth={2} aria-hidden />
            )}
          </span>
          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-2">
              <h2
                id="research-completion-title"
                className="text-base font-semibold tracking-tight"
              >
                {done
                  ? t(($) => $.completion_guide.done_title)
                  : t(($) => $.completion_guide.failed_title)}
              </h2>
              <span
                className={cn(
                  "rounded-md border px-1.5 py-0.5 text-[10px] font-semibold",
                  done
                    ? "border-success/35 bg-success/10 text-success-strong"
                    : "border-destructive/35 bg-destructive/10 text-destructive",
                )}
              >
                {done
                  ? t(($) => $.completion_guide.done_badge)
                  : t(($) => $.completion_guide.failed_badge)}
              </span>
            </div>
            <p className="mt-1 text-sm text-muted-foreground">
              {done
                ? t(($) => $.completion_guide.done_body)
                : t(($) => $.completion_guide.failed_body)}
            </p>
          </div>
          <Button
            type="button"
            size="icon-sm"
            variant="ghost"
            aria-label={t(($) => $.completion_guide.dismiss)}
            onClick={() => closeThen(onDismiss)}
          >
            <X className="size-4" aria-hidden />
          </Button>
        </div>

        <div className="flex flex-col gap-2 sm:flex-row sm:flex-wrap">
          <Button
            type="button"
            className="w-full bg-brand text-brand-foreground hover:bg-brand/90 sm:w-auto sm:flex-1"
            onClick={() => closeThen(onViewReport)}
          >
            <FileText className="size-4" aria-hidden />
            {t(($) => $.completion_guide.view_report)}
          </Button>
          <Button
            type="button"
            variant="outline"
            className="w-full sm:w-auto sm:flex-1"
            onClick={() => closeThen(onNewResearch)}
          >
            <Plus className="size-4" aria-hidden />
            {t(($) => $.completion_guide.new_research)}
          </Button>
          <Button
            type="button"
            variant="ghost"
            className="w-full sm:w-auto sm:flex-1"
            onClick={() => closeThen(onHome)}
          >
            <Home className="size-4" aria-hidden />
            {t(($) => $.completion_guide.home)}
          </Button>
        </div>
      </div>
    </dialog>,
    document.body,
  );
}
