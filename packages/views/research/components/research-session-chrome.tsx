"use client";

import { useState } from "react";
import type { ResearchFleetMember, ResearchSession, ResearchSource } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { Checkbox } from "@multica/ui/components/ui/checkbox";
import {
  Popover,
  PopoverContent,
  PopoverHeader,
  PopoverTitle,
  PopoverTrigger,
} from "@multica/ui/components/ui/popover";
import { useIsMobile } from "@multica/ui/hooks/use-mobile";
import { Compass } from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import { ResearchSessionMetaMenu } from "./research-session-meta-menu";

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
    pill: "border-success/35 bg-success/10 text-success",
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

function ContextChip({
  label,
  className,
  interactive,
  onClick,
}: {
  label: string;
  className?: string;
  interactive?: boolean;
  onClick?: () => void;
}) {
  const base = cn(
    "shrink-0 rounded-md border border-border/70 bg-muted/35 px-1.5 py-0.5 font-mono text-[10px] tracking-wider text-muted-foreground uppercase",
    interactive &&
      "cursor-pointer transition-colors hover:bg-muted/70 hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand/30",
    className,
  );
  if (interactive && onClick) {
    return (
      <button type="button" onClick={onClick} className={base}>
        {label}
      </button>
    );
  }
  return <span className={base}>{label}</span>;
}

export function ResearchSessionChrome({
  session,
  canConfirm,
  canHandoff,
  createProject,
  createChannel,
  onCreateProjectChange,
  onCreateChannelChange,
  onConfirm,
  onHandoff,
  confirmPending,
  handoffPending,
  onOpenDelivery,
  selectedSummary,
  members = EMPTY_MEMBERS,
  sources = EMPTY_SOURCES,
  onSelectStage,
}: {
  session: ResearchSession;
  canConfirm: boolean;
  canHandoff: boolean;
  createProject: boolean;
  createChannel: boolean;
  onCreateProjectChange: (v: boolean) => void;
  onCreateChannelChange: (v: boolean) => void;
  onConfirm: () => void;
  onHandoff: () => void;
  confirmPending?: boolean;
  handoffPending?: boolean;
  onOpenDelivery?: () => void;
  selectedSummary?: string | null;
  members?: ResearchFleetMember[];
  sources?: ResearchSource[];
  /** LRM-824 — top-bar stage chip anchors into the chat message area. */
  onSelectStage?: (stage: string) => void;
}) {
  const { t } = useT("research");
  const isMobile = useIsMobile();
  const [handoffOpen, setHandoffOpen] = useState(false);

  const status = session.status;
  const tone = STATUS_TONES[status] ?? DEFAULT_TONE;
  const statusLabel = t(($) => $.status[status as keyof typeof $.status] ?? status);
  const stageLabel = t(
    ($) => $.stage[session.current_stage as keyof typeof $.stage] ?? session.current_stage,
  );

  // One primary action per state (design lock): awaiting_user_confirm →
  // 确认并继续, completed → 交付移交, running → none.
  const showConfirm = status === "awaiting_user_confirm" && canConfirm;
  const showHandoff = status === "completed" && canHandoff;
  const hasPrimary = showConfirm || showHandoff;

  // LRM-995: on narrow, fold secondary "查看交付" into the tools menu so the
  // primary CTA is never crowded by an equal-weight outline button.
  const foldDeliveryIntoTools = Boolean(onOpenDelivery) && isMobile;
  const showDeliveryButton = Boolean(onOpenDelivery) && !isMobile;

  const primaryClass = "bg-brand text-brand-foreground hover:bg-brand/90";

  const roundChip =
    typeof session.product_round === "number" &&
    typeof session.product_round_budget === "number" &&
    session.product_round_budget > 0 ? (
      <span
        className={cn(
          "shrink-0 rounded-md border px-1.5 py-0.5 font-mono text-[10px] tracking-wide",
          session.product_round >= session.product_round_budget
            ? "border-warning/40 bg-warning/10 text-warning"
            : "border-border/70 bg-muted/35 text-muted-foreground",
        )}
        title={t(($) => $.round.subtitle)}
      >
        {t(($) => $.round.budget_chip, {
          used: session.product_round,
          budget: session.product_round_budget,
        })}
        {session.status === "completed" &&
        session.product_round >= session.product_round_budget
          ? ` · ${t(($) => $.round.budget_capped)}`
          : ""}
      </span>
    ) : null;

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

      {/* Identity: title + status (primary visual) */}
      <div
        data-testid="research-session-identity"
        className="flex items-center gap-2.5 px-4 pt-2.5 pb-1"
      >
        <span
          className="hidden size-7 shrink-0 items-center justify-center rounded-[8px] bg-brand/12 text-brand sm:flex"
          aria-hidden
        >
          <Compass className="size-3.5" strokeWidth={2} />
        </span>
        <h1 className="min-w-0 flex-1 truncate text-base font-semibold tracking-tight">
          {session.title}
        </h1>
        <span
          data-testid="research-session-status"
          className={cn(
            "inline-flex shrink-0 items-center gap-1.5 rounded-full border px-2 py-0.5 text-[11px] font-semibold",
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

      {/* Context + actions: stage/goal secondary; one primary CTA; secondary folded on narrow */}
      <div
        data-testid="research-session-toolbar"
        className="flex items-center justify-between gap-3 px-4 pb-2.5"
      >
        <div
          data-testid="research-session-context"
          className="flex min-w-0 flex-1 items-center gap-2"
        >
          <ContextChip
            label={stageLabel}
            interactive={Boolean(onSelectStage)}
            onClick={onSelectStage ? () => onSelectStage(session.current_stage) : undefined}
          />
          {roundChip}
          <p className="hidden min-w-0 flex-1 truncate text-xs text-muted-foreground sm:block">
            {selectedSummary ?? session.goal}
          </p>
        </div>
        <div
          data-testid="research-session-actions"
          className={cn(
            "flex shrink-0 items-center gap-2",
            hasPrimary && "pl-1",
          )}
        >
          {showConfirm ? (
            <Button
              size="sm"
              className={primaryClass}
              onClick={onConfirm}
              disabled={confirmPending}
              data-testid="research-session-primary"
            >
              {t(($) => $.panel.confirm_continue)}
            </Button>
          ) : null}
          {showHandoff ? (
            <Popover open={handoffOpen} onOpenChange={setHandoffOpen}>
              <PopoverTrigger
                render={
                  <Button
                    size="sm"
                    className={primaryClass}
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
                  disabled={handoffPending || (!createProject && !createChannel)}
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
