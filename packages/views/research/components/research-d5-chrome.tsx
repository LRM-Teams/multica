"use client";

import type { ComponentProps } from "react";
import { ChevronDown } from "lucide-react";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@multica/ui/components/ui/popover";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import { ResearchSessionChrome } from "./research-session-chrome";
import { ResearchSessionGoalCard } from "./research-session-goal-card";
import {
  RESEARCH_D5_LENSES,
  type ResearchD5Lens,
} from "../lib/research-d5-lens";
import type { GoalVersionEntry } from "../lib/research-d5-goal-history";

type ChromeProps = ComponentProps<typeof ResearchSessionChrome>;

export function ResearchD5Chrome({
  activeLens,
  onLensChange,
  goalVersion,
  goalHistory = [],
  goalImpact = null,
  className,
  ...chromeProps
}: ChromeProps & {
  activeLens: ResearchD5Lens;
  onLensChange: (lens: ResearchD5Lens) => void;
  goalVersion?: number | null;
  goalHistory?: readonly GoalVersionEntry[];
  goalImpact?: { labeledNodes: number; totalNodes: number } | null;
  className?: string;
}) {
  const { t } = useT("research");
  const { session, pendingSubstantiveGoal, onConfirmSubstantiveGoal, goalLoading, goalError, onGoalRetry } =
    chromeProps;

  return (
    <div data-testid="research-d5-chrome" className={cn("d5-chrome-shell", className)}>
      <div className="d5-chrome-top">
        <div className="d5-brand">
          <span className="d5-logo" aria-hidden>
            M
          </span>
          <div className="min-w-0">
            <b>{t(($) => $.d5.brand_title)}</b>
            <span>{t(($) => $.d5.brand_subtitle)}</span>
          </div>
        </div>

        <div className="d5-goal-slot min-w-0 flex-1 px-2">
          <ResearchSessionGoalCard
            sessionId={session.id}
            goal={session.goal}
            pendingSubstantive={pendingSubstantiveGoal}
            onConfirmSubstantive={onConfirmSubstantiveGoal}
            loading={goalLoading}
            error={goalError}
            onRetry={onGoalRetry}
            goalVersion={goalVersion}
            goalHistory={goalHistory}
            goalImpact={goalImpact}
            panelPlacement="below"
            className="max-w-full"
          />
        </div>

        <div className="d5-lens-group" role="tablist" aria-label={t(($) => $.d5.lens_group)}>
          {RESEARCH_D5_LENSES.map((lens) => (
            <button
              key={lens}
              type="button"
              role="tab"
              aria-selected={activeLens === lens}
              data-testid={`research-d5-lens-${lens}`}
              className={cn("d5-lens-btn", activeLens === lens && "d5-lens-btn-active")}
              onClick={() => onLensChange(lens)}
            >
              {t(($) => $.d5.lens[lens])}
            </button>
          ))}
        </div>

        <Popover>
          <PopoverTrigger
            data-testid="research-d5-lens-overflow-trigger"
            className="d5-lens-overflow-trigger d5-lens-btn inline-flex items-center gap-1"
            aria-label={t(($) => $.d5.lens_group)}
          >
            {t(($) => $.d5.lens[activeLens])}
            <ChevronDown className="size-3.5 opacity-70" aria-hidden />
          </PopoverTrigger>
          <PopoverContent align="end" className="w-44 p-1" data-testid="research-d5-lens-overflow">
            {RESEARCH_D5_LENSES.map((lens) => (
              <button
                key={lens}
                type="button"
                data-testid={`research-d5-lens-overflow-${lens}`}
                className={cn(
                  "w-full rounded-md px-2 py-1.5 text-left text-[11px]",
                  activeLens === lens
                    ? "bg-brand/10 text-foreground"
                    : "text-muted-foreground hover:bg-muted/60",
                )}
                onClick={() => onLensChange(lens)}
              >
                {t(($) => $.d5.lens[lens])}
              </button>
            ))}
          </PopoverContent>
        </Popover>
      </div>

      <ResearchSessionChrome {...chromeProps} hideGoalCard />
    </div>
  );
}
