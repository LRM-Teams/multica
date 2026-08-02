"use client";

import { useState } from "react";
import type { ResearchFleetMember, ResearchSource } from "@multica/core/types";
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
import { MoreHorizontal } from "lucide-react";
import { useT } from "../../i18n/use-t";
import { ResearchFleetStrip } from "./research-fleet-strip";
import { ResearchSourceBadges } from "./research-source-badges";

type MetaPanel = "fleet" | "sources" | null;

/**
 * LRM-919: fleet roster + source/confidence map live in a session settings
 * sheet — not as permanent canvas corner floats.
 */
export function ResearchSessionMetaMenu({
  members,
  sources,
  sessionStatus,
  leadingActions,
}: {
  members: ResearchFleetMember[];
  sources: ResearchSource[];
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
        </DropdownMenuContent>
      </DropdownMenu>

      <Sheet open={panel !== null} onOpenChange={(open) => !open && setPanel(null)}>
        <SheetContent
          side={isMobile ? "bottom" : "right"}
          className={
            isMobile
              ? "max-h-[85vh] gap-0 overflow-y-auto p-0"
              : "w-full gap-0 overflow-y-auto p-0 sm:max-w-md"
          }
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
          </div>
        </SheetContent>
      </Sheet>
    </>
  );
}
