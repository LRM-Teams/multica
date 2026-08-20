"use client";

import * as React from "react";
import { Bot, ClipboardList, FileText, ListTree } from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@multica/ui/components/ui/tooltip";
import { useT } from "../i18n";
import {
  NOTE_ASSISTANT_SATELLITE_IDS,
  noteAssistantSatelliteOffset,
  type NoteAssistantSatelliteId,
} from "./note-assistant-satellites";

export type NoteAssistantFabAction = NoteAssistantSatelliteId | "chat";

const SATELLITE_ICONS: Record<NoteAssistantSatelliteId, React.ReactNode> = {
  period_brief: <ClipboardList className="size-3.5 shrink-0" />,
  worker: <Bot className="size-3.5 shrink-0" />,
  highlights: <ListTree className="size-3.5 shrink-0" />,
};

export function NoteAssistantFabCluster({
  tooltip,
  isRunning,
  unreadCount,
  reducedMotion,
  onAction,
}: {
  tooltip: string;
  isRunning: boolean;
  unreadCount: number;
  reducedMotion: boolean;
  onAction: (action: NoteAssistantFabAction) => void;
}) {
  const { t } = useT("layout");
  const [expanded, setExpanded] = React.useState(false);
  const leaveTimerRef = React.useRef<number | null>(null);

  const clearLeaveTimer = () => {
    if (leaveTimerRef.current !== null) {
      window.clearTimeout(leaveTimerRef.current);
      leaveTimerRef.current = null;
    }
  };

  const openCluster = () => {
    clearLeaveTimer();
    setExpanded(true);
  };

  const scheduleCloseCluster = () => {
    clearLeaveTimer();
    leaveTimerRef.current = window.setTimeout(() => {
      setExpanded(false);
      leaveTimerRef.current = null;
    }, 140);
  };

  React.useEffect(() => () => clearLeaveTimer(), []);

  const satelliteCopy: Record<
    NoteAssistantSatelliteId,
    { label: string; hint: string }
  > = {
    period_brief: {
      label: t(($) => $.notes_page.assistant_satellite_period),
      hint: t(($) => $.notes_page.assistant_satellite_period_hint),
    },
    worker: {
      label: t(($) => $.notes_page.assistant_satellite_worker),
      hint: t(($) => $.notes_page.assistant_satellite_worker_hint),
    },
    highlights: {
      label: t(($) => $.notes_page.assistant_satellite_highlights),
      hint: t(($) => $.notes_page.assistant_satellite_highlights_hint),
    },
  };

  return (
    <TooltipProvider delay={200}>
      <div
        className={cn(
          "absolute bottom-2 right-14 z-50",
          expanded ? "h-32 w-40" : "h-12 w-12",
        )}
        onMouseEnter={openCluster}
        onMouseLeave={scheduleCloseCluster}
        onFocusCapture={openCluster}
        onBlurCapture={(event) => {
          if (!event.currentTarget.contains(event.relatedTarget as Node | null)) {
            scheduleCloseCluster();
          }
        }}
      >
        {NOTE_ASSISTANT_SATELLITE_IDS.map((id, index) => {
          const offset = noteAssistantSatelliteOffset(index, NOTE_ASSISTANT_SATELLITE_IDS.length);
          const copy = satelliteCopy[id];
          return (
            <Tooltip key={id}>
              <TooltipTrigger
                type="button"
                tabIndex={expanded ? 0 : -1}
                aria-hidden={!expanded}
                aria-label={copy.hint}
                disabled={!expanded}
                onClick={(event) => {
                  event.stopPropagation();
                  onAction(id);
                  setExpanded(false);
                }}
                style={{
                  transform: `translate(calc(-50% + ${offset.x}px), calc(-50% + ${offset.y}px)) scale(${
                    expanded || reducedMotion ? 1 : 0.75
                  })`,
                }}
                className={cn(
                  "absolute right-6 bottom-6 z-10 flex h-9 max-w-28 items-center gap-1.5 rounded-full bg-card px-2.5 text-xs font-medium text-foreground shadow-md ring-1 ring-foreground/10 transition-[opacity,transform] duration-200",
                  "hover:bg-accent hover:text-accent-foreground",
                  expanded
                    ? "pointer-events-auto opacity-100"
                    : "pointer-events-none opacity-0",
                )}
              >
                {SATELLITE_ICONS[id]}
                <span className="truncate">{copy.label}</span>
              </TooltipTrigger>
              {expanded ? (
                <TooltipContent side="left" sideOffset={8}>
                  {copy.hint}
                </TooltipContent>
              ) : null}
            </Tooltip>
          );
        })}
        <Tooltip open={expanded ? false : undefined}>
          <TooltipTrigger
            type="button"
            onClick={() => onAction("chat")}
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
            aria-expanded={expanded}
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
