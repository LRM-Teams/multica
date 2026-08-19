"use client";

import { FileText } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { cn } from "@multica/ui/lib/utils";
import { useChatStore } from "@multica/core/chat";
import { chatSessionsOptions, pendingChatTasksOptions } from "@multica/core/chat/queries";
import { useWorkspaceId } from "@multica/core/hooks";
import { createLogger } from "@multica/core/logger";
import { useIsMobile } from "@multica/ui/hooks/use-mobile";
import {
  Tooltip,
  TooltipTrigger,
  TooltipContent,
} from "@multica/ui/components/ui/tooltip";
import { useT } from "../i18n";
import { usePrefersReducedMotion } from "../common/use-prefers-reduced-motion";
import { excludeChannelShellSessions } from "../chat/lib/exclude-channel-shell-sessions";
import { ChatWindow } from "../chat/components/chat-window";

const logger = createLogger("chat.note-bubble");

/**
 * Notes-page assistant bubble: standalone chat_session bound to the current
 * note page (+ subtree via agent notes get / tree). Independent from the
 * global ChatFab and DM bubbles.
 */
export function NoteAssistantBubble({
  pageId,
  pageTitle,
  preferredAgentId,
}: {
  pageId: string;
  pageTitle?: string;
  preferredAgentId?: string | null;
}) {
  const { t } = useT("layout");
  const wsId = useWorkspaceId();
  const isMobile = useIsMobile();
  const layout = isMobile ? "fullscreen" : "floating";
  const openPageId = useChatStore((s) => s.noteBubbleOpenPageId);
  const toggleNoteBubble = useChatStore((s) => s.toggleNoteBubble);
  const { data: sessions = [] } = useQuery(chatSessionsOptions(wsId));
  const { data: pending } = useQuery(pendingChatTasksOptions(wsId));
  const prefersReducedMotion = usePrefersReducedMotion();

  const isOpen = openPageId === pageId;
  const pageSessions = excludeChannelShellSessions(
    sessions.filter((s) => s.context_note_page_id === pageId),
  );
  const unreadSessionCount = pageSessions.filter((s) => s.has_unread).length;
  const isRunning = (pending?.tasks ?? []).some((task) =>
    pageSessions.some((s) => s.id === task.chat_session_id),
  );

  const handleClick = () => {
    logger.info("noteBubble.fab.click", {
      pageId,
      unreadSessionCount,
      isRunning,
      willOpen: !isOpen,
    });
    toggleNoteBubble(pageId);
  };

  const titleHint = pageTitle?.trim() || t(($) => $.notes_page.assistant_bubble_untitled);
  const tooltip = isRunning
    ? t(($) => $.notes_page.assistant_bubble_running)
    : unreadSessionCount > 0
      ? t(($) => $.notes_page.assistant_bubble_replied, { title: titleHint })
      : t(($) => $.notes_page.assistant_bubble_default, { title: titleHint });

  return (
    <>
      <ChatWindow
        contextNotePageId={pageId}
        preferredAgentId={preferredAgentId}
        layout={layout}
      />
      {!isOpen && (
        <Tooltip>
          <TooltipTrigger
            onClick={handleClick}
            className={cn(
              // Sit above the global ChatFab (bottom-2 right-2) when both show.
              "absolute bottom-2 right-14 z-50 flex size-10 cursor-pointer items-center justify-center rounded-full ring-1 ring-foreground/10 bg-card text-muted-foreground shadow-sm transition-transform hover:scale-110 hover:text-accent-foreground active:scale-95",
              isRunning &&
                (prefersReducedMotion
                  ? "text-brand ring-brand/40"
                  : "animate-chat-impulse"),
              unreadSessionCount > 0 &&
                !isRunning &&
                "ring-2 ring-brand text-foreground shadow-md",
            )}
            aria-label={tooltip}
          >
            <FileText className="size-5" />
            {unreadSessionCount > 0 && (
              <span className="pointer-events-none absolute -top-0.5 -right-0.5 flex min-w-4 h-4 items-center justify-center rounded-full bg-brand px-1 text-xs font-semibold leading-none text-background">
                {unreadSessionCount > 9 ? "9+" : unreadSessionCount}
              </span>
            )}
          </TooltipTrigger>
          <TooltipContent side="top" sideOffset={10}>
            {tooltip}
          </TooltipContent>
        </Tooltip>
      )}
    </>
  );
}
