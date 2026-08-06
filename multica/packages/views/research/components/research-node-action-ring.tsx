"use client";

import { useCallback, useEffect, useEffectEvent, useRef } from "react";
import {
  Copy,
  Eye,
  LocateFixed,
  MoreHorizontal,
  RotateCcw,
  Sparkles,
} from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import type { ResearchGraphNode } from "@multica/core/types";
import { useT } from "../../i18n/use-t";
import {
  ringActionsForNode,
  type NodeRingAction,
} from "../lib/node-action-ring";

function ActionIcon({ id }: { id: NodeRingAction }) {
  switch (id) {
    case "detail":
      return <Eye className="size-4" aria-hidden />;
    case "locate_source":
      return <LocateFixed className="size-4" aria-hidden />;
    case "copy_prompt":
      return <Copy className="size-4" aria-hidden />;
    case "retry":
      return <RotateCcw className="size-4" aria-hidden />;
    case "dig_deeper":
      return <Sparkles className="size-4" aria-hidden />;
    case "more":
      return <MoreHorizontal className="size-4" aria-hidden />;
  }
}

export function ResearchNodeActionRing({
  node,
  mode,
  onAction,
  onClose,
}: {
  node: ResearchGraphNode;
  /** Desktop floating grid vs narrow bottom sheet. */
  mode: "ring" | "sheet";
  onAction: (action: NodeRingAction) => void;
  onClose: () => void;
}) {
  const { t } = useT("research");
  const actions = ringActionsForNode(node);
  const dialogRef = useRef<HTMLDialogElement | null>(null);

  const bindDialog = useCallback((dialog: HTMLDialogElement | null) => {
    dialogRef.current = dialog;
    if (!dialog || dialog.open) return;
    if (typeof dialog.showModal === "function") dialog.showModal();
    else dialog.setAttribute("open", "");
  }, []);

  const onEscapeClose = useEffectEvent(() => {
    onClose();
  });

  useEffect(() => {
    if (mode === "sheet") return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.preventDefault();
        onEscapeClose();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [mode]);

  const labelFor = (id: NodeRingAction) => {
    switch (id) {
      case "detail":
        return t(($) => $.ring.detail);
      case "locate_source":
        return t(($) => $.ring.locate_source);
      case "copy_prompt":
        return t(($) => $.ring.copy_prompt);
      case "retry":
        return t(($) => $.ring.retry);
      case "dig_deeper":
        return t(($) => $.ring.dig_deeper);
      case "more":
        return t(($) => $.ring.more);
    }
  };

  if (mode === "sheet") {
    return (
      <dialog
        ref={bindDialog}
        aria-label={t(($) => $.ring.title)}
        className="fixed inset-x-0 bottom-0 z-20 m-0 max-h-[38%] w-full max-w-none rounded-t-2xl border-0 border-t bg-card p-0 px-4 pb-5 pt-2 shadow-[0_-12px_32px_oklch(0_0_0_/_0.35)] open:block"
        onCancel={(event) => {
          event.preventDefault();
          const dialog = dialogRef.current;
          if (dialog?.open) {
            if (typeof dialog.close === "function") dialog.close();
            else dialog.removeAttribute("open");
          }
          onClose();
        }}
        onClose={onClose}
      >
        <div className="mx-auto mb-2.5 mt-1 h-1 w-9 rounded-full bg-muted-foreground/40" />
        <p className="mb-2 text-[11px] font-bold tracking-wide text-muted-foreground uppercase">
          {t(($) => $.ring.title)}
        </p>
        <div role="menu" className="flex flex-col gap-0.5 overflow-y-auto">
          {actions.map((a) => (
            <button
              key={a.id}
              type="button"
              role="menuitem"
              disabled={a.disabled}
              aria-label={labelFor(a.id)}
              onClick={() => {
                if (a.disabled) return;
                onAction(a.id);
              }}
              className={cn(
                "flex h-11 items-center gap-3 rounded-lg px-1 text-sm text-foreground",
                a.disabled && "opacity-35",
                a.candidate && "text-warning",
              )}
            >
              <span
                className={cn(
                  "flex size-8 shrink-0 items-center justify-center rounded-full bg-muted",
                  a.primary &&
                    "bg-brand text-brand-foreground shadow-[0_0_14px_color-mix(in_oklch,var(--brand)_50%,transparent)]",
                  a.candidate &&
                    "border border-dashed border-warning/75 bg-transparent text-warning",
                )}
              >
                <ActionIcon id={a.id} />
              </span>
              <span className="font-medium">{labelFor(a.id)}</span>
              {a.candidate ? (
                <span className="ml-auto text-[10px] text-muted-foreground">
                  {t(($) => $.ring.soon)}
                </span>
              ) : null}
            </button>
          ))}
        </div>
      </dialog>
    );
  }

  return (
    <div
      role="menu"
      aria-label={t(($) => $.ring.title)}
      className="relative z-20 grid animate-in fade-in zoom-in-95 grid-cols-3 gap-x-1.5 gap-y-2 rounded-[14px] border bg-card/95 p-2.5 shadow-lg backdrop-blur-md duration-150"
      style={{
        // NodeToolbar owns placement; size stays fixed 2×3 grid.
        width: 3 * 52 + 2 * 6 + 20,
      }}
    >
      {actions.map((a) => (
        <button
          key={a.id}
          type="button"
          role="menuitem"
          disabled={a.disabled}
          aria-label={labelFor(a.id)}
          onClick={(e) => {
            e.stopPropagation();
            if (a.disabled) return;
            onAction(a.id);
          }}
          className={cn(
            "ar flex flex-col items-center gap-1",
            a.disabled && "opacity-32",
          )}
        >
          <span
            className={cn(
              "flex size-[38px] items-center justify-center rounded-full bg-muted text-foreground",
              a.primary &&
                "bg-brand text-brand-foreground shadow-[0_0_14px_color-mix(in_oklch,var(--brand)_50%,transparent)]",
              a.candidate &&
                "border border-dashed border-warning/75 bg-transparent text-warning",
            )}
          >
            <ActionIcon id={a.id} />
          </span>
          <span
            className={cn(
              "text-[9px] tracking-wide text-muted-foreground whitespace-nowrap",
              a.primary && "text-brand",
              a.candidate && "text-warning",
            )}
          >
            {labelFor(a.id)}
          </span>
        </button>
      ))}
      <p className="col-span-3 mt-0.5 border-t pt-1.5 text-center text-[9.5px] text-muted-foreground">
        {t(($) => $.ring.esc_hint)}
      </p>
    </div>
  );
}
