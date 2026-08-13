"use client";

import {
  useCallback,
  useEffect,
  useId,
  useRef,
  useState,
  type KeyboardEvent as ReactKeyboardEvent,
  type ReactNode,
} from "react";
import { Copy, Eye, GitFork, LocateFixed, Play, RotateCcw, UserRoundCog } from "lucide-react";
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
    case "continue":
      return <Play className="size-4" aria-hidden />;
    case "fork":
      return <GitFork className="size-4" aria-hidden />;
    case "retry":
      return <RotateCcw className="size-4" aria-hidden />;
    case "reassign":
      return <UserRoundCog className="size-4" aria-hidden />;
  }
}

/** LRM-1105: disabled items stay in tab order via aria-disabled (1102). */
function RingActionButton({
  action,
  label,
  tabIndex,
  buttonRef,
  onFocusIndex,
  onActivate,
  className,
  iconClassName,
  labelClassName,
  trailing,
}: {
  action: { id: NodeRingAction; primary?: boolean; disabled?: boolean; candidate?: boolean };
  label: string;
  tabIndex: number;
  buttonRef?: (el: HTMLButtonElement | null) => void;
  onFocusIndex: () => void;
  onActivate: () => void;
  className?: string;
  iconClassName?: string;
  labelClassName?: string;
  trailing?: ReactNode;
}) {
  const disabled = !!action.disabled;
  return (
    <button
      ref={buttonRef}
      type="button"
      role="menuitem"
      tabIndex={tabIndex}
      aria-disabled={disabled || undefined}
      aria-label={label}
      onFocus={onFocusIndex}
      onClick={(e) => {
        e.stopPropagation();
        if (disabled) return;
        onActivate();
      }}
      onKeyDown={(e) => {
        if (disabled && (e.key === "Enter" || e.key === " ")) {
          e.preventDefault();
          e.stopPropagation();
        }
      }}
      className={cn(className, disabled && "opacity-32")}
    >
      <span className={iconClassName}>
        <ActionIcon id={action.id} />
      </span>
      <span className={labelClassName}>{label}</span>
      {trailing}
    </button>
  );
}

export function ResearchNodeActionRing({
  node,
  mode,
  onAction,
  onClose,
  pendingAction = null,
  error = null,
}: {
  node: ResearchGraphNode;
  /** Desktop floating grid vs narrow bottom sheet. */
  mode: "ring" | "sheet";
  onAction: (action: NodeRingAction) => void;
  onClose: () => void;
  pendingAction?: NodeRingAction | null;
  error?: string | null;
}) {
  const { t } = useT("research");
  const actions = ringActionsForNode(node);
  const dialogRef = useRef<HTMLDialogElement | null>(null);
  const itemRefs = useRef<Array<HTMLButtonElement | null>>([]);
  const [roving, setRoving] = useState({ key: `${node.id}:${mode}`, index: 0 });
  const rovingKey = `${node.id}:${mode}`;
  const focusIndex = roving.key === rovingKey ? roving.index : 0;
  const setFocusIndex = (index: number) => setRoving({ key: rovingKey, index });
  const menuId = useId();

  const bindDialog = useCallback((dialog: HTMLDialogElement | null) => {
    dialogRef.current = dialog;
    if (!dialog || dialog.open) return;
    if (typeof dialog.showModal === "function") dialog.showModal();
    else dialog.setAttribute("open", "");
  }, []);

  const onCloseRef = useRef(onClose);
  onCloseRef.current = onClose;

  useEffect(() => {
    if (mode === "sheet") return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.preventDefault();
        onCloseRef.current();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [mode]);

  // Focus primary item when ring opens / node changes — do not mirror props into state.
  useEffect(() => {
    queueMicrotask(() => itemRefs.current[0]?.focus());
  }, [rovingKey]);

  const labelFor = (id: NodeRingAction) => {
    switch (id) {
      case "detail":
        return t(($) => $.ring.detail);
      case "locate_source":
        return t(($) => $.ring.locate_source);
      case "copy_prompt":
        return t(($) => $.ring.copy_prompt);
      case "continue":
        return t(($) => $.ring.continue);
      case "fork":
        return t(($) => $.ring.fork);
      case "retry":
        return t(($) => $.ring.retry);
      case "reassign":
        return t(($) => $.ring.reassign);
    }
  };

  const moveFocus = (delta: number) => {
    if (actions.length === 0) return;
    const next = (focusIndex + delta + actions.length) % actions.length;
    setFocusIndex(next);
    itemRefs.current[next]?.focus();
  };

  const onMenuKeyDown = (e: ReactKeyboardEvent) => {
    if (e.key === "ArrowRight" || e.key === "ArrowDown") {
      e.preventDefault();
      moveFocus(1);
      return;
    }
    if (e.key === "ArrowLeft" || e.key === "ArrowUp") {
      e.preventDefault();
      moveFocus(-1);
      return;
    }
    if (e.key === "Home") {
      e.preventDefault();
      setFocusIndex(0);
      itemRefs.current[0]?.focus();
      return;
    }
    if (e.key === "End") {
      e.preventDefault();
      const last = actions.length - 1;
      setFocusIndex(last);
      itemRefs.current[last]?.focus();
      return;
    }
    if (e.key === "Escape") {
      e.preventDefault();
      onClose();
    }
  };

  if (mode === "sheet") {
    return (
      <dialog
        ref={bindDialog}
        aria-label={t(($) => $.ring.title)}
        aria-busy={!!pendingAction || undefined}
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
        <div
          role="menu"
          id={menuId}
          tabIndex={-1}
          className="flex flex-col gap-0.5 overflow-y-auto"
          onKeyDown={onMenuKeyDown}
        >
          {actions.map((a, index) => (
            <RingActionButton
              key={a.id}
              action={a}
              label={labelFor(a.id)}
              tabIndex={focusIndex === index ? 0 : -1}
              buttonRef={(el) => {
                itemRefs.current[index] = el;
              }}
              onFocusIndex={() => setFocusIndex(index)}
              onActivate={() => { if (!pendingAction) onAction(a.id); }}
              className={cn(
                "flex h-11 items-center gap-3 rounded-lg px-1 text-sm text-foreground",
                a.candidate && "text-warning",
              )}
              iconClassName={cn(
                "flex size-8 shrink-0 items-center justify-center rounded-full bg-muted",
                a.primary &&
                  "bg-brand text-brand-foreground shadow-[0_0_14px_color-mix(in_oklch,var(--brand)_50%,transparent)]",
                a.candidate &&
                  "border border-dashed border-warning/75 bg-transparent text-warning",
              )}
              labelClassName="font-medium"
              trailing={
                a.candidate ? (
                  <span className="ml-auto text-[10px] text-muted-foreground">
                    {t(($) => $.ring.soon)}
                  </span>
                ) : null
              }
            />
          ))}
        </div>
        {error ? <p role="alert" className="mt-2 text-xs text-destructive">{error}</p> : null}
      </dialog>
    );
  }

  return (
    <div
      role="menu"
      id={menuId}
      tabIndex={-1}
      aria-label={t(($) => $.ring.title)}
        aria-busy={!!pendingAction || undefined}
      className="relative z-20 grid motion-safe:animate-in motion-safe:fade-in motion-safe:zoom-in-95 grid-cols-3 gap-x-1.5 gap-y-2 rounded-[14px] border bg-card/95 p-2.5 shadow-lg backdrop-blur-md motion-safe:duration-150"
      style={{
        // NodeToolbar owns placement; size stays fixed 2×3 grid.
        width: 3 * 52 + 2 * 6 + 20,
      }}
      onKeyDown={onMenuKeyDown}
    >
      {actions.map((a, index) => (
        <RingActionButton
          key={a.id}
          action={a}
          label={labelFor(a.id)}
          tabIndex={focusIndex === index ? 0 : -1}
          buttonRef={(el) => {
            itemRefs.current[index] = el;
          }}
          onFocusIndex={() => setFocusIndex(index)}
          onActivate={() => { if (!pendingAction) onAction(a.id); }}
          className={cn("ar flex flex-col items-center gap-1", a.candidate && "text-warning")}
          iconClassName={cn(
            "flex size-[38px] items-center justify-center rounded-full bg-muted text-foreground",
            a.primary &&
              "bg-brand text-brand-foreground shadow-[0_0_14px_color-mix(in_oklch,var(--brand)_50%,transparent)]",
            a.candidate &&
              "border border-dashed border-warning/75 bg-transparent text-warning",
          )}
          labelClassName={cn(
            "text-[9px] tracking-wide text-muted-foreground whitespace-nowrap",
            a.primary && "text-brand",
            a.candidate && "text-warning",
          )}
        />
      ))}
      {error ? <p role="alert" className="col-span-3 text-xs text-destructive">{error}</p> : null}
      <p className="col-span-3 mt-0.5 border-t pt-1.5 text-center text-[9.5px] text-muted-foreground">
        {t(($) => $.ring.esc_hint)}
      </p>
    </div>
  );
}
