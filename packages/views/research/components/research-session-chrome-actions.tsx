"use client";

import { useRef, useState } from "react";
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
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import { ResearchSessionGoalCard } from "./research-session-goal-card";
import { ResearchSessionMetaMenu } from "./research-session-meta-menu";

const STATUS_TONES: Record<string, { dot: string; pill: string }> = {
  running: {
    dot: "bg-brand",
    pill: "border-brand/30 bg-brand/10 text-brand",
  },
  awaiting_user_confirm: {
    dot: "bg-warning",
    pill: "border-warning/35 bg-warning/10 text-warning",
  },
  completed: {
    dot: "bg-success",
    pill: "border-success/35 bg-success/10 text-success-strong",
  },
};
const DEFAULT_TONE = {
  dot: "bg-muted-foreground",
  pill: "border-border bg-muted/50 text-muted-foreground",
};

const EMPTY_MEMBERS: ResearchFleetMember[] = [];
const EMPTY_SOURCES: ResearchSource[] = [];

export function ResearchSessionChromeActions({
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
  pendingSubstantiveGoal = null,
  onConfirmSubstantiveGoal,
  goalLoading = false,
  goalError = false,
  onGoalRetry,
  showGoalCard = false,
  showStatus = false,
  className,
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
  onReject?: (reason: string) => void | Promise<void>;
  onHandoff: () => void;
  confirmPending?: boolean;
  rejectPending?: boolean;
  handoffPending?: boolean;
  onOpenDelivery?: () => void;
  members?: ResearchFleetMember[];
  sources?: ResearchSource[];
  pendingSubstantiveGoal?: string | null;
  onConfirmSubstantiveGoal?: (proposal: string) => void;
  goalLoading?: boolean;
  goalError?: boolean;
  onGoalRetry?: () => void;
  showGoalCard?: boolean;
  showStatus?: boolean;
  className?: string;
}) {
  const { t } = useT("research");
  const isMobile = useIsMobile();
  const [handoffOpen, setHandoffOpen] = useState(false);
  const [rejectOpen, setRejectOpen] = useState(false);
  const [rejectReason, setRejectReason] = useState("");
  const rejectSubmittingRef = useRef(false);

  const status = session.status;
  const tone = STATUS_TONES[status] ?? DEFAULT_TONE;
  const statusLabel = t(($) => $.status[status as keyof typeof $.status] ?? status);
  const showConfirm = status === "awaiting_user_confirm" && canConfirm;
  const showReject = showConfirm && Boolean(onReject);
  const showHandoff = status === "completed" && canHandoff;
  const hasPrimary = showConfirm || showHandoff;
  const gateBusy = Boolean(confirmPending || rejectPending);
  const foldDeliveryIntoTools = Boolean(onOpenDelivery) && isMobile;
  const showDeliveryButton = Boolean(onOpenDelivery) && !isMobile;
  const primaryClass = "bg-brand text-brand-foreground hover:bg-brand/90";

  return (
    <div
      className={cn(
        "flex shrink-0 flex-wrap items-center gap-2",
        hasPrimary && "pl-1",
        className,
      )}
    >
      {showStatus ? (
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
              status === "running" && "animate-pulse motion-reduce:animate-none",
            )}
          />
          {statusLabel}
        </span>
      ) : null}
      {showGoalCard ? (
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
          className={cn(primaryClass, gateBusy && "opacity-50 cursor-not-allowed")}
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
              <PopoverDescription>{t(($) => $.panel.gate_reject_hint)}</PopoverDescription>
            </PopoverHeader>
            <Textarea
              value={rejectReason}
              onChange={(e) => {
                if (rejectPending) return;
                setRejectReason(e.target.value);
              }}
              placeholder={t(($) => $.panel.gate_reject_placeholder)}
              rows={3}
              aria-disabled={rejectPending || undefined}
              className={cn(
                "min-h-[4.5rem] w-full resize-y text-sm",
                rejectPending && "cursor-not-allowed opacity-50",
              )}
              data-testid="research-session-gate-reject-reason"
            />
            <Button
              size="sm"
              variant="destructive"
              aria-disabled={rejectPending || undefined}
              aria-busy={rejectPending || undefined}
              className={cn(
                "w-full",
                rejectPending && "cursor-not-allowed opacity-50",
              )}
              data-testid="research-session-gate-reject-submit"
              onClick={async () => {
                if (rejectPending || rejectSubmittingRef.current) return;
                rejectSubmittingRef.current = true;
                try {
                  await onReject?.(rejectReason);
                  setRejectOpen(false);
                  setRejectReason("");
                } catch {
                  // The mutation owner presents the API error. Keep the popover
                  // and exact user feedback intact so recovery is lossless.
                } finally {
                  rejectSubmittingRef.current = false;
                }
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
            if (handoffPending && open) return;
            setHandoffOpen(open);
          }}
        >
          <PopoverTrigger
            render={
              <Button
                size="sm"
                className={cn(primaryClass, handoffPending && "opacity-50 cursor-not-allowed")}
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
  );
}
