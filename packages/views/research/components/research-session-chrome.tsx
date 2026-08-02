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
import { Compass } from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import { ResearchSessionMetaMenu } from "./research-session-meta-menu";

type StatusTone = { text: string; dot: string };

// Semantic status dot (design lock LRM-792: 状态中文+语义点).
const STATUS_TONES: Record<string, StatusTone> = {
  running: { text: "text-brand", dot: "bg-brand" },
  awaiting_user_confirm: { text: "text-warning", dot: "bg-warning" },
  completed: { text: "text-success", dot: "bg-success" },
};
const DEFAULT_TONE: StatusTone = {
  text: "text-muted-foreground",
  dot: "bg-muted-foreground",
};

// Module-level so the default doesn't allocate a new array identity on every
// render — an inline `= []` default breaks memo comparison downstream.
const EMPTY_MEMBERS: ResearchFleetMember[] = [];
const EMPTY_SOURCES: ResearchSource[] = [];

function StageChip({ label, className }: { label: string; className?: string }) {
  return (
    <span
      className={cn(
        "shrink-0 rounded-md border px-1.5 py-0.5 font-mono text-[10px] tracking-wider text-muted-foreground uppercase",
        className,
      )}
    >
      {label}
    </span>
  );
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
}) {
  const { t } = useT("research");
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

  const primaryClass = "bg-brand text-brand-foreground hover:bg-brand/90";

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
      <div className="flex items-center gap-2.5 px-4 pt-2.5 pb-1">
        <span
          className="hidden size-7 shrink-0 items-center justify-center rounded-[8px] bg-brand/12 text-brand sm:flex"
          aria-hidden
        >
          <Compass className="size-3.5" strokeWidth={2} />
        </span>
        <h1 className="min-w-0 truncate text-base font-semibold tracking-tight">
          {session.title}
        </h1>
        <span
          className={cn("flex shrink-0 items-center gap-1.5 text-xs font-semibold", tone.text)}
        >
          <span
            className={cn(
              "size-2 rounded-full",
              tone.dot,
              status === "running" && "animate-pulse",
            )}
          />
          {statusLabel}
        </span>
        <StageChip label={stageLabel} className="hidden sm:inline-flex" />
        {typeof session.product_round === "number" &&
        typeof session.product_round_budget === "number" &&
        session.product_round_budget > 0 ? (
          <span
            className={cn(
              "hidden shrink-0 rounded-md border px-1.5 py-0.5 font-mono text-[10px] tracking-wide sm:inline-flex",
              session.product_round >= session.product_round_budget
                ? "border-warning/40 text-warning"
                : "text-muted-foreground",
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
        ) : null}
      </div>
      <div className="flex items-center justify-between gap-3 px-4 pb-2.5">
        <p className="hidden min-w-0 flex-1 truncate text-xs text-muted-foreground sm:block">
          {selectedSummary ?? session.goal}
        </p>
        <StageChip label={stageLabel} className="sm:hidden" />
        <div className="ml-auto flex shrink-0 items-center gap-2">
          {showConfirm ? (
            <Button
              size="sm"
              className={primaryClass}
              onClick={onConfirm}
              disabled={confirmPending}
            >
              {t(($) => $.panel.confirm_continue)}
            </Button>
          ) : null}
          {showHandoff ? (
            <Popover open={handoffOpen} onOpenChange={setHandoffOpen}>
              <PopoverTrigger
                render={<Button size="sm" className={primaryClass} />}
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
          {onOpenDelivery ? (
            <Button size="sm" variant="outline" onClick={onOpenDelivery}>
              {t(($) => $.panel.view_delivery)}
            </Button>
          ) : null}
          <ResearchSessionMetaMenu
            members={members}
            sources={sources}
            sessionStatus={session.status}
          />
        </div>
      </div>
    </header>
  );
}
