"use client";

import { useState } from "react";
import type {
  ResearchFleetMember,
  ResearchRunContract,
  ResearchSession,
  ResearchSource,
} from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { Checkbox } from "@multica/ui/components/ui/checkbox";
import {
  Popover,
  PopoverContent,
  PopoverDescription,
  PopoverHeader,
  PopoverTitle,
  PopoverTrigger,
} from "@multica/ui/components/ui/popover";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { useIsMobile } from "@multica/ui/hooks/use-mobile";
import { Compass } from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import {
  DEPTH_TIERS,
  type ResearchCreateDepthTier,
  resolveSessionCreateParams,
} from "../lib/research-create-params";
import { ResearchSessionGoalCard } from "./research-session-goal-card";
import { ResearchSessionMetaMenu } from "./research-session-meta-menu";
import { ResearchStageTimeline } from "./research-stage-timeline";

type StatusTone = { text: string; dot: string; pill: string };

// Semantic status (design lock LRM-792 + LRM-995 hierarchy): pill is primary
// state signal; stage/round chips stay secondary context.
const STATUS_TONES: Record<string, StatusTone> = {
  running: {
    text: "text-brand",
    dot: "bg-brand",
    pill: "border-brand/30 bg-brand/10 text-brand",
  },
  awaiting_user_confirm: {
    text: "text-warning",
    dot: "bg-warning",
    pill: "border-warning/35 bg-warning/10 text-warning",
  },
  completed: {
    text: "text-success",
    dot: "bg-success",
    pill: "border-success/35 bg-success/10 text-success-strong",
  },
};
const DEFAULT_TONE: StatusTone = {
  text: "text-muted-foreground",
  dot: "bg-muted-foreground",
  pill: "border-border bg-muted/50 text-muted-foreground",
};

// Module-level so the default doesn't allocate a new array identity on every
// render — an inline `= []` default breaks memo comparison downstream.
const EMPTY_MEMBERS: ResearchFleetMember[] = [];
const EMPTY_SOURCES: ResearchSource[] = [];

export function ResearchSessionChrome({
  session,
  contract,
  canConfirm,
  canHandoff,
  createProject,
  createChannel,
  onCreateProjectChange,
  onCreateChannelChange,
  onConfirm,
  onReject,
  onHandoff,
  confirmPending,
  rejectPending,
  handoffPending,
  onOpenDelivery,
  members = EMPTY_MEMBERS,
  sources = EMPTY_SOURCES,
  onSelectStage,
  pendingSubstantiveGoal = null,
  onConfirmSubstantiveGoal,
  goalLoading = false,
  goalError = false,
  onGoalRetry,
  hideGoalCard = false,
}: {
  session: ResearchSession;
  contract?: ResearchRunContract | null;
  canConfirm: boolean;
  canHandoff: boolean;
  createProject: boolean;
  createChannel: boolean;
  onCreateProjectChange: (v: boolean) => void;
  onCreateChannelChange: (v: boolean) => void;
  onConfirm: () => void;
  /** LRM-840 — reject with optional feedback; parent posts tip + resumes. */
  onReject?: (reason: string) => void;
  onHandoff: () => void;
  confirmPending?: boolean;
  rejectPending?: boolean;
  handoffPending?: boolean;
  onOpenDelivery?: () => void;
  members?: ResearchFleetMember[];
  sources?: ResearchSource[];
  /** LRM-824 — top-bar stage chip anchors into the chat message area. */
  onSelectStage?: (stage: string) => void;
  /** LRM-1008 — pending substantive goal proposal (if any). */
  pendingSubstantiveGoal?: string | null;
  onConfirmSubstantiveGoal?: (proposal: string) => void;
  goalLoading?: boolean;
  goalError?: boolean;
  onGoalRetry?: () => void;
  /** D5 chrome owns the primary Goal surface — hide duplicate in actions row. */
  hideGoalCard?: boolean;
}) {
  const { t } = useT("research");
  const isMobile = useIsMobile();
  const [handoffOpen, setHandoffOpen] = useState(false);
  const [rejectOpen, setRejectOpen] = useState(false);
  const [rejectReason, setRejectReason] = useState("");

  const status = session.status;
  const tone = STATUS_TONES[status] ?? DEFAULT_TONE;
  const statusLabel = t(($) => $.status[status as keyof typeof $.status] ?? status);
  const createParams = resolveSessionCreateParams({
    goal: session.goal,
    depth_tier: session.depth_tier,
    contract,
  });
  const depthTierLabel =
    createParams.depth_tier === "shallow"
      ? t(($) => $.create_params.depth_tiers.shallow.label)
      : createParams.depth_tier === "deep"
        ? t(($) => $.create_params.depth_tiers.deep.label)
        : t(($) => $.create_params.depth_tiers.standard.label);
  const showDepthChip = DEPTH_TIERS.includes(
    createParams.depth_tier as ResearchCreateDepthTier,
  );

  // LRM-840: awaiting_user_confirm → approve + reject controls (not text-only).
  // completed → 交付移交; running → none.
  const showConfirm = status === "awaiting_user_confirm" && canConfirm;
  const showReject = showConfirm && Boolean(onReject);
  const showHandoff = status === "completed" && canHandoff;
  const hasPrimary = showConfirm || showHandoff;
  const gateBusy = Boolean(confirmPending || rejectPending);

  // LRM-995: on narrow, fold secondary "查看交付" into the tools menu so the
  // primary CTA is never crowded by an equal-weight outline button.
  const foldDeliveryIntoTools = Boolean(onOpenDelivery) && isMobile;
  const showDeliveryButton = Boolean(onOpenDelivery) && !isMobile;

  const primaryClass = "bg-brand text-brand-foreground hover:bg-brand/90";

  const roundLabel =
    typeof session.product_round === "number" &&
    typeof session.product_round_budget === "number" &&
    session.product_round_budget > 0
      ? `${t(($) => $.round.budget_chip, {
          used: session.product_round,
          budget: session.product_round_budget,
        })}${
          session.status === "completed" &&
          session.product_round >= session.product_round_budget
            ? ` · ${t(($) => $.round.budget_capped)}`
            : ""
        }`
      : null;

  return (
    <header
      data-testid="research-session-chrome"
      className="relative z-[1] shrink-0 border-b border-border/55 bg-background/70 backdrop-blur-md"
    >
      {/* LRM-971: brand hairline — same family as homepage façade. */}
      <div
        aria-hidden
        className="pointer-events-none absolute inset-x-0 top-0 h-px bg-gradient-to-r from-transparent via-brand/45 to-transparent"
      />

      {/* L1 — session identity is the only primary visual level. */}
      <div
        data-testid="research-session-identity"
        className="flex min-w-0 items-start gap-2.5 px-4 pt-2.5"
      >
        <span
          className="mt-0.5 hidden size-7 shrink-0 items-center justify-center rounded-[8px] bg-brand/12 text-brand md:flex"
          aria-hidden
        >
          <Compass className="size-3.5" strokeWidth={2} />
        </span>
        <div className="min-w-0 flex-1">
          <h1 className="truncate text-base font-semibold tracking-tight">
            {session.title}
          </h1>
          {showDepthChip || roundLabel ? (
            <div
              data-testid="research-session-meta"
              className="mt-0.5 flex min-w-0 flex-wrap items-center gap-x-2 gap-y-0.5 text-[11px] text-muted-foreground"
            >
              {showDepthChip ? (
                <span>{t(($) => $.create_params.chip_depth, { label: depthTierLabel })}</span>
              ) : null}
              {roundLabel ? <span title={t(($) => $.round.subtitle)}>{roundLabel}</span> : null}
            </div>
          ) : null}
        </div>
        <span
          data-testid="research-session-status"
          className={cn(
            "mt-0.5 inline-flex shrink-0 items-center gap-1.5 rounded-full border px-2 py-0.5 text-[11px] font-semibold",
            tone.pill,
          )}
        >
          <span
            className={cn(
              "size-1.5 rounded-full",
              tone.dot,
              status === "running" && "animate-pulse",
            )}
          />
          {statusLabel}
        </span>
      </div>

      {/* L2 + L3 — timeline and secondary actions share this header surface. */}
      <div
        data-testid="research-session-actions"
        className="flex flex-wrap items-center gap-x-2 gap-y-2 px-4 pt-2 pb-2.5"
      >
        <ResearchStageTimeline
          currentStage={session.current_stage}
          sessionStatus={session.status}
          onSelectStage={onSelectStage}
        />
        <div className={cn("ml-auto flex shrink-0 items-center gap-2", hasPrimary && "pl-1")}>
          {/* LRM-1008 / LRM-898 D — compact Goal Card: GOAL stays an icon even on desktop. */}
          {!hideGoalCard ? (
            <ResearchSessionGoalCard
              sessionId={session.id}
              goal={session.goal}
              pendingSubstantive={pendingSubstantiveGoal}
              onConfirmSubstantive={onConfirmSubstantiveGoal}
              loading={goalLoading}
              error={goalError}
              onRetry={onGoalRetry}
              compact
            />
          ) : null}
          {showConfirm ? (
            <Button
              size="sm"
              className={cn(
                primaryClass,
                // LRM-1240 — keep focusable while gateBusy (same frozen pattern as LRM-1213).
                gateBusy && "opacity-50 cursor-not-allowed",
              )}
              // Native `disabled` drops focus to <body> in Chromium after the
              // click that started the pending request. Guard the handler instead.
              aria-disabled={gateBusy || undefined}
              onClick={() => {
                if (gateBusy) return;
                onConfirm();
              }}
              data-testid="research-session-primary"
              data-gate-action="approve"
            >
              {t(($) => $.panel.gate_approve)}
            </Button>
          ) : null}
          {showReject ? (
            <Popover
              open={rejectOpen}
              onOpenChange={(open) => {
                // LRM-1240 — do not open reject popover while gate is busy.
                if (gateBusy && open) return;
                setRejectOpen(open);
                if (!open) setRejectReason("");
              }}
            >
              <PopoverTrigger
                render={
                  <Button
                    size="sm"
                    variant="outline"
                    aria-disabled={gateBusy || undefined}
                    className={cn(gateBusy && "opacity-50 cursor-not-allowed")}
                    data-testid="research-session-gate-reject"
                    data-gate-action="reject"
                  />
                }
              >
                {t(($) => $.panel.gate_reject)}
              </PopoverTrigger>
              <PopoverContent
                align="end"
                className="w-[min(20rem,calc(100vw-2rem))] gap-3 p-3"
                data-testid="research-session-gate-reject-popover"
              >
                <PopoverHeader>
                  <PopoverTitle>{t(($) => $.panel.gate_reject_title)}</PopoverTitle>
                  <PopoverDescription>
                    {t(($) => $.panel.gate_reject_hint)}
                  </PopoverDescription>
                </PopoverHeader>
                <Textarea
                  value={rejectReason}
                  onChange={(e) => setRejectReason(e.target.value)}
                  placeholder={t(($) => $.panel.gate_reject_placeholder)}
                  rows={3}
                  disabled={rejectPending}
                  className="min-h-[4.5rem] w-full resize-y text-sm"
                  data-testid="research-session-gate-reject-reason"
                />
                <Button
                  size="sm"
                  variant="destructive"
                  className="w-full"
                  disabled={rejectPending}
                  data-testid="research-session-gate-reject-submit"
                  onClick={() => {
                    onReject?.(rejectReason);
                    setRejectOpen(false);
                    setRejectReason("");
                  }}
                >
                  {rejectPending
                    ? t(($) => $.panel.gate_reject_submitting)
                    : t(($) => $.panel.gate_reject_submit)}
                </Button>
              </PopoverContent>
            </Popover>
          ) : null}
          {showHandoff ? (
            <Popover
              open={handoffOpen}
              onOpenChange={(open) => {
                // LRM-1265 — do not re-open handoff while pending (pending lives on trigger).
                if (handoffPending && open) return;
                setHandoffOpen(open);
              }}
            >
              <PopoverTrigger
                render={
                  <Button
                    size="sm"
                    className={cn(
                      primaryClass,
                      // LRM-1265 / LRM-1248·H — pending expression on trigger (not submit).
                      handoffPending && "opacity-50 cursor-not-allowed",
                    )}
                    // Native `disabled` drops focus to <body> after the click that
                    // started the pending handoff. Guard open/click instead.
                    aria-disabled={handoffPending || undefined}
                    onClick={() => {
                      if (handoffPending) return;
                    }}
                    data-testid="research-session-primary"
                  />
                }
              >
                {t(($) => $.panel.handoff_title)}
              </PopoverTrigger>
              <PopoverContent align="end" className="w-64 gap-3 p-3">
                <PopoverHeader>
                  <PopoverTitle>{t(($) => $.panel.handoff_title)}</PopoverTitle>
                </PopoverHeader>
                <label className="flex items-center gap-2 text-xs text-muted-foreground">
                  <Checkbox
                    checked={createProject}
                    onCheckedChange={(v) => onCreateProjectChange(v === true)}
                  />
                  {t(($) => $.panel.handoff_project)}
                </label>
                <label className="flex items-center gap-2 text-xs text-muted-foreground">
                  <Checkbox
                    checked={createChannel}
                    onCheckedChange={(v) => onCreateChannelChange(v === true)}
                  />
                  {t(($) => $.panel.handoff_channel)}
                </label>
                <Button
                  size="sm"
                  disabled={!createProject && !createChannel}
                  onClick={() => {
                    onHandoff();
                    setHandoffOpen(false);
                  }}
                >
                  {t(($) => $.panel.handoff)}
                </Button>
              </PopoverContent>
            </Popover>
          ) : null}
          {showDeliveryButton ? (
            <Button
              size="sm"
              variant="outline"
              onClick={onOpenDelivery}
              data-testid="research-session-delivery"
            >
              {t(($) => $.panel.view_delivery)}
            </Button>
          ) : null}
          <ResearchSessionMetaMenu
            members={members}
            sources={sources}
            session={session}
            contract={contract}
            sessionStatus={session.status}
            leadingActions={
              foldDeliveryIntoTools && onOpenDelivery
                ? [
                    {
                      id: "view-delivery",
                      label: t(($) => $.panel.view_delivery),
                      onSelect: onOpenDelivery,
                    },
                  ]
                : undefined
            }
          />
        </div>
      </div>
    </header>
  );
}
