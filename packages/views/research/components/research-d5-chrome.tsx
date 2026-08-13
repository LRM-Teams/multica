"use client";

import {
  useCallback,
  useMemo,
  type ComponentProps,
  type KeyboardEvent,
} from "react";
import { ChevronDown } from "lucide-react";
import type { TypedGraphNode } from "@multica/core/research";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@multica/ui/components/ui/popover";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import { ResearchSessionGoalCard } from "./research-session-goal-card";
import { ResearchSessionChromeActions } from "./research-session-chrome-actions";
import {
  RESEARCH_D5_LENSES,
  type ResearchD5Lens,
} from "../lib/research-d5-lens";
import { buildD5FilterOptions } from "../lib/research-d5-filter-options";
import type { GoalVersionEntry } from "../lib/research-d5-goal-history";
import { resolveD5LensNavigationIndex } from "../lib/research-d5-lens-keyboard";
import { ResearchD5CanvasFilter } from "./research-d5-canvas-filter";

type ChromeProps = ComponentProps<typeof ResearchSessionChromeActions>;

export function ResearchD5Chrome({
  activeLens,
  onLensChange,
  goalVersion,
  goalHistory = [],
  goalImpact = null,
  typedGraphNodes = [],
  className,
  session,
  pendingSubstantiveGoal,
  onConfirmSubstantiveGoal,
  goalLoading,
  goalError,
  onGoalRetry,
  ...actionProps
}: ChromeProps & {
  activeLens: ResearchD5Lens;
  onLensChange: (lens: ResearchD5Lens) => void;
  goalVersion?: number | null;
  goalHistory?: readonly GoalVersionEntry[];
  goalImpact?: { labeledNodes: number; totalNodes: number } | null;
  typedGraphNodes?: readonly TypedGraphNode[];
  className?: string;
}) {
  const { t } = useT("research");
  const filterOptions = useMemo(
    () => buildD5FilterOptions(typedGraphNodes),
    [typedGraphNodes],
  );
  const handleLensKeyDown = useCallback(
    (event: KeyboardEvent<HTMLButtonElement>) => {
      const lens = event.currentTarget.dataset.lens as ResearchD5Lens | undefined;
      if (!lens) return;
      const currentIndex = RESEARCH_D5_LENSES.indexOf(lens);
      const nextIndex = resolveD5LensNavigationIndex(
        event.key,
        currentIndex,
        RESEARCH_D5_LENSES.length,
      );
      if (nextIndex == null) return;

      event.preventDefault();
      const nextLens = RESEARCH_D5_LENSES[nextIndex];
      if (!nextLens) return;
      onLensChange(nextLens);
      const nextTab = event.currentTarget.parentElement?.children.item(nextIndex);
      if (nextTab instanceof HTMLElement) nextTab.focus();
    },
    [onLensChange],
  );

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
            productRound={session.product_round ?? null}
            productRoundBudget={session.product_round_budget ?? null}
            goalHistory={goalHistory}
            goalImpact={goalImpact}
            panelPlacement="below"
            className="max-w-full"
          />
        </div>

        <div className="d5-chrome-controls">
          <ResearchD5CanvasFilter options={filterOptions} />

          <div className="d5-lens-group" role="tablist" aria-label={t(($) => $.d5.lens_group)}>
            {RESEARCH_D5_LENSES.map((lens) => (
              <button
                key={lens}
                type="button"
                role="tab"
                aria-selected={activeLens === lens}
                tabIndex={activeLens === lens ? 0 : -1}
                data-lens={lens}
                data-testid={`research-d5-lens-${lens}`}
                className={cn("d5-lens-btn", activeLens === lens && "d5-lens-btn-active")}
                onClick={() => onLensChange(lens)}
                onKeyDown={handleLensKeyDown}
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

          <ResearchSessionChromeActions
            {...actionProps}
            className="d5-chrome-actions"
            session={session}
            pendingSubstantiveGoal={pendingSubstantiveGoal}
            onConfirmSubstantiveGoal={onConfirmSubstantiveGoal}
            goalLoading={goalLoading}
            goalError={goalError}
            onGoalRetry={onGoalRetry}
            showStatus
          />
        </div>
      </div>
    </div>
  );
}
