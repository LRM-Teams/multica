"use client";

import { AlertCircle, Loader2, MessageSquare } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import type { ChatDrawerMode } from "../lib/chat-drawer-mode";
import {
  FAB_ABOVE_MINIMAP_BOTTOM_PX,
  FAB_NARROW_BOTTOM_PX,
  FAB_SIZE_PX,
  OVERLAY_INSET_PX,
} from "../lib/canvas-overlay-grid";

const fabShell: Record<ChatDrawerMode, string> = {
  empty:
    "border border-border/70 bg-card/95 text-muted-foreground shadow-lg hover:bg-card hover:text-foreground",
  loading:
    "border border-brand/40 bg-brand/15 text-brand shadow-lg hover:bg-brand/20",
  running:
    "border border-brand/30 bg-primary text-primary-foreground shadow-lg hover:bg-primary/90",
  error:
    "border border-destructive/45 bg-destructive/15 text-destructive shadow-lg hover:bg-destructive/20",
};

/**
 * LRM-992 — canvas chat FAB with four scannable modes (empty / loading / running / error).
 * Hidden by the caller when the drawer is open or a narrow detail sheet covers the canvas.
 */
export function ResearchChatFab({
  mode,
  onOpen,
  isMobile = false,
}: {
  mode: ChatDrawerMode;
  onOpen: () => void;
  isMobile?: boolean;
}) {
  const { t } = useT("research");
  const modeLabel =
    mode === "empty"
      ? t(($) => $.panel.chat_mode.empty)
      : mode === "loading"
        ? t(($) => $.panel.chat_mode.loading)
        : mode === "error"
          ? t(($) => $.panel.chat_mode.error)
          : t(($) => $.panel.chat_mode.running);

  return (
    <Button
      type="button"
      size="icon"
      variant="ghost"
      className={cn(
        "pointer-events-auto absolute z-20 rounded-full",
        fabShell[mode],
      )}
      style={{
        width: FAB_SIZE_PX,
        height: FAB_SIZE_PX,
        right: OVERLAY_INSET_PX,
        bottom: isMobile ? FAB_NARROW_BOTTOM_PX : FAB_ABOVE_MINIMAP_BOTTOM_PX,
      }}
      onClick={onOpen}
      aria-label={`${t(($) => $.panel.chat)} · ${modeLabel}`}
      title={`${t(($) => $.panel.chat)} · ${modeLabel}`}
      data-testid="research-canvas-chat-fab"
      data-chat-mode={mode}
    >
      {mode === "loading" ? (
        <Loader2 className="h-5 w-5 animate-spin" aria-hidden />
      ) : mode === "error" ? (
        <AlertCircle className="h-5 w-5" aria-hidden />
      ) : (
        <MessageSquare className="h-5 w-5" aria-hidden />
      )}
      <span className="sr-only">{modeLabel}</span>
    </Button>
  );
}
