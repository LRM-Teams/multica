"use client";

import { useEffect, useRef, useState, type ReactNode } from "react";
import { Copy, FilePlus2, ListPlus, Loader2 } from "lucide-react";
import { useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { useWorkspaceId } from "@multica/core/hooks";
import { noteKeys } from "@multica/core/notes/queries";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { copyText } from "@multica/ui/lib/clipboard";
import { Tooltip, TooltipContent, TooltipTrigger } from "@multica/ui/components/ui/tooltip";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n";
import {
  insertMessageIntoNote,
  type NoteMessageInsertMode,
} from "./insert-message-into-note";

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

const HOVER_ICON_BUTTON_CLASS =
  "inline-flex size-5 items-center justify-center rounded-sm transition-colors hover:bg-muted hover:text-foreground focus-visible:bg-muted focus-visible:text-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:pointer-events-none disabled:opacity-50";

function HoverIconButton({
  label,
  onClick,
  disabled,
  children,
}: {
  label: string;
  onClick: () => void;
  disabled?: boolean;
  children: ReactNode;
}) {
  return (
    <Tooltip>
      <TooltipTrigger
        render={
          <button
            type="button"
            onClick={onClick}
            disabled={disabled}
            className={HOVER_ICON_BUTTON_CLASS}
            aria-label={label}
          />
        }
      >
        {children}
      </TooltipTrigger>
      <TooltipContent side="top">{label}</TooltipContent>
    </Tooltip>
  );
}

function NoteInsertHoverButtons({
  pageId,
  text,
  onInserted,
}: {
  pageId: string;
  text: string;
  onInserted: () => void;
}) {
  const { t } = useT("chat");
  const wsId = useWorkspaceId();
  const queryClient = useQueryClient();
  const [insertBusy, setInsertBusy] = useState<NoteMessageInsertMode | null>(null);

  const handleInsert = async (mode: NoteMessageInsertMode) => {
    if (insertBusy) return;
    onInserted();
    setInsertBusy(mode);
    try {
      const res = await insertMessageIntoNote({
        pageId,
        text,
        mode,
        titleFallback: t(($) => $.message_list.insert_title_fallback),
      });
      if (wsId) {
        void queryClient.invalidateQueries({ queryKey: noteKeys.all(wsId) });
      }
      toast.success(
        mode === "append"
          ? t(($) => $.message_list.insert_below_success)
          : t(($) => $.message_list.insert_child_success, { title: res.title }),
      );
    } catch (error) {
      showErrorToast(
        error instanceof Error ? error.message : t(($) => $.message_list.insert_failed),
      );
    } finally {
      setInsertBusy(null);
    }
  };

  return (
    <>
      <HoverIconButton
        label={t(($) => $.message_list.insert_below_action)}
        onClick={() => void handleInsert("append")}
        disabled={insertBusy != null}
      >
        {insertBusy === "append" ? (
          <Loader2 className="size-3 animate-spin" />
        ) : (
          <ListPlus className="size-3" />
        )}
      </HoverIconButton>
      <HoverIconButton
        label={t(($) => $.message_list.insert_child_action)}
        onClick={() => void handleInsert("child")}
        disabled={insertBusy != null}
      >
        {insertBusy === "child" ? (
          <Loader2 className="size-3 animate-spin" />
        ) : (
          <FilePlus2 className="size-3" />
        )}
      </HoverIconButton>
    </>
  );
}

export function ChatMessageHoverActionBar({
  onCopy,
  noteInsertPageId,
  copyTextValue,
  onInserted,
  pinned,
}: {
  onCopy: () => void;
  noteInsertPageId?: string | null;
  copyTextValue?: string;
  onInserted?: () => void;
  pinned?: boolean;
}) {
  const { t } = useT("chat");
  const pageId = noteInsertPageId?.trim() || "";
  return (
    <div
      data-testid="chat-message-action-bar"
      data-pinned={pinned ? "true" : "false"}
      className={cn(
        CHAT_MESSAGE_HOVER_ACTION_BAR_CLASS,
        pinned && "flex pointer-events-auto opacity-100",
      )}
    >
      <HoverIconButton label={t(($) => $.message_list.copy_action)} onClick={onCopy}>
        <Copy className="size-3" />
      </HoverIconButton>
      {pageId ? (
        <NoteInsertHoverButtons
          pageId={pageId}
          text={copyTextValue ?? ""}
          onInserted={onInserted ?? (() => undefined)}
        />
      ) : null}
    </div>
  );
}

export function ChatMessageHoverShell({
  enabled,
  copyTextValue,
  noteInsertPageId,
  children,
}: {
  enabled: boolean;
  copyTextValue: string;
  noteInsertPageId?: string | null;
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
      <ChatMessageHoverActionBar
        onCopy={() => void handleCopy()}
        noteInsertPageId={noteInsertPageId}
        copyTextValue={copyTextValue}
        onInserted={() => setPinned(false)}
        pinned={pinned}
      />
    </div>
  );
}
