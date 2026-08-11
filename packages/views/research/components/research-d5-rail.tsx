"use client";

import type { ComponentProps, ReactNode } from "react";
import { X } from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";

export type ResearchD5RailMode = "chat" | "detail";

export function ResearchD5Rail({
  mode,
  onModeChange,
  chatPanel,
  detailPanel,
  composer,
  onClose,
  className,
  ...rest
}: {
  mode: ResearchD5RailMode;
  onModeChange: (mode: ResearchD5RailMode) => void;
  chatPanel: ReactNode;
  detailPanel: ReactNode;
  composer?: ReactNode;
  onClose?: () => void;
  className?: string;
} & Pick<ComponentProps<"aside">, "inert">) {
  const { t } = useT("research");

  return (
    <aside
      data-testid="research-d5-rail"
      data-rail-mode={mode}
      className={cn("d5-rail", className)}
      {...rest}
    >
      <div className="d5-rail-tabs">
        <button
          type="button"
          className={cn("d5-rail-tab", mode === "chat" && "d5-rail-tab-active")}
          onClick={() => onModeChange("chat")}
        >
          {t(($) => $.d5.rail.chat_tab)}
        </button>
        <button
          type="button"
          className={cn("d5-rail-tab", mode === "detail" && "d5-rail-tab-active")}
          onClick={() => onModeChange("detail")}
        >
          {t(($) => $.d5.rail.detail_tab)}
        </button>
        {onClose ? (
          <button
            type="button"
            className="d5-rail-close"
            data-testid="research-d5-rail-close"
            aria-label={t(($) => $.d5.rail.hide)}
            onClick={onClose}
          >
            <X className="size-3.5" aria-hidden />
          </button>
        ) : null}
      </div>
      <div className="d5-rail-body">{mode === "chat" ? chatPanel : detailPanel}</div>
      {mode === "chat" && composer ? (
        <div className="d5-rail-footer">{composer}</div>
      ) : null}
    </aside>
  );
}
