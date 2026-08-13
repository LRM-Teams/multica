"use client";

import type {
  ResearchFleetMember,
  ResearchRunContract,
  ResearchSession,
  ResearchSource,
} from "@multica/core/types";
import { Compass } from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import {
  DEPTH_TIERS,
  type ResearchCreateDepthTier,
  resolveSessionCreateParams,
} from "../lib/research-create-params";
import { ResearchSessionChromeActions } from "./research-session-chrome-actions";
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
              status === "running" && "animate-pulse motion-reduce:animate-none",
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
        <ResearchSessionChromeActions
          session={session}
          contract={contract}
          canConfirm={canConfirm}
          canHandoff={canHandoff}
          createProject={createProject}
          createChannel={createChannel}
          onCreateProjectChange={onCreateProjectChange}
          onCreateChannelChange={onCreateChannelChange}
          onConfirm={onConfirm}
          onReject={onReject}
          onHandoff={onHandoff}
          confirmPending={confirmPending}
          rejectPending={rejectPending}
          handoffPending={handoffPending}
          onOpenDelivery={onOpenDelivery}
          members={members}
          sources={sources}
          pendingSubstantiveGoal={pendingSubstantiveGoal}
          onConfirmSubstantiveGoal={onConfirmSubstantiveGoal}
          goalLoading={goalLoading}
          goalError={goalError}
          onGoalRetry={onGoalRetry}
          showGoalCard={!hideGoalCard}
          className="ml-auto"
        />
      </div>
    </header>
  );
}
