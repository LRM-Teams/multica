"use client";

import { useState } from "react";
import type {
  ResearchFleetMember,
  ResearchSession,
  ResearchSource,
} from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@multica/ui/components/ui/dropdown-menu";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@multica/ui/components/ui/sheet";
import { useIsMobile } from "@multica/ui/hooks/use-mobile";
import { cn } from "@multica/ui/lib/utils";
import { MoreHorizontal } from "lucide-react";
import { useT } from "../../i18n/use-t";
import { ResearchFleetStrip } from "./research-fleet-strip";
import { ResearchSessionParamsSummary } from "./research-session-params-summary";
import { ResearchSourceBadges } from "./research-source-badges";

type MetaPanel = "fleet" | "sources" | "params" | null;

/**
 * LRM-919: fleet roster + source/confidence map live in a session settings
 * sheet — not as permanent canvas corner floats.
 * LRM-838: create params readback also lands here.
 */
export function ResearchSessionMetaMenu({
  members,
  sources,
  session,
  sessionStatus,
  leadingActions,
}: {
  members: ResearchFleetMember[];
  sources: ResearchSource[];
  session?: Pick<ResearchSession, "goal" | "depth_tier"> | null;
  sessionStatus?: string | null;
  /** LRM-995: narrow secondary actions (e.g. 查看交付) folded into tools. */
  leadingActions?: Array<{ id: string; label: string; onSelect: () => void }>;
}) {
  const { t } = useT("research");
  const isMobile = useIsMobile();
  const [panel, setPanel] = useState<MetaPanel>(null);

  const title =
    panel === "fleet"
      ? t(($) => $.panel.fleet)
      : panel === "sources"
        ? t(($) => $.panel.sources)
        : panel === "params"
          ? t(($) => $.create_params.session_menu)
          : "";

  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger
          render={
            <Button
              type="button"
              size="icon-sm"
              variant="ghost"
              aria-label={t(($) => $.panel.session_tools)}
              data-testid="research-session-tools"
            />
          }
        >
          <MoreHorizontal className="size-4" />
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="min-w-44">
          {leadingActions?.map((action) => (
            <DropdownMenuItem key={action.id} onClick={action.onSelect}>
              {action.label}
            </DropdownMenuItem>
          ))}
          {leadingActions && leadingActions.length > 0 ? <DropdownMenuSeparator /> : null}
          <DropdownMenuItem onClick={() => setPanel("fleet")}>
            {t(($) => $.panel.fleet)}
          </DropdownMenuItem>
          <DropdownMenuItem onClick={() => setPanel("sources")}>
            {t(($) => $.panel.sources)}
          </DropdownMenuItem>
          {session ? (
            <DropdownMenuItem
              onClick={() => setPanel("params")}
              data-testid="research-session-params-menu"
            >
              {t(($) => $.create_params.session_menu)}
            </DropdownMenuItem>
          ) : null}
        </DropdownMenuContent>
      </DropdownMenu>

      <Sheet open={panel !== null} onOpenChange={(open) => !open && setPanel(null)}>
        <SheetContent
          side={isMobile ? "bottom" : "right"}
          className={cn(
            "gap-0 overflow-y-auto p-0",
            isMobile && panel === "params"
              ? "inset-0 h-dvh max-h-dvh w-full border-0 sm:max-w-none"
              : isMobile
                ? "max-h-[85vh]"
                : "w-full sm:max-w-md",
          )}
        >
          <SheetHeader className="border-b">
            <SheetTitle>{title}</SheetTitle>
            <SheetDescription>{t(($) => $.panel.session_tools_hint)}</SheetDescription>
          </SheetHeader>
          <div className="p-3" data-testid="research-session-meta-panel">
            {panel === "fleet" ? (
              <ResearchFleetStrip
                members={members}
                sessionStatus={sessionStatus}
                embedded
              />
            ) : null}
            {panel === "sources" ? (
              <ResearchSourceBadges sources={sources} embedded />
            ) : null}
            {panel === "params" && session ? (
              <ResearchSessionParamsSummary session={session} />
            ) : null}
          </div>
        </SheetContent>
      </Sheet>
    </>
  );
}
