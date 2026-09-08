"use client";

import { FileText } from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@multica/ui/components/ui/tooltip";

/**
 * Closed-state Notes assistant entry. Quick actions (写汇报 / 要点) live
 * inside the open chat window — not as orbiting satellites on this FAB.
 */
export function NoteAssistantFabCluster({
  tooltip,
  isRunning,
  unreadCount,
  reducedMotion,
  onOpen,
}: {
  tooltip: string;
  isRunning: boolean;
  unreadCount: number;
  reducedMotion: boolean;
  onOpen: () => void;
}) {
  return (
    <TooltipProvider delay={200}>
      <div className="absolute bottom-2 right-14 z-50 h-12 w-12">
        <Tooltip>
          <TooltipTrigger
            type="button"
            onClick={onOpen}
            className={cn(
              "absolute right-0 bottom-0 z-20 flex size-12 cursor-pointer items-center justify-center rounded-full",
              "bg-gradient-to-br from-brand-soft via-card to-card text-brand",
              "shadow-[0_10px_24px_-10px] shadow-brand/50 ring-2 ring-brand/20",
              "transition-transform hover:scale-105 active:scale-95",
              isRunning &&
                (reducedMotion
                  ? "text-brand ring-brand/50"
                  : "animate-chat-impulse"),
              unreadCount > 0 && !isRunning && "ring-brand shadow-md",
            )}
            aria-label={tooltip}
          >
            <span className="pointer-events-none absolute inset-[3px] rounded-full bg-card/70 ring-1 ring-brand/10" />
            <FileText className="relative size-5" />
            {unreadCount > 0 && (
              <span className="pointer-events-none absolute -top-0.5 -right-0.5 flex h-4 min-w-4 items-center justify-center rounded-full bg-brand px-1 text-xs font-semibold leading-none text-background">
                {unreadCount > 9 ? "9+" : unreadCount}
              </span>
            )}
          </TooltipTrigger>
          <TooltipContent side="top" sideOffset={10}>
            {tooltip}
          </TooltipContent>
        </Tooltip>
      </div>
    </TooltipProvider>
  );
}
