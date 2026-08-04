"use client";

import { useState } from "react";
import { cn } from "@multica/ui/lib/utils";
import { Button } from "@multica/ui/components/ui/button";
import { ActorAvatar } from "../../common/actor-avatar";
import { useT } from "../../i18n/use-t";
import type { FleetStepCardModel, FleetStepStatus } from "../lib/fleet-step-cards";

/** LRM-1010 — semantic success/warning tokens (no palette emerald/amber / dark: forks). */
function badgeClass(status: FleetStepStatus): string {
  switch (status) {
    case "done":
      return "bg-success/10 text-success-strong";
    case "running":
      return "bg-primary/10 text-primary";
    case "waiting":
      return "bg-warning/10 text-warning";
    case "failed":
      return "bg-destructive/10 text-destructive";
  }
}

function cardClass(status: FleetStepStatus): string {
  switch (status) {
    case "done":
      return "border-success/25 bg-card";
    case "running":
      return "border-primary/30 bg-card";
    case "waiting":
      return "border-border bg-card/80";
    case "failed":
      return "border-destructive/30 bg-destructive/5";
  }
}

function initialGlyph(title: string): string {
  const trimmed = title.trim();
  if (!trimmed) return "步";
  return trimmed.slice(0, 1);
}

export function ResearchFleetStepCard({
  card,
  onRetry,
  onReassign,
}: {
  card: FleetStepCardModel;
  onRetry?: (card: FleetStepCardModel) => void;
  onReassign?: (card: FleetStepCardModel) => void;
}) {
  const { t } = useT("research");
  const [expanded, setExpanded] = useState(false);
  const statusLabel = t(($) => $.step_card.status[card.status]);
  const canExpand = Boolean(card.evidence && card.evidence.trim());

  return (
    <article
      data-testid="fleet-step-card"
      data-status={card.status}
      className={cn(
        "rounded-xl border px-3 py-2.5 text-sm shadow-sm motion-safe:animate-in motion-safe:fade-in",
        cardClass(card.status),
      )}
    >
      <header className="mb-1.5 flex items-center gap-2">
        {card.actorAgentId ? (
          <ActorAvatar
            actorType="agent"
            actorId={card.actorAgentId}
            size={22}
            enableHoverCard
            showStatusDot
            profileLink
          />
        ) : (
          <span
            className={cn(
              "flex h-[22px] w-[22px] shrink-0 items-center justify-center rounded-full text-[9px] font-semibold",
              card.status === "failed"
                ? "bg-destructive/15 text-destructive"
                : "bg-primary/15 text-primary",
            )}
          >
            {initialGlyph(card.title)}
          </span>
        )}
        <div className="min-w-0 flex-1 truncate text-xs font-semibold text-foreground">
          {card.title}
        </div>
        <span
          className={cn(
            "shrink-0 rounded-md px-1.5 py-0.5 text-[10px] font-bold",
            badgeClass(card.status),
          )}
        >
          {statusLabel}
        </span>
      </header>

      {card.stepLabel ? (
        <div className="text-[10.5px] text-muted-foreground">{card.stepLabel}</div>
      ) : null}

      <p className="mt-1 text-[12px] leading-relaxed text-muted-foreground">
        <span className="font-medium text-foreground">{card.summaryHeadline}</span>
        {card.summaryDetail ? (
          <>
            {" · "}
            {card.summaryDetail}
          </>
        ) : null}
      </p>

      {card.bullets.length > 0 ? (
        <ul className="mt-1.5 list-disc space-y-0.5 pl-4 text-[11.5px] leading-relaxed text-muted-foreground">
          {card.bullets.map((b) => (
            <li key={b}>{b}</li>
          ))}
        </ul>
      ) : null}

      {expanded && canExpand ? (
        <pre className="mt-2 max-h-40 overflow-auto whitespace-pre-wrap rounded-lg border bg-muted/40 p-2 text-[11px] leading-relaxed text-muted-foreground">
          {card.evidence}
        </pre>
      ) : null}

      {canExpand || card.showRetry || card.showReassign ? (
        <div className="mt-2 flex flex-wrap gap-1.5">
          {canExpand ? (
            <Button
              type="button"
              size="sm"
              variant="ghost"
              className="h-7 px-2 text-[11px]"
              onClick={() => setExpanded((v) => !v)}
            >
              {expanded
                ? t(($) => $.step_card.collapse)
                : t(($) => $.step_card.expand)}
            </Button>
          ) : null}
          {card.showRetry ? (
            <Button
              type="button"
              size="sm"
              className="h-7 px-2 text-[11px]"
              onClick={() => onRetry?.(card)}
            >
              {t(($) => $.step_card.retry)}
            </Button>
          ) : null}
          {card.showReassign ? (
            <Button
              type="button"
              size="sm"
              variant="outline"
              className="h-7 px-2 text-[11px]"
              onClick={() => onReassign?.(card)}
            >
              {t(($) => $.step_card.reassign)}
            </Button>
          ) : null}
        </div>
      ) : null}
    </article>
  );
}
