"use client";

import {
  useId,
  useRef,
  type ComponentProps,
  type KeyboardEvent,
  type ReactNode,
} from "react";
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
  id,
  ...rest
}: {
  mode: ResearchD5RailMode;
  onModeChange: (mode: ResearchD5RailMode) => void;
  chatPanel: ReactNode;
  detailPanel: ReactNode;
  composer?: ReactNode;
  onClose?: () => void;
  className?: string;
} & Pick<ComponentProps<"aside">, "id" | "inert" | "aria-hidden">) {
  const { t } = useT("research");
  const generatedId = useId();
  const railId = id ?? `research-d5-rail-${generatedId}`;
  const chatTabId = `${railId}-chat-tab`;
  const detailTabId = `${railId}-detail-tab`;
  const chatPanelId = `${railId}-chat-panel`;
  const detailPanelId = `${railId}-detail-panel`;
  const chatTabRef = useRef<HTMLButtonElement>(null);
  const detailTabRef = useRef<HTMLButtonElement>(null);
  const handleTabKeyDown = (event: KeyboardEvent<HTMLButtonElement>) => {
    if (
      event.key !== "ArrowLeft" &&
      event.key !== "ArrowRight" &&
      event.key !== "Home" &&
      event.key !== "End"
    ) {
      return;
    }
    event.preventDefault();
    let nextMode: ResearchD5RailMode;
    if (event.key === "Home") nextMode = "chat";
    else if (event.key === "End") nextMode = "detail";
    else nextMode = event.currentTarget === chatTabRef.current ? "detail" : "chat";
    onModeChange(nextMode);
    (nextMode === "chat" ? chatTabRef : detailTabRef).current?.focus();
  };

  return (
    <aside
      id={railId}
      data-testid="research-d5-rail"
      data-rail-mode={mode}
      className={cn("d5-rail", className)}
      {...rest}
    >
      <div className="d5-rail-tabs" role="tablist">
        <button
          ref={chatTabRef}
          id={chatTabId}
          type="button"
          role="tab"
          aria-selected={mode === "chat"}
          aria-controls={chatPanelId}
          tabIndex={mode === "chat" ? 0 : -1}
          className={cn("d5-rail-tab", mode === "chat" && "d5-rail-tab-active")}
          onClick={() => onModeChange("chat")}
          onKeyDown={handleTabKeyDown}
        >
          {t(($) => $.d5.rail.chat_tab)}
        </button>
        <button
          ref={detailTabRef}
          id={detailTabId}
          type="button"
          role="tab"
          aria-selected={mode === "detail"}
          aria-controls={detailPanelId}
          tabIndex={mode === "detail" ? 0 : -1}
          className={cn("d5-rail-tab", mode === "detail" && "d5-rail-tab-active")}
          onClick={() => onModeChange("detail")}
          onKeyDown={handleTabKeyDown}
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
      <div
        id={chatPanelId}
        role="tabpanel"
        aria-labelledby={chatTabId}
        className="d5-rail-body"
        hidden={mode !== "chat"}
      >
        {chatPanel}
      </div>
      <div
        id={detailPanelId}
        role="tabpanel"
        aria-labelledby={detailTabId}
        className="d5-rail-body"
        hidden={mode !== "detail"}
      >
        {detailPanel}
      </div>
      {mode === "chat" && composer ? (
        <div className="d5-rail-footer">{composer}</div>
      ) : null}
    </aside>
  );
}
