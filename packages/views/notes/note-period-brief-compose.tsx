"use client";

import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { Check, Cloud, Folder, Laptop, MonitorSmartphone } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { useAuthStore } from "@multica/core/auth";
import { useWorkspaceId } from "@multica/core/hooks";
import { resolvePeriodBriefSynthesizerId } from "@multica/core/notes/period-brief-agent";
import {
  defaultPeriodBriefCollectorIds,
  isPeriodBriefCollectorOnline,
  listOwnedPeriodBriefCollectorAgents,
  listOwnedPeriodBriefCollectorSlots,
  listPeriodBriefCollectorSlotsNeedingRuntime,
  listPeriodBriefCollectorSlotsNeedingSetup,
  periodBriefCollectorDaemonId,
  periodBriefCollectorLabel,
  periodBriefSlotDaemonId,
  togglePeriodBriefCollectorId,
  type PeriodBriefCollectorSlot,
} from "@multica/core/notes/period-brief-collectors";
import {
  isPeriodBriefPlanComplete,
  looksLikeUnconstrainedScope,
  type PeriodBriefComposeCollector,
  type PeriodBriefComposeRequest,
  type PeriodBriefComposeSelection,
} from "@multica/core/notes/period-brief-compose";
import {
  defaultPeriodBriefCustomRange,
  isValidPeriodBriefCustomRange,
} from "@multica/core/notes/period-brief-window";
import { computerListOptions, runtimeListOptions } from "@multica/core/runtimes";
import type { NotePeriodBriefPlan, NotePeriodBriefWindow } from "@multica/core/types";
import { agentListOptions } from "@multica/core/workspace/queries";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@multica/ui/components/ui/tooltip";
import { cn } from "@multica/ui/lib/utils";
import { useViewingTimezone } from "../common/use-viewing-timezone";
import { useT } from "../i18n/use-t";
import { NotesCollectorSetupCard } from "./notes-collector-setup-card";
import { PeriodBriefCollectRootsDialog } from "./period-brief-collect-roots-dialog";

export type NotePeriodBriefResolved = {
  agentId: string | null;
  timezone: string;
  canSubmit: boolean;
  unconstrained: boolean;
  selection: PeriodBriefComposeSelection;
  collectors: PeriodBriefComposeCollector[];
  request: PeriodBriefComposeRequest;
};

const WINDOW_KINDS: NotePeriodBriefWindow[] = ["day", "week", "month", "custom"];

function CollectorChipIconButton({
  label,
  testId,
  disabled,
  onClick,
  children,
}: {
  label: string;
  testId: string;
  disabled?: boolean;
  onClick: () => void;
  children: ReactNode;
}) {
  return (
    <Tooltip>
      <TooltipTrigger
        render={
          <Button
            type="button"
            size="icon-sm"
            variant="ghost"
            className="size-7"
            disabled={disabled}
            data-testid={testId}
            aria-label={label}
            onClick={onClick}
          />
        }
      >
        {children}
      </TooltipTrigger>
      <TooltipContent side="top">{label}</TooltipContent>
    </Tooltip>
  );
}

export function NotePeriodBriefCompose({
  active,
  text = "",
  submitting = false,
  disabled = false,
  plan = null,
  onPlanChange,
  onResolvedChange,
  onStart,
  onCancel,
  onConfigureCollector,
}: {
  active: boolean;
  text?: string;
  submitting?: boolean;
  disabled?: boolean;
  plan?: NotePeriodBriefPlan | null;
  onPlanChange?: (plan: NotePeriodBriefPlan) => void;
  onResolvedChange?: (resolved: NotePeriodBriefResolved) => void;
  onStart?: () => void;
  onCancel?: () => void;
  onConfigureCollector?: (slot: PeriodBriefCollectorSlot) => void;
}) {
  const { t } = useT("layout");
  const timezone = useViewingTimezone();
  const wsId = useWorkspaceId();
  const currentUserId = useAuthStore((s) => s.user?.id ?? null);
  const { data: agents = [] } = useQuery({
    ...agentListOptions(wsId),
    enabled: Boolean(wsId),
  });
  const { data: runtimes = [] } = useQuery({
    ...runtimeListOptions(wsId),
    enabled: Boolean(wsId),
  });
  const { data: computers = [] } = useQuery({
    ...computerListOptions(wsId ?? ""),
    enabled: Boolean(wsId),
  });
  const today = useMemo(() => {
    try {
      return new Intl.DateTimeFormat("en-CA", {
        timeZone: timezone,
        year: "numeric",
        month: "2-digit",
        day: "2-digit",
      }).format(new Date());
    } catch {
      return new Date().toISOString().slice(0, 10);
    }
  }, [timezone]);
  const [windowKind, setWindowKind] = useState<NotePeriodBriefWindow>("week");
  const defaultCustom = useMemo(() => defaultPeriodBriefCustomRange(today), [today]);
  const [startDate, setStartDate] = useState(defaultCustom.start_date);
  const [endDate, setEndDate] = useState(defaultCustom.end_date);
  const [collectorOverride, setCollectorOverride] = useState<string[] | null>(null);
  const [dismissedMissingKeys, setDismissedMissingKeys] = useState<string[]>([]);
  const [collectRootsTarget, setCollectRootsTarget] = useState<{
    machineId: string;
    label: string;
    online: boolean;
  } | null>(null);
  const agentId = resolvePeriodBriefSynthesizerId(agents);
  const collectorAgents = useMemo(
    () => listOwnedPeriodBriefCollectorAgents(agents, runtimes, currentUserId),
    [agents, runtimes, currentUserId],
  );
  const defaultCollectors = useMemo(
    () => defaultPeriodBriefCollectorIds(agents, runtimes, currentUserId),
    [agents, runtimes, currentUserId],
  );
  const missingCollectorSlots = useMemo(
    () =>
      listPeriodBriefCollectorSlotsNeedingSetup(
        runtimes,
        agents,
        currentUserId,
        computers,
      ).filter((slot) => !dismissedMissingKeys.includes(slot.key)),
    [agents, computers, currentUserId, dismissedMissingKeys, runtimes],
  );
  const waitingRuntimeSlots = useMemo(
    () =>
      listPeriodBriefCollectorSlotsNeedingRuntime(
        runtimes,
        agents,
        currentUserId,
        computers,
      ).filter((slot) => !dismissedMissingKeys.includes(slot.key)),
    [agents, computers, currentUserId, dismissedMissingKeys, runtimes],
  );
  const collectorSlots = useMemo(
    () => listOwnedPeriodBriefCollectorSlots(runtimes, agents, currentUserId, computers),
    [agents, computers, currentUserId, runtimes],
  );
  const collectorIds = collectorOverride ?? (plan ? plan.collector_agent_ids : defaultCollectors);

  const interactive = active && !disabled;
  const prevInteractiveRef = useRef(interactive);
  if (!plan && interactive !== prevInteractiveRef.current) {
    prevInteractiveRef.current = interactive;
    if (interactive) {
      setCollectorOverride(null);
      setDismissedMissingKeys([]);
      setWindowKind("week");
      const custom = defaultPeriodBriefCustomRange(today);
      setStartDate(custom.start_date);
      setEndDate(custom.end_date);
    }
  } else {
    prevInteractiveRef.current = interactive;
  }

  const planKey = plan
    ? `${plan.window}|${plan.date ?? ""}|${plan.start_date ?? ""}|${plan.end_date ?? ""}|${plan.collector_agent_ids.join(",")}|${plan.focus ?? ""}`
    : "";
  useEffect(() => {
    if (!plan) return;
    const kind = plan.window === "day" || plan.window === "week" || plan.window === "month" || plan.window === "custom"
      ? plan.window
      : "week";
    setWindowKind(kind);
    if (plan.start_date) setStartDate(plan.start_date);
    if (plan.end_date) setEndDate(plan.end_date);
    setCollectorOverride(plan.collector_agent_ids);
  }, [plan, planKey]);

  const collectors = useMemo(
    () =>
      collectorAgents.map((agent) => ({
        id: agent.id,
        label: periodBriefCollectorLabel(agent),
        runtime_mode: agent.runtime_mode,
      })),
    [collectorAgents],
  );
  const selection = useMemo<PeriodBriefComposeSelection>(
    () => ({
      window: windowKind,
      date: plan?.date || today,
      start_date: startDate,
      end_date: endDate,
      collector_ids: collectorIds,
    }),
    [collectorIds, endDate, plan?.date, startDate, today, windowKind],
  );
  const request = useMemo<PeriodBriefComposeRequest>(
    () => ({
      window: selection.window,
      date: selection.window === "custom" ? undefined : selection.date,
      start_date: selection.start_date,
      end_date: selection.end_date,
      collector_ids: [...selection.collector_ids],
      focus: plan?.focus?.trim() || undefined,
    }),
    [plan?.focus, selection],
  );
  const customRangeValid =
    request.window !== "custom" ||
    isValidPeriodBriefCustomRange(request.start_date ?? "", request.end_date ?? "");
  const unconstrained = looksLikeUnconstrainedScope(plan?.focus ?? text);
  const canSubmit =
    Boolean(agentId) &&
    !submitting &&
    isPeriodBriefPlanComplete({
      collectorIds: request.collector_ids,
      customRangeValid,
    });

  const emitPlan = (next: {
    window: NotePeriodBriefWindow;
    date: string;
    start_date: string;
    end_date: string;
    collector_agent_ids: string[];
  }) => {
    onPlanChange?.({
      window: next.window,
      date: next.window === "custom" ? undefined : next.date,
      start_date: next.window === "custom" ? next.start_date : undefined,
      end_date: next.window === "custom" ? next.end_date : undefined,
      collector_agent_ids: next.collector_agent_ids,
      focus: plan?.focus ?? "",
    });
  };

  useEffect(() => {
    onResolvedChange?.({
      agentId,
      timezone,
      canSubmit,
      unconstrained,
      selection,
      collectors,
      request,
    });
  }, [
    agentId,
    canSubmit,
    collectors,
    onResolvedChange,
    request,
    selection,
    timezone,
    unconstrained,
  ]);

  const busy = submitting || disabled;

  return (
    <div
      className={cn(
        "space-y-3 rounded-xl border bg-card px-3 py-3",
        disabled && "opacity-60",
      )}
      data-testid="period-brief-compose"
      data-spent={disabled ? "true" : "false"}
    >
      <div className="space-y-1.5">
        <p className="text-xs font-medium text-muted-foreground">
          {t(($) => $.notes_page.period_brief_window_label)}
        </p>
        <div className="flex flex-wrap gap-1.5">
          {WINDOW_KINDS.map((kind) => (
            <Button
              key={kind}
              type="button"
              size="sm"
              variant={windowKind === kind ? "default" : "outline"}
              onClick={() => {
                setWindowKind(kind);
                emitPlan({
                  window: kind,
                  date: plan?.date || today,
                  start_date: startDate,
                  end_date: endDate,
                  collector_agent_ids: collectorIds,
                });
              }}
              disabled={busy}
              data-testid={`period-brief-window-${kind}`}
            >
              {kind === "day"
                ? t(($) => $.notes_page.period_brief_window_day)
                : kind === "week"
                  ? t(($) => $.notes_page.period_brief_window_week)
                  : kind === "month"
                    ? t(($) => $.notes_page.period_brief_window_month)
                    : t(($) => $.notes_page.period_brief_window_custom)}
            </Button>
          ))}
        </div>
      </div>
      {windowKind === "custom" ? (
        <div className="grid grid-cols-2 gap-2">
          <Input
            type="date"
            value={startDate}
            onChange={(event) => {
              const value = event.target.value;
              setStartDate(value);
              emitPlan({
                window: "custom",
                date: selection.date,
                start_date: value,
                end_date: endDate,
                collector_agent_ids: collectorIds,
              });
            }}
            disabled={busy}
            aria-label={t(($) => $.notes_page.period_brief_start_date_label)}
            data-testid="period-brief-start-date"
          />
          <Input
            type="date"
            value={endDate}
            onChange={(event) => {
              const value = event.target.value;
              setEndDate(value);
              emitPlan({
                window: "custom",
                date: selection.date,
                start_date: startDate,
                end_date: value,
                collector_agent_ids: collectorIds,
              });
            }}
            disabled={busy}
            aria-label={t(($) => $.notes_page.period_brief_end_date_label)}
            data-testid="period-brief-end-date"
          />
        </div>
      ) : null}
      <div className="space-y-1.5">
        <p className="text-xs font-medium text-muted-foreground">
          {t(($) => $.notes_page.period_brief_collectors_label)}
        </p>
        <div className="max-h-48 space-y-1 overflow-y-auto" data-testid="period-brief-collectors">
          <TooltipProvider delay={200}>
          {collectorAgents.length === 0 &&
          missingCollectorSlots.length === 0 &&
          waitingRuntimeSlots.length === 0 ? (
            <div className="rounded-md border border-dashed px-2 py-2 text-xs text-muted-foreground">
              {t(($) => $.notes_page.period_brief_collectors_empty)}
            </div>
          ) : (
            <>
            {collectorAgents.map((agent) => {
              const selected = collectorIds.includes(agent.id);
              const online = isPeriodBriefCollectorOnline(agent, runtimes);
              const name = periodBriefCollectorLabel(agent);
              const isCloud = agent.runtime_mode === "cloud";
              const RuntimeIcon = isCloud ? Cloud : Laptop;
              const daemonId = periodBriefCollectorDaemonId(agent, runtimes);
              const slot = collectorSlots.find((item) => item.collector?.id === agent.id);
              return (
                <div
                  key={agent.id}
                  className={cn(
                    "flex w-full items-center gap-1 rounded-lg",
                    selected && "bg-muted text-foreground",
                    !online && "opacity-60",
                  )}
                >
                  <button
                    type="button"
                    className="flex min-w-0 flex-1 items-center gap-2 rounded-lg px-2 py-1.5 text-left text-sm hover:bg-muted/70"
                    onClick={() =>
                      setCollectorOverride((current) => {
                        const next = togglePeriodBriefCollectorId(
                          current ?? (plan ? plan.collector_agent_ids : defaultCollectors),
                          agent.id,
                        );
                        emitPlan({
                          window: windowKind,
                          date: selection.date,
                          start_date: startDate,
                          end_date: endDate,
                          collector_agent_ids: next,
                        });
                        return next;
                      })
                    }
                    disabled={busy}
                    data-testid={`period-brief-collector-${agent.id}`}
                  >
                    <RuntimeIcon className="size-3.5 shrink-0 text-muted-foreground" />
                    <span className="min-w-0 flex-1 truncate">{name}</span>
                    {!online ? (
                      <span className="shrink-0 text-[10px] text-muted-foreground">
                        {t(($) => $.notes_page.period_brief_collector_offline)}
                      </span>
                    ) : null}
                    {selected ? <Check className="size-3.5 shrink-0 text-primary" /> : null}
                  </button>
                  <div className="mr-1 flex shrink-0 items-center">
                    {daemonId ? (
                      <CollectorChipIconButton
                        label={t(($) => $.notes_page.period_brief_collect_roots_action)}
                        testId={`period-brief-collector-roots-${agent.id}`}
                        disabled={busy}
                        onClick={() =>
                          setCollectRootsTarget({
                            machineId: daemonId,
                            label: name,
                            online:
                              online ||
                              computers.some(
                                (computer) =>
                                  computer.daemon_id === daemonId && computer.connected,
                              ),
                          })
                        }
                      >
                        <Folder className="size-3.5" />
                      </CollectorChipIconButton>
                    ) : null}
                    {slot && onConfigureCollector ? (
                      <CollectorChipIconButton
                        label={t(($) => $.notes_page.period_brief_collector_rebind_action)}
                        testId={`period-brief-collector-runtime-${agent.id}`}
                        disabled={busy}
                        onClick={() => onConfigureCollector(slot)}
                      >
                        <MonitorSmartphone className="size-3.5" />
                      </CollectorChipIconButton>
                    ) : null}
                  </div>
                </div>
              );
            })}
            {missingCollectorSlots.map((slot) => (
              <NotesCollectorSetupCard
                key={slot.key}
                slotKey={slot.key}
                label={slot.label}
                onOpenRuntimePicker={() => onConfigureCollector?.(slot)}
                onOpenCollectRoots={
                  periodBriefSlotDaemonId(slot, runtimes)
                    ? () =>
                        setCollectRootsTarget({
                          machineId: periodBriefSlotDaemonId(slot, runtimes) ?? "",
                          label: slot.label,
                          online:
                            computers.some(
                              (computer) =>
                                computer.daemon_id === periodBriefSlotDaemonId(slot, runtimes) &&
                                computer.connected,
                            ) || slot.runtimeIds.some((id) =>
                              runtimes.some((runtime) => runtime.id === id && runtime.status === "online"),
                            ),
                        })
                    : undefined
                }
                onDismiss={() =>
                  setDismissedMissingKeys((current) =>
                    current.includes(slot.key) ? current : [...current, slot.key],
                  )
                }
              />
            ))}
            {waitingRuntimeSlots.map((slot) => (
              <NotesCollectorSetupCard
                key={slot.key}
                slotKey={slot.key}
                label={slot.label}
                reason="missing-runtime"
                onOpenCollectRoots={
                  periodBriefSlotDaemonId(slot, runtimes)
                    ? () =>
                        setCollectRootsTarget({
                          machineId: periodBriefSlotDaemonId(slot, runtimes) ?? "",
                          label: slot.label,
                          online: computers.some(
                            (computer) =>
                              computer.daemon_id === periodBriefSlotDaemonId(slot, runtimes) &&
                              computer.connected,
                          ),
                        })
                    : undefined
                }
                onDismiss={() =>
                  setDismissedMissingKeys((current) =>
                    current.includes(slot.key) ? current : [...current, slot.key],
                  )
                }
              />
            ))}
            </>
          )}
          </TooltipProvider>
        </div>
      </div>
      <p className="text-[11px] leading-4 text-muted-foreground">
        {t(($) => $.notes_page.period_brief_compose_hint)}
      </p>
      <div className="flex justify-end gap-2">
        {onCancel && !disabled ? (
          <Button
            type="button"
            size="sm"
            variant="ghost"
            data-testid="period-brief-cancel"
            onClick={onCancel}
          >
            {t(($) => $.notes_page.period_brief_cancel)}
          </Button>
        ) : null}
        {onStart ? (
          <Button
            type="button"
            size="sm"
            disabled={busy || !agentId || !canSubmit}
            data-testid="period-brief-start"
            onClick={disabled ? undefined : onStart}
          >
            {t(($) => $.notes_page.period_brief_start_collect)}
          </Button>
        ) : null}
      </div>
      {collectRootsTarget ? (
        <PeriodBriefCollectRootsDialog
          machineId={collectRootsTarget.machineId}
          label={collectRootsTarget.label}
          online={collectRootsTarget.online}
          onClose={() => setCollectRootsTarget(null)}
        />
      ) : null}
    </div>
  );
}
