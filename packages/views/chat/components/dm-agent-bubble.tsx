"use client";

import { MessageCircle } from "lucide-react";
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
import { useT } from "../../i18n";
import { ChatWindow } from "./chat-window";

const logger = createLogger("chat.dm-bubble");

/**
 * Agent-locked independent-session bubble for the web DM page.
 *
 * Opens a plain chat_session (not channel_agent_session) so model context is
 * isolated from the DM/group channel wake path, while still loading this
 * agent's own memory + skills via the normal claim pipeline.
 */
export function DmAgentBubble({
  agentId,
  agentName,
}: {
  agentId: string;
  agentName?: string;
}) {
  const { t } = useT("chat");
  const wsId = useWorkspaceId();
  const isMobile = useIsMobile();
  // Mobile opens as a true viewport sheet (ChatWindow portals fixed+dvh).
  // Desktop keeps the corner floating window.
  const layout = isMobile ? "fullscreen" : "floating";
  const openAgentId = useChatStore((s) => s.dmBubbleOpenAgentId);
  const toggleDmBubble = useChatStore((s) => s.toggleDmBubble);
  const { data: sessions = [] } = useQuery(chatSessionsOptions(wsId));
  const { data: pending } = useQuery(pendingChatTasksOptions(wsId));

  const isOpen = openAgentId === agentId;
  const agentSessions = sessions.filter((s) => s.agent_id === agentId);
  const unreadSessionCount = agentSessions.filter((s) => s.has_unread).length;
  const isRunning = (pending?.tasks ?? []).some((task) =>
    agentSessions.some((s) => s.id === task.chat_session_id),
  );

  const handleClick = () => {
    logger.info("dmBubble.fab.click", {
      agentId,
      unreadSessionCount,
      isRunning,
      willOpen: !isOpen,
    });
    toggleDmBubble(agentId);
  };

  const tooltip = isRunning
    ? t(($) => $.fab.running)
    : unreadSessionCount > 0
      ? t(($) => $.fab.unread, { count: unreadSessionCount })
      : t(($) => $.dm_bubble.fab_default, { name: agentName ?? "agent" });

  return (
    <>
      <ChatWindow
        lockedAgentId={agentId}
        layout={layout}
      />
      {!isOpen && (
        <Tooltip>
          <TooltipTrigger
            onClick={handleClick}
            className={cn(
              // Sit above the DM composer send/attach cluster on both mobile
              // and desktop (composer is ~56–72px tall at the bottom).
              "absolute bottom-20 right-3 z-40 flex size-10 cursor-pointer items-center justify-center rounded-full ring-1 ring-foreground/10 bg-card text-muted-foreground shadow-sm transition-transform hover:scale-110 hover:text-accent-foreground active:scale-95",
              isRunning && "animate-chat-impulse",
            )}
            aria-label={tooltip}
          >
            <MessageCircle className="size-5" />
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
