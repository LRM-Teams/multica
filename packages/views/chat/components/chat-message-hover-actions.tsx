"use client";

import { useEffect, useRef, useState, type ReactNode } from "react";
import { Copy } from "lucide-react";
import { toast } from "sonner";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { copyText } from "@multica/ui/lib/clipboard";
import { Tooltip, TooltipContent, TooltipTrigger } from "@multica/ui/components/ui/tooltip";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n";

/** Same hover-overlay chrome as channel `message-action-bar`. */
export const CHAT_MESSAGE_HOVER_ACTION_BAR_CLASS = [
  "pointer-events-none absolute right-2 z-10 hidden items-center gap-0.5 rounded-lg border border-line-strong bg-popover p-0.5 text-muted-foreground opacity-0 shadow-sm transition-opacity",
  "[@media(pointer:fine)_and_(min-width:640px)]:flex",
  "[@media(pointer:fine)_and_(min-width:640px)]:group-hover:pointer-events-auto",
  "[@media(pointer:fine)_and_(min-width:640px)]:group-hover:opacity-100",
  "[@media(pointer:fine)_and_(min-width:640px)]:group-focus-within:pointer-events-auto",
  "[@media(pointer:fine)_and_(min-width:640px)]:group-focus-within:opacity-100",
  "top-0 -translate-y-1/2",
].join(" ");

export function ChatMessageHoverActionBar({
  onCopy,
  pinned,
}: {
  onCopy: () => void;
  pinned?: boolean;
}) {
  const { t } = useT("chat");
  return (
    <div
      data-testid="chat-message-action-bar"
      data-pinned={pinned ? "true" : "false"}
      className={cn(
        CHAT_MESSAGE_HOVER_ACTION_BAR_CLASS,
        pinned && "flex pointer-events-auto opacity-100",
      )}
    >
      <Tooltip>
        <TooltipTrigger
          render={
            <button
              type="button"
              onClick={onCopy}
              className="inline-flex size-7 items-center justify-center rounded-md transition-colors hover:bg-muted hover:text-foreground focus-visible:bg-muted focus-visible:text-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
              aria-label={t(($) => $.message_list.copy_action)}
            />
          }
        >
          <Copy className="size-4" />
        </TooltipTrigger>
        <TooltipContent side="top">{t(($) => $.message_list.copy_action)}</TooltipContent>
      </Tooltip>
    </div>
  );
}

export function ChatMessageHoverShell({
  enabled,
  copyTextValue,
  children,
}: {
  enabled: boolean;
  copyTextValue: string;
  children: ReactNode;
}) {
  const { t } = useT("chat");
  const [pinned, setPinned] = useState(false);
  const holdTimerRef = useRef<number | null>(null);
  const rootRef = useRef<HTMLDivElement | null>(null);

  const clearHold = () => {
    if (holdTimerRef.current != null) {
      window.clearTimeout(holdTimerRef.current);
      holdTimerRef.current = null;
    }
  };

  useEffect(() => () => clearHold(), []);

  useEffect(() => {
    if (!pinned) return;
    const onPointerDown = (event: PointerEvent) => {
      if (rootRef.current?.contains(event.target as Node)) return;
      setPinned(false);
    };
    document.addEventListener("pointerdown", onPointerDown);
    return () => document.removeEventListener("pointerdown", onPointerDown);
  }, [pinned]);

  const handleCopy = async () => {
    setPinned(false);
    if (await copyText(copyTextValue)) {
      toast.success(t(($) => $.message_list.copied_toast));
    } else {
      showErrorToast(t(($) => $.message_list.copy_failed_toast));
    }
  };

  if (!enabled) {
    return children;
  }

  return (
    <div
      ref={rootRef}
      className="group relative"
      data-testid="chat-message-hover-shell"
      onPointerDown={(event) => {
        if (event.pointerType !== "touch") return;
        clearHold();
        holdTimerRef.current = window.setTimeout(() => setPinned(true), 450);
      }}
      onPointerUp={clearHold}
      onPointerCancel={clearHold}
    >
      {children}
      <ChatMessageHoverActionBar onCopy={() => void handleCopy()} pinned={pinned} />
    </div>
  );
}
