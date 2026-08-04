"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import type { ResearchProductRoundCard } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";

/** Morgan freeze: 30s with no click → auto-adopt Ronaldo decision (not goal_patch). */
export const ROUND_AUTO_ADOPT_SECONDS = 30;

function asGapList(gaps: unknown): string[] {
  if (!Array.isArray(gaps)) return [];
  return gaps.flatMap((g) => {
    let text = "";
    if (typeof g === "string") text = g.trim();
    else if (g && typeof g === "object" && "text" in g && typeof (g as { text: unknown }).text === "string") {
      text = (g as { text: string }).text.trim();
    } else if (
      g &&
      typeof g === "object" &&
      "title" in g &&
      typeof (g as { title: unknown }).title === "string"
    ) {
      text = (g as { title: string }).title.trim();
    } else {
      try {
        text = JSON.stringify(g);
      } catch {
        text = "";
      }
    }
    return text ? [text] : [];
  });
}

function decisionTone(decision: string): string {
  switch (decision) {
    case "continue":
      return "border-brand/30 bg-brand/5 text-brand";
    case "stop_enough":
      return "border-success/30 bg-success/5 text-success-strong";
    case "stop_budget":
      return "border-warning/40 bg-warning/10 text-warning";
    default:
      return "border-border bg-muted/40 text-muted-foreground";
  }
}

export function ResearchProductRoundCardView({
  card,
  currentGoal,
  compact,
  onAgree,
  onRejectContinue,
  onRejectStop,
  onConfirmGoalPatch,
  onRejectGoalPatch,
  onEditGoalPatch,
  pending,
  autoAdoptSeconds = ROUND_AUTO_ADOPT_SECONDS,
}: {
  card: ResearchProductRoundCard;
  currentGoal?: string;
  compact?: boolean;
  onAgree?: () => void;
  onRejectContinue?: () => void;
  onRejectStop?: () => void;
  onConfirmGoalPatch?: (text: string) => void;
  onRejectGoalPatch?: () => void;
  onEditGoalPatch?: (text: string) => void;
  pending?: boolean;
  /** Override for tests; production default 30. */
  autoAdoptSeconds?: number;
}) {
  const { t } = useT("research");
  const [open, setOpen] = useState(!compact);
  const [goalOpen, setGoalOpen] = useState(false);
  const [goalDraft, setGoalDraft] = useState(card.goal_patch_proposal ?? "");
  const [timer, setTimer] = useState({ left: autoAdoptSeconds, autoAdopted: false });
  const interactedRef = useRef(false);
  const onAgreeRef = useRef(onAgree);
  onAgreeRef.current = onAgree;

  const { left: secondsLeft, autoAdopted } = timer;

  const gaps = useMemo(() => asGapList(card.coverage_gaps), [card.coverage_gaps]);
  const isContinue = card.decision === "continue";
  const isStop = card.decision === "stop_enough" || card.decision === "stop_budget";
  const decisionLabel = t(
    ($) =>
      $.round.decision[card.decision as keyof typeof $.round.decision] ?? card.decision,
  );

  // Countdown from first visible moment; any decision click cancels.
  useEffect(() => {
    if (pending || autoAdoptSeconds <= 0) return;
    interactedRef.current = false;
    setTimer({ left: autoAdoptSeconds, autoAdopted: false });
    const started = Date.now();
    const id = window.setInterval(() => {
      if (interactedRef.current) {
        window.clearInterval(id);
        return;
      }
      const left = Math.max(
        0,
        autoAdoptSeconds - Math.floor((Date.now() - started) / 1000),
      );
      if (left <= 0) {
        window.clearInterval(id);
        // Auto-adopt judgment only — never silent goal_patch write (LRM-898).
        // Keep dialog open so the timeout state is visible (AC + review screenshots).
        setTimer({ left: 0, autoAdopted: true });
        onAgreeRef.current?.();
      } else {
        setTimer((prev) => ({ ...prev, left }));
      }
    }, 250);
    return () => window.clearInterval(id);
  }, [card.id, pending, autoAdoptSeconds]);

  const markInteracted = () => {
    interactedRef.current = true;
  };

  // LRM-1339 — summary 行的层级只靠字号/字重/等宽，不靠 alpha 压文字。
  // 这些 span 继承 `decisionTone` 的语义色（brand/success/warning/muted-foreground），
  // 再乘 opacity 会把 11px/10px 小字压到 WCAG AA 以下（同 LRM-1252 缺陷类）。
  const summary = (
    <button
      type="button"
      data-testid="research-round-summary"
      data-round-decision={card.decision}
      className={cn(
        "flex w-full items-start gap-2 rounded-lg border px-2.5 py-2 text-left text-xs transition-colors hover:bg-muted/40",
        decisionTone(card.decision),
      )}
      onClick={() => setOpen(true)}
    >
      <span className="font-semibold">
        {t(($) => $.round.round_n, { n: card.round_number })} · {decisionLabel}
      </span>
      <span
        data-testid="research-round-summary-note"
        className="min-w-0 flex-1 truncate text-[11px] font-normal"
      >
        {card.confidence_note || t(($) => $.round.open_detail)}
      </span>
      {!autoAdopted && secondsLeft > 0 ? (
        <span
          data-testid="research-round-summary-countdown"
          className="shrink-0 font-mono text-[10px] tabular-nums"
        >
          {t(($) => $.round.auto_adopt_countdown, { s: secondsLeft })}
        </span>
      ) : null}
      <span
        data-testid="research-round-summary-budget"
        className="shrink-0 font-mono text-[10px] tabular-nums"
      >
        {card.budget_used}/{card.budget_used + card.budget_remaining}
      </span>
    </button>
  );

  return (
    <>
      {compact ? summary : null}

      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="max-h-[85vh] max-w-lg overflow-y-auto sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>
              {t(($) => $.round.round_n, { n: card.round_number })} · {decisionLabel}
            </DialogTitle>
            <DialogDescription>{t(($) => $.round.subtitle)}</DialogDescription>
          </DialogHeader>

          <div className="space-y-3 text-sm">
            <div
              className={cn(
                "rounded-md border px-2.5 py-1.5 text-xs font-medium",
                decisionTone(card.decision),
              )}
            >
              {decisionLabel}
              {card.decision === "stop_budget" ? (
                <span
                  data-testid="research-round-budget-capped"
                  className="ml-2 font-normal"
                >
                  {t(($) => $.round.budget_capped)}
                </span>
              ) : null}
            </div>

            {autoAdopted ? (
              // LRM-1239 — native <output> (status) for react-doctor prefer-tag-over-role.
              <output
                className="block rounded-md border border-warning/30 bg-warning/10 px-2.5 py-1.5 text-[11px] text-warning"
              >
                {t(($) => $.round.auto_adopted)}
              </output>
            ) : secondsLeft > 0 && !pending ? (
              <output
                className="block rounded-md border bg-muted/40 px-2.5 py-1.5 font-mono text-[11px] text-muted-foreground tabular-nums"
              >
                {t(($) => $.round.auto_adopt_countdown, { s: secondsLeft })}
              </output>
            ) : null}

            <section>
              <h3 className="mb-1 text-[11px] font-semibold tracking-wide text-muted-foreground uppercase">
                {t(($) => $.round.confidence)}
              </h3>
              <p className="text-[13px] leading-relaxed whitespace-pre-wrap">
                {card.confidence_note || t(($) => $.round.empty_confidence)}
              </p>
            </section>

            <section>
              <h3 className="mb-1 text-[11px] font-semibold tracking-wide text-muted-foreground uppercase">
                {t(($) => $.round.gaps)}
              </h3>
              {gaps.length === 0 ? (
                <p className="text-[13px] text-muted-foreground">{t(($) => $.round.gaps_empty)}</p>
              ) : (
                <ul className="list-disc space-y-1 pl-4 text-[13px]">
                  {gaps.map((g) => (
                    <li key={g}>{g}</li>
                  ))}
                </ul>
              )}
            </section>

            <section className="flex flex-wrap gap-3 font-mono text-[11px] text-muted-foreground">
              <span>
                {t(($) => $.round.budget_used)}: {card.budget_used}
              </span>
              <span>
                {t(($) => $.round.budget_remaining)}: {card.budget_remaining}
              </span>
            </section>

            {isContinue && card.next_round_focus ? (
              <section>
                <h3 className="mb-1 text-[11px] font-semibold tracking-wide text-muted-foreground uppercase">
                  {t(($) => $.round.next_focus)}
                </h3>
                <p className="text-[13px] leading-relaxed">{card.next_round_focus}</p>
              </section>
            ) : null}

            {card.goal_patch_proposal ? (
              <section className="rounded-lg border border-dashed bg-muted/30 p-2.5">
                <h3 className="mb-1 text-[11px] font-semibold tracking-wide text-muted-foreground uppercase">
                  {t(($) => $.round.goal_patch)}
                </h3>
                {currentGoal ? (
                  <p
                    data-testid="research-round-goal-current"
                    className="mb-1 text-[11px] text-muted-foreground line-through"
                  >
                    {currentGoal}
                  </p>
                ) : null}
                <p className="mb-2 text-[13px] leading-relaxed">{card.goal_patch_proposal}</p>
                <p className="mb-2 text-[10px] text-muted-foreground">{t(($) => $.round.goal_patch_hint)}</p>
                <div className="flex flex-wrap gap-2">
                  <Button
                    size="sm"
                    data-testid="research-round-goal-confirm"
                    // LRM-1239 — pending must stay focusable (same root cause as LRM-1213).
                    aria-disabled={pending || undefined}
                    onClick={() => {
                      if (pending) return;
                      setGoalDraft(card.goal_patch_proposal ?? "");
                      setGoalOpen(true);
                    }}
                    className={cn(pending && "opacity-50 cursor-not-allowed")}
                  >
                    {t(($) => $.round.goal_confirm)}
                  </Button>
                  <Button
                    size="sm"
                    variant="outline"
                    data-testid="research-round-goal-edit"
                    aria-disabled={pending || undefined}
                    onClick={() => {
                      if (pending) return;
                      setGoalDraft(card.goal_patch_proposal ?? "");
                      setGoalOpen(true);
                    }}
                    className={cn(pending && "opacity-50 cursor-not-allowed")}
                  >
                    {t(($) => $.round.goal_edit)}
                  </Button>
                  <Button
                    size="sm"
                    variant="ghost"
                    data-testid="research-round-goal-reject"
                    aria-disabled={pending || undefined}
                    onClick={() => {
                      if (pending) return;
                      onRejectGoalPatch?.();
                    }}
                    className={cn(pending && "opacity-50 cursor-not-allowed")}
                  >
                    {t(($) => $.round.goal_reject)}
                  </Button>
                </div>
              </section>
            ) : null}
          </div>

          <DialogFooter className="flex-col gap-2 sm:flex-col sm:space-x-0">
            <Button
              className={cn(
                "w-full",
                pending && !autoAdopted && "opacity-50 cursor-not-allowed",
              )}
              data-testid="research-round-agree"
              // True unavailability (auto-adopted) may keep native disabled; pending
              // alone must stay focusable (LRM-1239 / LRM-1213).
              disabled={autoAdopted || undefined}
              aria-disabled={pending || undefined}
              onClick={() => {
                if (pending || autoAdopted) return;
                markInteracted();
                onAgree?.();
                setOpen(false);
              }}
            >
              {t(($) => $.round.agree)}
            </Button>
            {isContinue ? (
              <Button
                className={cn(
                  "w-full",
                  pending &&
                    !autoAdopted &&
                    card.budget_remaining > 0 &&
                    "opacity-50 cursor-not-allowed",
                )}
                variant="outline"
                data-testid="research-round-reject-continue"
                disabled={
                  autoAdopted || card.budget_remaining <= 0 || undefined
                }
                aria-disabled={pending || undefined}
                onClick={() => {
                  if (pending || autoAdopted || card.budget_remaining <= 0) return;
                  markInteracted();
                  onRejectContinue?.();
                  setOpen(false);
                }}
              >
                {t(($) => $.round.reject_continue)}
              </Button>
            ) : null}
            {isStop ? (
              <Button
                className={cn(
                  "w-full",
                  pending &&
                    !autoAdopted &&
                    card.budget_remaining > 0 &&
                    "opacity-50 cursor-not-allowed",
                )}
                variant="outline"
                data-testid="research-round-reject-stop"
                disabled={
                  autoAdopted || card.budget_remaining <= 0 || undefined
                }
                aria-disabled={pending || undefined}
                onClick={() => {
                  if (pending || autoAdopted || card.budget_remaining <= 0) return;
                  markInteracted();
                  onRejectStop?.();
                  setOpen(false);
                }}
              >
                {t(($) => $.round.reject_stop)}
              </Button>
            ) : null}
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={goalOpen} onOpenChange={setGoalOpen}>
        <DialogContent className="max-w-lg">
          <DialogHeader>
            <DialogTitle>{t(($) => $.round.goal_dialog_title)}</DialogTitle>
            <DialogDescription>{t(($) => $.round.goal_patch_hint)}</DialogDescription>
          </DialogHeader>
          <Textarea
            rows={5}
            value={goalDraft}
            onChange={(e) => setGoalDraft(e.target.value)}
          />
          <DialogFooter className="gap-2">
            <Button
              variant="outline"
              data-testid="research-round-goal-edit-send"
              // Empty draft is truly unavailable; pending alone stays focusable.
              disabled={!goalDraft.trim() || undefined}
              aria-disabled={pending || undefined}
              onClick={() => {
                if (pending || !goalDraft.trim()) return;
                onEditGoalPatch?.(goalDraft.trim());
                setGoalOpen(false);
              }}
              className={cn(
                pending && goalDraft.trim() && "opacity-50 cursor-not-allowed",
              )}
            >
              {t(($) => $.round.goal_edit_send)}
            </Button>
            <Button
              data-testid="research-round-goal-confirm-send"
              disabled={!goalDraft.trim() || undefined}
              aria-disabled={pending || undefined}
              onClick={() => {
                if (pending || !goalDraft.trim()) return;
                onConfirmGoalPatch?.(goalDraft.trim());
                setGoalOpen(false);
              }}
              className={cn(
                pending && goalDraft.trim() && "opacity-50 cursor-not-allowed",
              )}
            >
              {t(($) => $.round.goal_confirm)}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}
