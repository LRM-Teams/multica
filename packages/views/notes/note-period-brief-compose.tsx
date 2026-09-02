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
  resolvePeriodBriefComposeRequest,
  type PeriodBriefComposeCollector,
  type PeriodBriefComposeRequest,
  type PeriodBriefComposeSelection,
} from "@multica/core/notes/period-brief-compose";
import {
  defaultPeriodBriefCustomRange,
  isValidPeriodBriefCustomRange,
} from "@multica/core/notes/period-brief-window";
import { computerListOptions, runtimeListOptions } from "@multica/core/runtimes";
import type { NotePeriodBriefWindow } from "@multica/core/types";
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
  startedTitle = null,
  onResolvedChange,
  onCancel,
  onConfigureCollector,
}: {
  active: boolean;
  text?: string;
  submitting?: boolean;
  startedTitle?: string | null;
  onResolvedChange?: (resolved: NotePeriodBriefResolved) => void;
  onCancel?: () => void;
  onConfigureCollector?: (slot: PeriodBriefCollectorSlot) => void;
}) {
  const { t } = useT("layout");
  const timezone = useViewingTimezone();
  const wsId = useWorkspaceId();
  const currentUserId = useAuthStore((s) => s.user?.id ?? null);
  const { data: agents = [] } = useQuery({
    ...agentListOptions(wsId),
    enabled: Boolean(wsId) && active,
  });
  const { data: runtimes = [] } = useQuery({
    ...runtimeListOptions(wsId),
    enabled: Boolean(wsId) && active,
  });
  const { data: computers = [] } = useQuery({
    ...computerListOptions(wsId ?? ""),
    enabled: Boolean(wsId) && active,
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
  const collectorIds = collectorOverride ?? defaultCollectors;

  const prevActiveRef = useRef(active);
  if (active !== prevActiveRef.current) {
    prevActiveRef.current = active;
    if (active) {
      setCollectorOverride(null);
      setDismissedMissingKeys([]);
      setWindowKind("week");
      const custom = defaultPeriodBriefCustomRange(today);
      setStartDate(custom.start_date);
      setEndDate(custom.end_date);
    }
  }

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
      date: today,
      start_date: startDate,
      end_date: endDate,
      collector_ids: collectorIds,
    }),
    [collectorIds, endDate, startDate, today, windowKind],
  );
  const request = useMemo(
    () => resolvePeriodBriefComposeRequest(selection, collectors, text),
    [collectors, selection, text],
  );
  const customRangeValid =
    request.window !== "custom" ||
    isValidPeriodBriefCustomRange(request.start_date ?? "", request.end_date ?? "");
  const canSubmit =
    Boolean(agentId) &&
    request.collector_ids.length > 0 &&
    !submitting &&
    customRangeValid;

  useEffect(() => {
    onResolvedChange?.({
      agentId,
      timezone,
      canSubmit,
      selection,
      collectors,
      request,
    });
  }, [agentId, canSubmit, collectors, onResolvedChange, request, selection, timezone]);

  if (startedTitle) {
    return (
      <div
        className="mx-3 mb-2 rounded-xl border bg-card px-3 py-3 text-sm"
        data-testid="period-brief-started"
      >
        <p className="font-medium">{t(($) => $.notes_page.period_brief_started_title, { title: startedTitle })}</p>
        <p className="mt-1 text-xs text-muted-foreground">
          {t(($) => $.notes_page.period_brief_started_hint)}
        </p>
      </div>
    );
  }

  const busy = submitting;

  return (
    <div className="mx-3 mb-2 space-y-3 rounded-xl border bg-card px-3 py-3" data-testid="period-brief-compose">
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
              onClick={() => setWindowKind(kind)}
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
            onChange={(event) => setStartDate(event.target.value)}
            disabled={busy}
            aria-label={t(($) => $.notes_page.period_brief_start_date_label)}
            data-testid="period-brief-start-date"
          />
          <Input
            type="date"
            value={endDate}
            onChange={(event) => setEndDate(event.target.value)}
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
                      setCollectorOverride((current) =>
                        togglePeriodBriefCollectorId(current ?? defaultCollectors, agent.id),
                      )
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
      {onCancel ? (
        <div className="flex justify-end">
          <Button
            type="button"
            size="sm"
            variant="ghost"
            disabled={busy}
            data-testid="period-brief-cancel"
            onClick={onCancel}
          >
            {t(($) => $.notes_page.period_brief_cancel)}
          </Button>
        </div>
      ) : null}
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
