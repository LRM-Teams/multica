"use client";

import { useEffect, useRef, useState } from "react";
import { Crosshair, Loader2 } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@multica/ui/components/ui/popover";
import { useIsMobile } from "@multica/ui/hooks/use-mobile";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import {
  readGoalCardCollapsed,
  resolveSessionGoalModel,
  writeGoalCardCollapsed,
  type SessionGoalVisualState,
} from "../lib/session-goal";
import type { GoalVersionEntry } from "../lib/research-d5-goal-history";

const PULSE_MS = 2800;

/** LRM-1010 / LRM-790 — brand semantic tokens only (no hardcoded violet). */
function statusDotClass(state: SessionGoalVisualState): string {
  switch (state) {
    case "updated":
      return "bg-brand";
    case "pending_substantive":
      return "bg-warning";
    case "error":
      return "bg-destructive";
    case "empty":
    case "loading":
      return "bg-muted-foreground/50";
    default:
      return "bg-muted-foreground/70";
  }
}

/** LRM-1008 / LRM-898 scheme D — compact Goal Card → floating dialog. */
export function ResearchSessionGoalCard({
  sessionId,
  goal,
  pendingSubstantive = null,
  loading = false,
  error = false,
  onRetry,
  onConfirmSubstantive,
  confirmSubstantivePending = false,
  goalVersion = null,
  goalHistory = [],
  goalImpact = null,
  className,
  compact = false,
  panelPlacement = "dialog",
}: {
  sessionId: string;
  goal: string;
  pendingSubstantive?: string | null;
  loading?: boolean;
  error?: boolean;
  onRetry?: () => void;
  onConfirmSubstantive?: (proposal: string) => void | Promise<void>;
  confirmSubstantivePending?: boolean;
  goalVersion?: number | null;
  goalHistory?: readonly GoalVersionEntry[];
  goalImpact?: { labeledNodes: number; totalNodes: number } | null;
  className?: string;
  /** Frozen top-bar mode: keep GOAL at icon priority even on desktop (LRM-1112). */
  compact?: boolean;
  /** D5 chrome: expand the version panel below the card instead of a centered modal. */
  panelPlacement?: "dialog" | "below";
}) {
  const { t } = useT("research");
  const isMobile = useIsMobile();
  const [open, setOpen] = useState(false);
  const [collapsed, setCollapsed] = useState(() =>
    readGoalCardCollapsed(sessionId),
  );
  const [previousGoal, setPreviousGoal] = useState<string | null>(null);
  const [justUpdated, setJustUpdated] = useState(false);
  const lastGoalRef = useRef<string | null>(null);
  const collapsedSessionRef = useRef(sessionId);
  const confirmSubmittingRef = useRef(false);

  // Reset collapse preference when navigating sessions (render-time adjust).
  if (collapsedSessionRef.current !== sessionId) {
    collapsedSessionRef.current = sessionId;
    setCollapsed(readGoalCardCollapsed(sessionId));
  }

  // Detect user-driven goal writes during render (LRM-898: fleet cannot write goal).
  const nextGoal = goal ?? "";
  if (lastGoalRef.current === null) {
    lastGoalRef.current = nextGoal;
  } else if (lastGoalRef.current !== nextGoal) {
    const prev = lastGoalRef.current;
    lastGoalRef.current = nextGoal;
    if (prev.trim() && nextGoal.trim() && prev.trim() !== nextGoal.trim()) {
      if (previousGoal !== prev) setPreviousGoal(prev);
      if (!justUpdated) setJustUpdated(true);
    }
  }

  // Clear pulse after a short window — timer belongs in an effect.
  useEffect(() => {
    if (!justUpdated) return;
    const timer = window.setTimeout(() => setJustUpdated(false), PULSE_MS);
    return () => window.clearTimeout(timer);
  }, [justUpdated, nextGoal]);

  const model = resolveSessionGoalModel({
    goal,
    previousGoal,
    pendingSubstantive,
    loading,
    error,
    justUpdated,
  });

  const setCollapsedPersist = (value: boolean) => {
    setCollapsed(value);
    writeGoalCardCollapsed(sessionId, value);
  };

  const summaryLabel =
    model.state === "empty"
      ? t(($) => $.goal_card.empty_summary)
      : model.state === "loading"
        ? t(($) => $.goal_card.loading_summary)
        : model.state === "error"
          ? t(($) => $.goal_card.error_summary)
          : model.state === "pending_substantive"
            ? t(($) => $.goal_card.pending_summary)
            : model.summary;

  const card = (
    <button
      type="button"
      data-testid="research-session-goal-card"
      data-state={model.state}
      data-collapsed="false"
      onClick={panelPlacement === "below" ? undefined : () => setOpen(true)}
      onContextMenu={(e) => {
        e.preventDefault();
        setCollapsedPersist(true);
      }}
      title={t(($) => $.goal_card.card_title)}
      className={cn(
        "inline-flex max-w-[min(17.5rem,46vw)] items-center gap-1.5 rounded-lg border px-2 py-1 text-left transition-shadow",
        "border-brand/30 bg-brand/10 hover:bg-brand/15",
        "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand/30",
        model.state === "updated" && "shadow-[0_0_0_3px_color-mix(in_oklab,var(--brand)_22%,transparent)]",
        className,
      )}
    >
      <span className="shrink-0 text-[10px] font-extrabold tracking-wide text-brand">
        {t(($) => $.goal_card.label)}
      </span>
      {model.state === "loading" ? (
        <Loader2
          className="size-3.5 shrink-0 animate-spin text-muted-foreground motion-reduce:animate-none"
          aria-hidden
        />
      ) : (
        <span className="min-w-0 flex-1 truncate text-[11.5px] font-semibold text-foreground">
          {summaryLabel}
        </span>
      )}
      <span
        aria-hidden
        data-testid="research-session-goal-dot"
        className={cn("size-1.5 shrink-0 rounded-full", statusDotClass(model.state))}
      />
    </button>
  );

  const icon = (
    <button
      type="button"
      data-testid="research-session-goal-icon"
      data-state={model.state}
      data-collapsed="true"
      onClick={panelPlacement === "below" ? undefined : () => setOpen(true)}
      onContextMenu={(e) => {
        e.preventDefault();
        setCollapsedPersist(false);
      }}
      title={t(($) => $.goal_card.icon_title)}
      aria-label={t(($) => $.goal_card.icon_title)}
      className={cn(
        "relative inline-flex size-7 shrink-0 items-center justify-center rounded-lg border border-border bg-background text-brand",
        "hover:bg-muted/60 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand/30",
        className,
      )}
    >
      <Crosshair className="size-3.5" strokeWidth={2.25} aria-hidden />
      {(model.state === "updated" || model.state === "pending_substantive") && (
        <span
          aria-hidden
          className={cn(
            "absolute top-1 right-1 size-1.5 rounded-full",
            statusDotClass(model.state),
          )}
        />
      )}
    </button>
  );

  // LRM-1010/1112: top bar keeps Goal as icon so the primary CTA never overflows;
  // `compact` forces desktop to the icon surface per the LRM-1112 frozen header.
  const showIcon = compact || isMobile || collapsed;

  const panelBody = (
    <>
      <div className="space-y-2 px-4 pt-4 pb-2 text-left">
        <div className="flex items-center gap-2 text-sm font-medium">
          <span className="inline-flex max-w-full items-center gap-1.5 rounded-lg border border-brand/30 bg-brand/10 px-2 py-0.5">
            <span className="shrink-0 text-[10px] font-extrabold tracking-wide text-brand">
              {t(($) => $.goal_card.label)}
            </span>
            <span className="min-w-0 truncate text-[11.5px] font-semibold">
              {t(($) => $.goal_card.final_title)}
            </span>
          </span>
        </div>
        {model.note === "optimized" ? (
          <p className="text-[11px] text-brand">{t(($) => $.goal_card.optimized_note)}</p>
        ) : null}
      </div>

      <div className="space-y-2.5 px-4 pb-3">
            {model.state === "loading" ? (
              <p className="text-[12.5px] text-muted-foreground">
                {t(($) => $.goal_card.loading_body)}
              </p>
            ) : model.state === "error" ? (
              <p className="text-[12.5px] text-destructive">
                {t(($) => $.goal_card.error_body)}
              </p>
            ) : model.state === "empty" ? (
              <p className="text-[12.5px] text-muted-foreground">
                {t(($) => $.goal_card.empty_body)}
              </p>
            ) : (
              <p
                data-testid="research-session-goal-full"
                className="text-[12.5px] leading-relaxed text-muted-foreground"
              >
                {model.text}
              </p>
            )}

            {model.previousText ? (
              <div
                data-testid="research-session-goal-previous"
                className="rounded-lg bg-muted/50 px-2.5 py-2 text-[11px] leading-relaxed text-muted-foreground"
              >
                <div className="mb-1 font-semibold text-foreground">
                  {t(($) => $.goal_card.previous_label)}
                </div>
                {model.previousText}
              </div>
            ) : null}

            {model.substantiveProposal ? (
              <div
                data-testid="research-session-goal-substantive"
                className="rounded-lg border border-warning/35 bg-warning/10 px-2.5 py-2 text-[11px] leading-relaxed"
              >
                <div className="mb-1 font-semibold text-warning">
                  {t(($) => $.goal_card.substantive_label)}
                </div>
                <p className="text-muted-foreground">{model.substantiveProposal}</p>
              </div>
            ) : null}

            {goalVersion != null || goalHistory.length > 0 ? (
              <section
                data-testid="research-session-goal-versions"
                className="rounded-lg border border-border/70 bg-muted/20 px-2.5 py-2"
              >
                <div className="mb-2 text-[11px] font-semibold text-foreground">
                  {t(($) => $.d5.goal_panel.title)}
                  {goalVersion != null
                    ? ` · ${t(($) => $.d5.goal_panel.version, { version: goalVersion })}`
                    : null}
                </div>
                {goalImpact && goalImpact.totalNodes > 0 ? (
                  <p
                    data-testid="research-session-goal-impact"
                    className="mb-2 text-[10px] text-muted-foreground"
                  >
                    {t(($) => $.d5.goal_panel.impact, goalImpact)}
                  </p>
                ) : null}
                <div className="space-y-2">
                  {goalHistory.map((entry) => (
                    <div
                      key={entry.version}
                      data-testid={`research-session-goal-version-${entry.version}`}
                      className={cn(
                        "rounded-md border px-2 py-1.5 text-[11px]",
                        entry.isCurrent
                          ? "border-brand/30 bg-brand/5"
                          : "border-border/60 bg-background/40",
                      )}
                    >
                      <div className="font-semibold text-foreground">
                        {t(($) => $.d5.goal_panel.version, { version: entry.version })}
                        {entry.isCurrent
                          ? ` · ${t(($) => $.d5.goal_panel.current)}`
                          : null}
                      </div>
                      <p className="mt-1 leading-relaxed text-muted-foreground">{entry.goal}</p>
                      {entry.reason ? (
                        <p className="mt-1 text-[10px] text-muted-foreground">{entry.reason}</p>
                      ) : null}
                    </div>
                  ))}
                </div>
              </section>
            ) : null}
          </div>

      <div className="flex flex-row flex-wrap justify-between gap-2 border-t px-4 py-3">
            <div className="flex gap-1.5">
              {/* Collapse toggle is desktop-only — narrow always uses the icon trigger. */}
              {!isMobile ? (
                <Button
                  type="button"
                  size="sm"
                  variant="ghost"
                  data-testid="research-session-goal-toggle-collapse"
                  onClick={() => setCollapsedPersist(!collapsed)}
                >
                  {collapsed
                    ? t(($) => $.goal_card.expand_card)
                    : t(($) => $.goal_card.collapse_icon)}
                </Button>
              ) : null}
            </div>
            <div className="flex min-w-0 flex-wrap justify-end gap-1.5">
              {model.state === "error" && onRetry ? (
                <Button
                  type="button"
                  size="sm"
                  variant="outline"
                  onClick={() => {
                    onRetry();
                  }}
                >
                  {t(($) => $.goal_card.retry)}
                </Button>
              ) : null}
              <Button
                type="button"
                size="sm"
                variant="outline"
                data-testid="research-session-goal-close"
                onClick={() => setOpen(false)}
              >
                {t(($) => $.goal_card.close)}
              </Button>
              {model.substantiveProposal && onConfirmSubstantive ? (
                <Button
                  type="button"
                  size="sm"
                  data-testid="research-session-goal-confirm-substantive"
                  aria-disabled={confirmSubstantivePending || undefined}
                  aria-busy={confirmSubstantivePending || undefined}
                  className={cn(
                    confirmSubstantivePending && "cursor-not-allowed opacity-50",
                  )}
                  onClick={async () => {
                    if (
                      confirmSubstantivePending ||
                      confirmSubmittingRef.current
                    ) return;
                    confirmSubmittingRef.current = true;
                    try {
                      await onConfirmSubstantive(model.substantiveProposal!);
                      setOpen(false);
                    } catch {
                      // The mutation owner reports the API failure. Keep the
                      // proposal and Goal Card open for lossless recovery.
                    } finally {
                      confirmSubmittingRef.current = false;
                    }
                  }}
                >
                  {t(($) =>
                    confirmSubstantivePending
                      ? $.goal_card.confirming_substantive
                      : $.goal_card.confirm_substantive,
                  )}
                </Button>
              ) : null}
            </div>
          </div>
    </>
  );

  if (panelPlacement === "below") {
    return (
      <Popover open={open} onOpenChange={setOpen}>
        <PopoverTrigger render={showIcon ? icon : card} />
        <PopoverContent
          side="bottom"
          align="center"
          keepMounted
          data-testid="research-session-goal-popover"
          className="max-w-[min(26rem,calc(100vw-1.5rem))] gap-0 p-0"
        >
          {panelBody}
        </PopoverContent>
      </Popover>
    );
  }

  return (
    <>
      {showIcon ? icon : card}
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent
          data-testid="research-session-goal-popover"
          className="max-w-[calc(100vw-1.5rem)] gap-0 p-0 md:max-w-[26rem]"
        >
          <DialogHeader className="sr-only">
            <DialogTitle>{t(($) => $.goal_card.final_title)}</DialogTitle>
            <DialogDescription>{t(($) => $.goal_card.final_title)}</DialogDescription>
          </DialogHeader>
          {panelBody}
        </DialogContent>
      </Dialog>
    </>
  );
}
