"use client";

import type { ReactNode } from "react";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";

export type ResearchD5RailMode = "chat" | "detail";

export function ResearchD5Rail({
  mode,
  onModeChange,
  chatPanel,
  detailPanel,
  composer,
  className,
}: {
  mode: ResearchD5RailMode;
  onModeChange: (mode: ResearchD5RailMode) => void;
  chatPanel: ReactNode;
  detailPanel: ReactNode;
  composer?: ReactNode;
  className?: string;
}) {
  const { t } = useT("research");

  return (
    <aside
      data-testid="research-d5-rail"
      data-rail-mode={mode}
      className={cn("d5-rail", className)}
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
      </div>
      <div className="d5-rail-body">{mode === "chat" ? chatPanel : detailPanel}</div>
      {mode === "chat" && composer ? (
        <div className="d5-rail-footer">{composer}</div>
      ) : null}
    </aside>
  );
}
