"use client";

import { useEffect, useRef } from "react";
import type { ResearchGraphNode } from "@multica/core/types";
import { cn } from "@multica/ui/lib/utils";
import { Tooltip, TooltipTrigger, TooltipContent } from "@multica/ui/components/ui/tooltip";
import { useT } from "../../i18n/use-t";
import {
  cardMenuItemsForNode,
  type CardMenuActionId,
} from "../lib/card-menu-actions";

export function ResearchCardMenu({
  node,
  onClose,
  onRetry,
  onViewDetail,
}: {
  node: ResearchGraphNode;
  onClose: () => void;
  onRetry?: (node: ResearchGraphNode) => void;
  onViewDetail?: (node: ResearchGraphNode) => void;
}) {
  const { t } = useT("research");
  const ref = useRef<HTMLDivElement | null>(null);
  const items = cardMenuItemsForNode(node);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.preventDefault();
        onClose();
      }
    };
    const onDoc = (e: MouseEvent) => {
      if (!ref.current?.contains(e.target as globalThis.Node)) onClose();
    };
    window.addEventListener("keydown", onKey);
    document.addEventListener("mousedown", onDoc);
    return () => {
      window.removeEventListener("keydown", onKey);
      document.removeEventListener("mousedown", onDoc);
    };
  }, [onClose]);

  const labelFor = (id: CardMenuActionId) => {
    switch (id) {
      case "view_evidence":
        return t(($) => $.card_menu.view_evidence);
      case "view_io":
        return t(($) => $.card_menu.view_io);
      case "fork_from":
        return t(($) => $.card_menu.fork_from);
      case "retry_failed":
        return t(($) => $.card_menu.retry_failed);
      case "reassign":
        return t(($) => $.card_menu.reassign);
      case "cancel_run":
        return t(($) => $.card_menu.cancel_run);
    }
  };

  const run = (id: CardMenuActionId) => {
    const item = items.find((i) => i.id === id);
    if (!item?.enabled) return;
    if (item.needConfirm) {
      const ok = window.confirm(t(($) => $.card_menu.confirm, { action: labelFor(id) }));
      if (!ok) return;
    }
    switch (id) {
      case "view_evidence":
      case "view_io":
        onViewDetail?.(node);
        break;
      case "retry_failed":
        onRetry?.(node);
        break;
      default:
        break;
    }
    onClose();
  };

  return (
    <div
      ref={ref}
      role="menu"
      tabIndex={-1}
      data-testid="research-card-menu"
      className="nodrag nopan absolute top-8 right-1 z-30 min-w-[180px] rounded-lg border bg-card p-1 outline-none"
      onClick={(e) => e.stopPropagation()}
      onPointerDown={(e) => e.stopPropagation()}
    >
      {items.map((item) => {
        const buttonContent = (
          <>
            <span>
              {labelFor(item.id)}
              {item.needConfirm ? "…" : ""}
            </span>
            {!item.enabled && item.disabledReason ? (
              <span className="mt-0.5 text-xs leading-snug text-muted-foreground">
                {item.disabledReason}
              </span>
            ) : null}
          </>
        );
        const buttonClassName = cn(
          "flex w-full flex-col items-start rounded-md px-2.5 py-1.5 text-left text-sm",
          item.enabled ? "hover:bg-muted" : "cursor-not-allowed text-muted-foreground",
          item.danger && item.enabled && "text-destructive",
        );
        const button = (
          <button
            type="button"
            role="menuitem"
            disabled={!item.enabled}
            className={buttonClassName}
            onClick={() => run(item.id)}
          >
            {buttonContent}
          </button>
        );
        const showTitle = !item.enabled && Boolean(item.disabledReason);
        if (showTitle) {
          return (
            <Tooltip key={item.id}>
              <TooltipTrigger
                render={
                  <button
                    type="button"
                    role="menuitem"
                    disabled={!item.enabled}
                    className={buttonClassName}
                    onClick={() => run(item.id)}
                  />
                }
              >
                {buttonContent}
              </TooltipTrigger>
              <TooltipContent side="top">{item.disabledReason}</TooltipContent>
            </Tooltip>
          );
        }
        return button;
      })}
    </div>
  );
}
